package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/cmd"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	_ "modernc.org/sqlite"
)

// Every end-to-end broker runs under this project, so the canonical resolver
// produces the same socket for a predecessor and its successor.
const e2eProjectID = "e2e-test-project"

func TestE2E_TaskRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	env := newE2EEnv(t)
	broker := env.launch(t, "startup.log", "--initialize")

	assertCompetingStartsRejected(t, env.binary, broker.cmd, env.socket)
	socketPath := env.socket

	// Connect to broker and create session
	c, err := client.Connect(socketPath, config.Defaults.ConnectTimeout)
	if err != nil {
		t.Fatalf("connect to broker: %v", err)
	}
	defer c.Close()

	// Establish session
	resp, err := c.Send(protocol.Request{
		Cmd:  protocol.CmdConnect,
		Name: "e2e-test",
	})
	if err != nil {
		t.Fatalf("connect request: %v", err)
	}
	if !resp.OK {
		t.Fatalf("connect failed: %s", resp.Error)
	}

	// Create task
	resp, err = c.Send(protocol.Request{
		Cmd:     protocol.CmdTaskCreate,
		Payload: json.RawMessage(`{"desc":"e2e test"}`),
		Type:    "test",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("create failed: %s", resp.Error)
	}

	// Extract task ID
	var createData struct {
		ID int64 `json:"ID"`
	}
	if err := json.Unmarshal(resp.Data, &createData); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	taskID := fmt.Sprintf("%d", createData.ID)

	// Claim task
	resp, err = c.Send(protocol.Request{
		Cmd:  protocol.CmdTaskClaim,
		Type: "test",
	})
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("claim failed: %s", resp.Error)
	}

	// Extract claim token
	var claimData struct {
		ID         int64  `json:"ID"`
		ClaimToken string `json:"ClaimToken"`
	}
	if err := json.Unmarshal(resp.Data, &claimData); err != nil {
		t.Fatalf("unmarshal claim response: %v", err)
	}

	// Complete task
	resp, err = c.Send(protocol.Request{
		Cmd:        protocol.CmdTaskComplete,
		TaskID:     taskID,
		ClaimToken: claimData.ClaimToken,
		Result:     json.RawMessage(`{"status":"done"}`),
	})
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("complete failed: %s", resp.Error)
	}

	// Verify task is completed
	resp, err = c.Send(protocol.Request{
		Cmd:    protocol.CmdTaskGet,
		TaskID: taskID,
	})
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !resp.OK {
		t.Fatalf("get failed: %s", resp.Error)
	}

	var getData struct {
		State  string `json:"State"`
		Result string `json:"Result"`
	}
	if err := json.Unmarshal(resp.Data, &getData); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}

	if getData.State != "completed" {
		t.Fatalf("expected State=completed, got %s", getData.State)
	}

	// Verify result was stored
	var resultData struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(getData.Result), &resultData); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resultData.Status != "done" {
		t.Fatalf("expected status=done, got %s", resultData.Status)
	}
}

// Exercise the built foreground entrypoint while its predecessor is healthy
// and while SIGSTOP makes every health probe unresponsive.
func assertCompetingStartsRejected(t *testing.T, binary string, running *exec.Cmd, socket string) {
	t.Helper()
	original, err := os.Lstat(socket)
	if err != nil {
		t.Fatal(err)
	}
	start := func() {
		ctx, cancel := context.WithTimeout(t.Context(), 2*config.Defaults.StartupTimeout)
		defer cancel()
		contender := exec.CommandContext(ctx, binary, "start", "--foreground")
		contender.Env, contender.Dir = running.Env, running.Dir
		output, err := contender.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("competing startup exceeded its deadline: %s", output)
		}
		if err == nil {
			t.Fatal("second broker acquired a live owner's database")
		}
		if !strings.Contains(string(output), brokerstate.ErrOwnerAlive.Error()) {
			t.Fatalf("unexpected startup failure: %s", output)
		}
		current, err := os.Lstat(socket)
		if err != nil || !os.SameFile(original, current) {
			t.Fatalf("contender changed the live socket: %v", err)
		}
	}
	start()
	if err := running.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer running.Process.Signal(syscall.SIGCONT)
	start()
}

// A signalled broker must release ownership cleanly, so the next incarnation
// of the same project can acquire the store it left behind.
func TestE2E_SigtermShutdownReleasesOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	env := newE2EEnv(t)
	first := env.launch(t, "first.log", "--initialize")
	firstExit := first.terminate(t)

	// The successor opens the released store; only a real release lets it serve.
	second := env.launch(t, "second.log")
	ctx, cancel := context.WithTimeout(t.Context(), config.Defaults.ShutdownTimeout+config.Defaults.StartupTimeout)
	defer cancel()
	stop := exec.CommandContext(ctx, env.binary, "stop")
	stop.Dir, stop.Env = env.project, second.cmd.Env
	out, err := stop.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"message": "broker stopped"`) {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	// Success is checked at command return, before waiting for process exit.
	for _, path := range []string{env.socket, config.NewPaths(e2eProjectID).PID} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stop claimed success before endpoint removal: %s %v", path, err)
		}
	}
	select {
	case <-second.exited:
	case <-ctx.Done():
		t.Fatal("stopped broker did not exit")
	}
	if second.err != nil {
		t.Fatalf("stopped broker exited with %v", second.err)
	}
	third := env.launch(t, "third.log")
	thirdExit := third.terminate(t)
	t.Logf("SIGTERM shutdown took %s and %s; stop returned after endpoint removal", firstExit, thirdExit)
}

// The legacy store this proof is built on: two tasks, one of them carrying the
// ttl the retired broker's runtime migration added, and two rows in the
// name-addressed message table that broker created the first time a message was
// sent. The conversion has to carry every one of them across.
const (
	e2eLegacyTaskWithTTL = "legacy-ttl"
	e2eLegacyTaskPlain   = "legacy-plain"
	e2eLegacyMessages    = 2
	// e2eRetiredMessagesTable is the name internal/broker moves the retired
	// message table to, because the native schema takes the old name for a
	// different table. It is asserted here rather than imported because it is
	// what an operator reading a conversion report actually sees.
	e2eRetiredMessagesTable = "legacy_messages"
	// e2ePreparedState and e2eActiveState are the two cutover states an
	// inspection reports.
	e2ePreparedState = "prepared"
	e2eActiveState   = "active"
)

// A project store written by the retired broker must convert offline, keep
// every task it held, and only then admit a broker; and a conversion an
// operator changes their mind about must be undoable.
func TestE2E_LegacyStoreConvertsActivatesAndServes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	// The conversion runs the real writer census, so nothing in this test may
	// hold the store open across `store convert`, and no other Waggle process
	// may be running on this machine while it does.
	t.Run("converts, activates and serves the tasks it carried", func(t *testing.T) {
		env := newE2EEnv(t)
		paths := config.NewPaths(e2eProjectID)
		writeLegacyStore(t, paths)

		// A schema-v1 store is never migrated on open: the broker refuses it and
		// says what has to happen instead.
		_, refusal, err := env.runCLI(t, 2*config.Defaults.StartupTimeout, "start", "--foreground")
		if err == nil {
			t.Fatalf("broker started on a schema-v1 store\n%s", refusal)
		}
		if !strings.Contains(refusal, brokerstate.ErrSchemaVersion.Error()) {
			t.Fatalf("legacy store refused for the wrong reason: %s", refusal)
		}

		report := convertStore(t, env)
		if report.FromVersion != config.LegacySchemaVersion || report.ToVersion != config.NativeSchemaVersion {
			t.Fatalf("converted %d -> %d, want %d -> %d",
				report.FromVersion, report.ToVersion, config.LegacySchemaVersion, config.NativeSchemaVersion)
		}
		if report.Tasks != 2 {
			t.Fatalf("conversion carried %d tasks, want 2", report.Tasks)
		}
		if !slices.Equal(report.PreservedLegacyTables, []string{e2eRetiredMessagesTable}) {
			t.Fatalf("preserved legacy tables = %v, want [%s]", report.PreservedLegacyTables, e2eRetiredMessagesTable)
		}
		// The retired broker's endpoints outlived it; a committed conversion is
		// what removes them, so nothing can find them again.
		if !slices.Equal(report.RetiredEndpoints, []string{paths.LegacyPID, paths.LegacySocket}) {
			t.Fatalf("retired endpoints = %v, want [%s %s]", report.RetiredEndpoints, paths.LegacyPID, paths.LegacySocket)
		}
		if report.CensusScope == "" {
			t.Fatal("conversion reported no census scope")
		}
		for _, endpoint := range []string{paths.LegacyPID, paths.LegacySocket} {
			if _, err := os.Lstat(endpoint); !os.IsNotExist(err) {
				t.Fatalf("legacy endpoint survived conversion: %s %v", endpoint, err)
			}
		}
		requireRegular(t, report.Snapshot, "snapshot")

		view := inspectStore(t, env)
		if !view.Exists || view.SchemaVersion != config.NativeSchemaVersion || view.VersionRows != 1 {
			t.Fatalf("inspection reported exists=%v version=%d rows=%d, want true/%d/1",
				view.Exists, view.SchemaVersion, view.VersionRows, config.NativeSchemaVersion)
		}
		if view.Cutover != e2ePreparedState {
			t.Fatalf("cutover = %q, want %q", view.Cutover, e2ePreparedState)
		}
		if view.Provenance == nil {
			t.Fatal("converted store recorded no provenance")
		}
		if view.Provenance.SourceSchema != config.LegacySchemaVersion ||
			view.Provenance.Snapshot != report.Snapshot ||
			view.Provenance.ConvertedAt != report.ConvertedAt.Format(time.RFC3339Nano) {
			t.Fatalf("provenance %+v does not describe the conversion %+v", *view.Provenance, report)
		}
		if view.LegacyPIDPresent || view.LegacySocketPresent {
			t.Fatalf("inspection still sees legacy endpoints: pid=%v socket=%v",
				view.LegacyPIDPresent, view.LegacySocketPresent)
		}
		if !slices.Contains(view.Snapshots, report.Snapshot) {
			t.Fatalf("snapshots %v do not include %s", view.Snapshots, report.Snapshot)
		}

		// Preparation is not admission. The whole broker is refused, not only the
		// enrollment it would have served: a prepared store has no endpoints to
		// send an enrollment RPC to, which is a stronger guarantee than refusing
		// one. See the brief deviation recorded with this task.
		_, refusal, err = env.runCLI(t, 2*config.Defaults.StartupTimeout, "start", "--foreground")
		if err == nil {
			t.Fatalf("broker started on a prepared store\n%s", refusal)
		}
		if !strings.Contains(refusal, brokerstate.ErrPrepared.Error()) {
			t.Fatalf("prepared store refused for the wrong reason: %s", refusal)
		}

		out, refusal, err := env.runCLI(t, config.Defaults.ShutdownTimeout+config.Defaults.StartupTimeout,
			"store", "activate")
		if err != nil {
			t.Fatalf("activate: %v\n%s%s", err, out, refusal)
		}
		var activated struct {
			OK      bool   `json:"ok"`
			Message string `json:"message"`
		}
		decodeJSON(t, "activate", out, &activated)
		if !activated.OK || activated.Message == "" {
			t.Fatalf("activate reported %+v", activated)
		}
		if state := inspectStore(t, env).Cutover; state != e2eActiveState {
			t.Fatalf("cutover after activation = %q, want %q", state, e2eActiveState)
		}

		// Only now may a broker have it, and what it serves is the legacy store's
		// own tasks.
		broker := env.launch(t, "converted.log")
		out, refusal, err = env.runCLI(t, config.Defaults.ConnectTimeout+config.Defaults.StartupTimeout, "status")
		if err != nil {
			t.Fatalf("status: %v\n%s%s", err, out, refusal)
		}
		var status struct {
			OK bool `json:"ok"`
		}
		decodeJSON(t, "status", out, &status)
		if !status.OK {
			t.Fatalf("status on the converted store: %s", out)
		}
		assertLegacyTasksServed(t, env)

		out, refusal, err = env.runCLI(t, config.Defaults.ShutdownTimeout+config.Defaults.StartupTimeout, "stop")
		if err != nil || !strings.Contains(out, `"message": "broker stopped"`) {
			t.Fatalf("stop: %v\n%s%s", err, out, refusal)
		}
		select {
		case <-broker.exited:
		case <-time.After(config.Defaults.ShutdownTimeout + config.Defaults.StartupTimeout):
			t.Fatalf("stopped broker did not exit\n%s", broker.output())
		}
		if broker.err != nil {
			t.Fatalf("stopped broker exited with %v\n%s", broker.err, broker.output())
		}
		// The retired message rows are still there after a broker has owned and
		// served the store: preserving them is not a claim the report makes alone.
		if kept := legacyMessagesKept(t, paths.DB); kept != e2eLegacyMessages {
			t.Fatalf("%s holds %d rows, want %d", e2eRetiredMessagesTable, kept, e2eLegacyMessages)
		}
	})

	// An operator who converts and then thinks better of it gets the store they
	// had back, refusing a broker exactly as it did before.
	t.Run("rollback restores the store the conversion started from", func(t *testing.T) {
		env := newE2EEnv(t)
		paths := config.NewPaths(e2eProjectID)
		writeLegacyStore(t, paths)
		converted := convertStore(t, env)

		out, refusal, err := env.runCLI(t, 2*config.Defaults.CensusTimeout+config.Defaults.BusyTimeout,
			"store", "rollback")
		if err != nil {
			t.Fatalf("rollback: %v\n%s%s", err, out, refusal)
		}
		var restored struct {
			OK bool `json:"ok"`
			brokerstate.Report
		}
		decodeJSON(t, "rollback", out, &restored)
		if restored.FromVersion != config.NativeSchemaVersion || restored.ToVersion != config.LegacySchemaVersion {
			t.Fatalf("rolled back %d -> %d, want %d -> %d",
				restored.FromVersion, restored.ToVersion, config.NativeSchemaVersion, config.LegacySchemaVersion)
		}
		if restored.Snapshot != converted.Snapshot {
			t.Fatalf("rollback restored %s, want the conversion's snapshot %s", restored.Snapshot, converted.Snapshot)
		}
		if restored.Tasks != converted.Tasks {
			t.Fatalf("rollback restored %d tasks, want the %d the conversion carried", restored.Tasks, converted.Tasks)
		}

		view := inspectStore(t, env)
		if !view.Exists || view.SchemaVersion != config.LegacySchemaVersion || view.VersionRows != 1 {
			t.Fatalf("inspection after rollback reported exists=%v version=%d rows=%d, want true/%d/1",
				view.Exists, view.SchemaVersion, view.VersionRows, config.LegacySchemaVersion)
		}
		if view.Cutover != "" || view.Provenance != nil {
			t.Fatalf("restored store still carries native records: cutover=%q provenance=%+v", view.Cutover, view.Provenance)
		}

		_, refusal, err = env.runCLI(t, 2*config.Defaults.StartupTimeout, "start", "--foreground")
		if err == nil {
			t.Fatalf("broker started on a rolled-back store\n%s", refusal)
		}
		if !strings.Contains(refusal, brokerstate.ErrSchemaVersion.Error()) {
			t.Fatalf("rolled-back store refused for the wrong reason: %s", refusal)
		}
	})
}

// convertStore runs the real offline conversion and returns its report. The
// census reads the machine, so this is also where the test's own handles have
// to be gone: writeLegacyStore closes its connection before returning.
func convertStore(t *testing.T, env e2eEnv) brokerstate.Report {
	t.Helper()
	out, refusal, err := env.runCLI(t, 2*config.Defaults.CensusTimeout+config.Defaults.BusyTimeout,
		"store", "convert")
	if err != nil {
		t.Fatalf("convert: %v\n%s%s", err, out, refusal)
	}
	var report struct {
		OK bool `json:"ok"`
		brokerstate.Report
	}
	decodeJSON(t, "convert", out, &report)
	if !report.OK {
		t.Fatalf("convert reported failure: %s", out)
	}
	return report.Report
}

func inspectStore(t *testing.T, env e2eEnv) brokerstate.Inspection {
	t.Helper()
	out, refusal, err := env.runCLI(t, config.Defaults.BusyTimeout+config.Defaults.StartupTimeout,
		"store", "inspect")
	if err != nil {
		t.Fatalf("inspect: %v\n%s%s", err, out, refusal)
	}
	var view struct {
		OK bool `json:"ok"`
		brokerstate.Inspection
	}
	decodeJSON(t, "inspect", out, &view)
	if !view.OK {
		t.Fatalf("inspect reported failure: %s", out)
	}
	return view.Inspection
}

// assertLegacyTasksServed requires the broker to serve exactly the tasks the
// legacy store held, with the ttl the retired broker's migration had put there.
func assertLegacyTasksServed(t *testing.T, env e2eEnv) {
	t.Helper()
	out, refusal, err := env.runCLI(t, config.Defaults.ConnectTimeout+config.Defaults.StartupTimeout, "task", "list")
	if err != nil {
		t.Fatalf("task list: %v\n%s%s", err, out, refusal)
	}
	var listed struct {
		OK   bool `json:"ok"`
		Data []struct {
			IdempotencyKey string `json:"IdempotencyKey"`
			Payload        string `json:"Payload"`
			TTL            int    `json:"ttl"`
		} `json:"data"`
	}
	decodeJSON(t, "task list", out, &listed)
	if !listed.OK {
		t.Fatalf("task list reported failure: %s", out)
	}
	ttls := make(map[string]int, len(listed.Data))
	payloads := make(map[string]string, len(listed.Data))
	for _, task := range listed.Data {
		ttls[task.IdempotencyKey] = task.TTL
		payloads[task.IdempotencyKey] = task.Payload
	}
	// The ttl-bearing task keeps its value; the other one never had one, and a
	// conversion that invented one would be as wrong as one that dropped it.
	want := map[string]int{e2eLegacyTaskWithTTL: config.Defaults.MaxTaskTTL, e2eLegacyTaskPlain: 0}
	if len(listed.Data) != len(want) {
		t.Fatalf("broker serves %d tasks, want %d: %s", len(listed.Data), len(want), out)
	}
	for key, ttl := range want {
		if ttls[key] != ttl {
			t.Fatalf("task %s has ttl %d, want %d: %s", key, ttls[key], ttl, out)
		}
		if payloads[key] != legacyPayload(key) {
			t.Fatalf("task %s payload = %q, want %q", key, payloads[key], legacyPayload(key))
		}
	}
}

func legacyPayload(key string) string { return fmt.Sprintf(`{"task":%q}`, key) }

// writeLegacyStore plants a schema-v1 store at the canonical path, in the shape
// a broker that had been running for a while left behind: WAL journal, the
// tasks table carrying the ttl column its runtime migration added, the retired
// name-addressed messages table with rows in it, one version row, and the two
// endpoint files that broker served from. It is the store `waggle store
// convert` exists to upgrade.
//
// The statements are the retired broker's own, frozen in
// internal/brokerstate/statetest. The connection stays here: that package opens
// no database, and the architecture guard admits exactly one non-test site that
// opens the canonical store, which belongs to internal/brokerstate.
func writeLegacyStore(t *testing.T, paths config.Paths) {
	t.Helper()
	for _, dir := range []string{paths.DataDir, filepath.Dir(paths.Socket)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", legacyDSN(paths.DB, "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(config.CanonicalConnections)
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode=" + config.CanonicalJournalMode).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != config.CanonicalJournalMode {
		t.Fatalf("legacy fixture journal mode = %q", journal)
	}
	for _, statement := range statetest.LegacyStatements(true, true) {
		run(statement)
	}
	run("INSERT INTO schema_version(version) VALUES (?)", config.LegacySchemaVersion)
	// created_at defaults to now, so the surviving ttl is nowhere near expiry and
	// the maintenance loop has no reason to cancel the task it belongs to.
	run(`INSERT INTO tasks(idempotency_key, type, payload, ttl) VALUES (?, 'review', ?, ?)`,
		e2eLegacyTaskWithTTL, legacyPayload(e2eLegacyTaskWithTTL), config.Defaults.MaxTaskTTL)
	run(`INSERT INTO tasks(idempotency_key, type, payload) VALUES (?, 'review', ?)`,
		e2eLegacyTaskPlain, legacyPayload(e2eLegacyTaskPlain))
	for i := 1; i <= e2eLegacyMessages; i++ {
		run(`INSERT INTO messages(from_name, to_name, body, created_at) VALUES ('alice', 'bob', ?, '2026-01-01T00:00:00Z')`,
			fmt.Sprintf("body %d", i))
	}
	// The census that clears the conversion sees every handle on this store,
	// including this one, so it has to be gone before the test converts.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{paths.LegacyPID, paths.LegacySocket} {
		file, err := os.OpenFile(endpoint, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// legacyMessagesKept counts the rows in the table the conversion moved the
// retired message table to, reading the store without changing it.
func legacyMessagesKept(t *testing.T, database string) int {
	t.Helper()
	db, err := sql.Open("sqlite", legacyDSN(database, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var kept int
	if err := db.QueryRow("SELECT count(*) FROM " + e2eRetiredMessagesTable).Scan(&kept); err != nil {
		t.Fatalf("read %s: %v", e2eRetiredMessagesTable, err)
	}
	return kept
}

func legacyDSN(path, mode string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", mode)
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", config.Defaults.BusyTimeout.Milliseconds()))
	u.RawQuery = q.Encode()
	return u.String()
}

func decodeJSON(t *testing.T, what, out string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), into); err != nil {
		t.Fatalf("%s produced unreadable output: %v\n%s", what, err, out)
	}
}

func requireRegular(t *testing.T, path, what string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %s", what, info.Mode())
	}
}

// e2eEnv is a built entrypoint plus an isolated HOME whose canonical socket
// stays inside the 104-byte AF_UNIX path limit on macOS.
type e2eEnv struct {
	binary  string
	home    string
	project string
	socket  string
}

func newE2EEnv(t *testing.T) e2eEnv {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "waggle")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s\n%s", err, out)
	}
	// Keep the isolated HOME under the writable checkout, where the resolved
	// socket path is short enough for AF_UNIX.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, ".h-")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	// Use the canonical path resolver for the isolated project.
	t.Setenv("HOME", home)
	return e2eEnv{binary: binary, home: home, project: t.TempDir(), socket: config.NewPaths(e2eProjectID).Socket}
}

// launch starts the real foreground entrypoint, retains its failure
// diagnostics, and returns once the broker is serving.
func (e e2eEnv) launch(t *testing.T, logName string, args ...string) *e2eBroker {
	t.Helper()
	logPath := filepath.Join(e.home, logName)
	output, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { output.Close() })

	startCmd := exec.Command(e.binary, append([]string{"start", "--foreground"}, args...)...)
	startCmd.Dir = e.project
	startCmd.Env = append(os.Environ(), "HOME="+e.home, "WAGGLE_PROJECT_ID="+e2eProjectID)
	startCmd.Stdout, startCmd.Stderr = output, output
	if err := startCmd.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	broker := &e2eBroker{cmd: startCmd, logPath: logPath, exited: make(chan struct{})}
	go func() {
		broker.err = startCmd.Wait()
		close(broker.exited)
	}()
	t.Cleanup(func() {
		startCmd.Process.Kill()
		<-broker.exited
	})

	ticker := time.NewTicker(config.Defaults.ShutdownPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * config.Defaults.StartupTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-broker.exited:
			t.Fatalf("foreground broker exited before readiness: %v\n%s", broker.err, broker.output())
		case <-deadline.C:
			t.Fatalf("foreground broker exceeded readiness deadline\n%s", broker.output())
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", e.socket, config.Defaults.ConnectTimeout)
			if err == nil {
				conn.Close()
				return broker
			}
		}
	}
}

// runCLI runs one built command against this environment's isolated HOME and
// project, and keeps the two streams apart: a result is printed on stdout and a
// refusal on stderr, so a test that reads one is never reading the other. A
// command that outruns its budget is a failure of this test, not a result.
func (e e2eEnv) runCLI(t *testing.T, budget time.Duration, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	command := exec.CommandContext(ctx, e.binary, args...)
	command.Dir = e.project
	command.Env = append(os.Environ(), "HOME="+e.home, "WAGGLE_PROJECT_ID="+e2eProjectID)
	var out, errOut bytes.Buffer
	command.Stdout, command.Stderr = &out, &errOut
	err = command.Run()
	if ctx.Err() != nil {
		t.Fatalf("%v exceeded %s\n%s%s", args, budget, out.String(), errOut.String())
	}
	return out.String(), errOut.String(), err
}

// e2eBroker is a running foreground broker. Reading err is safe only after
// exited closes.
type e2eBroker struct {
	cmd     *exec.Cmd
	logPath string
	exited  chan struct{}
	err     error
}

func (b *e2eBroker) output() string {
	data, _ := os.ReadFile(b.logPath)
	return string(data)
}

// terminate requires the first SIGTERM to drain and release within the
// configured shutdown budget, without reporting a missed deadline.
func (b *e2eBroker) terminate(t *testing.T) time.Duration {
	t.Helper()
	signalled := time.Now()
	if err := b.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal broker: %v", err)
	}
	budget := config.Defaults.ShutdownTimeout + 2*config.Defaults.StartupTimeout
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-b.exited:
	case <-timer.C:
		t.Fatalf("broker did not exit within %s of SIGTERM\n%s", budget, b.output())
	}
	elapsed := time.Since(signalled)
	if b.err != nil {
		t.Fatalf("broker exited %v after SIGTERM\n%s", b.err, b.output())
	}
	if out := b.output(); strings.Contains(out, cmd.ErrShutdownDeadlineMissed.Error()) {
		t.Fatalf("orderly shutdown reported a missed deadline\n%s", out)
	}
	return elapsed
}

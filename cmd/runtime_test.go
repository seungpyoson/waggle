package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

func TestRuntimeSubcommandsSkipBrokerAutoStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WAGGLE_PROJECT_ID", "proj-runtime")

	originalNoAutoStart := noAutoStart
	originalPaths := paths
	t.Cleanup(func() {
		noAutoStart = originalNoAutoStart
		paths = originalPaths
	})

	noAutoStart = false
	paths = config.NewPaths("stale-project")

	cmd := rootCmd
	cmd.SetArgs([]string{"runtime", "watches"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute runtime watches: %v", err)
	}

	if paths.ProjectID != "stale-project" {
		t.Fatalf("paths.ProjectID = %q, want broker paths to remain untouched", paths.ProjectID)
	}
}

func TestRuntimeWatchCommandsUseSharedMachineStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("WAGGLE_PROJECT_ID", "proj-one")
	stdout, stderr := executeRootCommandForTest(t, "runtime", "watch", "agent-a")
	if stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("runtime watch proj-one stdout=%q stderr=%q", stdout, stderr)
	}

	t.Setenv("WAGGLE_PROJECT_ID", "proj-two")
	stdout, stderr = executeRootCommandForTest(t, "runtime", "watch", "agent-b")
	if stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("runtime watch proj-two stdout=%q stderr=%q", stdout, stderr)
	}

	store := openRuntimeStoreForTest(t)
	watches, err := store.ListWatches()
	if err != nil {
		t.Fatalf("list watches: %v", err)
	}
	if len(watches) != 2 {
		t.Fatalf("watch count = %d, want 2", len(watches))
	}

	stdout, stderr = executeRootCommandForTest(t, "runtime", "watches")
	if stderr != "" {
		t.Fatalf("runtime watches stderr = %q, want empty", stderr)
	}
	var watchesResp struct {
		OK      bool       `json:"ok"`
		Watches []rt.Watch `json:"watches"`
	}
	if err := json.Unmarshal([]byte(stdout), &watchesResp); err != nil {
		t.Fatalf("unmarshal watches response: %v", err)
	}
	if !watchesResp.OK || len(watchesResp.Watches) != 2 {
		t.Fatalf("watches response = %+v, want ok with two watches", watchesResp)
	}
}

func TestRuntimePullReturnsUnreadRecordsAndMarksThemSurfaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-pull")

	store := openRuntimeStoreForTest(t)
	now := time.Now().UTC().Round(time.Second)
	if err := store.AddRecord(rt.DeliveryRecord{
		ProjectID:  "proj-pull",
		AgentName:  "agent-a",
		MessageID:  101,
		FromName:   "sender-1",
		Body:       "first message",
		SentAt:     now.Add(-2 * time.Minute),
		ReceivedAt: now.Add(-time.Minute),
		NotifiedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("add unread record: %v", err)
	}
	if err := store.AddRecord(rt.DeliveryRecord{
		ProjectID:  "proj-pull",
		AgentName:  "agent-a",
		MessageID:  102,
		FromName:   "sender-2",
		Body:       "already surfaced",
		SentAt:     now.Add(-4 * time.Minute),
		ReceivedAt: now.Add(-3 * time.Minute),
		NotifiedAt: now.Add(-3 * time.Minute),
		SurfacedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("add surfaced record: %v", err)
	}

	stdout, stderr := executeRootCommandForTest(t, "runtime", "pull", "agent-a")
	if stderr != "" {
		t.Fatalf("runtime pull stderr = %q, want empty", stderr)
	}

	var pullResp struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &pullResp); err != nil {
		t.Fatalf("unmarshal pull response: %v", err)
	}
	if !pullResp.OK || len(pullResp.Records) != 1 || pullResp.Records[0].MessageID != 101 {
		t.Fatalf("pull response = %+v, want unread record 101 only", pullResp)
	}

	unread, err := store.Unread("proj-pull", "agent-a")
	if err != nil {
		t.Fatalf("list unread after pull: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count after pull = %d, want 0", len(unread))
	}
}

func TestExecuteRootCommandForTestDoesNotLeakRuntimePullProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store := openRuntimeStoreForTest(t)
	now := time.Now().UTC().Round(time.Second)
	for _, rec := range []rt.DeliveryRecord{
		{
			ProjectID:  "proj-flag",
			AgentName:  "agent-a",
			MessageID:  101,
			FromName:   "sender-flag",
			Body:       "from explicit project",
			SentAt:     now.Add(-2 * time.Minute),
			ReceivedAt: now.Add(-1 * time.Minute),
			NotifiedAt: now.Add(-1 * time.Minute),
		},
		{
			ProjectID:  "proj-env",
			AgentName:  "agent-a",
			MessageID:  202,
			FromName:   "sender-env",
			Body:       "from env project",
			SentAt:     now.Add(-4 * time.Minute),
			ReceivedAt: now.Add(-3 * time.Minute),
			NotifiedAt: now.Add(-3 * time.Minute),
		},
	} {
		if err := store.AddRecord(rec); err != nil {
			t.Fatalf("add unread record: %v", err)
		}
	}

	stdout, stderr := executeRootCommandForTest(t, "runtime", "pull", "agent-a", "--project-id", "proj-flag")
	if stderr != "" {
		t.Fatalf("runtime pull with explicit project stderr = %q, want empty", stderr)
	}

	var firstPull struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &firstPull); err != nil {
		t.Fatalf("unmarshal first pull response: %v", err)
	}
	if !firstPull.OK || len(firstPull.Records) != 1 || firstPull.Records[0].MessageID != 101 {
		t.Fatalf("first pull response = %+v, want unread record 101 only", firstPull)
	}

	t.Setenv("WAGGLE_PROJECT_ID", "proj-env")
	stdout, stderr = executeRootCommandForTest(t, "runtime", "pull", "agent-a")
	if stderr != "" {
		t.Fatalf("runtime pull with env project stderr = %q, want empty", stderr)
	}

	var secondPull struct {
		OK      bool                `json:"ok"`
		Records []rt.DeliveryRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &secondPull); err != nil {
		t.Fatalf("unmarshal second pull response: %v", err)
	}
	if !secondPull.OK || len(secondPull.Records) != 1 || secondPull.Records[0].MessageID != 202 {
		t.Fatalf("second pull response = %+v, want unread record 202 only", secondPull)
	}
}

func TestRuntimeStatusReportsStoppedWhenStateMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stdout, stderr := executeRootCommandForTest(t, "runtime", "status")
	if stderr != "" {
		t.Fatalf("runtime status stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"running": false`) {
		t.Fatalf("runtime status = %q, want running false", stdout)
	}
}

func TestExecuteRootCommandForTestDoesNotLeakInstallUninstallFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stdout, stderr := executeRootCommandForTest(t, "install", "codex", "--uninstall")
	if stderr != "" {
		t.Fatalf("install uninstall stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"Codex integration removed"`) {
		t.Fatalf("install uninstall stdout = %q, want removal message", stdout)
	}

	stdout, stderr = executeRootCommandForTest(t, "install", "codex")
	if stderr != "" {
		t.Fatalf("install stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"Codex integration installed. Restart Codex to activate."`) {
		t.Fatalf("install stdout = %q, want install message", stdout)
	}
}

type commandTestState struct {
	noAutoStart               bool
	paths                     config.Paths
	installUninstall          bool
	listenName                string
	listenOutput              string
	completeToken             string
	listState                 string
	listType                  string
	listOwner                 string
	runtimeForeground         bool
	spawnName                 string
	spawnType                 string
	inboxName                 string
	runtimePullProjectID      string
	claimType                 string
	claimTags                 string
	runtimeWatchProjectID     string
	runtimeWatchSource        string
	runtimeUnwatchProjectID   string
	runtimeWatchesProjectID   string
	sendName                  string
	sendPriority              string
	sendTTL                   int
	sendAwaitAck              bool
	sendTimeout               int
	taskType                  string
	taskTags                  string
	taskDependsOn             string
	taskLease                 int
	taskMaxRetries            int
	taskPriority              int
	taskIdempotencyKey        string
	taskCreateTTL             string
	adapterBootstrapTool      string
	adapterBootstrapAgent     string
	adapterBootstrapProjectID string
	adapterBootstrapSource    string
	adapterBootstrapFormat    string
	failToken                 string
	ackName                   string
	foreground                bool
	presenceName              string
	connectName               string
	updatePriority            int
	updateTags                string
	heartbeatToken            string
}

func captureCommandTestState() commandTestState {
	return commandTestState{
		noAutoStart:               noAutoStart,
		paths:                     paths,
		installUninstall:          installUninstall,
		listenName:                listenName,
		listenOutput:              listenOutput,
		completeToken:             completeToken,
		listState:                 listState,
		listType:                  listType,
		listOwner:                 listOwner,
		runtimeForeground:         runtimeForeground,
		spawnName:                 spawnName,
		spawnType:                 spawnType,
		inboxName:                 inboxName,
		runtimePullProjectID:      runtimePullProjectID,
		claimType:                 claimType,
		claimTags:                 claimTags,
		runtimeWatchProjectID:     runtimeWatchProjectID,
		runtimeWatchSource:        runtimeWatchSource,
		runtimeUnwatchProjectID:   runtimeUnwatchProjectID,
		runtimeWatchesProjectID:   runtimeWatchesProjectID,
		sendName:                  sendName,
		sendPriority:              sendPriority,
		sendTTL:                   sendTTL,
		sendAwaitAck:              sendAwaitAck,
		sendTimeout:               sendTimeout,
		taskType:                  taskType,
		taskTags:                  taskTags,
		taskDependsOn:             taskDependsOn,
		taskLease:                 taskLease,
		taskMaxRetries:            taskMaxRetries,
		taskPriority:              taskPriority,
		taskIdempotencyKey:        taskIdempotencyKey,
		taskCreateTTL:             taskCreateTTL,
		adapterBootstrapTool:      adapterBootstrapTool,
		adapterBootstrapAgent:     adapterBootstrapAgent,
		adapterBootstrapProjectID: adapterBootstrapProjectID,
		adapterBootstrapSource:    adapterBootstrapSource,
		adapterBootstrapFormat:    adapterBootstrapFormat,
		failToken:                 failToken,
		ackName:                   ackName,
		foreground:                foreground,
		presenceName:              presenceName,
		connectName:               connectName,
		updatePriority:            updatePriority,
		updateTags:                updateTags,
		heartbeatToken:            heartbeatToken,
	}
}

func (s commandTestState) restore() {
	noAutoStart = s.noAutoStart
	paths = s.paths
	installUninstall = s.installUninstall
	listenName = s.listenName
	listenOutput = s.listenOutput
	completeToken = s.completeToken
	listState = s.listState
	listType = s.listType
	listOwner = s.listOwner
	runtimeForeground = s.runtimeForeground
	spawnName = s.spawnName
	spawnType = s.spawnType
	inboxName = s.inboxName
	runtimePullProjectID = s.runtimePullProjectID
	claimType = s.claimType
	claimTags = s.claimTags
	runtimeWatchProjectID = s.runtimeWatchProjectID
	runtimeWatchSource = s.runtimeWatchSource
	runtimeUnwatchProjectID = s.runtimeUnwatchProjectID
	runtimeWatchesProjectID = s.runtimeWatchesProjectID
	sendName = s.sendName
	sendPriority = s.sendPriority
	sendTTL = s.sendTTL
	sendAwaitAck = s.sendAwaitAck
	sendTimeout = s.sendTimeout
	taskType = s.taskType
	taskTags = s.taskTags
	taskDependsOn = s.taskDependsOn
	taskLease = s.taskLease
	taskMaxRetries = s.taskMaxRetries
	taskPriority = s.taskPriority
	taskIdempotencyKey = s.taskIdempotencyKey
	taskCreateTTL = s.taskCreateTTL
	adapterBootstrapTool = s.adapterBootstrapTool
	adapterBootstrapAgent = s.adapterBootstrapAgent
	adapterBootstrapProjectID = s.adapterBootstrapProjectID
	adapterBootstrapSource = s.adapterBootstrapSource
	adapterBootstrapFormat = s.adapterBootstrapFormat
	failToken = s.failToken
	ackName = s.ackName
	foreground = s.foreground
	presenceName = s.presenceName
	connectName = s.connectName
	updatePriority = s.updatePriority
	updateTags = s.updateTags
	heartbeatToken = s.heartbeatToken
}

func executeRootCommandForTest(t *testing.T, args ...string) (string, string) {
	t.Helper()

	originalState := captureCommandTestState()
	defer func() {
		originalState.restore()
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
		rootCmd.SetArgs(nil)
	}()

	noAutoStart = false
	paths = config.Paths{}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}

	return stdout.String(), stderr.String()
}

func openRuntimeStoreForTest(t *testing.T) *rt.Store {
	t.Helper()

	paths := config.NewPaths("")
	store, err := rt.NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatalf("open runtime store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

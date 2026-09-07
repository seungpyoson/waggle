package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/brokerstate"
	"github.com/seungpyoson/waggle/internal/brokerstate/statetest"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/tasks"
)

func TestBrokerShutdownRequeuesClaimsBeforeRelease(t *testing.T) {
	database := filepath.Join(t.TempDir(), "state.db")
	socket := shortBrokerSocketPath(t, "waggle-drain-*")
	b, err := newOwnedTestBroker(t, database, config.CreateStore,
		config.NewBrokerConfig(config.BrokerEndpoints{Socket: socket, PID: socket + ".pid"}))
	if err != nil {
		t.Fatal(err)
	}
	serving := make(chan error, 1)
	go func() { serving <- b.Serve() }()
	t.Cleanup(func() {
		if err := b.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	c := connectClient(t, socket)
	defer c.Close()
	for _, req := range []protocol.Request{
		{Cmd: protocol.CmdConnect, Name: "worker"},
		{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{}`)},
	} {
		if resp := sendRequest(t, c, req); !resp.OK {
			t.Fatal(resp.Error)
		}
	}
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var claim tasks.Task
	unmarshalResponse(t, resp, &claim)
	if claim.State != tasks.StateClaimed {
		t.Fatal("test did not establish a live claim")
	}

	// Leave the worker connected. Shutdown closes admission before closing
	// that connection; its cleanup must still commit before ownership release.
	if err := b.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := <-serving; err != nil {
		t.Fatal(err)
	}
	// Acquire storage directly, without broker startup recovery masking a
	// missing disconnect transaction in the predecessor.
	next, err := brokerstate.Acquire(t.Context(), config.NewOwnershipConfig(database, config.OpenStore), statetest.Process{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := next.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := statetest.Write(next, func(tx *brokerstate.WriteTx) error {
		task, err := tasks.NewStore(tx).Get(claim.ID)
		if err != nil {
			return err
		}
		if task.State != tasks.StatePending || task.ClaimToken != "" || task.RetryCount != claim.RetryCount+1 {
			return fmt.Errorf("disconnect cleanup must requeue exactly once before release: state=%s retries=%d", task.State, task.RetryCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerStartupRequiresTaskSchemaBeforeBinding(t *testing.T) {
	owner := statetest.New(t, "UPDATE cutover SET state = 'active' WHERE singleton = 1")
	dir := t.TempDir()
	endpoints := config.BrokerEndpoints{Socket: filepath.Join(dir, "broker.sock"), PID: filepath.Join(dir, "broker.pid")}
	b, err := New(t.Context(), owner, config.NewBrokerConfig(endpoints), newNativeFixture())
	if b != nil || err == nil || !strings.HasPrefix(err.Error(), "recover task claims:") {
		t.Fatalf("startup did not reject incomplete canonical schema before binding: %v", err)
	}
	for _, path := range []string{endpoints.Socket, endpoints.PID} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("failed startup touched %s: %v", path, err)
		}
	}
}

func TestBrokerCompletionAndDependenciesRollbackTogether(t *testing.T) {
	socket, b, shutdown := startTestBroker(t)
	defer shutdown()
	c := connectClient(t, socket)
	defer c.Close()
	if resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdConnect, Name: "worker"}); !resp.OK {
		t.Fatal(resp.Error)
	}
	create := func(deps string) tasks.Task {
		resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskCreate, Payload: json.RawMessage(`{}`), DependsOn: deps})
		if !resp.OK {
			t.Fatal(resp.Error)
		}
		var task tasks.Task
		unmarshalResponse(t, resp, &task)
		return task
	}
	parent := create("")
	child := create(fmt.Sprint(parent.ID))
	resp := sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskClaim})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	var claim tasks.Task
	unmarshalResponse(t, resp, &claim)
	if claim.ID != parent.ID {
		t.Fatal("claimed blocked dependency")
	}
	// Inject failure at the second domain write, after completion would occur.
	if err := statetest.Write(b.owner, func(tx *brokerstate.WriteTx) error {
		_, err := tx.Exec(fmt.Sprintf(`CREATE TRIGGER reject_dependency BEFORE UPDATE OF blocked ON tasks
		WHEN NEW.id = %d BEGIN SELECT RAISE(ABORT, 'injected dependency failure'); END`, child.ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resp = sendRequest(t, c, protocol.Request{Cmd: protocol.CmdTaskComplete, TaskID: fmt.Sprint(parent.ID), ClaimToken: claim.ClaimToken, Result: json.RawMessage(`{}`)})
	if resp.OK {
		t.Fatal("reported completion after dependency failure")
	}
	unchanged, err := readTask(b, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.State != tasks.StateClaimed || unchanged.ClaimToken != claim.ClaimToken {
		t.Fatalf("partial completion persisted: %+v", unchanged)
	}
	dependent, err := readTask(b, child.ID)
	if err != nil || !dependent.Blocked {
		t.Fatalf("dependency changed: %+v %v", dependent, err)
	}
	for _, path := range []string{socket, b.config.Endpoints.PID} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal(err)
		}
	}
}

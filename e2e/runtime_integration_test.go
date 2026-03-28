package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

func TestRuntimeEndToEndPushStoreAndPull(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "wg-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(home)
	})
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-e2e")

	paths := config.NewPaths("proj-e2e")
	if err := broker.EnsureDirs(filepath.Dir(paths.DB), filepath.Dir(paths.Socket)); err != nil {
		t.Fatal(err)
	}

	b, err := broker.New(broker.Config{
		SocketPath: paths.Socket,
		DBPath:     paths.DB,
	})
	if err != nil {
		t.Fatal(err)
	}

	brokerDone := make(chan error, 1)
	go func() {
		brokerDone <- b.Serve()
	}()
	t.Cleanup(func() {
		_ = b.Shutdown()
		select {
		case <-brokerDone:
		case <-time.After(2 * time.Second):
		}
	})

	waitForCondition(t, "broker socket", func() bool {
		c, err := client.Connect(paths.Socket, 200*time.Millisecond)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})

	store, err := rt.NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	manager := rt.NewManager(store, rt.NewBrokerListenerFactory(), nil)
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		mgrCancel()
		_ = manager.Stop()
	})
	if err := manager.Start(mgrCtx); err != nil {
		t.Fatal(err)
	}

	if err := rt.RegisterWatch(config.NewPaths("proj-e2e"), rt.Watch{
		ProjectID: "proj-e2e",
		AgentName: "agent-a",
		Source:    "explicit",
	}); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, "runtime watch propagation", func() bool {
		return manager.WatchCount() == 1
	})

	sender, err := client.Connect(paths.Socket, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	if _, err := sender.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender"}); err != nil {
		t.Fatal(err)
	}
	resp, err := sender.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "agent-a",
		Message: "hello runtime",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("send failed: %s", resp.Error)
	}

	waitForCondition(t, "runtime store record", func() bool {
		rec, err := store.GetRecord("proj-e2e", "agent-a", 1)
		return err == nil && !rec.NotifiedAt.IsZero() && rec.Body == "hello runtime"
	})

	pulled, err := pullUnreadRecords(store, "proj-e2e", "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pulled) != 1 {
		t.Fatalf("pulled record count = %d, want 1", len(pulled))
	}
	if pulled[0].Body != "hello runtime" {
		t.Fatalf("pulled record body = %q, want hello runtime", pulled[0].Body)
	}

	unread, err := store.Unread("proj-e2e", "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread count after pull = %d, want 0", len(unread))
	}
}

func pullUnreadRecords(store *rt.Store, projectID, agentName string) ([]rt.DeliveryRecord, error) {
	records, err := store.Unread(projectID, agentName)
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		if err := store.MarkSurfaced(projectID, agentName, rec.MessageID); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func waitForCondition(t *testing.T, name string, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", name)
}

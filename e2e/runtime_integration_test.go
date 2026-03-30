package e2e

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/client"
	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

type catchUpCounter struct {
	rt.ListenerFactory
	count int64
}

func (c *catchUpCounter) CatchUp(w rt.Watch, handler rt.DeliveryHandler) error {
	atomic.AddInt64(&c.count, 1)
	return c.ListenerFactory.CatchUp(w, handler)
}

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

func TestRuntimeBrokerRestartReconnect(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "wg-e2e-reconnect-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(home)
	})
	t.Setenv("HOME", home)
	t.Setenv("WAGGLE_PROJECT_ID", "proj-reconnect")

	paths := config.NewPaths("proj-reconnect")
	if err := broker.EnsureDirs(filepath.Dir(paths.DB), filepath.Dir(paths.Socket)); err != nil {
		t.Fatal(err)
	}

	// Step 1: Start broker #1
	b1, err := broker.New(broker.Config{
		SocketPath: paths.Socket,
		DBPath:     paths.DB,
	})
	if err != nil {
		t.Fatal(err)
	}

	brokerDone1 := make(chan error, 1)
	go func() {
		brokerDone1 <- b1.Serve()
	}()
	var broker1Shutdown bool
	t.Cleanup(func() {
		if !broker1Shutdown {
			_ = b1.Shutdown()
			select {
			case <-brokerDone1:
			case <-time.After(2 * time.Second):
			}
		}
	})

	waitForCondition(t, "broker #1 socket", func() bool {
		c, err := client.Connect(paths.Socket, 200*time.Millisecond)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})

	// Step 2: Start manager
	store, err := rt.NewStore(paths.RuntimeDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	factory := &catchUpCounter{ListenerFactory: rt.NewBrokerListenerFactory()}
	manager := rt.NewManager(store, factory, nil)
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		mgrCancel()
		_ = manager.Stop()
	})
	if err := manager.Start(mgrCtx); err != nil {
		t.Fatal(err)
	}

	// Step 3: Register watch for agent
	if err := rt.RegisterWatch(paths, rt.Watch{
		ProjectID: "proj-reconnect",
		AgentName: "agent-reconnect",
		Source:    "explicit",
	}); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, "runtime watch propagation", func() bool {
		return manager.WatchCount() == 1
	})

	// Step 4: Send message #1 via broker #1
	sender1, err := client.Connect(paths.Socket, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sender1.Close()

	if _, err := sender1.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender1"}); err != nil {
		t.Fatal(err)
	}

	resp, err := sender1.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "agent-reconnect",
		Message: "message before restart",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("send message #1 failed: %s", resp.Error)
	}

	// Step 5: Verify message #1 is in store
	waitForCondition(t, "message #1 in store", func() bool {
		rec, err := store.GetRecord("proj-reconnect", "agent-reconnect", 1)
		return err == nil && !rec.NotifiedAt.IsZero() && rec.Body == "message before restart"
	})

	sender1.Close()

	// Step 6: Shutdown broker #1
	if err := b1.Shutdown(); err != nil {
		// Ignore shutdown errors from b1 - it's being shut down early
	}
	broker1Shutdown = true
	select {
	case <-brokerDone1:
	case <-time.After(2 * time.Second):
	}

	// Step 7: Verify socket is gone
	waitForCondition(t, "broker #1 socket gone", func() bool {
		c, err := client.Connect(paths.Socket, 200*time.Millisecond)
		if err != nil {
			return true // Connection failed = socket is gone
		}
		c.Close()
		return false
	})

	// Step 8: Start broker #2 on the same socket path
	b2, err := broker.New(broker.Config{
		SocketPath: paths.Socket,
		DBPath:     paths.DB,
	})
	if err != nil {
		t.Fatal(err)
	}

	brokerDone2 := make(chan error, 1)
	go func() {
		brokerDone2 <- b2.Serve()
	}()
	t.Cleanup(func() {
		_ = b2.Shutdown()
		select {
		case <-brokerDone2:
		case <-time.After(2 * time.Second):
		}
	})

	// Step 9: Wait for broker #2 socket to be available
	waitForCondition(t, "broker #2 socket", func() bool {
		c, err := client.Connect(paths.Socket, 200*time.Millisecond)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})

	// Step 10: Send message #2 via broker #2 immediately after restart.
	// This should land in broker storage even if the manager push session has
	// not reconnected yet; catch-up after reconnect must recover it.
	sender2, err := client.Connect(paths.Socket, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sender2.Close()

	if _, err := sender2.Send(protocol.Request{Cmd: protocol.CmdConnect, Name: "sender2"}); err != nil {
		t.Fatal(err)
	}

	resp, err = sender2.Send(protocol.Request{
		Cmd:     protocol.CmdSend,
		Name:    "agent-reconnect",
		Message: "message after restart",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("send message #2 failed: %s", resp.Error)
	}

	// Step 11: Verify message #2 is in store after reconnect catch-up.
	waitForConditionWithTimeout(t, "message #2 in store", 15*time.Second, func() bool {
		rec, err := store.GetRecord("proj-reconnect", "agent-reconnect", 2)
		return err == nil && !rec.NotifiedAt.IsZero() && rec.Body == "message after restart"
	})
	if n := atomic.LoadInt64(&factory.count); n == 0 {
		t.Fatal("CatchUp was never called — message #2 may have arrived via push instead of catch-up")
	}

	// Step 12: Verify both messages are in store
	rec1, err := store.GetRecord("proj-reconnect", "agent-reconnect", 1)
	if err != nil {
		t.Fatalf("failed to get message #1: %v", err)
	}
	if rec1.Body != "message before restart" {
		t.Fatalf("message #1 body = %q, want 'message before restart'", rec1.Body)
	}

	rec2, err := store.GetRecord("proj-reconnect", "agent-reconnect", 2)
	if err != nil {
		t.Fatalf("failed to get message #2: %v", err)
	}
	if rec2.Body != "message after restart" {
		t.Fatalf("message #2 body = %q, want 'message after restart'", rec2.Body)
	}

	// Step 13: Verify watch count is still 1 (no duplicates)
	if count := manager.WatchCount(); count != 1 {
		t.Fatalf("watch count = %d, want 1", count)
	}
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

func waitForConditionWithTimeout(t *testing.T, name string, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", name)
}

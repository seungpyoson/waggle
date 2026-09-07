package brokerstate

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
)

func endpointPaths(t *testing.T) (socket, pid string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(cwd, ".ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(dir, "b.sock"), filepath.Join(dir, "b.pid")
}

func activateFixture(t *testing.T, o *Owner) {
	t.Helper()
	if err := write(o, func(tx *WriteTx) error {
		_, err := tx.Exec("UPDATE cutover SET state = 'active' WHERE singleton = 1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedOwnerCannotBind(t *testing.T) {
	socket, pid := endpointPaths(t)
	o, _ := newOwner(t)
	if err := o.Bind(t.Context(), config.BrokerEndpoints{Socket: socket, PID: pid}); !errors.Is(err, ErrPrepared) {
		t.Fatal(err)
	}
	for _, path := range []string{socket, pid} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("prepared owner created %s: %v", path, err)
		}
	}
}

func TestFirstOwnerRejectsUnattributedEndpointFiles(t *testing.T) {
	socket, pid := endpointPaths(t)
	o, _ := newOwner(t)
	activateFixture(t, o)
	data := []byte("unexpected predecessor")
	if err := os.WriteFile(pid, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := o.Bind(t.Context(), config.BrokerEndpoints{Socket: socket, PID: pid}); err == nil {
		t.Fatal("accepted unowned endpoint file")
	}
	got, err := os.ReadFile(pid)
	if err != nil || string(got) != string(data) {
		t.Fatalf("endpoint altered: %q %v", got, err)
	}
}

func TestOwnerShutdownDrainsUnnamedConnectionsBeforeRemovingEndpoints(t *testing.T) {
	socket, pid := endpointPaths(t)
	o, cfg := newOwner(t)
	activateFixture(t, o)
	if err := o.Bind(t.Context(), config.BrokerEndpoints{Socket: socket, PID: pid}); err != nil {
		t.Fatal(err)
	}
	entered, returned := make(chan struct{}), make(chan struct{})
	serving := make(chan error, 1)
	go func() {
		serving <- o.Serve(context.Background(), func(conn net.Conn) {
			close(entered)
			io.Copy(io.Discard, conn)
			// The handler must finish while its endpoint files still exist.
			for _, path := range []string{socket, pid} {
				if _, err := os.Lstat(path); err != nil {
					t.Error(err)
				}
			}
			close(returned)
		})
	}()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-entered
	if err := o.Serve(t.Context(), func(net.Conn) {}); err == nil {
		t.Error("second service loop admitted")
	}
	o.BeginShutdown(nil)
	if err := o.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-returned
	if err := <-serving; err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{socket, pid} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("endpoint retained after release: %v", err)
		}
	}
	cfg.Action = config.OpenStore
	next, err := Acquire(t.Context(), cfg, processFixture{status: ProcessAlive})
	if err != nil {
		t.Fatal(err)
	}
	if next.state.generation != o.state.generation+1 {
		t.Fatal("generation did not advance")
	}
	next.BeginShutdown(nil)
	if err := next.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupRequiresRecordedFileIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.pid")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedFile(ownedFile{path: path}); err == nil {
		t.Fatal("removed file without creation evidence")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
		t.Fatalf("unverified file changed: %q %v", got, err)
	}
}

func TestBindLosingToDrainingClosesUnpublishedEndpoint(t *testing.T) {
	o, _ := newOwner(t)
	if err := write(o, func(tx *WriteTx) error {
		_, err := tx.Exec("UPDATE cutover SET state='active' WHERE singleton=1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// endpointPaths keeps the socket inside the 104-byte sun_path limit.
	socket, pid := endpointPaths(t)
	paths := config.BrokerEndpoints{Socket: socket, PID: pid}
	o.state.beforePublish = func() { o.BeginShutdown(errors.New("race")) }
	if err := o.Bind(t.Context(), paths); !errors.Is(err, ErrAdmissionClosed) {
		t.Fatalf("bind published after draining began: %v", err)
	}
	for _, path := range []string{paths.Socket, paths.PID} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unpublished endpoint left %s: %v", path, err)
		}
	}
	if err := o.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

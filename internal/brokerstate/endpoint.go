package brokerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

type ownedFile struct {
	path     string
	identity os.FileInfo
	removed  bool // cleanup progress survives a later failure; never re-deleted
}

type ownedEndpoint struct {
	listener *net.UnixListener
	files    []ownedFile
}

// Bind is the only IPC constructor. Filesystem work and listening happen
// outside the lifecycle mutex; publication is one decision against draining.
// If draining wins, the created endpoint is closed and removed unpublished.
func (o *Owner) Bind(ctx context.Context, paths config.BrokerEndpoints) error {
	if err := paths.Validate(); err != nil {
		return err
	}
	return o.Do(ctx, func(op *Operation) error {
		s := o.state
		s.mu.Lock()
		if s.endpoint != nil || s.binding {
			s.mu.Unlock()
			return fmt.Errorf("broker endpoints already bound")
		}
		s.binding = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.binding = false; s.mu.Unlock() }()
		if err := op.Write(ctx, func(tx *WriteTx) error {
			if err := tx.RequireActive(); err != nil {
				return err
			}
			if err := s.reclaimEndpoints(ctx, tx, paths); err != nil {
				return err
			}
			// Record provenance durably before any new endpoint file exists.
			// Identity comes from the fenced canonical owner, not the PID file.
			_, err := tx.Exec(`INSERT INTO endpoint_binding
				(singleton, instance_id, generation, boot_id, pid, process_start, bound_at)
				SELECT singleton, instance_id, generation, boot_id, pid, process_start, ?
				FROM broker_owner WHERE singleton = 1
				ON CONFLICT(singleton) DO UPDATE SET instance_id=excluded.instance_id,
				generation=excluded.generation, boot_id=excluded.boot_id, pid=excluded.pid,
				process_start=excluded.process_start, bound_at=excluded.bound_at`, time.Now().UTC().Format(time.RFC3339Nano))
			return err
		}); err != nil {
			return err
		}
		ep, err := createEndpoint(paths)
		if err != nil {
			if ep != nil {
				// Failed construction could not join/discard its resources. Keep
				// their cleanup progress and binding provenance under this owner.
				s.mu.Lock()
				s.endpoint = ep
				s.mu.Unlock()
				s.reportFatal(err)
			}
			return err
		}
		if s.beforePublish != nil {
			s.beforePublish()
		}
		s.mu.Lock()
		if s.phase != serving || s.endpoint != nil {
			s.mu.Unlock()
			derr := ep.discard()
			if derr == nil {
				if err := op.Write(context.WithoutCancel(ctx), s.deleteBinding); err != nil {
					s.recordFatal(err)
					return errors.Join(ErrAdmissionClosed, err)
				}
				return ErrAdmissionClosed
			}
			// An unresolved discard retains ownership and its binding record.
			s.recordFatal(derr)
			return errors.Join(ErrAdmissionClosed, derr)
		}
		s.endpoint = ep
		s.mu.Unlock()
		return nil
	})
}

func createEndpoint(paths config.BrokerEndpoints) (unresolved *ownedEndpoint, err error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.Socket, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("bind broker socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	ep := &ownedEndpoint{listener: listener}
	defer func() {
		if err != nil {
			if closeErr := ep.discard(); closeErr != nil {
				unresolved = ep
				err = errors.Join(err, fmt.Errorf("discard failed broker endpoint: %w", closeErr))
			}
		}
	}()
	info, err := os.Lstat(paths.Socket)
	if err != nil {
		return nil, fmt.Errorf("record broker socket identity: %w", err)
	}
	ep.files = append(ep.files, ownedFile{path: paths.Socket, identity: info})
	if err = os.Chmod(paths.Socket, 0700); err != nil {
		return nil, err
	}
	pid, err := os.OpenFile(paths.PID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("create broker PID file: %w", err)
	}
	// Identity comes from the descriptor this process created exclusively, and
	// is recorded before any further fallible step so the deferred discard
	// removes the file. If even that fails there is nothing recorded to remove,
	// so this branch removes the exclusively created path itself.
	info, statErr := pid.Stat()
	if statErr != nil {
		return nil, errors.Join(fmt.Errorf("record broker PID file identity: %w", statErr), pid.Close(), os.Remove(paths.PID))
	}
	ep.files = append(ep.files, ownedFile{path: paths.PID, identity: info})
	_, writeErr := fmt.Fprintln(pid, os.Getpid())
	if err = errors.Join(writeErr, pid.Close()); err != nil {
		return nil, err
	}
	return ep, nil
}

// discard closes an unpublished or failed endpoint and removes what it created.
func (ep *ownedEndpoint) discard() error {
	err := ep.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return errors.Join(err, ep.remove())
}

// remove deletes recorded files in order and records each success, so a
// later failure never repeats or forgets a completed removal.
func (ep *ownedEndpoint) remove() error {
	for i := range ep.files {
		file := &ep.files[i]
		if file.removed {
			continue
		}
		if err := removeOwnedFile(*file); err != nil {
			return err
		}
		file.removed = true
	}
	return nil
}

func (s *ownerState) reclaimEndpoints(ctx context.Context, tx *WriteTx, paths config.BrokerEndpoints) error {
	var leftovers []ownedFile
	for _, path := range []string{paths.Socket, paths.PID} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect broker endpoint: %w", err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected ownership or symlink at broker endpoint %s", path)
		}
		if path == paths.Socket && info.Mode()&os.ModeSocket == 0 || path == paths.PID && !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected file type at broker endpoint %s", path)
		}
		leftovers = append(leftovers, ownedFile{path: path, identity: info})
	}
	if len(leftovers) == 0 {
		return nil
	}
	var binding ProcessIdentity
	if err := tx.Scan(`SELECT boot_id, pid, process_start FROM endpoint_binding WHERE singleton = 1`, nil,
		&binding.BootID, &binding.PID, &binding.Start); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("leftover broker endpoints have no recorded binding owner")
		}
		return fmt.Errorf("read endpoint binding owner: %w", err)
	}
	if err := binding.validate(); err != nil {
		return err
	}
	status, err := s.inspector.Inspect(ctx, binding)
	if err != nil {
		return fmt.Errorf("verify endpoint binding owner exit: %w", err)
	}
	if status != ProcessExited {
		return fmt.Errorf("cannot reclaim endpoints without verified binding owner exit")
	}
	for _, file := range leftovers {
		if file.path == paths.PID {
			data, err := os.ReadFile(file.path)
			if err != nil {
				return err
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid != binding.PID {
				return fmt.Errorf("leftover PID file does not match recorded binding owner")
			}
		}
	}
	for _, file := range leftovers {
		if err := removeOwnedFile(file); err != nil {
			return err
		}
	}
	_, err = tx.Exec("DELETE FROM endpoint_binding WHERE singleton = 1")
	return err
}

// Maintenance generations must never erase another process's file provenance.
func (s *ownerState) deleteBinding(tx *WriteTx) error {
	_, err := tx.Exec("DELETE FROM endpoint_binding WHERE singleton = 1 AND instance_id = ? AND generation = ?", s.identity, s.generation)
	return err
}

func removeOwnedFile(file ownedFile) error {
	if file.identity == nil {
		return fmt.Errorf("endpoint identity was not established; ownership retained: %s", file.path)
	}
	current, err := os.Lstat(file.path)
	if err != nil {
		return fmt.Errorf("inspect owned endpoint %s: %w", file.path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, file.identity) {
		return fmt.Errorf("broker endpoint identity changed: %s", file.path)
	}
	return os.Remove(file.path)
}

func (s *ownerState) closeListener() error {
	s.mu.Lock()
	ep := s.endpoint
	s.mu.Unlock()
	if ep == nil {
		return nil // Maintenance ownership has no IPC endpoint.
	}
	err := ep.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// removeEndpoints runs after quiescence; nothing else touches the endpoint.
func (s *ownerState) removeEndpoints() error {
	s.mu.Lock()
	ep := s.endpoint
	s.mu.Unlock()
	if ep == nil {
		return nil
	}
	return ep.remove()
}

// Serve owns every accepted connection until its handler returns. Shutdown
// closes the listener, this loop closes clients, and the admitted operation
// ends only after all handlers finish. Registration names play no lifecycle role.
func (o *Owner) Serve(ctx context.Context, handle func(net.Conn)) error {
	return o.Do(ctx, func(op *Operation) error {
		s := o.state
		s.mu.Lock()
		ep := s.endpoint
		if s.serviceStarted {
			s.mu.Unlock()
			return fmt.Errorf("broker service may start only once per ownership generation")
		}
		if ep == nil || handle == nil {
			s.mu.Unlock()
			return fmt.Errorf("broker service requires ready endpoints and a handler")
		}
		s.serviceStarted = true
		s.mu.Unlock()
		var mu sync.Mutex
		var handlers sync.WaitGroup
		connections := make(map[net.Conn]struct{})
		defer func() {
			mu.Lock()
			for conn := range connections {
				conn.Close()
			}
			mu.Unlock()
			handlers.Wait()
		}()
		for {
			conn, err := ep.listener.Accept()
			if err != nil {
				select {
				case <-s.stop:
					return nil
				default:
					return fmt.Errorf("accept broker connection: %w", err)
				}
			}
			mu.Lock()
			connections[conn] = struct{}{}
			mu.Unlock()
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				handle(conn)
			}()
		}
	})
}

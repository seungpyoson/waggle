package brokerstate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

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
		bound := s.endpoint != nil
		s.mu.Unlock()
		if bound {
			return fmt.Errorf("broker endpoints already bound")
		}
		if err := op.Write(ctx, func(tx *WriteTx) error { return tx.RequireActive() }); err != nil {
			return err
		}
		if err := s.reclaimEndpoints(ctx, paths); err != nil {
			return err
		}
		ep, err := createEndpoint(paths)
		if err != nil {
			return err
		}
		if s.beforePublish != nil {
			s.beforePublish()
		}
		s.mu.Lock()
		if s.phase != serving || s.endpoint != nil {
			s.mu.Unlock()
			return errors.Join(ErrAdmissionClosed, ep.discard())
		}
		s.endpoint = ep
		s.mu.Unlock()
		return nil
	})
}

func createEndpoint(paths config.BrokerEndpoints) (_ *ownedEndpoint, err error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.Socket, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("bind broker socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	ep := &ownedEndpoint{listener: listener}
	defer func() {
		if err != nil {
			err = errors.Join(err, ep.discard())
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
	info, statErr := pid.Stat()
	if statErr != nil {
		return nil, errors.Join(statErr, pid.Close())
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

func (s *ownerState) reclaimEndpoints(ctx context.Context, paths config.BrokerEndpoints) error {
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
	if s.predecessor == nil {
		return fmt.Errorf("leftover broker endpoints have no recorded predecessor")
	}
	status, err := s.inspector.Inspect(ctx, *s.predecessor)
	if err != nil {
		return fmt.Errorf("verify endpoint predecessor exit: %w", err)
	}
	if status != ProcessExited {
		return fmt.Errorf("cannot reclaim endpoints without verified predecessor exit")
	}
	for _, file := range leftovers {
		if file.path == paths.PID {
			data, err := os.ReadFile(file.path)
			if err != nil {
				return err
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid != s.predecessor.PID {
				return fmt.Errorf("leftover PID file does not match recorded predecessor")
			}
		}
	}
	for _, file := range leftovers {
		if err := removeOwnedFile(file); err != nil {
			return err
		}
	}
	return nil
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

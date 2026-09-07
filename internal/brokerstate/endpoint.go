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
}

type ownedEndpoint struct {
	listener *net.UnixListener
	files    []ownedFile
	ready    bool
}

// Bind is the only IPC constructor. It records each created file immediately,
// so partial startup uses the same owned shutdown as a running broker.
func (o *Owner) Bind(ctx context.Context, paths config.BrokerEndpoints) error {
	if err := paths.Validate(); err != nil {
		return err
	}
	return o.Do(ctx, func(op *Operation) error {
		return op.Write(ctx, func(tx *WriteTx) error {
			if err := tx.RequireActive(); err != nil {
				return err
			}
			s := o.state
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.endpoint != nil {
				return fmt.Errorf("broker endpoints already bound")
			}
			if err := s.reclaimEndpoints(ctx, paths); err != nil {
				return err
			}
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: paths.Socket, Net: "unix"})
			if err != nil {
				return fmt.Errorf("bind broker socket: %w", err)
			}
			listener.SetUnlinkOnClose(false)
			s.endpoint = &ownedEndpoint{listener: listener, files: []ownedFile{{path: paths.Socket}}}
			info, err := os.Lstat(paths.Socket)
			if err != nil {
				return fmt.Errorf("record broker socket identity: %w", err)
			}
			s.endpoint.files[0].identity = info
			if err := os.Chmod(paths.Socket, 0700); err != nil {
				return err
			}
			pid, err := os.OpenFile(paths.PID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return fmt.Errorf("create broker PID file: %w", err)
			}
			s.endpoint.files = append(s.endpoint.files, ownedFile{path: paths.PID})
			info, statErr := pid.Stat()
			if statErr != nil {
				return errors.Join(statErr, pid.Close())
			}
			s.endpoint.files[1].identity = info
			_, writeErr := fmt.Fprintln(pid, os.Getpid())
			if err := errors.Join(writeErr, pid.Close()); err != nil {
				return err
			}
			s.endpoint.ready = true
			return nil
		})
	})
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
		leftovers = append(leftovers, ownedFile{path, info})
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
	defer s.mu.Unlock()
	if s.endpoint == nil {
		return nil
	} // Maintenance ownership has no IPC endpoint.
	err := s.endpoint.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *ownerState) removeEndpoints() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endpoint == nil {
		return nil
	}
	for _, file := range s.endpoint.files {
		if err := removeOwnedFile(file); err != nil {
			return err
		}
	}
	return nil
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
		if ep == nil || !ep.ready || handle == nil {
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
				case <-s.stopping:
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

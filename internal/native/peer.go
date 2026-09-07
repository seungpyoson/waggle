package native

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// Verification concerns the connected peer, never a PID or pathname preflight.
// No HTTP upgrade or provider input is written before this succeeds.
func verifyPeer(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var uid uint32
	var credentialErr error
	err = raw.Control(func(fd uintptr) { uid, credentialErr = peerUID(fd) })
	if err := errors.Join(err, credentialErr); err != nil {
		return err
	}
	if uid != uint32(os.Geteuid()) {
		return fmt.Errorf("native peer belongs to another OS user")
	}
	return nil
}

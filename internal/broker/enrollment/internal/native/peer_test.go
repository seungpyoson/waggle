//go:build darwin || linux

package native

import (
	"golang.org/x/sys/unix"
	"net"
	"os"
	"testing"
)

// A socketpair verifies kernel credential handling independently of pathname
// binding. It is not a substitute for the broker's blocked IPC entrypoint tests.
func TestKernelPeerIdentityOnConnectedUnixStream(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		file := os.NewFile(uintptr(fd), "native-peer-fixture")
		defer file.Close()
		conn, err := net.FileConn(file)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := verifyPeer(conn.(*net.UnixConn)); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if err := verifyPeer(conn.(*net.UnixConn)); err == nil {
			t.Fatal("closed connection supplied native peer identity")
		}
	}
}

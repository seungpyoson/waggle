package brokerstate

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOSProcessIdentity(t *testing.T) {
	i := OSProcessInspector{}
	self, err := i.Current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	status, err := i.Inspect(t.Context(), self)
	if err != nil || status != ProcessAlive {
		t.Fatalf("self: %v %v", status, err)
	}
	child := exec.Command("/bin/cat")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); child.Wait() })
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	recorded := ProcessIdentity{BootID: self.BootID, PID: child.Process.Pid, Start: darwinStart(proc)}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	status, err = i.Inspect(t.Context(), recorded)
	if err != nil || status != ProcessExited {
		t.Fatalf("exited child: %v %v", status, err)
	}
}

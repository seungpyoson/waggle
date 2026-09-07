package brokerstate

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func (OSProcessInspector) Current(ctx context.Context) (ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ProcessIdentity{}, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("%w: boot identity: %v", ErrIdentityUnavailable, err)
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("%w: own process identity: %v", ErrIdentityUnavailable, err)
	}
	p := ProcessIdentity{BootID: boot, PID: os.Getpid(), Start: darwinStart(proc)}
	return p, p.validate()
}

func (OSProcessInspector) Inspect(ctx context.Context, recorded ProcessIdentity) (ProcessStatus, error) {
	if err := recorded.validate(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		return 0, fmt.Errorf("%w: boot identity: %v", ErrIdentityUnavailable, err)
	}
	if boot != recorded.BootID {
		return ProcessExited, nil
	}
	// The slice API reports a successful empty kernel result for an exited PID.
	// The single-record API maps both missing and malformed results to EIO.
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pid", recorded.PID)
	if err != nil {
		return 0, fmt.Errorf("%w: predecessor process query: %v", ErrIdentityUnavailable, err)
	}
	if len(procs) == 0 {
		return ProcessExited, nil
	}
	if len(procs) != 1 || int(procs[0].Proc.P_pid) != recorded.PID {
		return 0, ErrIdentityUnavailable
	}
	if darwinStart(&procs[0]) != recorded.Start {
		return ProcessExited, nil
	}
	return ProcessAlive, nil
}

func darwinStart(p *unix.KinfoProc) string {
	return fmt.Sprintf("%d:%d", p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec)
}

package spawn

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/seungpyoson/waggle/internal/config"
)

type Terminal string

const (
	TerminalApp       Terminal = "terminal"
	ITerm2            Terminal = "iterm2"
	Gnome             Terminal = "gnome-terminal"
	XTerm             Terminal = "xterm"
	XTerminalEmulator Terminal = "x-terminal-emulator"
)

// Launcher contains one resolved terminal executable. Launch cannot select
// another terminal after an error.
type Launcher struct {
	terminal   Terminal
	executable string
	shell      string
}

func ResolveTerminal(name string) (*Launcher, error) {
	t := Terminal(name)
	var command string
	switch t {
	case TerminalApp, ITerm2:
		command = "osascript"
	case Gnome, XTerm, XTerminalEmulator:
		command = name
	default:
		return nil, fmt.Errorf("select a supported terminal with --terminal: terminal, iterm2, gnome-terminal, xterm, x-terminal-emulator")
	}
	path, err := resolveExecutable(command)
	if err != nil {
		return nil, fmt.Errorf("resolve selected terminal %q: %w", name, err)
	}
	shell, err := resolveExecutable(config.LaunchShell)
	if err != nil {
		return nil, fmt.Errorf("resolve launch shell: %w", err)
	}
	return &Launcher{terminal: t, executable: path, shell: shell}, nil
}

func resolveExecutable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// OpenTab requests one launch. Native enrollment establishes identity and readiness.
func (l *Launcher) OpenTab(ctx context.Context, command string, args []string, env map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, err := resolveExecutable(command)
	if err != nil {
		return fmt.Errorf("resolve selected agent: %w", err)
	}
	shellCmd, err := BuildShellCommand(env, executable, args)
	if err != nil {
		return fmt.Errorf("build terminal command: %w", err)
	}
	var launchArgs []string
	switch l.terminal {
	case TerminalApp, ITerm2:
		launchArgs = []string{"-e", BuildAppleScript(l.terminal, shellCmd)}
	case Gnome:
		launchArgs = []string{"--", l.shell, "-c", shellCmd}
	case XTerm, XTerminalEmulator:
		launchArgs = []string{"-e", l.shell, "-c", shellCmd}
	default:
		return fmt.Errorf("terminal launcher is not resolved")
	}
	if l.terminal == TerminalApp || l.terminal == ITerm2 {
		ctx, cancel := context.WithTimeout(ctx, config.Defaults.SpawnLaunchTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, l.executable, launchArgs...).Run(); err != nil {
			return fmt.Errorf("terminal launch result unconfirmed: %w", err)
		}
		return nil
	}
	launcher := exec.Command(l.executable, launchArgs...)
	if err := launcher.Start(); err != nil {
		return fmt.Errorf("start selected terminal: %w", err)
	}
	return launcher.Process.Release()
}

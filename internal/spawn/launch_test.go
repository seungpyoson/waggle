package spawn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakeLaunchCommands(t *testing.T, launcher string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log, probe := filepath.Join(dir, "launches"), filepath.Join(dir, "pid-probes")
	t.Setenv("PATH", dir)
	t.Setenv("SPAWN_LAUNCH_LOG", log)
	t.Setenv("SPAWN_PID_PROBE", probe)
	for name, body := range map[string]string{
		"osascript": launcher,
		"claude":    "exit 0\n",
		"sh":        "exit 0\n",
		"pgrep":     "printf x >> \"$SPAWN_PID_PROBE\"\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return log, probe
}

func TestOpenTabPropagatesLauncherFailureWithoutRetry(t *testing.T) {
	for _, term := range []Terminal{TerminalApp, ITerm2} {
		t.Run(map[Terminal]string{TerminalApp: "Terminal", ITerm2: "iTerm"}[term], func(t *testing.T) {
			log, probe := fakeLaunchCommands(t, "printf x >> \"$SPAWN_LAUNCH_LOG\"\nexit 7\n")
			if err := resolvedLauncher(t, term).OpenTab(t.Context(), "claude", nil, nil); err == nil {
				t.Fatal("launcher failure reported success")
			}
			if got, err := os.ReadFile(log); err != nil || string(got) != "x" {
				t.Fatalf("failed launch was retried: %q %v", got, err)
			}
			if _, err := os.Stat(probe); !os.IsNotExist(err) {
				t.Fatalf("failed launch entered PID lookup: %v", err)
			}
		})
	}
}

func TestOpenTabCancellationBeforeLaunchHasNoEffect(t *testing.T) {
	log, _ := fakeLaunchCommands(t, "printf x >> \"$SPAWN_LAUNCH_LOG\"\nexit 0\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := resolvedLauncher(t, TerminalApp).OpenTab(ctx, "claude", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("canceled request launched a terminal: %v", err)
	}
}

func TestOpenTabFailedLinuxLauncherDoesNotSwitchTerminals(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, name := range []string{"sh", "claude"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// LookPath accepts this executable file, but the kernel cannot execute it.
	if err := os.WriteFile(filepath.Join(dir, "gnome-terminal"), []byte("invalid executable"), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "alternate-launch")
	t.Setenv("SPAWN_LAUNCH_LOG", log)
	for _, name := range []string{"xterm", "x-terminal-emulator"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf x >> \"$SPAWN_LAUNCH_LOG\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := resolvedLauncher(t, Gnome).OpenTab(t.Context(), "claude", nil, nil); err == nil {
		t.Fatal("failed executable reported launch success")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("failure switched to another terminal: %v", err)
	}
}

func TestOpenTabDoesNotInferNativeIdentity(t *testing.T) {
	for _, term := range []Terminal{TerminalApp, ITerm2} {
		t.Run(map[Terminal]string{TerminalApp: "Terminal", ITerm2: "iTerm"}[term], func(t *testing.T) {
			log, probe := fakeLaunchCommands(t, "printf x >> \"$SPAWN_LAUNCH_LOG\"\nexit 0\n")
			if err := resolvedLauncher(t, term).OpenTab(t.Context(), "claude", nil, nil); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(log); err != nil || string(got) != "x" {
				t.Fatalf("launch requested more or less than once: %q %v", got, err)
			}
			if _, err := os.Stat(probe); !os.IsNotExist(err) {
				t.Fatalf("terminal launch inferred native identity through PID lookup: %v", err)
			}
		})
	}
}

func resolvedLauncher(t *testing.T, term Terminal) *Launcher {
	t.Helper()
	launcher, err := ResolveTerminal(string(term))
	if err != nil {
		t.Fatal(err)
	}
	return launcher
}

func TestSelectedTerminalLookupFailureCannotChooseAnother(t *testing.T) {
	for _, mode := range []string{"missing", "nonexecutable", "directory"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			selected := filepath.Join(dir, "gnome-terminal")
			switch mode {
			case "nonexecutable":
				if err := os.WriteFile(selected, []byte("no execute permission"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(selected, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"xterm", "x-terminal-emulator", "sh"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			launcher, err := ResolveTerminal("gnome-terminal")
			if err == nil || launcher != nil {
				t.Fatal("failed selected terminal was replaced")
			}
		})
	}
}

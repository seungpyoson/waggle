package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommandSpecForGOOS_UnsupportedPlatformIsSafeNoOp(t *testing.T) {
	_, err := commandSpecForGOOS("plan9", "title", "body")
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q, want unsupported platform message", err.Error())
	}
}

func TestCommandNotifier_NotifyPassesContextAndCommandSpec(t *testing.T) {
	ctx := context.WithValue(context.Background(), "key", "value")
	notifier := &CommandNotifier{
		goos: "darwin",
		run: func(runCtx context.Context, spec commandSpec) error {
			if got := runCtx.Value("key"); got != "value" {
				t.Fatalf("context value = %v, want value", got)
			}
			if spec.name != "osascript" {
				t.Fatalf("command = %q, want osascript", spec.name)
			}
			if len(spec.args) != 2 {
				t.Fatalf("args len = %d, want 2", len(spec.args))
			}
			if !strings.Contains(spec.args[1], "display notification") {
				t.Fatalf("script = %q, want display notification", spec.args[1])
			}
			return nil
		},
	}

	if err := notifier.Notify(ctx, "title", "body"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandNotifier_NotifyReturnsRunnerError(t *testing.T) {
	notifier := &CommandNotifier{
		goos: "darwin",
		run: func(context.Context, commandSpec) error {
			return errors.New("boom")
		},
	}

	err := notifier.Notify(context.Background(), "title", "body")
	if err == nil {
		t.Fatal("expected runner error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %q, want wrapped runner error", err.Error())
	}
}

func TestCommandNotifier_NotifyUnsupportedPlatformIsSafeNoOp(t *testing.T) {
	called := false
	notifier := &CommandNotifier{
		goos: "plan9",
		run: func(context.Context, commandSpec) error {
			called = true
			return nil
		},
	}

	if err := notifier.Notify(context.Background(), "title", "body"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
	if called {
		t.Fatal("runner should not be called on unsupported platform")
	}
}

func TestCommandSpecForGOOS_LinuxUsesNotifySend(t *testing.T) {
	spec, err := commandSpecForGOOS("linux", "title", "body")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.name != "notify-send" {
		t.Fatalf("command = %q, want notify-send", spec.name)
	}
	if len(spec.args) != 2 || spec.args[0] != "title" || spec.args[1] != "body" {
		t.Fatalf("args = %#v, want [title body]", spec.args)
	}
}

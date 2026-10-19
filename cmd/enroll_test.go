package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
)

func enrollmentInput(t *testing.T, text string) *os.File {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { read.Close() })
	go func() {
		defer write.Close()
		_, _ = write.WriteString(text)
	}()
	return read
}

func TestEnrollmentMissingBrokerIsDiagnosticAndNonFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("WAGGLE_PROJECT_ID", "hook-without-service")
	t.Setenv(config.ClaudeMessagingSocketEnv, filepath.Join(home, "native.sock"))
	envPath := filepath.Join(home, "native-environment")
	t.Setenv(config.ClaudeEnvironmentFileEnv, envPath)
	original := os.Stdin
	os.Stdin = enrollmentInput(t, `{"hook_event_name":"SessionStart","session_id":"fresh-session"}`)
	t.Cleanup(func() { os.Stdin = original })
	stdout, stderr := executeRootCommandForTest(t, "enroll", "claude-code")
	if stdout != "" || !strings.Contains(stderr, "start the broker and restart this provider session") {
		t.Fatal("missing service was not reported as nonfatal enrollment unavailability")
	}
	if _, err := os.Lstat(envPath); !os.IsNotExist(err) {
		t.Fatal("missing service created credential state")
	}
	if _, err := os.Lstat(filepath.Join(home, ".waggle")); !os.IsNotExist(err) {
		t.Fatal("enrollment created a broker or alternate store")
	}
}

func TestEnrollmentDeadlineInterruptsIncompleteHookInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.ClaudeMessagingSocketEnv, filepath.Join(home, "native.sock"))
	t.Setenv(config.ClaudeEnvironmentFileEnv, filepath.Join(home, "native-environment"))
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- enrollHook(ctx, "claude-code", read) }()
	select {
	case err := <-done:
		if err == nil || ctx.Err() == nil {
			t.Fatal("incomplete hook input did not fail at its deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("hook remained blocked after its configured deadline")
	}
}

func TestEnrollmentRejectsAmbiguousOrOversizedNativeInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.ClaudeMessagingSocketEnv, filepath.Join(home, "native.sock"))
	t.Setenv(config.ClaudeEnvironmentFileEnv, filepath.Join(home, "native-environment"))
	for _, input := range []string{
		`{"hook_event_name":"SessionStart","session_id":"a"} {"session_id":"b"}`,
		`{"hook_event_name":"SessionStart"}`,
		`{"hook_event_name":"OtherEvent","session_id":"a"}`,
		strings.Repeat("x", int(config.Defaults.MaxMessageSize)+1),
	} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		err := enrollHook(ctx, "claude-code", enrollmentInput(t, input))
		cancel()
		if err == nil {
			t.Fatal("invalid native input accepted")
		}
	}
}

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/install"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestStartRejectsInvalidNativeConfigurationBeforeCreatingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Chdir(t.TempDir())
	_, _, err := executeRootCommandForTestWithError(t, "start", "--foreground", "--initialize", "--codex-app-server", "relative.sock")
	if err == nil || !strings.Contains(err.Error(), "endpoint must be absolute") {
		t.Fatalf("invalid native prerequisite was not rejected first: %v", err)
	}
	items, err := os.ReadDir(home)
	if err != nil || len(items) != 0 {
		t.Fatal("invalid provider configuration created broker state")
	}
}

func TestExecuteRootCommandForTestDoesNotLeakInstallUninstallFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stdout, stderr := executeRootCommandForTest(t, "install", "codex", "--uninstall")
	if stderr != "" {
		t.Fatalf("install uninstall stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"Codex integration removed"`) {
		t.Fatalf("install uninstall stdout = %q, want removal message", stdout)
	}

	stdout, stderr = executeRootCommandForTest(t, "install", "codex")
	if stderr != "" {
		t.Fatalf("install stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"codex integration files installed; native messaging is not yet available in this build."`) {
		t.Fatalf("install stdout = %q, want install message", stdout)
	}
}

func TestUninstallAllPreservesState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if stdout, stderr := executeRootCommandForTest(t, "install", "codex"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install codex stdout=%q stderr=%q", stdout, stderr)
	}
	if stdout, stderr := executeRootCommandForTest(t, "install", "gemini"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install gemini stdout=%q stderr=%q", stdout, stderr)
	}
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".waggle", "runtime", "runtime.db"), []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := executeRootCommandForTest(t, "uninstall", "--all")
	if stderr != "" {
		t.Fatalf("uninstall stderr = %q, want empty", stderr)
	}
	for _, want := range []string{"claude-code", "codex", "gemini", "auggie", "augment", "shell-hook"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("uninstall stdout = %q, want action for %q", stdout, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime")); !os.IsNotExist(err) {
		t.Fatalf("Codex skill should be removed, stat err = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(home, ".waggle", "runtime", "runtime.db")); err != nil || string(data) != "state" {
		t.Fatalf("uninstall changed canonical state: %q, %v", data, err)
	}
}

func TestUninstallAllDryRunDoesNotMutate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if stdout, stderr := executeRootCommandForTest(t, "install", "codex"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install codex stdout=%q stderr=%q", stdout, stderr)
	}
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := executeRootCommandForTest(t, "uninstall", "--all", "--dry-run")
	if stderr != "" {
		t.Fatalf("uninstall dry-run stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `"dry_run": true`) || !strings.Contains(stdout, "would remove integration") {
		t.Fatalf("uninstall dry-run stdout = %q, want dry-run planned actions", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime", "SKILL.md")); err != nil {
		t.Fatalf("Codex skill should remain after dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".waggle")); err != nil {
		t.Fatalf(".waggle should remain after dry-run: %v", err)
	}
}

func TestRunUninstallAllAttemptsEveryIntegrationBeforeReturningErrors(t *testing.T) {
	originalTargets := uninstallTargets
	t.Cleanup(func() {
		uninstallTargets = originalTargets
	})

	var called []string
	uninstallTargets = []struct {
		name string
		fn   func() error
	}{
		{
			name: "first",
			fn: func() error {
				called = append(called, "first")
				return fmt.Errorf("first failed")
			},
		},
		{
			name: "second",
			fn: func() error {
				called = append(called, "second")
				return nil
			},
		},
		{
			name: "third",
			fn: func() error {
				called = append(called, "third")
				return fmt.Errorf("third failed")
			},
		},
	}

	actions, err := runUninstall(false)
	if err == nil {
		t.Fatal("runUninstall error = nil, want joined uninstall errors")
	}
	if !strings.Contains(err.Error(), "uninstall first") || !strings.Contains(err.Error(), "uninstall third") {
		t.Fatalf("runUninstall error = %v, want both uninstall failures", err)
	}
	if strings.Join(called, ",") != "first,second,third" {
		t.Fatalf("called targets = %v, want all targets attempted", called)
	}
	if len(actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(actions))
	}
}

func TestUninstallAllReportsActionsAfterIntegrationErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".waggle", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalTargets := uninstallTargets
	t.Cleanup(func() {
		uninstallTargets = originalTargets
	})

	uninstallTargets = []struct {
		name string
		fn   func() error
	}{
		{
			name: "failing",
			fn: func() error {
				return fmt.Errorf("integration failed")
			},
		},
	}

	stdout, stderr, err := executeRootCommandForTestWithError(t, "uninstall", "--all")
	if err == nil {
		t.Fatal("uninstall error = nil, want integration failure")
	}
	if stderr != "" {
		t.Fatalf("uninstall stderr = %q, want empty", stderr)
	}

	var resp struct {
		OK      bool             `json:"ok"`
		Code    string           `json:"code"`
		Error   string           `json:"error"`
		Actions []map[string]any `json:"actions"`
	}
	if unmarshalErr := json.Unmarshal([]byte(stdout), &resp); unmarshalErr != nil {
		t.Fatalf("unmarshal uninstall response: %v\nstdout=%s", unmarshalErr, stdout)
	}
	if resp.OK || resp.Code != "UNINSTALL_ERROR" || !strings.Contains(resp.Error, "uninstall failing") {
		t.Fatalf("uninstall response = %+v, want structured uninstall error", resp)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("actions len = %d, want the integration attempt", len(resp.Actions))
	}
	if _, statErr := os.Stat(filepath.Join(home, ".waggle")); statErr != nil {
		t.Fatalf("uninstall changed state directory: %v", statErr)
	}
}

func TestUninstallRejectsPurgeBeforeRemovingIntegrations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if stdout, stderr := executeRootCommandForTest(t, "install", "codex"); stderr != "" || !strings.Contains(stdout, `"ok": true`) {
		t.Fatalf("install codex stdout=%q stderr=%q", stdout, stderr)
	}
	_, _, err := executeRootCommandForTestWithError(t, "uninstall", "--all", "--purge")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --purge") {
		t.Fatalf("uninstall --purge error = %v, want unsupported flag", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime", "SKILL.md")); err != nil {
		t.Fatalf("rejected purge removed integration: %v", err)
	}
}

func TestInstallNoArgsInstallsDetectedIntegrations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := executeRootCommandForTest(t, "install")
	if stderr != "" {
		t.Fatalf("install stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, install.InstallMessage(install.PlatformCodex)) {
		t.Fatalf("install stdout = %q, want Codex install message", stdout)
	}
	var response struct {
		OK                bool                    `json:"ok"`
		InstalledAdapters []string                `json:"installed_adapters"`
		Results           []install.InstallResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("install stdout is not a single JSON object: %v\n%s", err, stdout)
	}
	if !response.OK || len(response.InstalledAdapters) != 1 || response.InstalledAdapters[0] != install.PlatformCodex {
		t.Fatalf("install response = %+v, want ok with only Codex installed", response)
	}
	for _, unwanted := range []string{install.InstallMessage(install.PlatformClaudeCode), install.InstallMessage(install.PlatformGemini), install.InstallMessage(install.PlatformAuggie), install.InstallMessage(install.PlatformAugment)} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("install stdout = %q, did not expect %q", stdout, unwanted)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "waggle-runtime", "SKILL.md")); err != nil {
		t.Fatalf("Codex skill not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "GEMINI.md")); !os.IsNotExist(err) {
		t.Fatalf("Gemini should not have been installed, stat err = %v", err)
	}
}

func TestInstallNoArgsReportsPartialResultsOnError(t *testing.T) {
	originalInstallDetected := installDetected
	t.Cleanup(func() {
		installDetected = originalInstallDetected
	})

	installDetected = func() ([]install.InstallResult, error) {
		return []install.InstallResult{{
			Platform: install.PlatformCodex,
			Message:  "codex integration files installed; native messaging is not yet available in this build.",
		}}, errors.New("install gemini: permission denied")
	}

	stdout, stderr, err := executeRootCommandForTestWithError(t, "install")
	if err == nil {
		t.Fatal("install returned nil error, want partial install error")
	}
	if stdout != "" {
		t.Fatalf("install stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		`"ok": false`,
		`"code": "INSTALL_ERROR"`,
		`"error": "install gemini: permission denied"`,
		`"installed_adapters":`,
		`"codex"`,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("install stderr = %q, want %q", stderr, want)
		}
	}
}

func TestInstallTooManyArgsReportsCobraError(t *testing.T) {
	stdout, stderr, err := executeRootCommandForTestWithError(t, "install", "codex", "gemini")
	if err == nil {
		t.Fatal("install returned nil error, want too-many-args error")
	}
	if stdout != "" {
		t.Fatalf("install stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "accepts at most 1 arg") {
		t.Fatalf("install stderr = %q, want Cobra argument error", stderr)
	}
}

type commandTestState struct {
	paths config.Paths
}

func captureCommandTestState() commandTestState {
	return commandTestState{
		paths: paths,
	}
}

func (s commandTestState) restore() {
	paths = s.paths
	resetCommandFlagState(rootCmd)
}

func resetCommandFlagState(cmd *cobra.Command) {
	cmd.Flags().VisitAll(resetFlagToDefault)
	cmd.PersistentFlags().VisitAll(resetFlagToDefault)
	for _, child := range cmd.Commands() {
		resetCommandFlagState(child)
	}
}

func resetFlagToDefault(flag *pflag.Flag) {
	_ = flag.Value.Set(flag.DefValue)
	flag.Changed = false
}

func executeRootCommandForTest(t *testing.T, args ...string) (string, string) {
	t.Helper()
	stdout, stderr, err := executeRootCommandForTestWithError(t, args...)
	if err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}

	return stdout, stderr
}

func executeRootCommandForTestWithError(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	originalState := captureCommandTestState()
	defer func() {
		originalState.restore()
		installCmd.SilenceErrors = false
		installCmd.SilenceUsage = false
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
		rootCmd.SetArgs(nil)
	}()

	resetCommandFlagState(rootCmd)
	paths = config.Paths{}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

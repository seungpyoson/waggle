package install

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungpyoson/waggle/internal/fsutil"
)

func TestInstallShellHook_WritesScript(t *testing.T) {
	home := t.TempDir()
	if err := installShellHook(home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".waggle", "shell-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "__waggle_check") {
		t.Fatal("missing function")
	}
	if !strings.Contains(string(data), `case "$_apid" in`) {
		t.Fatal("missing numeric PPID guard case")
	}
	if !strings.Contains(string(data), `*[!0-9]*) return 0 ;;`) {
		t.Fatal("missing non-numeric PPID early return")
	}
}

func TestShellHookBlock_OnlyExportsBashEnv(t *testing.T) {
	want := strings.Join([]string{
		shellHookBegin,
		`[ -f "$HOME/.waggle/shell-hook.sh" ] && source "$HOME/.waggle/shell-hook.sh"`,
		`export BASH_ENV="$HOME/.waggle/shell-hook.sh"`,
		shellHookEnd,
	}, "\n")

	if shellHookBlock != want {
		t.Fatalf("shellHookBlock = %q, want %q", shellHookBlock, want)
	}
	if strings.Contains(shellHookBlock, "WAGGLE_AGENT_PPID") {
		t.Fatal("shellHookBlock should not export WAGGLE_AGENT_PPID")
	}
}

func TestInstallShellHook_ZshenvWithMarkers(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".zshenv"), []byte("# existing\n"), 0o644)
	installShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	s := string(data)
	if !strings.Contains(s, "WAGGLE-SHELL-HOOK-BEGIN") {
		t.Fatal("missing begin marker")
	}
	if !strings.Contains(s, "WAGGLE-SHELL-HOOK-END") {
		t.Fatal("missing end marker")
	}
	if !strings.Contains(s, "shell-hook.sh") {
		t.Fatal("missing source line")
	}
	if !strings.Contains(s, "# existing") {
		t.Fatal("lost existing content")
	}
}

func TestInstallShellHook_ScriptRefreshesBothMappings(t *testing.T) {
	home := t.TempDir()
	if err := installShellHook(home); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".waggle", "shell-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, `touch "$_sm" "$_pm"`) {
		t.Fatal("shell hook should refresh both session and pointer mappings")
	}
	if !strings.Contains(content, ".c-") {
		t.Fatal("shell hook should handle consumed orphan files")
	}
}

func TestInstallShellHook_ValidatesSessionTokens(t *testing.T) {
	home := t.TempDir()
	if err := installShellHook(home); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".waggle", "shell-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `case "$_wa" in *[!A-Za-z0-9_-]*) return 0 ;; esac`) {
		t.Fatal("shell hook should validate agent token from session file")
	}
	if !strings.Contains(content, `case "$_wp" in *[!A-Za-z0-9_-]*) return 0 ;; esac`) {
		t.Fatal("shell hook should validate project token from session file")
	}
}

func TestInstallShellHook_BashrcWithMarkers(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# bash\n"), 0o644)
	installShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if !strings.Contains(string(data), "WAGGLE-SHELL-HOOK-BEGIN") {
		t.Fatal("missing marker in .bashrc")
	}
}

func TestInstallShellHook_Idempotent(t *testing.T) {
	home := t.TempDir()
	installShellHook(home)
	installShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	if strings.Count(string(data), "WAGGLE-SHELL-HOOK-BEGIN") != 1 {
		t.Fatal("marker duplicated")
	}
}

func TestUpsertShellHookBlock_ReturnsErrorOnUnreadableFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".zshenv")
	const content = "# existing\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}

	err := upsertShellHookBlock(path, home)

	if restoreErr := os.Chmod(path, 0o600); restoreErr != nil {
		t.Fatalf("restore perms: %v", restoreErr)
	}
	if err == nil {
		t.Fatal("expected read error for unreadable file")
	}
	if !strings.Contains(err.Error(), "read "+path) {
		t.Fatalf("expected read error mentioning path, got %v", err)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != content {
		t.Fatalf("file content changed: got %q want %q", got, content)
	}
}

func TestUninstallShellHook_RemovesBlock(t *testing.T) {
	home := t.TempDir()
	installShellHook(home)
	uninstallShellHook(home)
	for _, f := range []string{".zshenv", ".bashrc"} {
		data, _ := os.ReadFile(filepath.Join(home, f))
		if strings.Contains(string(data), "waggle") {
			t.Fatalf("%s still has waggle content", f)
		}
	}
}

func TestUninstallShellHook_PreservesOtherContent(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".zshenv"), []byte("# keep this\n"), 0o644)
	installShellHook(home)
	uninstallShellHook(home)
	data, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	if !strings.Contains(string(data), "# keep this") {
		t.Fatal("lost existing content")
	}
}

func TestRemoveShellHookBlock_ReturnsErrorOnRewriteFailure(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".zshenv")
	content := strings.Join([]string{
		"# keep this",
		shellHookBlock,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(home, 0o700)
	})

	err := removeShellHookBlock(path, home)
	if err == nil {
		t.Fatal("expected rewrite error, got nil")
	}
	if !strings.Contains(err.Error(), "write "+path) {
		t.Fatalf("expected write error mentioning path, got %v", err)
	}
}

func TestUninstallShellHook_LogsSafeRemoveWarningAndContinues(t *testing.T) {
	home := t.TempDir()
	waggleReal := filepath.Join(home, ".waggle-real")
	if err := os.MkdirAll(waggleReal, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte("# keep this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waggleReal, "shell-hook.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(waggleReal, filepath.Join(home, ".waggle")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	if err := uninstallShellHook(home); err != nil {
		t.Fatalf("uninstallShellHook returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "warning: remove shell hook script") {
		t.Fatalf("expected safeRemove warning log, got %q", buf.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".waggle")); err != nil {
		t.Fatalf("symlink should remain after best-effort uninstall: %v", err)
	}
}

func TestInstallShellHook_RejectsAncestorSymlink(t *testing.T) {
	home := t.TempDir()
	realDir := filepath.Join(home, ".waggle-real")
	os.MkdirAll(realDir, 0o700)
	os.Symlink(realDir, filepath.Join(home, ".waggle"))

	err := installShellHook(home)
	if err == nil {
		t.Fatal("expected error for ancestor symlink, got nil")
	}
	if !strings.Contains(err.Error(), "ancestor symlink") {
		t.Fatalf("expected ancestor symlink error, got: %v", err)
	}
}

func TestAtomicWriteFile_BasicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello world\n")
	if err := atomicWriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("got %q, want %q", got, content)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm = %o, want 644", perm)
	}
}

func TestAtomicWriteFile_NoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	atomicWriteFile(path, []byte("data"), 0o644)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".waggle-tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestHasAncestorSymlink_DetectsSymlinkedParent(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	os.MkdirAll(realDir, 0o700)
	linkDir := filepath.Join(base, "link")
	os.Symlink(realDir, linkDir)

	if !fsutil.HasAncestorSymlink(filepath.Join(linkDir, "file.txt"), base) {
		t.Fatal("should detect symlinked parent")
	}
}

func TestHasAncestorSymlink_NoSymlink(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "subdir")
	os.MkdirAll(dir, 0o700)
	if fsutil.HasAncestorSymlink(filepath.Join(dir, "file.txt"), base) {
		t.Fatal("should not detect symlink in real dir")
	}
}

func TestHasAncestorSymlink_AbsolutePathsNeverRelError(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "subdir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file.txt")

	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatalf("filepath.Rel returned error for absolute paths: %v", err)
	}
	if rel != filepath.Join("subdir", "file.txt") {
		t.Fatalf("rel = %q, want %q", rel, filepath.Join("subdir", "file.txt"))
	}
	if fsutil.HasAncestorSymlink(path, base) {
		t.Fatal("absolute path under root should not be treated as a symlink escape")
	}
}

func TestHasAncestorSymlink_RejectsPathEscapingRoot(t *testing.T) {
	base := t.TempDir()
	escaped := filepath.Join(base, "..", "etc", "passwd")
	if !fsutil.HasAncestorSymlink(escaped, base) {
		t.Fatal("should reject path escaping root via ..")
	}
}

func TestHasAncestorSymlink_DeeplyNestedSymlink(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real", "deep")
	os.MkdirAll(realDir, 0o700)
	os.MkdirAll(filepath.Join(base, "a"), 0o700)
	os.Symlink(realDir, filepath.Join(base, "a", "link"))
	if !fsutil.HasAncestorSymlink(filepath.Join(base, "a", "link", "file.txt"), base) {
		t.Fatal("should detect deeply nested symlink")
	}
}

func TestUpsertShellHookBlock_RejectsLeafSymlink(t *testing.T) {
	home := t.TempDir()
	realFile := filepath.Join(home, ".real-zshenv")
	os.WriteFile(realFile, []byte("# real\n"), 0o644)
	os.Symlink(realFile, filepath.Join(home, ".zshenv"))

	err := upsertShellHookBlock(filepath.Join(home, ".zshenv"), home)
	if err == nil {
		t.Fatal("expected error for leaf symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}
}

func TestAtomicWriteFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("old"), 0o644)
	if err := atomicWriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("got %q, want %q", got, "new")
	}
}

func TestSafeWriteFile_RejectsLeafSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := safeWriteFile(link, []byte("mutated"), 0o644, base)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("target content = %q, want %q", got, "original")
	}
}

func TestSafeRemoveAll_RejectsLeafSymlink(t *testing.T) {
	base := t.TempDir()
	targetDir := filepath.Join(base, "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(targetDir, link); err != nil {
		t.Fatal(err)
	}

	err := safeRemoveAll(link, base)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}

	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("target dir should remain: %v", err)
	}
}

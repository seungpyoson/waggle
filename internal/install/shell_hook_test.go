package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

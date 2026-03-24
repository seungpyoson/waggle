package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// C8, C5 partial: no .git anywhere → error mentions WAGGLE_ROOT
func TestFindProjectRoot_NoGitDir(t *testing.T) {
	tmp := t.TempDir()
	child := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := FindProjectRoot(child)
	if err == nil {
		t.Fatal("expected error when no .git exists")
	}
	if got := err.Error(); !regexp.MustCompile(`WAGGLE_ROOT`).MatchString(got) {
		t.Fatalf("error should mention WAGGLE_ROOT, got: %s", got)
	}
}

// Walk up from subdir to find .git in parent
func TestFindProjectRoot_FromSubdir(t *testing.T) {
	tmp := t.TempDir()
	// create .git dir at root
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := FindProjectRoot(child)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolve tmp too for comparison (macOS /private/var symlinks)
	wantRoot, _ := filepath.EvalSymlinks(tmp)
	if root != wantRoot {
		t.Fatalf("got %q, want %q", root, wantRoot)
	}
}

// C5: WAGGLE_ROOT overrides .git detection completely
func TestFindProjectRoot_EnvOverride(t *testing.T) {
	tmp := t.TempDir()
	override := filepath.Join(tmp, "override")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WAGGLE_ROOT", override)

	unrelated := filepath.Join(tmp, "unrelated")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := FindProjectRoot(unrelated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(override)
	if root != wantRoot {
		t.Fatalf("got %q, want %q", root, wantRoot)
	}
}

// WAGGLE_ROOT pointing to a file (not a directory) → error
func TestFindProjectRoot_EnvNotDirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAGGLE_ROOT", file)

	_, err := FindProjectRoot("/anywhere")
	if err == nil {
		t.Fatal("expected error when WAGGLE_ROOT is a file")
	}
}

// FindProjectRoot works with relative startDir
func TestFindProjectRoot_RelativePath(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(tmp, "sub")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	// chdir to child, call with "."
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	os.Chdir(child)

	root, err := FindProjectRoot(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("root should be absolute, got %q", root)
	}
}

// NewPaths normalizes path variations to the same hash
func TestPaths_PathNormalization(t *testing.T) {
	a := NewPaths("/tmp/proj")
	b := NewPaths("/tmp/proj/")
	c := NewPaths("/tmp/proj/.")
	if a.Socket != b.Socket {
		t.Fatalf("trailing slash produced different socket: %q vs %q", a.Socket, b.Socket)
	}
	if a.Socket != c.Socket {
		t.Fatalf("trailing /. produced different socket: %q vs %q", a.Socket, c.Socket)
	}
}

// C4: Symlinked paths to same dir produce same root
func TestFindProjectRoot_SymlinkResolved(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	if err := os.MkdirAll(filepath.Join(real, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	rootFromReal, err := FindProjectRoot(real)
	if err != nil {
		t.Fatalf("real: %v", err)
	}
	rootFromLink, err := FindProjectRoot(link)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if rootFromReal != rootFromLink {
		t.Fatalf("symlink not resolved: real=%q link=%q", rootFromReal, rootFromLink)
	}
}

// C7: All Paths fields are non-empty and absolute (Socket empty when HOME missing)
func TestPaths_AllFieldsAbsolute(t *testing.T) {
	p := NewPaths("/tmp/testroot")
	v := reflect.ValueOf(p)
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := typ.Field(i)
		val := v.Field(i).String()
		if field.Name == "Socket" && val == "" {
			continue // Socket is empty when HOME is not set
		}
		if val == "" {
			t.Errorf("field %s is empty", field.Name)
		}
		if !filepath.IsAbs(val) {
			t.Errorf("field %s is not absolute: %q", field.Name, val)
		}
	}
}

// Socket is empty when HOME is not set (no silent fallback to bad path)
func TestPaths_SocketEmptyWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	p := NewPaths("/tmp/proj")
	if p.Socket != "" {
		t.Fatalf("expected empty socket without HOME, got %q", p.Socket)
	}
}

// All fields derived correctly from root
func TestPaths_DerivedFromRoot(t *testing.T) {
	p := NewPaths("/tmp/proj")
	if p.Root != "/tmp/proj" {
		t.Fatalf("Root = %q, want /tmp/proj", p.Root)
	}
	if p.WaggleDir != filepath.Join("/tmp/proj", Defaults.DirName) {
		t.Fatalf("WaggleDir = %q", p.WaggleDir)
	}
	if p.DB != filepath.Join("/tmp/proj", Defaults.DirName, Defaults.DBFile) {
		t.Fatalf("DB = %q", p.DB)
	}
	if p.Config != filepath.Join("/tmp/proj", Defaults.DirName, Defaults.ConfigFile) {
		t.Fatalf("Config = %q", p.Config)
	}
	if p.PID != filepath.Join("/tmp/proj", Defaults.DirName, Defaults.PIDFile) {
		t.Fatalf("PID = %q", p.PID)
	}
	if p.Lock != filepath.Join("/tmp/proj", Defaults.DirName, Defaults.LockFile) {
		t.Fatalf("Lock = %q", p.Lock)
	}
	if p.Log != filepath.Join("/tmp/proj", Defaults.DirName, Defaults.LogFile) {
		t.Fatalf("Log = %q", p.Log)
	}
	// Socket uses home dir + Defaults.DirName (not a duplicate literal)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	wantSocketDir := filepath.Join(home, Defaults.DirName, "sockets")
	if !filepath.HasPrefix(p.Socket, wantSocketDir) {
		t.Fatalf("Socket %q not under %q", p.Socket, wantSocketDir)
	}
}

// C2: Same root → same socket
func TestPaths_SocketHashDeterministic(t *testing.T) {
	a := NewPaths("/tmp/x")
	b := NewPaths("/tmp/x")
	if a.Socket != b.Socket {
		t.Fatalf("same root produced different sockets: %q vs %q", a.Socket, b.Socket)
	}
}

// C3: Different roots → different sockets
func TestPaths_SocketHashDiffers(t *testing.T) {
	a := NewPaths("/tmp/a")
	b := NewPaths("/tmp/b")
	if a.Socket == b.Socket {
		t.Fatal("different roots produced same socket")
	}
}

// C6: Socket hash is exactly 12 hex chars
func TestPaths_SocketHashLength(t *testing.T) {
	p := NewPaths("/tmp/proj")
	// Socket path: .../<DirName>/sockets/<12hex>/broker.sock
	dir := filepath.Base(filepath.Dir(p.Socket))
	re := regexp.MustCompile(`^[0-9a-f]+$`)
	if !re.MatchString(dir) {
		t.Fatalf("socket parent dir %q is not hex", dir)
	}
	if len(dir) != 12 {
		t.Fatalf("hash length = %d, want 12, hash = %q", len(dir), dir)
	}
	if filepath.Base(p.Socket) != "broker.sock" {
		t.Fatalf("socket filename = %q, want %q", filepath.Base(p.Socket), "broker.sock")
	}
}

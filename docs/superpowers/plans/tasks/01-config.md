# Task 1: Config Module — Path Resolution and Defaults

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

This is the foundation. Every other module depends on it. Zero hardcodes enforced here.

- [ ] **Step 1: Write failing tests for project root detection**

```go
// internal/config/config_test.go
package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestFindProjectRoot_FromSubdir(t *testing.T) {
    root := t.TempDir()
    if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
        t.Fatal(err)
    }
    sub := filepath.Join(root, "src", "pkg")
    if err := os.MkdirAll(sub, 0755); err != nil {
        t.Fatal(err)
    }

    got, err := FindProjectRoot(sub)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if got != root {
        t.Errorf("got %q, want %q", got, root)
    }
}

func TestFindProjectRoot_NoGitDir(t *testing.T) {
    tmp := t.TempDir()
    _, err := FindProjectRoot(tmp)
    if err == nil {
        t.Fatal("expected error for missing .git, got nil")
    }
}

func TestFindProjectRoot_EnvOverride(t *testing.T) {
    override := t.TempDir()
    t.Setenv("WAGGLE_ROOT", override)

    got, err := FindProjectRoot("/some/irrelevant/path")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if got != override {
        t.Errorf("got %q, want %q", got, override)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v`
Expected: FAIL — `FindProjectRoot` not defined

- [ ] **Step 3: Implement FindProjectRoot**

```go
// internal/config/config.go
package config

import (
    "crypto/sha256"
    "fmt"
    "os"
    "path/filepath"
    "time"
)

// Defaults — the single source of truth for all configurable values.
var Defaults = struct {
    WaggleDir        string
    DBFile           string
    ConfigFile       string
    PIDFile          string
    LockFile         string
    LogFile          string
    SocketDir        string
    SocketFile       string
    LeaseDuration    time.Duration
    MaxRetries       int
    IdleTimeout      time.Duration
    LeaseCheckPeriod time.Duration
    DefaultPriority  int
}{
    WaggleDir:        ".waggle",
    DBFile:           "state.db",
    ConfigFile:       "config.json",
    PIDFile:          "broker.pid",
    LockFile:         "start.lock",
    LogFile:          "broker.log",
    SocketDir:        ".waggle/sockets",
    SocketFile:       "broker.sock",
    LeaseDuration:    5 * time.Minute,
    MaxRetries:       3,
    IdleTimeout:      30 * time.Minute,
    LeaseCheckPeriod: 30 * time.Second,
    DefaultPriority:  0,
}

const envRoot = "WAGGLE_ROOT"

// FindProjectRoot walks up from startDir looking for .git.
// WAGGLE_ROOT env var overrides auto-detection.
func FindProjectRoot(startDir string) (string, error) {
    if override := os.Getenv(envRoot); override != "" {
        resolved, err := filepath.EvalSymlinks(filepath.Clean(override))
        if err != nil {
            return "", fmt.Errorf("resolve WAGGLE_ROOT symlinks: %w", err)
        }
        return resolved, nil
    }

    dir, err := filepath.Abs(startDir)
    if err != nil {
        return "", fmt.Errorf("resolve path: %w", err)
    }
    // Resolve symlinks so different paths to the same project produce the same hash
    dir, err = filepath.EvalSymlinks(dir)
    if err != nil {
        return "", fmt.Errorf("resolve symlinks: %w", err)
    }

    for {
        if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
            return dir, nil
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            return "", fmt.Errorf("no .git found above %s (set WAGGLE_ROOT to override)", startDir)
        }
        dir = parent
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v`
Expected: PASS (3/3)

- [ ] **Step 5: Write failing tests for path derivation**

```go
// Append to internal/config/config_test.go

func TestPaths_DerivedFromRoot(t *testing.T) {
    root := "/tmp/test-project"
    p := NewPaths(root)

    if p.WaggleDir != filepath.Join(root, ".waggle") {
        t.Errorf("WaggleDir = %q", p.WaggleDir)
    }
    if p.DB != filepath.Join(root, ".waggle", "state.db") {
        t.Errorf("DB = %q", p.DB)
    }
    if p.PID != filepath.Join(root, ".waggle", "broker.pid") {
        t.Errorf("PID = %q", p.PID)
    }
}

func TestPaths_SocketHashDeterministic(t *testing.T) {
    p1 := NewPaths("/tmp/project-a")
    p2 := NewPaths("/tmp/project-a")
    if p1.Socket != p2.Socket {
        t.Errorf("same root produced different socket paths: %q vs %q", p1.Socket, p2.Socket)
    }
}

func TestPaths_SocketHashDiffers(t *testing.T) {
    p1 := NewPaths("/tmp/project-a")
    p2 := NewPaths("/tmp/project-b")
    if p1.Socket == p2.Socket {
        t.Error("different roots produced same socket path")
    }
}
```

- [ ] **Step 6: Implement NewPaths**

```go
// Append to internal/config/config.go

// Paths holds all derived paths for a project. Computed once from root.
type Paths struct {
    Root      string
    WaggleDir string
    DB        string
    Config    string
    PID       string
    Lock      string
    Log       string
    Socket    string
}

// NewPaths computes all paths from a project root. This is the only place paths are derived.
func NewPaths(root string) Paths {
    waggleDir := filepath.Join(root, Defaults.WaggleDir)
    home, _ := os.UserHomeDir()
    hash := projectHash(root)
    socketDir := filepath.Join(home, Defaults.SocketDir, hash)

    return Paths{
        Root:      root,
        WaggleDir: waggleDir,
        DB:        filepath.Join(waggleDir, Defaults.DBFile),
        Config:    filepath.Join(waggleDir, Defaults.ConfigFile),
        PID:       filepath.Join(waggleDir, Defaults.PIDFile),
        Lock:      filepath.Join(waggleDir, Defaults.LockFile),
        Log:       filepath.Join(waggleDir, Defaults.LogFile),
        Socket:    filepath.Join(socketDir, Defaults.SocketFile),
    }
}

func projectHash(root string) string {
    h := sha256.Sum256([]byte(root))
    return fmt.Sprintf("%x", h[:6]) // 12 hex chars = 48 bits
}
```

- [ ] **Step 7: Run all config tests**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/config/ -v`
Expected: PASS (6/6)

- [ ] **Step 8: Commit**

```bash
git add internal/config/
python3 ~/.claude/lib/safe_git.py commit -m "feat: config module — path resolution and defaults"
```

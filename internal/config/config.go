package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"
)

var Defaults = struct {
	DirName    string
	DBFile     string
	ConfigFile string
	PIDFile    string
	LockFile   string
	LogFile    string

	ShutdownTimeout time.Duration
	PollInterval    time.Duration
	MaxLogSize      int64
	LeaseDuration   time.Duration
	MaxRetries      int
}{
	DirName:    ".waggle",
	DBFile:     "state.db",
	ConfigFile: "config.json",
	PIDFile:    "waggle.pid",
	LockFile:   "waggle.lock",
	LogFile:    "waggle.log",

	ShutdownTimeout: 5 * time.Second,
	PollInterval:    500 * time.Millisecond,
	MaxLogSize:      10 * 1024 * 1024,
	LeaseDuration:   5 * time.Minute,
	MaxRetries:      3,
}

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

// NewPaths computes all derived paths from root. Socket will be empty if
// os.UserHomeDir fails (e.g., no HOME set) — callers must check before use.
func NewPaths(root string) Paths {
	if !filepath.IsAbs(root) {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	root = filepath.Clean(root)
	dir := filepath.Join(root, Defaults.DirName)

	// Socket: ~/<DirName>/sockets/<hash>/broker.sock
	// 12 hex chars (48 bits) of FNV-1a per spec — balances collision
	// resistance with macOS 104-byte UDS path length limit.
	f := fnv.New64a()
	_, _ = f.Write([]byte(root))
	hash := fmt.Sprintf("%012x", f.Sum64()&0xffffffffffff)
	var sock string
	if home, err := os.UserHomeDir(); err == nil {
		sock = filepath.Join(home, Defaults.DirName, "sockets", hash, "broker.sock")
	}

	return Paths{
		Root:      root,
		WaggleDir: dir,
		DB:        filepath.Join(dir, Defaults.DBFile),
		Config:    filepath.Join(dir, Defaults.ConfigFile),
		PID:       filepath.Join(dir, Defaults.PIDFile),
		Lock:      filepath.Join(dir, Defaults.LockFile),
		Log:       filepath.Join(dir, Defaults.LogFile),
		Socket:    sock,
	}
}

// FindProjectRoot locates the project root by walking up from startDir looking
// for .git. WAGGLE_ROOT env var overrides detection entirely — this is trusted
// same-user input (env vars require same-UID or root to set).
func FindProjectRoot(startDir string) (string, error) {
	if env := os.Getenv("WAGGLE_ROOT"); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return "", fmt.Errorf("WAGGLE_ROOT %q: %w", env, err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("WAGGLE_ROOT %q: %w", env, err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("WAGGLE_ROOT %q: %w", env, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("WAGGLE_ROOT %q is not a directory", env)
		}
		return resolved, nil
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", startDir, err)
	}
	for {
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			return filepath.EvalSymlinks(dir)
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf(".git at %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .git found from %s; set WAGGLE_ROOT to override", startDir)
		}
		dir = parent
	}
}

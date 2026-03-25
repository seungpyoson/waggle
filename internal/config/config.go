package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"
)

// ValidateDefaults checks that all duration and numeric config fields have
// valid (positive) values. Returns an error describing the first invalid field.
// Called from store.NewStore() and broker.New() before using config values.
// ValidateDefaults checks that all numeric and duration config fields have
// valid (positive) values. Returns an error describing the first invalid field.
// Uses ordered slices for deterministic error reporting.
func ValidateDefaults() error {
	type durCheck struct {
		name string
		val  time.Duration
	}
	durations := []durCheck{
		{"ShutdownTimeout", Defaults.ShutdownTimeout},
		{"PollInterval", Defaults.PollInterval},
		{"LeaseDuration", Defaults.LeaseDuration},
		{"IdleTimeout", Defaults.IdleTimeout},
		{"BusyTimeout", Defaults.BusyTimeout},
		{"LeaseCheckPeriod", Defaults.LeaseCheckPeriod},
		{"IdleCheckInterval", Defaults.IdleCheckInterval},
		{"StartupPollInterval", Defaults.StartupPollInterval},
		{"StartupTimeout", Defaults.StartupTimeout},
	}
	for _, c := range durations {
		if c.val <= 0 {
			return fmt.Errorf("config.Defaults.%s must be positive, got %v", c.name, c.val)
		}
	}

	type intCheck struct {
		name string
		val  int
	}
	ints := []intCheck{
		{"MaxRetries", Defaults.MaxRetries},
		{"MaxPriority", Defaults.MaxPriority},
		{"MaxFieldLength", Defaults.MaxFieldLength},
	}
	for _, c := range ints {
		if c.val <= 0 {
			return fmt.Errorf("config.Defaults.%s must be positive, got %d", c.name, c.val)
		}
	}

	type int64Check struct {
		name string
		val  int64
	}
	sizes := []int64Check{
		{"MaxLogSize", Defaults.MaxLogSize},
		{"MaxMessageSize", Defaults.MaxMessageSize},
	}
	for _, c := range sizes {
		if c.val <= 0 {
			return fmt.Errorf("config.Defaults.%s must be positive, got %d", c.name, c.val)
		}
	}

	return nil
}

var Defaults = struct {
	DirName    string
	DBFile     string
	ConfigFile string
	PIDFile    string
	LockFile   string
	LogFile    string

	ShutdownTimeout     time.Duration
	PollInterval        time.Duration
	MaxLogSize          int64
	MaxMessageSize      int64
	LeaseDuration       time.Duration
	IdleTimeout         time.Duration
	BusyTimeout         time.Duration
	LeaseCheckPeriod    time.Duration
	IdleCheckInterval   time.Duration
	StartupPollInterval time.Duration
	StartupTimeout      time.Duration
	MaxRetries          int
	MaxPriority         int
	MaxFieldLength      int
}{
	DirName:    ".waggle",
	DBFile:     "state.db",
	ConfigFile: "config.json",
	PIDFile:    "waggle.pid",
	LockFile:   "waggle.lock",
	LogFile:    "waggle.log",

	ShutdownTimeout:     5 * time.Second,
	PollInterval:        500 * time.Millisecond,
	MaxLogSize:          10 * 1024 * 1024,
	MaxMessageSize:      1024 * 1024, // 1MB buffer for large AI agent payloads
	LeaseDuration:       5 * time.Minute,
	IdleTimeout:         5 * time.Minute,
	BusyTimeout:         5 * time.Second,
	LeaseCheckPeriod:    30 * time.Second,
	IdleCheckInterval:   1 * time.Second,
	StartupPollInterval: 100 * time.Millisecond,
	StartupTimeout:      2 * time.Second,
	MaxRetries:          3,
	MaxPriority:         100,
	MaxFieldLength:      256,
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

package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// ValidateDefaults uses reflection to check every numeric field in the Defaults
// struct. Adding a new field to the struct automatically validates it — no manual
// list to maintain. Iteration order is deterministic (struct field order).
// Called from store.NewStore() and broker.New() before using config values.
func ValidateDefaults() error {
	v := reflect.ValueOf(Defaults)
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Int64:
			// time.Duration is int64 underneath
			if field.Type == reflect.TypeOf(time.Duration(0)) {
				d := time.Duration(fv.Int())
				if d <= 0 {
					return fmt.Errorf("config.Defaults.%s must be positive, got %v", field.Name, d)
				}
			} else {
				if fv.Int() <= 0 {
					return fmt.Errorf("config.Defaults.%s must be positive, got %d", field.Name, fv.Int())
				}
			}
		case reflect.Int:
			if fv.Int() <= 0 {
				return fmt.Errorf("config.Defaults.%s must be positive, got %d", field.Name, fv.Int())
			}
		default:
			// String fields, bools, etc. — no positive-value constraint
		}
	}

	// Boundary constraint: LeaseDuration is cast to int(Seconds()) for SQL
	// schema DEFAULT. Sub-second values truncate to 0, producing invalid SQL.
	if Defaults.LeaseDuration < time.Second {
		return fmt.Errorf("config.Defaults.LeaseDuration must be >= 1s (used as integer seconds in SQL), got %v", Defaults.LeaseDuration)
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
	StartupPollInterval  time.Duration
	StartupTimeout       time.Duration
	DisconnectTimeout    time.Duration
	MaxRetries           int
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
	StartupPollInterval:  100 * time.Millisecond,
	StartupTimeout:       2 * time.Second,
	DisconnectTimeout:    2 * time.Second,
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

// ResolveProjectID returns a stable identifier for the current project.
// Priority: WAGGLE_PROJECT_ID env var → git root commit SHA → "path:" + WAGGLE_ROOT → error.
func ResolveProjectID() (string, error) {
	if id := os.Getenv("WAGGLE_PROJECT_ID"); id != "" {
		return id, nil
	}
	if id, err := gitRootCommit(); err == nil {
		return id, nil
	}
	if root := os.Getenv("WAGGLE_ROOT"); root != "" {
		return "path:" + root, nil
	}
	return "", fmt.Errorf("cannot identify project: not in a git repo; set WAGGLE_PROJECT_ID or WAGGLE_ROOT")
}

// gitRootCommit returns the SHA of the earliest root commit (sorted lexicographically
// to be deterministic when multiple root commits exist, e.g. merged unrelated histories).
func gitRootCommit() (string, error) {
	if _, err := exec.Command("git", "rev-parse", "--git-common-dir").Output(); err != nil {
		return "", fmt.Errorf("not a git repo: %w", err)
	}
	out, err := exec.Command("git", "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-list: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("no root commits found")
	}
	sort.Strings(lines)
	return lines[0], nil
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

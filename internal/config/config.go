package config

import (
	"context"
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
	PIDFile    string
	SocketFile string
	LockFile   string
	LogFile    string

	// The retired broker's IPC names. They are never opened or served: the
	// native runtime only recognizes them so conversion can report and retire
	// them. Nothing may fall back to these endpoints.
	LegacyPIDFile    string
	LegacySocketFile string

	// SnapshotDir holds pre-conversion database copies, under DataDir.
	SnapshotDir string

	ShutdownTimeout      time.Duration
	MaxMessageSize       int64
	LeaseDuration        time.Duration
	BusyTimeout          time.Duration
	LeaseCheckPeriod     time.Duration
	ShutdownPollInterval time.Duration
	StartupTimeout       time.Duration
	DisconnectTimeout    time.Duration
	MaxRetries           int
	MaxPriority          int
	MaxFieldLength       int

	// Terminal-launch defaults
	SpawnLaunchTimeout time.Duration
	AgentConfigFile    string

	// Connection timeout defaults
	ConnectTimeout time.Duration

	// Task lifecycle defaults
	TaskTTLCheckPeriod time.Duration
	TaskStaleThreshold time.Duration
	MaxTaskTTL         int
}{
	DirName:    ".waggle",
	DBFile:     "state.db",
	PIDFile:    "waggle-v2.pid",
	SocketFile: "broker-v2.sock",
	LockFile:   "waggle.lock",
	LogFile:    "waggle.log",

	LegacyPIDFile:    "waggle.pid",
	LegacySocketFile: "broker.sock",

	SnapshotDir: "rollback",

	ShutdownTimeout:      5 * time.Second,
	MaxMessageSize:       1024 * 1024, // 1MB buffer for large AI agent payloads
	LeaseDuration:        5 * time.Minute,
	BusyTimeout:          5 * time.Second,
	LeaseCheckPeriod:     30 * time.Second,
	ShutdownPollInterval: 100 * time.Millisecond,
	StartupTimeout:       2 * time.Second,
	DisconnectTimeout:    2 * time.Second,
	MaxRetries:           3,
	MaxPriority:          100,
	MaxFieldLength:       256,

	SpawnLaunchTimeout: 10 * time.Second,
	AgentConfigFile:    "agents.json",

	ConnectTimeout: 5 * time.Second,

	TaskTTLCheckPeriod: 30 * time.Second,
	TaskStaleThreshold: 5 * time.Minute,
	MaxTaskTTL:         86400, // 24 hours

}

type Paths struct {
	ProjectID string
	DataDir   string
	DB        string
	PID       string
	Lock      string
	Log       string
	Socket    string

	// LegacyPID and LegacySocket are the retired broker's endpoints in the same
	// resolved directories. Conversion reports and retires them; nothing serves
	// or discovers them.
	LegacyPID    string
	LegacySocket string

	// SnapshotDir holds the pre-conversion copies a rollback restores.
	SnapshotDir string
}

// NewPaths computes all derived paths from a project ID. Broker state lives
// under ~/.waggle/data/<hash>/ and ~/.waggle/sockets/<hash>/.
// If os.UserHomeDir fails (no HOME set), all paths will be empty — callers must
// check before use.
func NewPaths(projectID string) Paths {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{ProjectID: projectID}
	}

	f := fnv.New64a()
	if _, err := f.Write([]byte(projectID)); err != nil {
		panic(fmt.Sprintf("hash project ID: %v", err))
	}
	hash := fmt.Sprintf("%012x", f.Sum64()&0xffffffffffff)

	dataDir := filepath.Join(home, Defaults.DirName, "data", hash)
	socketDir := filepath.Join(home, Defaults.DirName, "sockets", hash)

	return Paths{
		ProjectID:    projectID,
		DataDir:      dataDir,
		DB:           filepath.Join(dataDir, Defaults.DBFile),
		PID:          filepath.Join(dataDir, Defaults.PIDFile),
		Lock:         filepath.Join(dataDir, Defaults.LockFile),
		Log:          filepath.Join(dataDir, Defaults.LogFile),
		Socket:       filepath.Join(socketDir, Defaults.SocketFile),
		LegacyPID:    filepath.Join(dataDir, Defaults.LegacyPIDFile),
		LegacySocket: filepath.Join(socketDir, Defaults.LegacySocketFile),
		SnapshotDir:  filepath.Join(dataDir, Defaults.SnapshotDir),
	}
}

// ResolveProjectID returns a stable identifier for the current project.
// Priority: WAGGLE_PROJECT_ID env var → git root commit SHA → "path:" + WAGGLE_ROOT → error.
func ResolveProjectID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}
	if id := os.Getenv("WAGGLE_PROJECT_ID"); id != "" {
		return id, nil
	}
	if id, err := gitRootCommit(ctx); err == nil {
		return id, nil
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}
	if root := os.Getenv("WAGGLE_ROOT"); root != "" {
		return "path:" + root, nil
	}
	return "", fmt.Errorf("cannot identify project: not in a git repo; set WAGGLE_PROJECT_ID or WAGGLE_ROOT")
}

// gitRootCommit returns the SHA of the earliest root commit (sorted lexicographically
// to be deterministic when multiple root commits exist, e.g. merged unrelated histories).
func gitRootCommit(ctx context.Context) (string, error) {
	if _, err := exec.CommandContext(ctx, "git", "rev-parse", "--git-common-dir").Output(); err != nil {
		return "", fmt.Errorf("not a git repo: %w", err)
	}
	out, err := exec.CommandContext(ctx, "git", "rev-list", "--max-parents=0", "HEAD").Output()
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

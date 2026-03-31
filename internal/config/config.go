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
	DirName                 string
	DBFile                  string
	ConfigFile              string
	PIDFile                 string
	LockFile                string
	LogFile                 string
	RuntimeDirName          string
	RuntimeStartLockDirName string
	RuntimeDBFile           string
	RuntimePIDFile          string
	RuntimeLogFile          string
	RuntimeStateFile        string
	SignalDirName           string
	SignalMaxBytes          int64

	ShutdownTimeout                       time.Duration
	RuntimeNotificationTimeout            time.Duration
	RuntimeReconnectMaxBackoff            time.Duration
	RuntimeEphemeralWatchTTL              time.Duration
	RuntimeDeliveryRetention              time.Duration
	RuntimeStartLockStaleThreshold        time.Duration
	RuntimeReconcileInterval              time.Duration
	RuntimeNotificationRetrySweepInterval time.Duration
	RuntimeStateRefreshInterval           time.Duration
	PollInterval                          time.Duration
	RuntimeNotificationRetryBatchSize     int
	MaxLogSize                            int64
	MaxMessageSize                        int64
	LeaseDuration                         time.Duration
	IdleTimeout                           time.Duration
	BusyTimeout                           time.Duration
	LeaseCheckPeriod                      time.Duration
	IdleCheckInterval                     time.Duration
	StartupPollInterval                   time.Duration
	StartupTimeout                        time.Duration
	DisconnectTimeout                     time.Duration
	CatchUpMaxRetries                     int
	RuntimeNotificationRetryLimit         int
	MaxRetries                            int
	MaxPriority                           int
	MaxFieldLength                        int
	TTLCheckPeriod                        time.Duration
	AwaitAckDefaultTimeout                time.Duration
	MaxTTL                                int
	DefaultMsgPriority                    string
	ValidMsgPriorities                    []string

	// Spawn-related defaults
	SpawnPIDTimeout       time.Duration
	SpawnPIDPollInterval  time.Duration
	SpawnStopTimeout      time.Duration
	SpawnStopPollInterval time.Duration
	SpawnKillPollInterval time.Duration
	AgentConfigFile       string

	// Connection timeout defaults
	ConnectTimeout     time.Duration
	HealthCheckTimeout time.Duration

	// Task lifecycle defaults
	TaskTTLCheckPeriod time.Duration
	TaskStaleThreshold time.Duration
	MaxTaskTTL         int

	// Runtime observability
	RuntimeRecentErrorCap int
}{
	DirName:                 ".waggle",
	DBFile:                  "state.db",
	ConfigFile:              "config.json",
	PIDFile:                 "waggle.pid",
	LockFile:                "waggle.lock",
	LogFile:                 "waggle.log",
	RuntimeDirName:          "runtime",
	RuntimeStartLockDirName: "runtime-start.lock",
	RuntimeDBFile:           "runtime.db",
	RuntimePIDFile:          "runtime.pid",
	RuntimeLogFile:          "runtime.log",
	RuntimeStateFile:        "state.json",
	SignalDirName:           "signals",
	SignalMaxBytes:          65536,

	ShutdownTimeout:                       5 * time.Second,
	RuntimeNotificationTimeout:            2 * time.Second,
	RuntimeReconnectMaxBackoff:            30 * time.Second,
	RuntimeEphemeralWatchTTL:              24 * time.Hour,
	RuntimeDeliveryRetention:              30 * 24 * time.Hour,
	RuntimeStartLockStaleThreshold:        10 * time.Second,
	RuntimeReconcileInterval:              2 * time.Second,
	RuntimeNotificationRetrySweepInterval: 1 * time.Second,
	RuntimeStateRefreshInterval:           2 * time.Second,
	PollInterval:                          500 * time.Millisecond,
	RuntimeNotificationRetryBatchSize:     128,
	MaxLogSize:                            10 * 1024 * 1024,
	MaxMessageSize:                        1024 * 1024, // 1MB buffer for large AI agent payloads
	LeaseDuration:                         5 * time.Minute,
	IdleTimeout:                           5 * time.Minute,
	BusyTimeout:                           5 * time.Second,
	LeaseCheckPeriod:                      30 * time.Second,
	IdleCheckInterval:                     1 * time.Second,
	StartupPollInterval:                   100 * time.Millisecond,
	StartupTimeout:                        2 * time.Second,
	DisconnectTimeout:                     2 * time.Second,
	CatchUpMaxRetries:                     3,
	RuntimeNotificationRetryLimit:         5,
	MaxRetries:                            3,
	MaxPriority:                           100,
	MaxFieldLength:                        256,
	TTLCheckPeriod:                        30 * time.Second,
	AwaitAckDefaultTimeout:                30 * time.Second,
	MaxTTL:                                86400,
	DefaultMsgPriority:                    "normal",
	ValidMsgPriorities:                    []string{"critical", "normal", "bulk"},

	SpawnPIDTimeout:       3 * time.Second,
	SpawnPIDPollInterval:  200 * time.Millisecond,
	SpawnStopTimeout:      5 * time.Second,
	SpawnStopPollInterval: 100 * time.Millisecond,
	SpawnKillPollInterval: 50 * time.Millisecond,
	AgentConfigFile:       "agents.json",

	ConnectTimeout:     5 * time.Second,
	HealthCheckTimeout: 1 * time.Second,

	TaskTTLCheckPeriod: 30 * time.Second,
	TaskStaleThreshold: 5 * time.Minute,
	MaxTaskTTL:         86400, // 24 hours

	RuntimeRecentErrorCap: 20,
}

type Paths struct {
	ProjectID           string
	DataDir             string
	RuntimeDir          string
	RuntimeDB           string
	RuntimePID          string
	RuntimeLog          string
	RuntimeState        string
	RuntimeStartLockDir string
	RuntimeSignalDir    string
	DB                  string
	PID                 string
	Lock                string
	Log                 string
	Socket              string
}

// NewPaths computes all derived paths from a project ID. Broker state lives
// under ~/.waggle/data/<hash>/ and ~/.waggle/sockets/<hash>/. Machine-runtime
// state is machine-local and shared across projects under ~/.waggle/runtime/.
// If os.UserHomeDir fails (no HOME set), all paths will be empty — callers must
// check before use.
func NewPaths(projectID string) Paths {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{ProjectID: projectID}
	}

	f := fnv.New64a()
	_, _ = f.Write([]byte(projectID))
	hash := fmt.Sprintf("%012x", f.Sum64()&0xffffffffffff)

	dataDir := filepath.Join(home, Defaults.DirName, "data", hash)
	socketDir := filepath.Join(home, Defaults.DirName, "sockets", hash)
	runtimeDir := filepath.Join(home, Defaults.DirName, Defaults.RuntimeDirName)

	return Paths{
		ProjectID:           projectID,
		DataDir:             dataDir,
		RuntimeDir:          runtimeDir,
		RuntimeDB:           filepath.Join(runtimeDir, Defaults.RuntimeDBFile),
		RuntimePID:          filepath.Join(runtimeDir, Defaults.RuntimePIDFile),
		RuntimeLog:          filepath.Join(runtimeDir, Defaults.RuntimeLogFile),
		RuntimeState:        filepath.Join(runtimeDir, Defaults.RuntimeStateFile),
		RuntimeStartLockDir: filepath.Join(runtimeDir, Defaults.RuntimeStartLockDirName),
		RuntimeSignalDir:    filepath.Join(runtimeDir, Defaults.SignalDirName),
		DB:                  filepath.Join(dataDir, Defaults.DBFile),
		PID:                 filepath.Join(dataDir, Defaults.PIDFile),
		Lock:                filepath.Join(dataDir, Defaults.LockFile),
		Log:                 filepath.Join(dataDir, Defaults.LogFile),
		Socket:              filepath.Join(socketDir, "broker.sock"),
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

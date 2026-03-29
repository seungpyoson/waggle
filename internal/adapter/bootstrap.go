package adapter

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seungpyoson/waggle/internal/broker"
	"github.com/seungpyoson/waggle/internal/config"
	rt "github.com/seungpyoson/waggle/internal/runtime"
)

type BootstrapInput struct {
	Tool      string
	AgentName string
	ProjectID string
	Source    string
}

type BootstrapResult struct {
	Tool           string              `json:"tool"`
	ProjectID      string              `json:"project_id"`
	AgentName      string              `json:"agent_name"`
	Source         string              `json:"source"`
	RuntimeRunning bool                `json:"runtime_running"`
	RuntimeError   string              `json:"runtime_error,omitempty"`
	Records        []rt.DeliveryRecord `json:"records"`
	Skipped        bool                `json:"skipped,omitempty"`
	SkipReason     string              `json:"skip_reason,omitempty"`
}

func Bootstrap(input BootstrapInput) (BootstrapResult, error) {
	tool := sanitizeToken(input.Tool)
	if tool == "" {
		return BootstrapResult{}, fmt.Errorf("tool required")
	}

	runtimePaths, err := resolveRuntimePaths()
	if err != nil {
		return BootstrapResult{Tool: tool, Skipped: true, SkipReason: err.Error()}, nil
	}

	projectID, err := resolveProjectID(input.ProjectID)
	if err != nil {
		return BootstrapResult{Tool: tool, Skipped: true, SkipReason: err.Error()}, nil
	}

	agentName := ResolveAgentName(tool, input.AgentName, resolveTTY(), os.Getppid(), os.Getpid())
	source := input.Source
	if source == "" {
		source = tool + "-adapter"
	}

	runtimeRunning := rt.IsRunning(runtimePaths)
	runtimeErr := ""
	if !runtimeRunning {
		if err := ensureRuntimeStarted(runtimePaths); err != nil {
			runtimeErr = err.Error()
		} else {
			runtimeRunning = rt.IsRunning(runtimePaths)
		}
	}

	store, err := rt.OpenStore(runtimePaths)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer store.Close()

	if err := store.UpsertWatch(rt.Watch{
		ProjectID: projectID,
		AgentName: agentName,
		Source:    source,
	}); err != nil {
		return BootstrapResult{}, err
	}

	records, err := store.Unread(projectID, agentName)
	if err != nil {
		return BootstrapResult{}, err
	}

	messageIDs := make([]int64, 0, len(records))
	for _, rec := range records {
		messageIDs = append(messageIDs, rec.MessageID)
	}
	if err := store.MarkSurfacedBatch(projectID, agentName, messageIDs); err != nil {
		return BootstrapResult{}, err
	}

	return BootstrapResult{
		Tool:           tool,
		ProjectID:      projectID,
		AgentName:      agentName,
		Source:         source,
		RuntimeRunning: runtimeRunning,
		RuntimeError:   runtimeErr,
		Records:        records,
	}, nil
}

func ResolveAgentName(tool, explicit, tty string, ppid, pid int) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("WAGGLE_AGENT_NAME"); env != "" {
		return env
	}

	tool = sanitizeToken(tool)
	if tool == "" {
		tool = "agent"
	}

	if ttyName := sanitizeTTY(tty); ttyName != "" {
		return tool + "-" + ttyName
	}
	if ppid > 1 {
		return fmt.Sprintf("%s-%d", tool, ppid)
	}
	return fmt.Sprintf("%s-%d", tool, pid)
}

func resolveRuntimePaths() (config.Paths, error) {
	paths := config.NewPaths("")
	if paths.RuntimeDir == "" || paths.RuntimeDB == "" || paths.RuntimePID == "" || paths.RuntimeLog == "" || paths.RuntimeState == "" || paths.RuntimeStartLockDir == "" {
		return config.Paths{}, fmt.Errorf("cannot determine runtime paths: HOME not set")
	}
	return paths, nil
}

func resolveProjectID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return config.ResolveProjectID()
}

func ensureRuntimeStarted(runtimePaths config.Paths) error {
	if rt.IsRunning(runtimePaths) {
		return nil
	}
	if shouldSkipRuntimeStartForTest() {
		return nil
	}
	if err := rt.CleanupStale(runtimePaths); err != nil {
		return err
	}

	releaseLock, err := rt.AcquireStartLock(runtimePaths, config.Defaults.RuntimeStartLockStaleThreshold)
	if err != nil {
		return err
	}
	defer func() {
		_ = releaseLock()
	}()

	daemonArgs := []string{os.Args[0], "runtime", "start", "--foreground"}
	if err := rt.StartDaemon(runtimePaths, daemonArgs); err != nil {
		return err
	}
	if err := rt.WaitForReady(runtimePaths, config.Defaults.StartupTimeout, config.Defaults.StartupPollInterval); err != nil {
		return fmt.Errorf("runtime failed to start (check %s): %w", runtimePaths.RuntimeLog, err)
	}
	if _, err := broker.ReadPID(runtimePaths.RuntimePID); err != nil {
		return fmt.Errorf("runtime started but cannot read PID: %w", err)
	}
	return nil
}

func shouldSkipRuntimeStartForTest() bool {
	if os.Getenv("WAGGLE_ADAPTER_SKIP_RUNTIME_START") != "1" {
		return false
	}
	return flag.Lookup("test.v") != nil
}

func resolveTTY() string {
	if tty := os.Getenv("TTY"); tty != "" {
		return tty
	}
	return ""
}

func sanitizeTTY(tty string) string {
	tty = strings.TrimSpace(tty)
	if tty == "" {
		return ""
	}
	base := filepath.Base(tty)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	return sanitizeToken(base)
}

func sanitizeToken(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}

	var b strings.Builder
	prevDash := false
	for _, r := range v {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

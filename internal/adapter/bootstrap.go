package adapter

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	result := BootstrapResult{
		Tool:      tool,
		ProjectID: projectID,
		AgentName: agentName,
		Source:    source,
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
	result.RuntimeRunning = runtimeRunning
	result.RuntimeError = runtimeErr

	store, err := rt.OpenStore(runtimePaths)
	if err != nil {
		return skipRuntimeStore(result, err), nil
	}
	defer store.Close()

	if err := store.UpsertWatch(rt.Watch{
		ProjectID: projectID,
		AgentName: agentName,
		Source:    source,
	}); err != nil {
		return skipRuntimeStore(result, err), nil
	}

	ppid := resolveAgentPPID()
	nonce := fmt.Sprintf("%d-%d", ppid, time.Now().UnixNano())
	// Best-effort: runtime delivery is driven by the watch store, and this mapping only
	// enables shell-hook signal discovery for the current terminal session.
	if err := WriteSessionMapping(runtimePaths.RuntimeDir, ppid, nonce, agentName, projectID); err != nil {
		log.Printf("warning: write session mapping failed: %v; push delivery degraded", err)
	}

	records, err := store.Unread(projectID, agentName)
	if err != nil {
		return skipRuntimeStore(result, err), nil
	}

	messageIDs := make([]int64, 0, len(records))
	for _, rec := range records {
		messageIDs = append(messageIDs, rec.MessageID)
	}
	if err := store.MarkSurfacedAndDismissBatch(projectID, agentName, messageIDs); err != nil {
		return skipRuntimeStore(result, fmt.Errorf("mark dismissed: %w", err)), nil
	}

	result.Records = records
	return result, nil
}

func skipRuntimeStore(result BootstrapResult, err error) BootstrapResult {
	reason := fmt.Sprintf("runtime store unavailable: %v", err)
	result.Skipped = true
	result.SkipReason = reason
	result.RuntimeError = reason
	result.Records = nil
	return result
}

func ResolveAgentName(tool, explicit, tty string, ppid, pid int) string {
	if explicit != "" {
		return sanitizeToken(explicit)
	}
	if env := os.Getenv("WAGGLE_AGENT_NAME"); env != "" {
		return sanitizeToken(env)
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

func ensureRuntimeStarted(runtimePaths config.Paths) (retErr error) {
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
		if err := releaseLock(); err != nil {
			if retErr == nil {
				retErr = fmt.Errorf("release runtime start lock: %w", err)
				return
			}
			log.Printf("warning: release runtime start lock: %v", err)
		}
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

// WriteSessionMapping writes the PPID pointer and unique session mapping for hook discovery.
// The projectID is hashed to the same opaque key used for signal directory names,
// so shell/JS hooks never receive raw project paths in session mapping files.
func WriteSessionMapping(runtimeDir string, ppid int, nonce, agentName, projectID string) error {
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}

	projectKey := rt.ProjectPathKey(projectID)
	sessionPath := filepath.Join(runtimeDir, "agent-session-"+nonce)
	if err := writeRuntimeFileAtomic(sessionPath, []byte(agentName+"\n"+projectKey+"\n"), 0o600); err != nil {
		return err
	}

	ppidPath := filepath.Join(runtimeDir, fmt.Sprintf("agent-ppid-%d", ppid))
	return writeRuntimeFileAtomic(ppidPath, []byte(nonce+"\n"), 0o600)
}

func writeRuntimeFileAtomic(path string, data []byte, perm os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	tmpOpen := true
	cleanupPending := true
	defer func() {
		if tmpOpen {
			if err := tmp.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close temp %s: %w", tmpName, err))
			}
		}
		if cleanupPending {
			if err := os.Remove(tmpName); err != nil && !os.IsNotExist(err) {
				retErr = errors.Join(retErr, fmt.Errorf("remove temp %s: %w", tmpName, err))
			}
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	closeErr := tmp.Close()
	tmpOpen = false
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanupPending = false
	return nil
}

// resolveAgentPPID returns the agent process PID for PPID mapping.
// Prefers WAGGLE_AGENT_PPID env var (set by callers who know the real agent PID)
// over os.Getppid() (which returns the intermediate shell, not the agent).
func resolveAgentPPID() int {
	if ep := os.Getenv("WAGGLE_AGENT_PPID"); ep != "" {
		if p, err := strconv.Atoi(ep); err == nil && p > 0 {
			return p
		}
	}
	return os.Getppid()
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

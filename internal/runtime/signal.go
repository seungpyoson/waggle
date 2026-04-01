package runtime

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/fsutil"
)

// ansiPattern matches ANSI escape sequences:
// - CSI: ESC [ (params including ?, !, >, space, etc.) final byte
// - OSC: ESC ] ... (BEL or ST)
// - DCS: ESC P ... ST
// - Simple ESC: ESC + letter
var ansiPattern = regexp.MustCompile("(?:" +
	`\x1b\[[\x20-\x3f]*[A-Za-z@]` + "|" + // CSI with full ECMA-48 parameter/intermediate bytes
	`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + "|" + // OSC terminated by BEL or ST
	`\x1bP[^\x1b]*(?:\x1b\\)?` + "|" + // DCS with payload + optional ST
	`\x1b[A-Za-z]` + // Simple ESC sequences
	")")

var ErrSignalCapExceeded = errors.New("signal cap exceeded")

// ProjectPathKey returns the opaque filesystem key for a project ID.
// It uses a 64-bit FNV-1a hash truncated to 12 lowercase hex characters.
func ProjectPathKey(rawID string) string {
	if rawID == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(rawID))
	return fmt.Sprintf("%012x", h.Sum64()&0xffffffffffff)
}

// stripANSI removes ANSI escape sequences and dangerous control characters from s.
// Preserves \n and \t for legitimate formatting.
func stripANSI(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	// Strip raw C0 control chars (CR, BS, BEL, etc.) and C1 range (0x80-0x9F)
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		if r >= 0x7f && r <= 0x9f {
			return -1
		}
		return r
	}, s)
}

// WriteSignal appends a formatted message to the agent's signal file.
// Returns ErrSignalCapExceeded if the file would exceed maxBytes after append.
// Hashes projectID to an opaque path key, sanitizes agentName, and strips ANSI escapes from body.
func WriteSignal(signalDir, projectID, agentName, fromName, body string, maxBytes int64) error {
	projectKey := ProjectPathKey(projectID)
	if projectKey == "" {
		return fmt.Errorf("empty project ID")
	}
	safeAgentName := SanitizeAgentName(agentName)
	if safeAgentName == "" {
		return fmt.Errorf("empty agent name after sanitization")
	}
	body = stripANSI(body)
	fromName = stripANSI(fromName)
	dir := filepath.Join(signalDir, projectKey)
	signalRoot := filepath.Dir(signalDir)
	if fsutil.HasAncestorSymlink(dir, signalRoot) {
		return fmt.Errorf("refusing to create signal dir with ancestor symlink: %s", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create signal dir: %w", err)
	}
	path := filepath.Join(dir, safeAgentName)
	if fsutil.HasAncestorSymlink(path, signalRoot) {
		return fmt.Errorf("refusing to write signal through ancestor symlink: %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to overwrite symlink: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat signal: %w", err)
	}
	msg := fmt.Sprintf("📨 waggle message from %s: %s\n", fromName, body)
	if maxBytes > 0 {
		existing := int64(0)
		if info, err := os.Stat(path); err == nil {
			existing = info.Size()
		}
		if existing+int64(len(msg)) >= maxBytes {
			log.Printf("dropping signal for %q/%q: cap %d bytes exceeded", projectKey, safeAgentName, maxBytes)
			return ErrSignalCapExceeded
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open signal: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(msg)
	return err
}

// PruneStaleFiles removes files matching prefix older than maxAge.
func PruneStaleFiles(dir, prefix string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Best-effort cleanup: skip unreadable directories to keep runtime cleanup non-fatal.
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			// Best-effort cleanup: stale files may already be gone after concurrent cleanup.
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// PruneStaleSignals removes signal files older than maxAge across all project subdirectories.
func PruneStaleSignals(signalDir string, maxAge time.Duration) {
	projects, err := os.ReadDir(signalDir)
	if err != nil {
		// Best-effort cleanup: skip unreadable signal roots to keep runtime cleanup non-fatal.
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, proj := range projects {
		if !proj.IsDir() {
			continue
		}
		projPath := filepath.Join(signalDir, proj.Name())
		agents, err := os.ReadDir(projPath)
		if err != nil {
			// Best-effort cleanup: skip unreadable project directories and continue pruning others.
			continue
		}
		for _, agent := range agents {
			info, err := agent.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				// Best-effort cleanup: stale files may already be gone after concurrent cleanup.
				os.Remove(filepath.Join(projPath, agent.Name()))
			}
		}
		// Remove empty project directories
		// Best-effort cleanup: if the empty-dir probe fails, leave the directory for a later cycle.
		remaining, _ := os.ReadDir(projPath)
		if len(remaining) == 0 {
			// Best-effort cleanup: another writer may recreate the directory before removal.
			os.Remove(projPath)
		}
	}
}

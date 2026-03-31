package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ansiPattern matches ANSI escape sequences:
// - CSI: ESC [ (params including ?, !, >, space, etc.) final byte
// - OSC: ESC ] ... (BEL or ST)
// - DCS: ESC P ... ST
// - Simple ESC: ESC + letter
var ansiPattern = regexp.MustCompile("(?:" +
	`\x1b\[[0-9;?!>"' ]*[A-Za-z@]` + "|" + // CSI with intermediate bytes
	`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + "|" + // OSC terminated by BEL or ST
	`\x1bP[^\x1b]*(?:\x1b\\)?` + "|" + // DCS with payload + optional ST
	`\x1b[A-Za-z]` + // Simple ESC sequences
	")")

// SanitizeProjectIDForPath transforms a project ID into a filesystem-safe directory name.
// Replaces non-alphanumeric characters (slashes, colons, dots) with hyphens and collapses runs.
// Deterministic: same input always produces same output.
// Handles both git SHA hashes ("abcdef01") and path-based IDs ("path:/Users/foo").
func SanitizeProjectIDForPath(id string) string {
	if id == "" {
		return ""
	}
	var b strings.Builder
	prevDash := false
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
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
// Drops the write silently if the file would exceed maxBytes after append.
// Sanitizes projectID (path traversal) and strips ANSI escapes from body (terminal hijack).
func WriteSignal(signalDir, projectID, agentName, fromName, body string, maxBytes int64) error {
	safeProjectID := SanitizeProjectIDForPath(projectID)
	if safeProjectID == "" {
		return fmt.Errorf("empty project ID after sanitization")
	}
	body = stripANSI(body)
	fromName = stripANSI(fromName)
	dir := filepath.Join(signalDir, safeProjectID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create signal dir: %w", err)
	}
	path := filepath.Join(dir, agentName)
	msg := fmt.Sprintf("📨 waggle message from %s: %s\n", fromName, body)
	if maxBytes > 0 {
		existing := int64(0)
		if info, err := os.Stat(path); err == nil {
			existing = info.Size()
		}
		if existing+int64(len(msg)) >= maxBytes {
			return nil
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

// ConsumeSignal atomically reads and removes the signal file.
// Rename-then-read ensures no data loss if the daemon writes during consume.
func ConsumeSignal(signalDir, projectID, agentName string) (string, error) {
	safeProjectID := SanitizeProjectIDForPath(projectID)
	if safeProjectID == "" {
		return "", fmt.Errorf("empty project ID after sanitization")
	}
	path := filepath.Join(signalDir, safeProjectID, agentName)
	tmp := fmt.Sprintf("%s.consuming-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, tmp); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	data, err := os.ReadFile(tmp)
	os.Remove(tmp)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// PruneStaleFiles removes files matching prefix older than maxAge.
func PruneStaleFiles(dir, prefix string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
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
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// PruneStaleSignals removes signal files older than maxAge across all project subdirectories.
func PruneStaleSignals(signalDir string, maxAge time.Duration) {
	projects, err := os.ReadDir(signalDir)
	if err != nil {
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
			continue
		}
		for _, agent := range agents {
			info, err := agent.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(projPath, agent.Name()))
			}
		}
		// Remove empty project directories
		remaining, _ := os.ReadDir(projPath)
		if len(remaining) == 0 {
			os.Remove(projPath)
		}
	}
}

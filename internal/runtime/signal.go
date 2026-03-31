package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteSignal appends a formatted message to the agent's signal file.
// Drops the write silently if the file would exceed maxBytes after append.
func WriteSignal(signalDir, projectID, agentName, fromName, body string, maxBytes int64) error {
	dir := filepath.Join(signalDir, projectID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create signal dir: %w", err)
	}
	path := filepath.Join(dir, agentName)
	msg := fmt.Sprintf("📨 waggle message from %s: %s\n", fromName, body)
	if maxBytes > 0 {
		if info, err := os.Stat(path); err == nil && info.Size()+int64(len(msg)) >= maxBytes {
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
func ConsumeSignal(signalDir, agentName string) (string, error) {
	path := filepath.Join(signalDir, agentName)
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

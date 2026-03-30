package install

import (
	"fmt"
	"os"
	"strings"
)

// validateMarkerTopology checks that a file's managed-block markers are in a
// sane state before any mutation. This prevents upsert/remove from silently
// producing a file that the health check would reject.
//
// Rules:
//  1. At most one begin marker and one end marker.
//  2. An end marker without a begin marker is always invalid (orphaned end).
//  3. A begin marker without an end marker is tolerable (repair handles it),
//     but the begin marker must be at the start of a line.
//  4. If both exist: begin must be at start of line, end must be at end of
//     line (followed only by \n or EOF).
func validateMarkerTopology(content, begin, end string) error {
	beginCount := strings.Count(content, begin)
	endCount := strings.Count(content, end)

	if beginCount > 1 {
		return fmt.Errorf("duplicate begin markers (%d found); refusing to mutate", beginCount)
	}
	if endCount > 1 {
		return fmt.Errorf("duplicate end markers (%d found); refusing to mutate", endCount)
	}
	if endCount == 1 && beginCount == 0 {
		return fmt.Errorf("orphaned end marker without begin marker; refusing to mutate")
	}

	if beginCount == 1 {
		idx := strings.Index(content, begin)
		if idx > 0 && content[idx-1] != '\n' {
			return fmt.Errorf("begin marker not at start of line; refusing to mutate")
		}
	}

	if endCount == 1 {
		idx := strings.Index(content, end)
		endAbs := idx + len(end)
		if endAbs < len(content) && content[endAbs] != '\n' {
			return fmt.Errorf("end marker not at end of line; refusing to mutate")
		}
	}

	if beginCount == 1 && endCount == 1 {
		beginIdx := strings.Index(content, begin)
		endIdx := strings.Index(content, end)
		if endIdx < beginIdx {
			return fmt.Errorf("end marker appears before begin marker; refusing to mutate")
		}
	}

	return nil
}

func upsertManagedBlock(path, begin, end, body string) error {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	content := string(current)
	if err := validateMarkerTopology(content, begin, end); err != nil {
		return fmt.Errorf("invalid marker topology in %s: %w", path, err)
	}

	block := canonicalManagedBlock(begin, end, body)

	if idx := strings.Index(content, begin); idx >= 0 {
		endIdx := strings.Index(content[idx:], end)
		if endIdx < 0 {
			return fmt.Errorf("managed block start found without end marker in %s", path)
		}
		endAbs := idx + endIdx + len(end)
		replaced := content[:idx] + block + content[endAbs:]
		return os.WriteFile(path, managedBlockBytes(replaced, content[endAbs:] == ""), 0644)
	}

	if content == "" {
		return os.WriteFile(path, managedBlockBytes(block, true), 0644)
	}

	// Separator: if the existing file doesn't end with a newline, we add one
	// so the begin marker starts on its own line. This means install-then-
	// uninstall on a non-POSIX file (no trailing newline) adds one byte — the
	// trailing \n. This is the correct trade-off: adding \n normalizes to
	// POSIX convention, whereas stripping it would break files that already
	// had a trailing newline (the common case).
	separator := ""
	if !strings.HasSuffix(content, "\n") {
		separator = "\n"
	}

	merged := content + separator + block
	return os.WriteFile(path, managedBlockBytes(merged, true), 0644)
}

func removeManagedBlock(path, begin, end string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}

	content := string(current)
	if err := validateMarkerTopology(content, begin, end); err != nil {
		return fmt.Errorf("invalid marker topology in %s: %w", path, err)
	}

	idx := strings.Index(content, begin)
	if idx < 0 {
		return nil
	}
	endIdx := strings.Index(content[idx:], end)
	if endIdx < 0 {
		return fmt.Errorf("managed block start found without end marker in %s", path)
	}
	endAbs := idx + endIdx + len(end)
	after := content[endAbs:]
	if strings.HasPrefix(after, "\n") {
		after = after[1:]
	}

	updated := content[:idx] + after
	return os.WriteFile(path, []byte(updated), 0644)
}

func canonicalManagedBlock(begin, end, body string) string {
	return strings.TrimSpace(strings.Join([]string{begin, strings.TrimSpace(body), end}, "\n"))
}

func managedBlockBytes(content string, ensureTrailingNewline bool) []byte {
	if content == "" {
		return []byte{}
	}
	if ensureTrailingNewline && !strings.HasSuffix(content, "\n") {
		return []byte(content + "\n")
	}
	return []byte(content)
}

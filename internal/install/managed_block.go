package install

import (
	"fmt"
	"os"
	"strings"
)

func upsertManagedBlock(path, begin, end, body string) error {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	block := canonicalManagedBlock(begin, end, body)
	content := string(current)

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

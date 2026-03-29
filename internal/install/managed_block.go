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

	block := strings.TrimSpace(strings.Join([]string{begin, strings.TrimSpace(body), end}, "\n")) + "\n"
	content := string(current)

	if idx := strings.Index(content, begin); idx >= 0 {
		endIdx := strings.Index(content[idx:], end)
		if endIdx < 0 {
			return fmt.Errorf("managed block start found without end marker in %s", path)
		}
		endAbs := idx + endIdx + len(end)
		replaced := content[:idx] + block + content[endAbs:]
		return os.WriteFile(path, normalizeManagedBlockWhitespace(replaced), 0644)
	}

	if strings.TrimSpace(content) == "" {
		return os.WriteFile(path, []byte(block), 0644)
	}

	merged := strings.TrimRight(content, "\n") + "\n\n" + block
	return os.WriteFile(path, normalizeManagedBlockWhitespace(merged), 0644)
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
	updated := content[:idx] + content[endAbs:]
	return os.WriteFile(path, normalizeManagedBlockWhitespace(updated), 0644)
}

func normalizeManagedBlockWhitespace(s string) []byte {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, line)
	}
	text := strings.TrimSpace(strings.Join(out, "\n"))
	if text == "" {
		return []byte{}
	}
	return []byte(text + "\n")
}

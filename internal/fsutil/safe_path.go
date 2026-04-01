package fsutil

import (
	"os"
	"path/filepath"
	"strings"
)

// HasAncestorSymlink checks whether any directory component between root and path
// is a symlink. Only checks components below root to avoid false positives on
// system-level symlinks (for example, /var -> /private/var on macOS).
func HasAncestorSymlink(path, root string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") {
		return true
	}

	current := root
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}

	return false
}

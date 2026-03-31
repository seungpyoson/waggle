package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func safeWriteFile(path string, data []byte, perm os.FileMode, root string) error {
	if hasAncestorSymlink(path, root) {
		return fmt.Errorf("refusing to write through ancestor symlink: %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to overwrite symlink: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	if err := atomicWriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func safeRemove(path string, root string) error {
	if hasAncestorSymlink(path, root) {
		return fmt.Errorf("refusing to remove through ancestor symlink: %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to remove symlink: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	return os.Remove(path)
}

func safeRemoveAll(path string, root string) error {
	if hasAncestorSymlink(path, root) {
		return fmt.Errorf("refusing to remove through ancestor symlink: %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to remove symlink: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	return os.RemoveAll(path)
}

func safeMkdirAll(path string, root string, perm os.FileMode) error {
	if hasAncestorSymlink(path, root) {
		return fmt.Errorf("refusing to create path with ancestor symlink: %s", path)
	}
	return os.MkdirAll(path, perm)
}

// atomicWriteFile writes data to path atomically via temp+rename.
// Prevents TOCTOU attacks where the target is swapped between check and write.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".waggle-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	tmpName = ""
	return nil
}

// hasAncestorSymlink checks whether any directory component between root and path
// is a symlink. Only checks components below root to avoid false positives on
// system-level symlinks (e.g., /var → /private/var on macOS).
func hasAncestorSymlink(path, root string) bool {
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

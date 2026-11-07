// Package files moves finished scan files to their destination.
package files

import (
	"fmt"
	"os"
	"path/filepath"
)

// MoveScannedFile moves the scanned file from src to the target directory,
// optionally renaming it. If targetPath is empty, the file is moved to
// targetDir with its original filename. If targetPath is set, the file is
// moved and renamed to targetPath.
func MoveScannedFile(src, targetDir, targetPath string) (string, error) {
	if targetDir == "" && targetPath == "" {
		return src, nil // no move needed
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("creating target dir %s: %w", targetDir, err)
	}

	var dest string
	if targetPath != "" {
		dest = targetPath
	} else {
		dest = filepath.Join(targetDir, filepath.Base(src))
	}

	// If source and destination are the same, skip.
	if src == dest {
		return dest, nil
	}

	// Remove destination if it exists (overwrite).
	os.Remove(dest)

	if err := os.Rename(src, dest); err != nil {
		// Rename can fail across filesystems; fall back to copy + remove.
		if err := copyFile(src, dest); err != nil {
			return "", fmt.Errorf("moving scan output: %w", err)
		}
		os.Remove(src)
	}
	return dest, nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

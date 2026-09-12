// Package datadir prepares local storage for Yamata.
package datadir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Prepare creates path if necessary and checks file creation, writing, and removal.
// It returns an absolute path. Existing files and permissions remain unchanged.
// The caller selects a directory owned by this project, including any symlink target.
func Prepare(path string) (string, error) {
	if path == "" {
		return "", errors.New("data directory must not be empty")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	if err := os.MkdirAll(resolved, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	file, err := os.CreateTemp(resolved, ".yamata-check-*")
	if err != nil {
		return "", fmt.Errorf("create data directory check file: %w", err)
	}

	// Close and remove the check file even when writing fails.
	_, writeErr := file.WriteString("yamata\n")
	closeErr := file.Close()
	removeErr := os.Remove(file.Name())
	if err := errors.Join(writeErr, closeErr, removeErr); err != nil {
		return "", fmt.Errorf("check data directory: %w", err)
	}
	return resolved, nil
}

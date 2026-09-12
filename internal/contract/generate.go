//go:build ignore

// This command refreshes the embedded schema and the public file hashes.
package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := generate(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	const root = "../../contract/v1"
	var manifest strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == "SHA256SUMS" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(data), filepath.ToSlash(name))
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(manifest.String()), 0o644); err != nil {
		return err
	}
	schema, err := os.ReadFile(filepath.Join(root, "exchange.schema.json"))
	if err != nil {
		return err
	}
	return os.WriteFile("schema.json", schema, 0o644)
}

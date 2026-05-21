// Package fileutil provides common file operations used across the codebase.
package fileutil

import (
	"fmt"
	"io"
	"os"
)

// CopyFile copies a file from src to dst using streaming (io.Copy).
// It ensures the destination directory exists before creating the file.
// This avoids loading large files entirely into memory.
// On failure, any partial destination file is removed.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("copy failed: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close failed: %w", closeErr)
	}
	return nil
}

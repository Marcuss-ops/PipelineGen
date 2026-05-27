package ingest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"velox/go-master/internal/pkg/hashutil"
)

func (s *Service) materializeImage(sourcePath, filename string, req *Request) (string, string, func(), error) {
	if strings.TrimSpace(sourcePath) == "" {
		return "", "", nil, fmt.Errorf("image source path is required")
	}

	sourcePath = strings.TrimSpace(sourcePath)
	if strings.TrimSpace(filename) == "" {
		filename = filepath.Base(sourcePath)
	}

	slug := slugify(firstNonEmpty(req.Group, req.Name, req.SourceID, "image"))
	if slug == "" {
		slug = "image"
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = filepath.Ext(sourcePath)
	}
	if ext == "" {
		ext = ".jpg"
	}

	fullDir := filepath.Join(s.imagesDir, slug)
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return "", "", nil, fmt.Errorf("failed to create image dir: %w", err)
	}

	hash, err := hashutil.MD5File(sourcePath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to hash image source: %w", err)
	}

	dstPath := filepath.Join(fullDir, hash+ext)
	if sameFile(sourcePath, dstPath) {
		return dstPath, filepath.Base(dstPath), nil, nil
	}

	in, err := os.Open(sourcePath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to open image source: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dstPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create image destination: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dstPath)
		return "", "", nil, fmt.Errorf("failed to copy image into storage: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", "", nil, fmt.Errorf("failed to close image destination: %w", err)
	}

	return dstPath, filepath.Base(dstPath), nil, nil
}

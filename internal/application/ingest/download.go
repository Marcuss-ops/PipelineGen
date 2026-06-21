package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) acquireLocalPath(ctx context.Context, kind Kind, req *Request) (string, string, func(), error) {
	localPath := strings.TrimSpace(req.LocalPath)
	filename := strings.TrimSpace(req.Filename)
	if localPath != "" {
		if filename == "" {
			filename = filepath.Base(localPath)
		}
		return localPath, filename, nil, nil
	}

	remoteURL := strings.TrimSpace(req.URL)
	if remoteURL == "" {
		return "", "", nil, fmt.Errorf("local_path or url is required")
	}

	tmpDir, err := os.MkdirTemp(s.tempDir, "media-ingest-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	dstName := filename
	if dstName == "" {
		if parsed, err := url.Parse(remoteURL); err == nil {
			dstName = filepath.Base(parsed.Path)
		}
	}
	if dstName == "" {
		dstName = fmt.Sprintf("%s.bin", string(kind))
	}

	dstPath := filepath.Join(tmpDir, dstName)
	if err := s.downloadToFile(ctx, remoteURL, dstPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", nil, err
	}

	if filename == "" {
		filename = dstName
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}
	return dstPath, filename, cleanup, nil
}

func (s *Service) downloadToFile(ctx context.Context, remoteURL, dstPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("failed to create destination dir: %w", err)
	}

	out, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write downloaded file: %w", err)
	}

	return nil
}

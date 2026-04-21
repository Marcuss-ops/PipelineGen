// Package imagesasset downloads image assets for local caching and DB persistence.
package imagesasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/imagesdb"
)

// Result holds the outcome of an image asset download.
type Result struct {
	LocalPath    string
	MimeType     string
	FileSize     int64
	AssetHash    string
	DownloadedAt time.Time
	Cached       bool
}

// Downloader downloads images to a local cache directory.
type Downloader struct {
	rootDir string
	client  *http.Client
}

// New creates a new downloader.
func New(rootDir string) *Downloader {
	return NewWithClient(rootDir, nil)
}

// NewWithClient creates a downloader with a custom HTTP client.
func NewWithClient(rootDir string, client *http.Client) *Downloader {
	if strings.TrimSpace(rootDir) == "" {
		rootDir = filepath.Join(os.TempDir(), "velox-images")
	}
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	return &Downloader{
		rootDir: rootDir,
		client:  client,
	}
}

// Download fetches an image URL and stores it locally.
func (d *Downloader) Download(ctx context.Context, rec imagesdb.ImageRecord) (*Result, error) {
	if strings.TrimSpace(rec.ImageURL) == "" {
		return nil, fmt.Errorf("image url is required")
	}

	assetDir := filepath.Join(d.rootDir, safeName(rec.Entity))
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create image cache dir: %w", err)
	}

	hashInput := rec.ImageURL + "|" + rec.Entity + "|" + rec.Query
	sum := sha256.Sum256([]byte(hashInput))
	assetHash := hex.EncodeToString(sum[:])
	if matches, _ := filepath.Glob(filepath.Join(assetDir, assetHash+".*")); len(matches) > 0 {
		for _, match := range matches {
			if st, err := os.Stat(match); err == nil && !st.IsDir() && st.Size() > 0 {
				ext := strings.ToLower(filepath.Ext(match))
				mimeType := mime.TypeByExtension(ext)
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				return &Result{
					LocalPath:    match,
					MimeType:     mimeType,
					FileSize:     st.Size(),
					AssetHash:    assetHash,
					DownloadedAt: time.Now().UTC(),
					Cached:       true,
				}, nil
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rec.ImageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; VeloxEditing/1.0)")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("image download failed: status %d", resp.StatusCode)
	}

	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if i := strings.Index(mimeType, ";"); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	ext := extensionFromMime(mimeType, rec.ImageURL)
	localPath := filepath.Join(assetDir, assetHash+ext)
	if st, err := os.Stat(localPath); err == nil && st.Size() > 0 {
		return &Result{
			LocalPath:    localPath,
			MimeType:     mimeType,
			FileSize:     st.Size(),
			AssetHash:    assetHash,
			DownloadedAt: time.Now().UTC(),
			Cached:       true,
		}, nil
	}

	tmpPath := localPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)
	size, err := io.Copy(writer, resp.Body)
	if err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if err := os.Rename(tmpPath, localPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}

	return &Result{
		LocalPath:    localPath,
		MimeType:     mimeType,
		FileSize:     size,
		AssetHash:    hex.EncodeToString(hasher.Sum(nil)),
		DownloadedAt: time.Now().UTC(),
		Cached:       false,
	}, nil
}

func extensionFromMime(mimeType, sourceURL string) string {
	if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
		return exts[0]
	}
	if u, err := url.Parse(sourceURL); err == nil {
		ext := strings.ToLower(filepath.Ext(u.Path))
		if ext != "" && len(ext) <= 5 {
			return ext
		}
	}
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".img"
	}
}

func safeName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "unknown"
	}
	return out
}

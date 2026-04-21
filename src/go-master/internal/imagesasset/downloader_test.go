package imagesasset

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"velox/go-master/internal/imagesdb"
)

func TestDownloaderCachesAndDownloads(t *testing.T) {
	var img bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&img, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}

	hits := 0
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			hits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(bytes.NewReader(img.Bytes())),
				Request:    req,
			}, nil
		}),
	}

	tmpDir := t.TempDir()
	dl := NewWithClient(tmpDir, client)
	rec := imagesdb.ImageRecord{
		Entity:   "Canada mountains",
		Query:    "Canada mountains",
		ImageURL: "https://example.com/image.png",
	}

	first, err := dl.Download(context.Background(), rec)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if first == nil || first.LocalPath == "" {
		t.Fatalf("Download() local path empty")
	}
	if first.Cached {
		t.Fatalf("first download unexpectedly cached")
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}

	second, err := dl.Download(context.Background(), rec)
	if err != nil {
		t.Fatalf("Download() second error = %v", err)
	}
	if second == nil || !second.Cached {
		t.Fatalf("second download should be cached")
	}
	if second.LocalPath != first.LocalPath {
		t.Fatalf("cached path mismatch: %q vs %q", second.LocalPath, first.LocalPath)
	}
	if hits != 1 {
		t.Fatalf("server hits after cache = %d, want 1", hits)
	}
	if _, err := filepath.EvalSymlinks(first.LocalPath); err != nil && !filepath.IsAbs(first.LocalPath) {
		t.Fatalf("local path invalid: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

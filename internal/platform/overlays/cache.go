package overlays

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"bytes"
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Cache is disposable, content-addressed renderer state. It is never the
// authority for job state or artifact identity.
type Cache struct{ Root string }

func NewCache(root string) (*Cache, error) {
	if root == "" {
		return nil, fmt.Errorf("overlay cache root is empty")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("overlay cache: %w", err)
	}
	return &Cache{Root: root}, nil
}

func (c *Cache) Path(namespace, key, filename string) string {
	if c == nil || len(key) < 2 {
		return ""
	}
	return filepath.Join(c.Root, namespace, key[:2], key, filename)
}

func (c *Cache) Put(namespace, key, filename string, r io.Reader) (string, error) {
	if c == nil || len(key) < 2 {
		return "", fmt.Errorf("overlay cache: invalid key")
	}
	p := c.Path(namespace, key, filename)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".part-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, p); err != nil {
		return "", err
	}
	return p, nil
}

func (c *Cache) PutFile(namespace, key, filename, source string) (string, error) {
	f, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return c.Put(namespace, key, filename, f)
}

func (c *Cache) Has(namespace, key, filename string) bool {
	p := c.Path(namespace, key, filename)
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// EnsureAsset downloads and verifies one plan asset when it is not already
// cached. Missing URL on a cache miss is a hard error: prepare is an
// optimization, but render must never silently render with the wrong asset.
func (c *Cache) EnsureAsset(ctx context.Context, refURL, sha256Hex string) (string, error) {
	if len(sha256Hex) < 2 {
		return "", fmt.Errorf("overlay asset: sha256 is required")
	}
	if c.Has("assets", sha256Hex, "asset.bin") {
		return c.Path("assets", sha256Hex, "asset.bin"), nil
	}
	if refURL == "" {
		return "", fmt.Errorf("overlay asset %s: URL is required on cache miss", sha256Hex)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, refURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("overlay asset download: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	got := digest.SHA256Bytes(b)
	if got != sha256Hex {
		return "", fmt.Errorf("overlay asset SHA-256 mismatch: got %s want %s", got, sha256Hex)
	}
	return c.Put("assets", sha256Hex, "asset.bin", bytes.NewReader(b))
}



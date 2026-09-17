package overlays

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/assets/materializer"
)

// AssetRef is the minimal content-addressed asset identity needed by the
// canonical overlay asset preparer.
type AssetRef struct {
	AssetID string
	URL     string
	SHA256  string
}

// Cache is disposable, content-addressed renderer state. It is never the
// authority for job state or artifact identity.
type Cache struct {
	Root string
	// assets is the canonical materializer (internal/platform/assets). The
	// cache no longer implements "download and verify" itself: that invariant
	// has ONE owner. A nil field falls back to the package default, so the
	// zero value and struct literals keep working.
	assets *materializer.Materializer
}

// defaultMaterializer backs caches constructed as struct literals.
var defaultMaterializer = materializer.New(materializer.Options{})

// materializerFor returns the cache's materializer, falling back to the
// canonical default so a zero-value Cache never silently skips verification.
func (c *Cache) materializerFor() *materializer.Materializer {
	if c != nil && c.assets != nil {
		return c.assets
	}
	return defaultMaterializer
}

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

// assetNamespace is the content-addressed namespace whose key IS a digest.
const assetNamespace = "assets"

// Has reports whether a cache entry exists. In the content-addressed `assets`
// namespace it additionally re-verifies the bytes against the key, because for
// that namespace "the file is there" and "the asset is there" are different
// questions: a truncated or replaced file must read as ABSENT so no caller can
// skip materialization and render the wrong pixels. Other namespaces are plain
// presence probes — their keys are not content addresses, so there is nothing
// to verify them against.
func (c *Cache) Has(namespace, key, filename string) bool {
	p := c.Path(namespace, key, filename)
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	if namespace != assetNamespace {
		return true
	}
	// A zero-byte file can never be a valid media asset, and it is the exact
	// shape a crashed install leaves behind.
	if info.Size() == 0 {
		return false
	}
	return c.materializerFor().Matches(p, key)
}

// EnsureAsset materializes one plan asset under its content address, using the
// canonical materializer. Missing URL on a cache miss is a hard error: prepare
// is an optimization, but render must never silently render with the wrong
// asset.
//
// Three behaviours changed when this delegated, all of them fixes rather than
// refactors:
//
//  1. a cache HIT is re-verified. The previous implementation returned the path
//     whenever the file existed, so a truncated or replaced cache file was
//     rendered silently — the single most dangerous thing a content-addressed
//     cache can do.
//  2. the download streams through the materializer and is hashed while it is
//     written, instead of buffering the whole asset in memory.
//  3. the HTTP client is bounded. http.DefaultClient has no timeout, so a
//     hanging origin pinned the caller and the lease it held.
//
// The on-disk layout is unchanged (`assets/<addr[:2]>/<addr>/asset.bin`), so
// existing caches keep working.
func (c *Cache) EnsureAsset(ctx context.Context, refURL, sha256Hex string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("overlay cache is not configured")
	}
	address := strings.ToLower(strings.TrimSpace(sha256Hex))
	if len(address) < 2 {
		return "", fmt.Errorf("overlay asset: sha256 is required")
	}
	materialized, err := c.materializerFor().Ensure(ctx,
		materializer.Ref{SHA256: address},
		materializer.Source{URL: refURL},
		c.Path("assets", address, "asset.bin"))
	if err != nil {
		return "", err
	}
	return materialized.Path, nil
}

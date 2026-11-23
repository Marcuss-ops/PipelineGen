package cliprender

import (
	"context"
	"fmt"
	"strings"
)

// memoryRenderCache is the in-process double for tests. It lives in _test.go
// because it has no production caller: the media-disabled path and the tests
// both use the RenderCache port, and the only implementation shipped in the
// binary is pgRenderCache.
//
// It holds only the records Put into it and has no view of media_assets, so it
// cannot reproduce the postgres cache's staleness check. That check is pinned
// against a live PostgreSQL in render_cache_postgres_test.go; keeping the
// in-process double validation-free is deliberate — a double that pretended to
// validate would make the hermetic suite pass over the very bug the SQL JOIN
// exists to prevent.
type memoryRenderCache struct {
	m map[string]*RenderCacheRecord
}

func newMemoryRenderCache() *memoryRenderCache {
	return &memoryRenderCache{m: make(map[string]*RenderCacheRecord)}
}

func (c *memoryRenderCache) Get(_ context.Context, fingerprint string) (*RenderCacheRecord, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return nil, fmt.Errorf("clip.render cache: fingerprint is required")
	}
	if rec, ok := c.m[fingerprint]; ok {
		cp := *rec
		return &cp, nil
	}
	return nil, ErrCacheMiss
}

func (c *memoryRenderCache) GetMany(_ context.Context, fingerprints []string) (map[string]*RenderCacheRecord, error) {
	out := make(map[string]*RenderCacheRecord, len(fingerprints))
	for _, fp := range fingerprints {
		fp = strings.ToLower(strings.TrimSpace(fp))
		if rec, ok := c.m[fp]; ok {
			cp := *rec
			out[fp] = &cp
		}
	}
	return out, nil
}

func (c *memoryRenderCache) Put(_ context.Context, rec *RenderCacheRecord) error {
	if rec == nil || strings.TrimSpace(rec.Fingerprint) == "" {
		return fmt.Errorf("clip.render cache: record/fingerprint is required")
	}
	cp := *rec
	cp.Fingerprint = strings.ToLower(cp.Fingerprint)
	c.m[cp.Fingerprint] = &cp
	return nil
}

// Ensure memory cache satisfies the port (keeps the test helper honest).
var _ RenderCache = (*memoryRenderCache)(nil)

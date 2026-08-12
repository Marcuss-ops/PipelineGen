// Package recovery provides a read-only adapter for recovering enrichment
// from historical Qdrant projections. It never mutates Qdrant.
package recovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

type Reader struct {
	client      *transport.Client
	collections []string
	pageSize    int
	once        sync.Once
	mu          sync.RWMutex
	index       map[string]*enrichment.HistoricalText
	err         error
}

func NewReader(client *transport.Client, collections []string, pageSize int) (*Reader, error) {
	if client == nil {
		return nil, fmt.Errorf("qdrant recovery: client is required")
	}
	clean := make([]string, 0, len(collections))
	seen := map[string]struct{}{}
	for _, c := range collections {
		c = strings.TrimSpace(c)
		if c != "" {
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				clean = append(clean, c)
			}
		}
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("qdrant recovery: at least one historical collection is required")
	}
	if pageSize <= 0 {
		pageSize = 500
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	return &Reader{client: client, collections: clean, pageSize: pageSize, index: map[string]*enrichment.HistoricalText{}}, nil
}

func (r *Reader) FindByAssetID(ctx context.Context, assetID string) (*enrichment.HistoricalText, error) {
	r.once.Do(func() { r.err = r.load(ctx) })
	if r.err != nil {
		return nil, r.err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item := r.index[strings.TrimSpace(assetID)]
	if item == nil {
		return nil, nil
	}
	copy := *item
	return &copy, nil
}

func (r *Reader) load(ctx context.Context) error {
	for _, collection := range r.collections {
		offset := ""
		for {
			page, err := r.client.ScrollPoints(ctx, collection, offset, r.pageSize, nil)
			if err != nil {
				return fmt.Errorf("scroll historical collection %s: %w", collection, err)
			}
			if page == nil {
				break
			}
			for _, point := range page.Points {
				p := point.Payload
				id := stringValue(p["asset_id"])
				if id == "" {
					continue
				}
				candidate := &enrichment.HistoricalText{AssetID: id, Description: firstString(p, "description", "semantic_description", "visual_summary"), SearchText: stringValue(p["search_text"]), Projection: collection}
				r.mu.Lock()
				existing := r.index[id]
				if existing == nil || better(candidate, existing) {
					r.index[id] = candidate
				}
				r.mu.Unlock()
			}
			if page.NextOffset == "" {
				break
			}
			offset = page.NextOffset
		}
	}
	return nil
}

func better(a, b *enrichment.HistoricalText) bool {
	aScore := score(a)
	bScore := score(b)
	if aScore != bScore {
		return aScore > bScore
	}
	return a.Projection < b.Projection
}
func score(t *enrichment.HistoricalText) int {
	return len(strings.TrimSpace(t.Description))*2 + len(strings.TrimSpace(t.SearchText))
}
func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := stringValue(m[k]); v != "" {
			return v
		}
	}
	return ""
}

// Collections returns a deterministic copy for diagnostics and manifests.
func (r *Reader) Collections() []string {
	out := append([]string(nil), r.collections...)
	sort.Strings(out)
	return out
}

var _ enrichment.HistoricalTextReader = (*Reader)(nil)

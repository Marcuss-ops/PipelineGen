// Package retrieved (application/images/retrieved) — provider_duckduckgo.go
// holds the DuckDuckGoProvider concrete implementation. Per PR-IMG-SPLIT-3
// (July 2026), each concrete provider lives in its own file.
//
// DuckDuckGoProvider scrapes DuckDuckGo image search via the public
// /i.js endpoint. Healthy() returns nil (DDG has no health endpoint).
package retrieved

import (
	"context"
	"strings"
	"time"

	nethttp "net/http"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// DuckDuckGoProvider scrapes DuckDuckGo image search via the public
// /i.js endpoint.
type DuckDuckGoProvider struct {
	bridge  StorageBridge
	client  httpDoer
	log     *zap.Logger
	baseURL string
}

// NewDuckDuckGoProvider constructs a DuckDuckGoProvider wired to the
// parent ImageStorageService via StorageBridge.
func NewDuckDuckGoProvider(bridge StorageBridge, client httpDoer, log *zap.Logger) *DuckDuckGoProvider {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	return &DuckDuckGoProvider{
		bridge:  bridge,
		client:  client,
		log:     log,
		baseURL: "https://duckduckgo.com",
	}
}

func (p *DuckDuckGoProvider) Name() asset.ImageProvider { return asset.ProviderDuckDuckGo }

// Healthy always returns nil — DDG has no dedicated health endpoint
// but requests will go out. The registry caller relies on per-query
// success to detect rate-limits.
func (p *DuckDuckGoProvider) Healthy(_ context.Context) error {
	return nil
}

func (p *DuckDuckGoProvider) Search(ctx context.Context, query string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if p == nil || p.bridge == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if many, ok := p.bridge.(interface {
		SearchDDGWideMany(context.Context, string, int) []string
	}); ok {
		urls := many.SearchDDGWideMany(ctx, query, 10)
		out := make([]RetrievalSearchResult, 0, len(urls))
		for _, imgURL := range urls {
			if strings.TrimSpace(imgURL) == "" {
				continue
			}
			out = append(out, RetrievalSearchResult{
				Provider: asset.ProviderDuckDuckGo, Origin: asset.ImageOriginRetrieved,
				PreviewURL: imgURL, PageURL: imgURL, License: "Unknown", Author: "Unknown",
			})
		}
		return out, nil
	}
	imgURL := p.bridge.SearchDDGWide(ctx, query)
	if imgURL == "" {
		return nil, nil
	}
	return []RetrievalSearchResult{{
		Provider:   asset.ProviderDuckDuckGo,
		Origin:     asset.ImageOriginRetrieved,
		PreviewURL: imgURL,
		PageURL:    imgURL,
		License:    "Unknown",
		Author:     "Unknown",
	}}, nil
}

// ID returns the canonical string ID of this provider.
func (p *DuckDuckGoProvider) ID() string { return string(p.Name()) }

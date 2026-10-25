// Package retrieved (application/images/retrieved) — provider_duckduckgo.go
// holds the DuckDuckGoProvider concrete implementation. Per PR-IMG-SPLIT-3
// (July 2026), each concrete provider lives in its own file.
//
// DuckDuckGoProvider scrapes DuckDuckGo image search via the public
// /i.js endpoint. Healthy() returns nil (DDG has no health endpoint).
package retrieved

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/duckduckgo"
	"go.uber.org/zap"
	nethttp "net/http"
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

func (p *DuckDuckGoProvider) Name() detail.ImageProvider { return detail.ProviderDuckDuckGo }

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
		if len(urls) == 0 {
			if err := p.challengeError(); err != nil {
				return nil, err
			}
		}
		out := make([]RetrievalSearchResult, 0, len(urls))
		for _, imgURL := range urls {
			if strings.TrimSpace(imgURL) == "" {
				continue
			}
			out = append(out, RetrievalSearchResult{
				Provider: detail.ProviderDuckDuckGo, Origin: detail.ImageOriginRetrieved,
				PreviewURL: imgURL, PageURL: imgURL, License: "Unknown", Author: "Unknown",
			})
		}
		return out, nil
	}
	imgURL := p.bridge.SearchDDGWide(ctx, query)
	if imgURL == "" {
		if err := p.challengeError(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return []RetrievalSearchResult{{
		Provider:   detail.ProviderDuckDuckGo,
		Origin:     detail.ImageOriginRetrieved,
		PreviewURL: imgURL,
		PageURL:    imgURL,
		License:    "Unknown",
		Author:     "Unknown",
	}}, nil
}

// challengeError turns an empty answer caused by an anti-bot cooldown into a
// typed refusal. The retrieval registry folds provider errors into the union
// and keeps its siblings, so the honest classification costs nothing and stops
// a blocked provider from being reported as "no results".
func (p *DuckDuckGoProvider) challengeError() error {
	if p == nil || p.bridge == nil {
		return nil
	}
	probe, ok := p.bridge.(interface{ DuckDuckGoChallenged() bool })
	if !ok || !probe.DuckDuckGoChallenged() {
		return nil
	}
	return &duckduckgo.Challenge{Reason: "cooldown active"}
}

// ID returns the canonical string ID of this provider.
func (p *DuckDuckGoProvider) ID() string { return string(p.Name()) }

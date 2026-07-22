// Package retrieved (application/images/retrieved) — provider_searxng.go
// holds the SearXNGProvider concrete implementation. Per PR-IMG-SPLIT-3
// (July 2026), each concrete provider lives in its own file.
//
// SearXNGProvider searches the configured SearXNG instance for images.
// Healthy() probes /healthz; Search returns 0 results when the
// instance is unreachable or unconfigured.
package retrieved

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// SearXNGProvider searches the configured SearXNG instance for
// images. Healthy() probes /healthz; Search returns 0 results when
// the instance is unreachable or unconfigured.
type SearXNGProvider struct {
	bridge    StorageBridge
	client    httpDoer
	log       *zap.Logger
	baseURL   string // resolved from cfg.External.SearxngURL; empty = unconfigured
	probePath string
}

// NewSearXNGProvider constructs a SearXNGProvider. baseURL is the
// canonical SearXNG root (e.g. http://localhost:18080); empty means
// provider is unconfigured and will be skipped at Search/Healthy time.
func NewSearXNGProvider(bridge StorageBridge, client httpDoer, log *zap.Logger, baseURL string) *SearXNGProvider {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	return &SearXNGProvider{
		bridge:    bridge,
		client:    client,
		log:       log,
		baseURL:   strings.TrimRight(baseURL, "/"),
		probePath: "/healthz",
	}
}

func (p *SearXNGProvider) Name() asset.ImageProvider { return asset.ProviderSearXNG }

func (p *SearXNGProvider) Healthy(ctx context.Context) error {
	if p.baseURL == "" {
		return errors.New("searxng: base URL not configured")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := nethttp.NewRequestWithContext(probeCtx, "GET", p.baseURL+p.probePath, nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("searxng unreachable: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *SearXNGProvider) Search(ctx context.Context, query string, opts routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error) {
	if p.baseURL == "" || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	imgURL := p.bridge.SearchSearXNGImages(ctx, query)
	if imgURL == "" {
		return nil, nil
	}
	return []routing.RetrievalSearchResult{{
		Provider:   asset.ProviderSearXNG,
		Origin:     asset.ImageOriginRetrieved,
		PreviewURL: imgURL,
		PageURL:    imgURL,
		License:    "Unknown",
		Author:     "Unknown",
	}}, nil
}

// ID returns the canonical string ID of this provider.
func (p *SearXNGProvider) ID() string { return string(p.Name()) }

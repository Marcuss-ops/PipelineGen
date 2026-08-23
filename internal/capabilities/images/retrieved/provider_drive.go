// Package retrieved (application/images/retrieved) — provider_drive.go
// holds the DriveImageProvider concrete implementation. Per PR-IMG-SPLIT-3
// (July 2026), each concrete provider lives in its own file.
//
// DriveImageProvider surfaces images already ingested into the
// project's Google Drive asset tree by previous runs. It's a
// short-circuit step: if we already have an image for the slug,
// don't bother with the web search fallback.
//
// The provider also serves as the canonical migration target for
// Step 9 (Style-aware assets) and beyond, when the on-disk index
// must be queried before any network round-trip.
package retrieved

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// DriveImageProvider surfaces images already ingested into the
// project's Google Drive asset tree by previous runs.
type DriveImageProvider struct {
	bridge StorageBridge
	log    *zap.Logger
}

func NewDriveImageProvider(bridge StorageBridge, log *zap.Logger) *DriveImageProvider {
	return &DriveImageProvider{bridge: bridge, log: log}
}

func (p *DriveImageProvider) Name() asset.ImageProvider { return asset.ProviderDrive }

func (p *DriveImageProvider) Healthy(_ context.Context) error { return nil }

func (p *DriveImageProvider) Search(_ context.Context, query string, _ routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error) {
	slug := strings.TrimSpace(query)
	if slug == "" {
		return nil, nil
	}
	hits := p.bridge.SearchBySlug(context.Background(), slug, 1)
	out := make([]routing.RetrievalSearchResult, 0, len(hits))
	for _, url := range hits {
		out = append(out, routing.RetrievalSearchResult{
			Provider:   asset.ProviderDrive,
			Origin:     asset.ImageOriginRetrieved,
			PreviewURL: url,
			PageURL:    url,
			License:    "Unknown",
			Author:     "Unknown",
		})
	}
	return out, nil
}

// ID returns the canonical string ID of this provider.
func (p *DriveImageProvider) ID() string { return string(p.Name()) }

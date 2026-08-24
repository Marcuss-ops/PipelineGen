// Package app — YouTube enrichment + fetch adapters
// consolidated from youtube_enrichment_adapter.go + youtube_fetch_adapter.go
// (PR-GODOBJ-Azione-4, July 2026).
//
// 2 adapters: youtubeEnrichmentAdapter, sourcingFetchAdapter.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
)

// ── youtubeEnrichmentAdapter ───────────────────────────────────────────

type youtubeEnrichmentAdapter struct {
	jobs       sourcing.JobsPort
	enrichment sourcing.EnrichmentPort
	search     sourcing.SearchProviderPort
	config     sourcing.ConfigPort
}

func (a *youtubeEnrichmentAdapter) IndexingEnabled() bool {
	return a.enrichment != nil && a.jobs != nil
}

func (a *youtubeEnrichmentAdapter) DispatchPostRegister(ctx context.Context, clipID, source, localPath string) error {
	if a.jobs == nil {
		return nil
	}
	_, err := a.jobs.Enqueue(ctx, sourcing.EnqueueRequest{
		Type:       "media.enrich",
		MaxRetries: 1,
		Payload: sourcing.JobPayload{
			"asset_id":   clipID,
			"source":     source,
			"local_path": localPath,
		},
	})
	return err
}

func (a *youtubeEnrichmentAdapter) SearchRelated(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
	if a.search == nil {
		return nil, nil
	}
	return a.search.Search(ctx, query, limit)
}

func (a *youtubeEnrichmentAdapter) FolderDefaults() (clipsFolder, rootFolder string) {
	if a.config == nil {
		return "", ""
	}
	return a.config.ClipsFolder(), a.config.RootFolder()
}

// ── sourcingFetchAdapter ───────────────────────────────────────────────

type sourcingFetchAdapter struct {
	registry *providers.Registry
}

// Compile-time pin: sourcingFetchAdapter satisfies sourcing.FetchProviderPort.
// PR-CLIP-DECOM-5 (July 2026): drift in the Fetch signature surfaces as a
// build failure, not a runtime panic. NoAudio forwarding is the 4th adapter
// layer in the YouTube fetch chain (usecase.FetchRequest → sourcing.FetchRequest
// → providers.FetchRequest → ffmpeg.Processor).
var _ sourcing.FetchProviderPort = (*sourcingFetchAdapter)(nil)

func (a *sourcingFetchAdapter) Fetch(ctx context.Context, req sourcing.FetchRequest) (*sourcing.FetchedAsset, error) {
	if a.registry == nil {
		return nil, fmt.Errorf("register fetch provider registry not configured")
	}
	for _, p := range a.registry.ByCapability(providers.CapabilityFetch) {
		if p.Name() != "youtube" {
			continue
		}
		fp, ok := p.(providers.FetchProvider)
		if !ok {
			continue
		}
		res, err := fp.Fetch(ctx, providers.FetchRequest{
			AssetID:      req.AssetID,
			SourceRef:    req.SourceRef,
			SegmentStart: req.SegmentStart,
			SegmentEnd:   req.SegmentEnd,
			NoAudio:      req.NoAudio,
		})
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		out := &sourcing.FetchedAsset{
			LocalPath: res.LocalPath,
			AssetID:   req.AssetID,
			Name:      "",
			Duration:  0,
			Bytes:     res.Bytes,
			Metadata:  map[string]string{},
		}
		if res.Asset != nil {
			out.AssetID = res.Asset.ID
			out.Name = res.Asset.Name
			out.Duration = res.Asset.Duration
			out.Metadata = map[string]string{}
		}
		return out, nil
	}
	return nil, fmt.Errorf("youtube fetch provider not registered")
}

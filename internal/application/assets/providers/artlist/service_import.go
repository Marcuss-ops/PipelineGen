package artlist

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrAssetMutationDispatcherUnavailable is declared in ports.go (Phase 7
// split: declaration canonical location is the ports surface; this file
// only consumes it). Mirrors QDRANT-002 composition-time fail-closed
// discipline: a missing dispatcher at the import endpoint is a wiring
// defect, not a recoverable runtime condition.

// ImportClip imports a single Artlist clip by its detail page URL.
// When req.Download is false the asset is persisted as STAGING/DISCOVERED
// and returned without downloading the video. When req.Download is true
// the clip is also downloaded, normalized, uploaded to Drive, and
// prepared for indexing via the canonical outbox path.
func (s *Service) ImportClip(ctx context.Context, req *ImportClipRequest) (*ImportClipResponse, error) {
	if s.detailFetcher == nil {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(req.ClipPageURL) == "" {
		return nil, ErrEmpty
	}
	clipID := extractClipIDFromURL(req.ClipPageURL)

	// Avoid duplicate imports: if the clip already exists, return the
	// existing record without scraping the detail page again.
	if s.assetStore != nil {
		if existing, getErr := s.assetStore.Get(ctx, clipID); getErr == nil && existing != nil {
			s.log.Info("artlist import skipped: clip already exists", zap.String("clip_id", existing.ID))
			return existingAssetToImportResponse(existing), nil
		}
	}

	candidate, err := s.detailFetcher.FetchDetails(ctx, req.ClipPageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch clip details: %w", err)
	}
	if candidate == nil {
		return nil, ErrEmptyResult
	}

	clip := candidateToAsset(candidate, req.ClipPageURL)

	resp := &ImportClipResponse{
		OK:           true,
		ClipID:       clip.ID,
		Name:         clip.Name,
		ClipPageURL:  clip.ClipPageURL,
		ThumbnailURL: candidate.ThumbnailURL,
		PreviewURL:   candidate.PreviewURL,
		Tags:         candidate.Keywords,
		Categories:   candidate.Categories,
		Creator:      candidate.Creator,
		Metadata:     make(map[string]any),
	}
	if clip.Metadata != nil {
		resp.Metadata = clip.Metadata
	}
	if candidate.RawMetadata != nil {
		if country, ok := candidate.RawMetadata["country"].(string); ok {
			resp.Country = country
		}
		if loc, ok := candidate.RawMetadata["location"].(string); ok {
			resp.Location = loc
		}
	}

	if !req.Download {
		if s.dispatcher == nil {
			return nil, ErrAssetMutationDispatcherUnavailable
		}
		if err := s.dispatcher.SaveDiscoveredAsset(ctx, clip, asset.StateStaging, asset.StateDiscovered); err != nil {
			return nil, fmt.Errorf("save discovered asset: %w", err)
		}
		resp.Status = "discovered"
		return resp, nil
	}

	item, err := s.runOrchestrator.ImportSingleClip(ctx, req, clip)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		resp.Status = "failed"
		return resp, err
	}

	resp.Status = item.Status
	resp.DriveLink = item.DriveLink
	resp.DriveFileID = item.DriveFileID
	resp.LocalPath = item.LocalPath
	resp.FileHash = item.FileHash
	resp.DownloadLink = item.DownloadLink
	if resp.Status == "" {
		resp.Status = "completed"
	}

	return resp, nil
}

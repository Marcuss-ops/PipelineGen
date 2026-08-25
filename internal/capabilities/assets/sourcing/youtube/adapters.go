// File adapters.go — use-case port ← service port bridge adapters for
// the YouTubeRegistrar. Extracted from service.go per AGENTS.md Pattern 5
// v2 (1 concetto per file; code-motion pura, zero logica cambiata).
//
// Concept scope: every adapter here wraps a canonical sourcing.* port
// into the corresponding usecase.* port so Register can delegate
// sequentially to the 3 use cases in the subpackage usecase/.
//
// Composition root adapter guarantees (PR-CLIP-DECOM-5 + 6, July 2026):
//
//   - fetcherAdapter   ← sourcing.FetchProviderPort ↔ usecase.Fetcher
//   - hasherAdapter    ← hashutil.LegacyMD5File ↔ usecase.FileHasher
//   - publisherAdapter ← sourcing.PublisherPort  ↔ usecase.DrivePublisher
//   - clipIndexerAdapter ← IndexDispatcherPort   ↔ usecase.ClipIndexer
//
// godlike/07 NO-FAKE-AVAILABILITY: each adapter's nil-inner branch
// returns the typed sentinel through fmt.Errorf; compose-time wiring
// gates catch the empty case via adapter_test.go.
package youtube

import (
	"context"
	"fmt"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// ── Adapters (use case port ← service port) ──────────────────────────────────

// fetcherAdapter wraps sourcing.FetchProviderPort for usecase.Fetcher.
type fetcherAdapter struct {
	inner sourcing.FetchProviderPort
}

func (a *fetcherAdapter) Fetch(ctx context.Context, req usecase.FetchRequest) (*usecase.FetchedAsset, error) {
	if a.inner == nil {
		return nil, fmt.Errorf("usecase.fetcherAdapter: inner fetch provider is nil")
	}
	result, err := a.inner.Fetch(ctx, sourcing.FetchRequest{
		AssetID:      req.AssetID,
		SourceRef:    req.SourceRef,
		SegmentStart: req.SegmentStart,
		SegmentEnd:   req.SegmentEnd,
		NoAudio:      req.NoAudio,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("fetcherAdapter: inner Fetch returned nil result")
	}
	return &usecase.FetchedAsset{
		LocalPath: result.LocalPath,
		AssetID:   result.AssetID,
		Name:      result.Name,
		Duration:  result.Duration,
		Bytes:     result.Bytes,
		Metadata:  result.Metadata,
	}, nil
}

// hasherAdapter wraps checksum.LegacyMD5File for usecase.FileHasher.
type hasherAdapter struct{}

func (a *hasherAdapter) MD5File(path string) (string, error) {
	return checksum.LegacyMD5File(path)
}

// publisherAdapter wraps sourcing.PublisherPort for usecase.DrivePublisher.
type publisherAdapter struct {
	inner sourcing.PublisherPort
}

func (a *publisherAdapter) Publish(ctx context.Context, req usecase.PublishRequest) (*usecase.PublishResult, error) {
	if a.inner == nil {
		return nil, fmt.Errorf("usecase.publisherAdapter: inner publisher is nil")
	}

	// The resolved caller folder is an exact destination folder. Keep the
	// application layer on the canonical DestinationFolderID seam; the
	// ParentFolderID escape hatch is reserved for infrastructure/admin.
	result, err := a.inner.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationYouTubeClip,
		LocalPath:           req.LocalPath,
		Filename:            req.Filename,
		Description:         req.Description,
		AssetID:             req.AssetID,
		ProjectID:           req.ProjectID,
		Group:               req.Group,
		Subject:             req.Subject,
		DestinationFolderID: req.RootFolder,
		Category:            req.Category,
		Provider:            req.Provider,
		Tags:                req.Tags,
		Language:            req.Language,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("publisherAdapter: inner Publish returned nil result")
	}
	return &usecase.PublishResult{
		FileID:      result.FileID,
		WebViewLink: result.WebViewLink,
		FolderID:    result.FolderID,
	}, nil
}

// clipIndexerAdapter wraps IndexDispatcherPort for usecase.ClipIndexer.
// PR-CLIP-DECOM-6 (July 2026): bridges the legacy atomic EnqueueAndIndex
// to the use-case-owned ClipIndexer port per Pattern 0.
type clipIndexerAdapter struct {
	inner IndexDispatcherPort
}

func (a *clipIndexerAdapter) EnqueueAndIndex(ctx context.Context, clip usecase.ClipRecord, contentHash string) error {
	if a.inner == nil {
		return fmt.Errorf("usecase.clipIndexerAdapter: inner dispatcher is nil")
	}
	return a.inner.EnqueueAndIndex(ctx, &sourcing.ExistingClip{
		ID:              clip.ID,
		Name:            clip.Name,
		Filename:        clip.Filename,
		Source:          clip.Source,
		SourceURL:       clip.SourceURL,
		SourceProvider:  clip.SourceProvider,
		SourceVideoID:   clip.SourceVideoID,
		StartSec:        clip.StartSec,
		EndSec:          clip.EndSec,
		Category:        clip.Category,
		Tags:            clip.Tags,
		Duration:        clip.Duration,
		LocalPath:       clip.LocalPath,
		LegacyFileMD5:   clip.LegacyFileMD5,
		DriveLink:       clip.DriveLink,
		DriveFileID:     clip.DriveFileID,
		Summary:         clip.Summary,
		Topics:          clip.Topics,
		Speakers:        clip.Speakers,
		MentionedPeople: clip.MentionedPeople,
		Hook:            clip.Hook,
	}, contentHash)
}

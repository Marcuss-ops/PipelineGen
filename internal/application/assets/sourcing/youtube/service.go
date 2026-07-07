// Package youtube — Service is the YouTubeRegistrar use case extracted from
// the historical sourcing.Service.RegisterFromYouTube god method (P0-1 / commit 1,
// June 2026).
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per file):
// the YouTubeRegistrar owns the single-clip YouTube flow as a focused service
// with 8 narrow ports. PR-CLIP-DECOM-5 (July 2026) refactored the 450-line
// Register() god method into a thin orchestrator (~75 lines) that delegates
// metadata resolution, download+hash, and Drive publish to 3 use cases in the
// subpackage usecase/.
//
// PR-YT-DRIVE-SERVICE-COMMENT-CLEANUP (July 2026): the legacy
// `sourcing.DrivePort` field is retired — Publisher is the canonical
// Drive upload canal since FASE 5.
//
// The façade sourcing.Service.RegisterFromYouTube delegates to
// *Service.Register for API stability. Composition root
// internal/app/assets_register_sourcing.go builds *Service with the v2
// adapters.
//
// Package layout (post-split, 4 files per AGENTS.md Pattern 5 v2):
//
//   - service.go          (this file) — slim orchestrator: Service struct + NewService + Register entry-point
//   - errors.go           — typed-error sentinel SSOT
//   - adapters.go         — 4 use-case port ← service port bridge adapters
//   - register_helpers.go — 9 Register-pipeline helper methods (dedupCheck / buildDriveParams / etc.)
//
// Pure code-motion: no API renames, no signature drift, no logic change.
// godlike/06 SSOT: each file owns its single concept; godlike/07
// minimum-blast-radius: same-package visibility lets Register reach
// all helpers + adapters without import cycles.
package youtube

import (
	"context"
	"fmt"
	"strings"
	"time"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube/usecase"
	asset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Service is the YouTubeRegistrar implementation. 8-port surface
// (Fetcher, Clips, Publisher, Transcriber, Metadata, IndexDispatcher,
// Enrichment, Log). Publisher is the canonical Drive upload canal
// since FASE 5.
type Service struct {
	fetcher     sourcing.FetchProviderPort
	clips       sourcing.ClipStorePort
	publisher   sourcing.PublisherPort // FASE 5: canonical Drive upload canal
	transcriber sourcing.TranscriptionPort
	metadata    sourcing.MetadataUploadPort
	indexDisp   IndexDispatcherPort
	enrichment  EnrichmentPort
	log         sourcing.Logger

	// RequireDrive, when true, causes Register to return an error if the
	// Drive Publisher fails (P0.2, July 2026).
	RequireDrive bool
}

// NewService creates a YouTubeRegistrar service. indexDisp is REQUIRED
// (QDRANT-asset-mutation isolation June 2026 forbids the legacy UpsertClip
// fallback). All other ports are best-effort: nil causes the corresponding
// sub-operation to be skipped gracefully.
func NewService(
	fetcher sourcing.FetchProviderPort,
	clips sourcing.ClipStorePort,
	publisher sourcing.PublisherPort,
	transcriber sourcing.TranscriptionPort,
	metadata sourcing.MetadataUploadPort,
	indexDisp IndexDispatcherPort,
	enrichment EnrichmentPort,
	log sourcing.Logger,
) *Service {
	return &Service{
		fetcher:     fetcher,
		clips:       clips,
		publisher:   publisher,
		transcriber: transcriber,
		metadata:    metadata,
		indexDisp:   indexDisp,
		enrichment:  enrichment,
		log:         log,
	}
}

// Register downloads a YouTube clip, uploads to Drive, saves to DB, and
// triggers enrichment/indexing. All sub-operations are best-effort except
// the indexDisp save which is REQUIRED (QDRANT-asset-mutation isolation).
//
// PR-CLIP-DECOM-5 (July 2026): refactored from 450+ lines to a thin
// orchestrator that delegates to 3 use cases in the subpackage usecase/:
//
//	ResolveClipMetadata   — URL parsing, validation, metadata fallback
//	DownloadAndHashClip   — yt-dlp fetch, MD5 hash, clipID derivation
//	PublishClipToDrive    — Drive folder resolution + upload
func (s *Service) Register(ctx context.Context, cmd sourcing.RegisterClipCommand) (*sourcing.RegisterClipResult, error) {
	// ── 1. Resolve metadata (URL + validation + name/desc/duration) ──
	md, err := usecase.ResolveClipMetadata(usecase.ResolveMetadataCommand{
		URL: cmd.URL, Name: cmd.Name, Description: cmd.Description,
		Source: cmd.Source, StartSec: cmd.StartSec, EndSec: cmd.EndSec,
	})
	if err != nil {
		return nil, err
	}

	// ── 2. Dedup pre-check ──────────────────────────────────────────
	if !cmd.Force && s.clips != nil {
		if result := s.dedupCheck(ctx, cmd, md); result != nil {
			return result, nil
		}
	}

	// ── 3. Download + hash via use case ─────────────────────────────
	s.log.Info("fetching YouTube video", "video_id", md.VideoID, "start", md.StartSec, "end", md.EndSec)
	fetched, err := usecase.DownloadAndHashClip(ctx,
		&fetcherAdapter{inner: s.fetcher},
		&hasherAdapter{},
		usecase.DownloadAndHashCommand{
			VideoID:      md.VideoID,
			SourceRef:    md.RawURL,
			SegmentStart: time.Duration(md.StartSec * float64(time.Second)),
			SegmentEnd:   time.Duration(md.EndSec * float64(time.Second)),
		})
	if err != nil {
		return nil, err
	}
	if fetched.FileHash == "" {
		s.log.Warn("file hash empty; proceeding with best-effort clip_id derivation", "video_id", md.VideoID)
	}

	// ── 4. Enrich metadata with fetched data ────────────────────────
	// Pass already-resolved fields to avoid re-parsing the URL; only the
	// fetched metadata enriches name/description/duration.
	md2, enrichErr := usecase.ResolveClipMetadata(usecase.ResolveMetadataCommand{
		URL: md.RawURL, Name: cmd.Name, Description: cmd.Description,
		Source: md.Source, StartSec: md.StartSec, EndSec: md.EndSec,
		FetchedName: fetched.Name, FetchedDescription: fetched.Metadata["youtube_description"],
		FetchedDuration: fetched.Duration,
	})
	if enrichErr != nil {
		s.log.Warn("metadata enrichment failed (using pre-fetch metadata)", "error", enrichErr)
	} else {
		md = md2
	}
	s.warnNameCollision(ctx, md.Name)
	if md.DurationSec > 0 && md.EndSec > md.DurationSec {
		s.log.Warn("end exceeds video duration, clip was truncated", "end", md.EndSec, "duration", md.DurationSec)
	}

	// ── 5. Drive: build params + publish via use case ───────────────
	driveFilename, driveDesc, videoSlug, group := s.buildDriveParams(cmd, md)
	var pubResult *usecase.PublishClipResult
	var pubErr error
	if s.publisher != nil {
		// PR-YT-CLIP-SEMANTIC-LOCATION-FIX: thread Category, Provider,
		// Tags, and Language from the command into the publish request
		// so the Drive Publisher's YouTubeClipPath can build the correct
		// folder hierarchy from semantic metadata. Provider defaults to
		// "youtube" when Location.Provider is empty.
		provider := strings.TrimSpace(cmd.Location.Provider)
		if provider == "" {
			provider = "youtube"
		}
		pubResult, pubErr = usecase.PublishClipToDrive(ctx,
			&publisherAdapter{inner: s.publisher},
			usecase.PublishClipCommand{
				AssetID:     fetched.ClipID,
				Group:       group,
				Subject:     videoSlug,
				RootFolder:  strings.TrimSpace(cmd.FolderID),
				LocalPath:   fetched.LocalPath,
				Filename:    driveFilename,
				Description: driveDesc,
				Category:    cmd.Category,
				Provider:    provider,
				Tags:        cmd.Tags,
				Language:    cmd.Location.Language,
			})
	}
	uploadResult, targetFolderID, deliveryStatus := s.processPublishResult(md.VideoID, pubResult, pubErr)
	if s.RequireDrive && deliveryStatus == asset.AssetPublishFailed {
		return nil, fmt.Errorf("%w: publisher returned %v", ErrYouTubeDriveRequired, deliveryStatus)
	}

	clipID, fileHash := fetched.ClipID, fetched.FileHash

	// ── 6. Transcribe (best-effort) ─────────────────────────────────
	transcript, detectedLang := "", ""
	if s.transcriber != nil {
		transcript, detectedLang, _ = s.transcriber.Transcribe(ctx, fetched.LocalPath)
	}

	// ── 7. Upload cumulative metadata.json ──────────────────────────
	s.uploadCumulativeMetadata(ctx, cmd, clipID, md, fetched, uploadResult, targetFolderID, group, driveFilename, fileHash, transcript, detectedLang)

	// ── 8. Save to DB via IndexDispatcherPort ───────────────────────
	if err := s.saveClipToDB(ctx, cmd, clipID, md, driveFilename, fileHash, fetched.LocalPath, uploadResult); err != nil {
		return nil, err
	}

	// ── 9. Enrichment + related clips ───────────────────────────────
	indexed := s.dispatchEnrichment(ctx, clipID, md.Source, fetched.LocalPath)
	related := s.findRelated(ctx, md.Name, cmd.Category, cmd.Tags)

	// ── 10. Build result ────────────────────────────────────────────
	return s.buildResult(md, clipID, fileHash, driveFilename, fetched.LocalPath, uploadResult, deliveryStatus, indexed, transcript, detectedLang, related, cmd, targetFolderID, group, videoSlug), nil
}

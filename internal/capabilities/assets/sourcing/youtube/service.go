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
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"time"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing/youtube/usecase"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Service is the YouTubeRegistrar implementation. 8-port surface
// (Fetcher, Clips, Publisher, Transcriber, Metadata, IndexDispatcher,
// Enrichment, Log). Publisher is the canonical Drive upload canal
// since FASE 5.
type Service struct {
	fetcher       sourcing.FetchProviderPort
	clips         sourcing.ClipStorePort
	publisher     sourcing.PublisherPort // FASE 5: canonical Drive upload canal
	transcriber   sourcing.TranscriptionPort
	metadata      sourcing.MetadataUploadPort
	indexDisp     IndexDispatcherPort
	enrichment    EnrichmentPort
	log           sourcing.Logger
	textTrackRepo detail.TextTrackRepository

	// requireDrive, when true, causes Register to return an error if the
	// Drive Publisher fails (P0.2, July 2026). Set at construction via
	// NewService (not post-construction mutation per godlike/06 SSOT).
	requireDrive bool
}

// ServiceDeps carries the ports for NewService. Grouping them keeps the
// constructor under the archcheck 8-parameter cap while preserving the
// canonical 8-port YouTubeRegistrar surface.
type ServiceDeps struct {
	Fetcher       sourcing.FetchProviderPort
	Clips         sourcing.ClipStorePort
	Publisher     sourcing.PublisherPort
	Transcriber   sourcing.TranscriptionPort
	Metadata      sourcing.MetadataUploadPort
	IndexDisp     IndexDispatcherPort
	Enrichment    EnrichmentPort
	Log           sourcing.Logger
	TextTrackRepo detail.TextTrackRepository
}

// NewService creates a YouTubeRegistrar service. deps.IndexDisp is REQUIRED
// (QDRANT-asset-mutation isolation June 2026 forbids the legacy UpsertClip
// fallback). All other ports are best-effort: nil causes the corresponding
// sub-operation to be skipped gracefully.
func NewService(deps ServiceDeps) *Service {
	return &Service{
		fetcher:       deps.Fetcher,
		clips:         deps.Clips,
		publisher:     deps.Publisher,
		transcriber:   deps.Transcriber,
		metadata:      deps.Metadata,
		indexDisp:     deps.IndexDisp,
		enrichment:    deps.Enrichment,
		log:           deps.Log,
		textTrackRepo: deps.TextTrackRepo,
	}
}

// WithRequireDrive sets the requireDrive flag. When true, Register returns
// ErrYouTubeDriveRequired if the Drive Publisher fails (P0.2, July 2026).
// Mirrors the fluent-setter pattern used by WithLocationResolver,
// WithTranscriptStore, and WithFolderEnsurer for config-driven behavior.
func (s *Service) WithRequireDrive(v bool) *Service {
	s.requireDrive = v
	return s
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
			FetchAssetID: fmt.Sprintf("%s_%d_%d", md.VideoID, int64(md.StartSec*1000), int64(md.EndSec*1000)),
			SourceRef:    md.RawURL,
			SegmentStart: time.Duration(md.StartSec * float64(time.Second)),
			SegmentEnd:   time.Duration(md.EndSec * float64(time.Second)),
			NoAudio:      cmd.NoAudio,
		})
	if err != nil {
		return nil, err
	}
	if fetched.LegacyFileMD5 == "" {
		s.log.Warn("file hash empty; proceeding with best-effort clip_id derivation", "video_id", md.VideoID)
	}

	// ── 4. Enrich metadata with fetched data ────────────────────────
	// Pass already-resolved fields to avoid re-parsing the URL; only the
	// fetched metadata enriches name/description/duration.
	md2, enrichErr := usecase.ResolveClipMetadata(usecase.ResolveMetadataCommand{
		URL: md.RawURL, Name: cmd.Name, Description: cmd.Description,
		Source: md.Source, StartSec: md.StartSec, EndSec: md.EndSec,
		FetchedName: fetched.Name, FetchedDescription: providerMetadataString(fetched.Metadata, "youtube_description"),
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
				ProjectID:   strings.TrimSpace(cmd.Location.Project),
				Category:    cmd.Category,
				Provider:    provider,
				Tags:        cmd.Tags,
				Language:    cmd.Location.Language,
			})
	}
	uploadResult, targetFolderID, deliveryStatus := s.processPublishResult(md.VideoID, pubResult, pubErr)
	if s.requireDrive && deliveryStatus != asset.AssetPublishPublished {
		return nil, fmt.Errorf("%w: publisher returned %v (Drive is required but asset was not published)", ErrYouTubeDriveRequired, deliveryStatus)
	}

	clipID, fileHash := fetched.ClipID, fetched.LegacyFileMD5

	// ── 6. Transcribe (mandatory per user request) ──────────────────
	if s.transcriber == nil {
		return nil, fmt.Errorf("youtube transcription: transcriber is not wired")
	}
	transcript, detectedLang, err := s.transcriber.Transcribe(ctx, fetched.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("youtube transcription failed: %w", err)
	}

	// ── 7. Upload cumulative metadata.json ──────────────────────────
	s.uploadCumulativeMetadata(ctx, cmd, clipID, md, fetched, uploadResult, targetFolderID, group, driveFilename, fileHash, transcript, detectedLang)

	// ── 8. Save to DB via IndexDispatcherPort ───────────────────────
	if err := s.saveClipToDB(ctx, cmd, clipID, md, driveFilename, fileHash, fetched.LocalPath, uploadResult); err != nil {
		return nil, err
	}

	// ── 8.5 Save transcript to DB (mandatory per user request) ──────
	if s.textTrackRepo == nil {
		return nil, fmt.Errorf("youtube transcription: textTrackRepo is not wired")
	}
	lang, _ := asset.Normalize(detectedLang)
	if lang == "" {
		lang = "und"
	}
	hash := detail.TextHash(transcript, lang, detail.TextTrackTranscript)
	track := detail.TextTrack{
		AssetID:            clipID,
		LanguageCode:       lang,
		TextKind:           detail.TextTrackTranscript,
		TextContent:        transcript,
		SourceType:         detail.TextSourceWhisper,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           "",
		ModelName:          "tiny",
		ModelVersion:       "",
		TextHash:           hash,
		SourceVersion:      detail.SourceVersion(hash, lang, lang, "", "tiny", "", ""),
		IsCurrent:          true,
		Status:             detail.TextTrackReady,
	}
	if err := s.textTrackRepo.UpsertBatch(ctx, []detail.TextTrack{track}); err != nil {
		return nil, fmt.Errorf("failed to save transcript to DB: %w", err)
	}

	// ── 9. Enrichment + related clips ───────────────────────────────
	indexed := s.dispatchEnrichment(ctx, clipID, md.Source, fetched.LocalPath)
	related := s.findRelated(ctx, md.Name, cmd.Category, cmd.Tags)

	// ── 10. Build result ────────────────────────────────────────────
	return s.buildResult(buildResultInput{
		MD:             md,
		ClipID:         clipID,
		LegacyFileMD5:  fileHash,
		DriveFilename:  driveFilename,
		LocalPath:      fetched.LocalPath,
		UploadResult:   uploadResult,
		DeliveryStatus: deliveryStatus,
		Indexed:        indexed,
		Transcript:     transcript,
		DetectedLang:   detectedLang,
		Related:        related,
		Cmd:            cmd,
		TargetFolderID: targetFolderID,
		Group:          group,
		VideoSlug:      videoSlug,
	}), nil
}

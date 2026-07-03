// Package youtube — Service is the YouTubeRegistrar use case extracted from
// the historical sourcing.Service.RegisterFromYouTube god method (P0-1 / commit 1,
// June 2026).
//
// Per AGENTS.md Pattern 0 (port abstraction) + Pattern 5 (one concept per file):
// the YouTubeRegistrar owns the single-clip YouTube flow as a focused service
// with 8 narrow ports (Fetcher, Clips, Drive, Transcriber, Metadata,
// IndexDispatcher, Enrichment, Log — the latter two are v2 ports that merge
// AssetTree + Jobs + Search + Config + legacy Enrichment into a single surface,
// and have adapters built in the composition root). Pre-extraction the ctor
// took 13 deps; v2 port-merging lands at 8 per
// architecture/policy.yaml::max_constructor_deps.
//
// The façade sourcing.Service.RegisterFromYouTube delegates to
// *Service.Register for API stability. Composition root
// internal/app/assets_register_sourcing.go builds *Service with the v2
// adapters.
package youtube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	asset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ErrYouTubeDriveRequired is returned when Drive upload is mandatory and
// the Publisher fails (P0.2, July 2026). Callers can probe with errors.Is.
var ErrYouTubeDriveRequired = errors.New("youtube.Register: Drive upload is required but Publisher failed")

// Service is the YouTubeRegistrar implementation. 9-port budget (8 + 1
// transitional for Publisher migration). The `drive` field is retained
// as fallback during FASE 5-8 migration; once all callers pass
// Publisher-only, drive will be removed and the port count returns to 8.
type Service struct {
	fetcher     sourcing.FetchProviderPort
	clips       sourcing.ClipStorePort
	drive       sourcing.DrivePort     // Deprecated: fallback during Publisher migration
	publisher   sourcing.PublisherPort // FASE 5: canonical Drive upload canal
	transcriber sourcing.TranscriptionPort
	metadata    sourcing.MetadataUploadPort
	indexDisp   IndexDispatcherPort // v2: merges IndexDisp + AssetTree
	enrichment  EnrichmentPort      // v2: merges Jobs + Search + Config + legacy Enrichment
	log         sourcing.Logger

	// RequireDrive, when true, causes Register to return an error if the
	// Drive Publisher fails (P0.2, July 2026). Default is false: Drive
	// failure returns PUBLISH_FAILED + RetryScheduled without blocking
	// the registration.
	RequireDrive bool
}

// NewService creates a YouTubeRegistrar service. indexDisp is REQUIRED
// (QDRANT-asset-mutation isolation June 2026 forbids the legacy UpsertClip
// fallback). All other ports are best-effort: nil causes the corresponding
// sub-operation to be skipped gracefully.
func NewService(
	fetcher sourcing.FetchProviderPort,
	clips sourcing.ClipStorePort,
	drive sourcing.DrivePort,
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
		drive:       drive,
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
// Behaviour mirrors the historical sourcing.Service.RegisterFromYouTube.
func (s *Service) Register(ctx context.Context, cmd sourcing.RegisterClipCommand) (*sourcing.RegisterClipResult, error) {
	// ── 1. Sanitize URL + extract video ID ─────────────────────────────────
	rawURL := cmd.URL
	videoID := ExtractVideoIDFromURL(rawURL)
	if videoID == "" {
		videoID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if videoID != "" && !strings.HasPrefix(rawURL, "https://www.youtube.com/watch?v="+videoID) {
		rawURL = "https://www.youtube.com/watch?v=" + videoID
	}
	if cmd.StartSec == 0 {
		cmd.StartSec = ExtractURLParam(rawURL, "start")
	}
	if cmd.EndSec == 0 {
		cmd.EndSec = ExtractURLParam(rawURL, "end")
	}

	// ── 2. Basic validation ─────────────────────────────────────────────────
	if cmd.EndSec > 0 && cmd.StartSec >= cmd.EndSec {
		return nil, fmt.Errorf("invalid segment: start (%.1f) must be less than end (%.1f)", cmd.StartSec, cmd.EndSec)
	}
	if cmd.StartSec < 0 || cmd.EndSec < 0 {
		return nil, fmt.Errorf("start and end must be non-negative")
	}

	source := strings.TrimSpace(cmd.Source)
	if source == "" {
		source = "youtube-manual"
	}

	// ── 3. Dedup pre-check ──────────────────────────────────────────────────
	if !cmd.Force && s.clips != nil {
		if existing, err := s.clips.FindExisting(ctx, videoID, rawURL, cmd.StartSec, cmd.EndSec); err == nil && existing != "" {
			if existingClip, gerr := s.clips.GetClip(ctx, existing); gerr == nil && existingClip != nil {
				s.log.Debug("dedup hit", "existing_id", existing, "video_id", videoID)
				indexed := s.enrichment != nil && s.enrichment.IndexingEnabled()
				publishStatus := asset.AssetPublishLocalOnly
				if existingClip.DriveFileID != "" {
					publishStatus = asset.AssetPublishPublished
				}
				return &sourcing.RegisterClipResult{
					OK: true, Duplicate: true, ClipID: existingClip.ID, VideoID: videoID,
					Name: existingClip.Name, Filename: existingClip.Filename,
					DurationSec: int(existingClip.Duration.Seconds()),
					DriveLink:   existingClip.DriveLink, DriveFileID: existingClip.DriveFileID,
					FileHash: existingClip.FileHash, Source: existingClip.Source,
					Category: existingClip.Category, Tags: existingClip.Tags,
					LocalPath: existingClip.LocalPath, Indexed: indexed,
					IndexingStatus: IndexStatus(indexed),
					Message:        "clip already registered for this YouTube video",
					DeliveryStatus: publishStatus,
				}, nil
			}
		}
	}

	// ── 4. Fetch video via provider ─────────────────────────────────────
	if s.fetcher == nil {
		return nil, fmt.Errorf("fetch provider not configured")
	}
	s.log.Info("fetching YouTube video", "video_id", videoID, "start", cmd.StartSec, "end", cmd.EndSec)
	fetched, err := s.fetcher.Fetch(ctx, sourcing.FetchRequest{
		AssetID:      videoID,
		SourceRef:    rawURL,
		SegmentStart: time.Duration(cmd.StartSec * float64(time.Second)),
		SegmentEnd:   time.Duration(cmd.EndSec * float64(time.Second)),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch video: %w", err)
	}

	// ── 5. Populate metadata ─────────────────────────────────────────────────
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		name = fetched.Name
	}
	if name == "" {
		name = videoID
	}

	description := strings.TrimSpace(cmd.Description)
	if description == "" {
		if d := fetched.Metadata["youtube_description"]; d != "" {
			description = textutil.Truncate(d, 1000)
		}
	}

	durationSec := 0.0
	if fetched.Duration > 0 {
		durationSec = fetched.Duration.Seconds()
	} else if cmd.EndSec > cmd.StartSec {
		durationSec = cmd.EndSec - cmd.StartSec
	}
	duration := int(durationSec)

	if durationSec > 0 {
		if cmd.StartSec > 0 && cmd.StartSec >= durationSec {
			return nil, fmt.Errorf("start (%.1f) exceeds video duration (%.1f)", cmd.StartSec, durationSec)
		}
		if cmd.EndSec > durationSec {
			s.log.Warn("end exceeds video duration, clip was truncated", "end", cmd.EndSec, "duration", durationSec)
		}
	}

	if s.clips != nil {
		if existingNameID, _ := s.clips.FindByName(ctx, name); existingNameID != "" {
			s.log.Warn("name collision", "existing_id", existingNameID, "name", name)
		}
	}

	// ── 6+9. Drive: resolve folder + upload via Publisher (FASE 5) ──────
	// The Publisher replaces the legacy 3-call sequence:
	//   GetOrCreateFolder(group) → GetOrCreateFolder(videoSlug) → UploadFileWithDescription
	// with a single Publish call that resolves the path and uploads atomically.
	// cmd.FolderID (backward compat) is passed as RootFolderOverride.
	group := strings.TrimSpace(cmd.Group)
	videoSlug := videoID
	if cmd.Name != "" {
		if titleSlug := textutil.SlugifyWithMax(cmd.Name, 60); titleSlug != "" {
			videoSlug = videoID + "-" + titleSlug
		}
	}

	// ── 7. Compute MD5 hash (HashPort dropped; pkg/hashutil inlined) ────
	// Pre-extraction the YouTubeRegistrar exposed a HashPort dep that took
	// 1 of the 13 ctor slots; collapsing to pkg/hashutil.MD5File is a
	// test-suite delta (test fixtures that mocked the port lose their
	// mock surface) but production behaviour is identical.
	fileHash := ""
	if h, ferr := hashutil.MD5File(fetched.LocalPath); ferr == nil {
		fileHash = h
	}
	if fileHash == "" {
		s.log.Warn("file hash empty; proceeding with best-effort clip_id derivation", "video_id", videoID)
	}
	clipID := fmt.Sprintf("yt_%s_%s", videoID, fileHash[:min(8, len(fileHash))])

	// ── 8. Transcribe audio (best-effort) ───────────────────────────────
	var transcript, detectedLang string
	if s.transcriber != nil {
		transcript, detectedLang, _ = s.transcriber.Transcribe(ctx, fetched.LocalPath)
	}

	// ── 9. Upload to Google Drive via Publisher (FASE 5) ───────────────
	ext := ".mp4"
	driveFilename := fmt.Sprintf("%s - %s%s", videoID, name, ext)
	driveDesc := BuildDriveDescription(name, cmd.Description, description, cmd.Tags, cmd.Category, source, rawURL, videoID)

	var uploadResult *sourcing.DriveUploadResult
	targetFolderID := ""
	deliveryStatus := asset.AssetPublishLocalOnly

	if s.publisher != nil {
		// Canonical path: Publisher resolves folder + uploads.
		result, err := s.publisher.Publish(ctx, delivery.PublishRequest{
			Destination:        delivery.DestinationYouTubeClip,
			LocalPath:          fetched.LocalPath,
			Filename:           driveFilename,
			Description:        driveDesc,
			AssetID:            clipID,
			Group:              group,
			Subject:            videoSlug,
			RootFolderOverride: strings.TrimSpace(cmd.FolderID),
		})
		if err != nil {
			s.log.Warn("Drive upload via Publisher failed", "error", err, "delivery_status", asset.AssetPublishFailed)
			deliveryStatus = asset.AssetPublishFailed
		} else {
			targetFolderID = result.FolderID
			uploadResult = &sourcing.DriveUploadResult{
				FileID:      result.FileID,
				WebViewLink: result.WebViewLink,
			}
			deliveryStatus = asset.AssetPublishPublished
			s.log.Info("uploaded to Drive via Publisher", "file_id", result.FileID, "link", result.WebViewLink)
		}
	} else {
		// P2.6 closure (DRIVE-CUTOVER-P0-1): the pre-FASE-9 dead-path
		// `else if s.drive != nil { s.drive.UploadFileWithDescription(...) }`
		// fallback block has been retired. The composition root
		// (`internal/app/assets_register_sourcing.go::newAssetRegisterService`)
		// wires `&sourcingPublisherAdapter{publisher: publisher}` non-nil at
		// all production sites — a nil `s.publisher` here is a wiring bug
		// (no test harness relies on the dead-path branch post-CUTOVER).
		// This branch only logs + falls through to the local-only
		// delivery path; no Drive-side recovery is attempted here.
		s.log.Warn("Drive Publisher unwired — wiring bug or pre-CUTOVER composition site; recording local-only deliveryStatus; investigate composition wiring",
			"video_id", videoID, "delivery_status", asset.AssetPublishLocalOnly)
	}

	// ── 9a. Mandatory-Drive check (P0.2, July 2026) ──────────────
	// When RequireDrive is set and the Publisher failed, fail the
	// entire operation BEFORE saving to DB. The asset is downloaded
	// but not registered; the caller can retry.
	if s.RequireDrive && deliveryStatus == asset.AssetPublishFailed {
		return nil, fmt.Errorf("%w: publisher returned %v", ErrYouTubeDriveRequired, deliveryStatus)
	}

	// ── 10. Upload cumulative metadata.json to Drive ────────────
	if s.metadata != nil && targetFolderID != "" {
		clipEntry := map[string]any{
			"clip_id":       clipID,
			"name":          name,
			"description":   description,
			"category":      cmd.Category,
			"source":        source,
			"group":         group,
			"tags":          cmd.Tags,
			"youtube_url":   rawURL,
			"youtube_id":    videoID,
			"filename":      driveFilename,
			"file_hash":     fileHash,
			"duration_sec":  duration,
			"created_at":    time.Now().UTC().Format(time.RFC3339),
			"drive_file_id": "",
			"drive_link":    "",
		}
		if title := fetched.Metadata["youtube_title"]; title != "" {
			clipEntry["youtube_title"] = title
		}
		if uploader := fetched.Metadata["youtube_uploader"]; uploader != "" {
			clipEntry["youtube_uploader"] = uploader
		}
		if uploadDate := fetched.Metadata["youtube_upload_date"]; uploadDate != "" {
			clipEntry["youtube_upload_date"] = uploadDate
		}
		if transcript != "" {
			clipEntry["clean_transcript"] = transcript
		}
		if detectedLang != "" {
			clipEntry["language"] = detectedLang
		}
		if cmd.StartSec > 0 {
			clipEntry["start_sec"] = cmd.StartSec
		}
		if cmd.EndSec > 0 {
			clipEntry["end_sec"] = cmd.EndSec
		}
		if uploadResult != nil {
			clipEntry["drive_file_id"] = uploadResult.FileID
			clipEntry["drive_link"] = uploadResult.WebViewLink
		}
		_ = s.metadata.UpdateCumulativeJSON(ctx, "", targetFolderID, clipID, clipEntry)
	}

	// ── 11. Save to database via IndexDispatcherPort v2 ────────────────
	// QDRANT-asset-mutation isolation (June 2026): the legacy
	// `s.clips.UpsertClip` fallback is REMOVED. Sourcing callers MUST
	// route every media_assets write through IndexDispatcherPort (which
	// atomically upserts + emits an outbox event in a single tx). When
	// the dispatcher is not wired (test fixture with a nil
	// IndexDispatcherPort) we record the failure clearly rather than
	// silently dropping the write into the legacy bypass path —
	// fail-closed is the only safe behaviour here. The composition root
	// adapter additionally performs an asset-tree upsert post-dispatcher
	// (warn-only; swallowed at the adapter boundary so callers see
	// "dispatcher ok").
	clip := &sourcing.ExistingClip{
		ID:        clipID,
		Name:      name,
		Filename:  driveFilename,
		Source:    source,
		Category:  cmd.Category,
		Tags:      cmd.Tags,
		Duration:  time.Duration(duration) * time.Second,
		LocalPath: fetched.LocalPath,
		FileHash:  fileHash,
	}
	if uploadResult != nil {
		clip.DriveLink = uploadResult.WebViewLink
		clip.DriveFileID = uploadResult.FileID
	}

	if s.indexDisp == nil {
		return nil, fmt.Errorf("youtube.Register: dispatcher is required (QDRANT-asset-mutation isolation forbids the legacy UpsertClip fallback; wire IndexDispatcherPort at composition time)")
	}
	if err := s.indexDisp.EnqueueAndIndex(ctx, clip, fileHash); err != nil {
		return nil, fmt.Errorf("save clip via dispatcher: %w", err)
	}
	s.log.Info("saved clip to DB", "clip_id", clipID, "via_dispatcher", true)

	// ── 12. Trigger async enrichment + indexing via jobs dispatch ──────────
	// S1a (June 2026): media.enrich is enqueued via the canonical jobs
	// system rather than the prior context.WithoutCancel goroutine.
	// The v2 EnrichmentPort.DispatchPostRegister wraps Jobs.Enqueue with
	// the canonical media.enrich payload; nil port or nil internal
	// JobsPort is a Warn-level no-op rather than a goroutine detach
	// (which Wave 22 forbids).
	indexed := s.enrichment != nil && s.enrichment.IndexingEnabled()
	if indexed && s.enrichment != nil {
		if err := s.enrichment.DispatchPostRegister(ctx, clipID, source, fetched.LocalPath); err != nil {
			s.log.Warn("failed to enqueue media.enrich job; clip is saved (operator can reindex via POST /api/media/clips/:id/reindex)",
				"clip_id", clipID, "error", err)
		}
	}

	// ── 13. Related clips via search providers ────────────────────────
	relatedClips := map[string]any{}
	if s.enrichment != nil {
		query := BuildRelatedClipsQuery(name, cmd.Category, cmd.Tags)
		if candidates, err := s.enrichment.SearchRelated(ctx, query, 5); err == nil && len(candidates) > 0 {
			relatedClips["search"] = map[string]any{
				"count":   len(candidates),
				"results": candidates,
			}
		}
	}

	// ── 14. Build result ──────────────────────────────────────────────────────
	res := &sourcing.RegisterClipResult{
		OK: true, ClipID: clipID, VideoID: videoID,
		Name: name, Filename: driveFilename, DurationSec: duration,
		FileHash: fileHash, Source: source, Category: cmd.Category,
		Tags: cmd.Tags, LocalPath: fetched.LocalPath,
		Indexed: indexed, IndexingStatus: IndexStatus(indexed),
		Transcribed: transcript != "", Language: detectedLang,
		RelatedClips: relatedClips,
	}
	if uploadResult != nil {
		res.DriveLink = uploadResult.WebViewLink
		res.DriveFileID = uploadResult.FileID
	}

	// ── 15. Delivery status (P0.2, July 2026) ────────────────────
	// Eliminates the pre-P0.2 ambiguous "OK=true for both Drive-success
	// and Drive-failure". The caller can now distinguish:
	//   - delivery_status: PUBLISHED → Drive upload succeeded
	//   - delivery_status: PUBLISH_FAILED → Drive failed, retry scheduled
	res.DeliveryStatus = deliveryStatus
	if deliveryStatus == asset.AssetPublishFailed {
		res.RetryScheduled = true
		res.Message = "asset registered locally; Drive upload failed — retry scheduled"
	}
	return res, nil
}

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
package youtube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube/usecase"
	asset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ErrYouTubeDriveRequired is returned when Drive upload is mandatory and
// the Publisher fails (P0.2, July 2026). Callers can probe with errors.Is.
var ErrYouTubeDriveRequired = errors.New("youtube.Register: Drive upload is required but Publisher failed")

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
		pubResult, pubErr = usecase.PublishClipToDrive(ctx,
			&publisherAdapter{inner: s.publisher},
			usecase.PublishClipCommand{
				AssetID: fetched.ClipID, Group: group, Subject: videoSlug,
				RootFolder: strings.TrimSpace(cmd.FolderID), LocalPath: fetched.LocalPath,
				Filename: driveFilename, Description: driveDesc,
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
	return s.buildResult(md, clipID, fileHash, driveFilename, fetched.LocalPath, uploadResult, deliveryStatus, indexed, transcript, detectedLang, related, cmd), nil
}

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
	})
	if err != nil {
		return nil, err
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

// hasherAdapter wraps hashutil.MD5File for usecase.FileHasher.
type hasherAdapter struct{}

func (a *hasherAdapter) MD5File(path string) (string, error) {
	return hashutil.MD5File(path)
}

// publisherAdapter wraps sourcing.PublisherPort for usecase.DrivePublisher.
type publisherAdapter struct {
	inner sourcing.PublisherPort
}

func (a *publisherAdapter) Publish(ctx context.Context, req usecase.PublishRequest) (*usecase.PublishResult, error) {
	if a.inner == nil {
		return nil, fmt.Errorf("usecase.publisherAdapter: inner publisher is nil")
	}
	result, err := a.inner.Publish(ctx, delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          req.LocalPath,
		Filename:           req.Filename,
		Description:        req.Description,
		AssetID:            req.AssetID,
		Group:              req.Group,
		Subject:            req.Subject,
		RootFolderOverride: req.RootFolderOverride,
	})
	if err != nil {
		return nil, err
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
		Category:        clip.Category,
		Tags:            clip.Tags,
		Duration:        clip.Duration,
		LocalPath:       clip.LocalPath,
		FileHash:        clip.FileHash,
		DriveLink:       clip.DriveLink,
		DriveFileID:     clip.DriveFileID,
		Summary:         clip.Summary,
		Topics:          clip.Topics,
		Speakers:        clip.Speakers,
		MentionedPeople: clip.MentionedPeople,
		Hook:            clip.Hook,
	}, contentHash)
}

// ── Private helpers ─────────────────────────────────────────────────────────

// dedupCheck returns a pre-built RegisterClipResult when the clip already
// exists in the database, or nil when registration should proceed.
func (s *Service) dedupCheck(ctx context.Context, cmd sourcing.RegisterClipCommand, md *usecase.ResolvedMetadata) *sourcing.RegisterClipResult {
	existing, err := s.clips.FindExisting(ctx, md.VideoID, md.RawURL, md.StartSec, md.EndSec)
	if err != nil || existing == "" {
		return nil
	}
	existingClip, gerr := s.clips.GetClip(ctx, existing)
	if gerr != nil || existingClip == nil {
		return nil
	}
	s.log.Debug("dedup hit", "existing_id", existing, "video_id", md.VideoID)
	indexed := s.enrichment != nil && s.enrichment.IndexingEnabled()
	publishStatus := asset.AssetPublishLocalOnly
	if existingClip.DriveFileID != "" {
		publishStatus = asset.AssetPublishPublished
	}
	return &sourcing.RegisterClipResult{
		OK: true, Duplicate: true, ClipID: existingClip.ID, VideoID: md.VideoID,
		Name: existingClip.Name, Filename: existingClip.Filename,
		DurationSec: int(existingClip.Duration.Seconds()),
		DriveLink:   existingClip.DriveLink, DriveFileID: existingClip.DriveFileID,
		FileHash: existingClip.FileHash, Source: existingClip.Source,
		Category: existingClip.Category, Tags: existingClip.Tags,
		LocalPath: existingClip.LocalPath, Indexed: indexed,
		IndexingStatus: IndexStatus(indexed),
		Message:        "clip already registered for this YouTube video",
		DeliveryStatus: publishStatus,
	}
}

// warnNameCollision logs a warning when another clip shares the same name.
func (s *Service) warnNameCollision(ctx context.Context, name string) {
	if s.clips != nil {
		if existingNameID, _ := s.clips.FindByName(ctx, name); existingNameID != "" {
			s.log.Warn("name collision", "existing_id", existingNameID, "name", name)
		}
	}
}

// buildDriveParams derives the Drive filename, description, video slug, and
// group from the resolved metadata and command.
func (s *Service) buildDriveParams(cmd sourcing.RegisterClipCommand, md *usecase.ResolvedMetadata) (driveFilename, driveDesc, videoSlug, group string) {
	group = strings.TrimSpace(cmd.Group)
	videoSlug = md.VideoID
	if cmd.Name != "" {
		if titleSlug := textutil.SlugifyWithMax(cmd.Name, 60); titleSlug != "" {
			videoSlug = md.VideoID + "-" + titleSlug
		}
	}
	driveFilename = fmt.Sprintf("%s - %s.mp4", md.VideoID, md.Name)
	driveDesc = BuildDriveDescription(md.Name, cmd.Description, md.Description, cmd.Tags, cmd.Category, md.Source, md.RawURL, md.VideoID)
	return
}

// processPublishResult translates the use case outcome into the concrete
// upload result, folder ID, and delivery status used by downstream steps.
func (s *Service) processPublishResult(videoID string, pubResult *usecase.PublishClipResult, pubErr error) (*sourcing.DriveUploadResult, string, asset.AssetPublishStatus) {
	if pubErr != nil {
		s.log.Warn("Drive upload via Publisher failed", "error", pubErr, "delivery_status", asset.AssetPublishFailed)
		return nil, "", asset.AssetPublishFailed
	}
	if pubResult == nil || !pubResult.Published {
		s.log.Warn("Drive Publisher unwired", "video_id", videoID)
		return nil, "", asset.AssetPublishLocalOnly
	}
	s.log.Info("uploaded to Drive via Publisher", "file_id", pubResult.FileID, "link", pubResult.WebViewLink)
	return &sourcing.DriveUploadResult{FileID: pubResult.FileID, WebViewLink: pubResult.WebViewLink}, pubResult.FolderID, asset.AssetPublishPublished
}

// uploadCumulativeMetadata writes the aggregate clip metadata JSON to Drive.
func (s *Service) uploadCumulativeMetadata(ctx context.Context, cmd sourcing.RegisterClipCommand, clipID string, md *usecase.ResolvedMetadata, fetched *usecase.DownloadAndHashResult, uploadResult *sourcing.DriveUploadResult, targetFolderID, group, driveFilename, fileHash, transcript, detectedLang string) {
	if s.metadata == nil || targetFolderID == "" {
		return
	}
	entry := map[string]any{
		"clip_id": clipID, "name": md.Name, "description": md.Description,
		"category": cmd.Category, "source": md.Source, "group": group, "tags": cmd.Tags,
		"youtube_url": md.RawURL, "youtube_id": md.VideoID, "filename": driveFilename,
		"file_hash": fileHash, "duration_sec": md.Duration, "created_at": time.Now().UTC().Format(time.RFC3339),
		"drive_file_id": "", "drive_link": "",
	}
	if cmd.Summary != "" {
		entry["clip_summary"] = cmd.Summary
	}
	if len(cmd.Topics) > 0 {
		entry["topics"] = cmd.Topics
	}
	if len(cmd.Speakers) > 0 {
		entry["speakers"] = cmd.Speakers
	}
	if len(cmd.MentionedPeople) > 0 {
		entry["mentioned_people"] = cmd.MentionedPeople
	}
	if cmd.Hook != "" {
		entry["hook"] = cmd.Hook
	}
	if title := fetched.Metadata["youtube_title"]; title != "" {
		entry["youtube_title"] = title
	}
	if uploader := fetched.Metadata["youtube_uploader"]; uploader != "" {
		entry["youtube_uploader"] = uploader
	}
	if uploadDate := fetched.Metadata["youtube_upload_date"]; uploadDate != "" {
		entry["youtube_upload_date"] = uploadDate
	}
	if transcript != "" {
		entry["clean_transcript"] = transcript
	}
	if detectedLang != "" {
		entry["language"] = detectedLang
	}
	if md.StartSec > 0 {
		entry["start_sec"] = md.StartSec
	}
	if md.EndSec > 0 {
		entry["end_sec"] = md.EndSec
	}
	if uploadResult != nil {
		entry["drive_file_id"] = uploadResult.FileID
		entry["drive_link"] = uploadResult.WebViewLink
	}
	_ = s.metadata.UpdateCumulativeJSON(ctx, "", targetFolderID, clipID, entry)
}

// saveClipToDB persists the clip in media_assets via PersistClipAndIndex.
// PR-CLIP-DECOM-6 (July 2026): delegates to the use case via clipIndexerAdapter
// instead of calling IndexDispatcherPort.EnqueueAndIndex directly.
func (s *Service) saveClipToDB(ctx context.Context, cmd sourcing.RegisterClipCommand, clipID string, md *usecase.ResolvedMetadata, driveFilename, fileHash, localPath string, uploadResult *sourcing.DriveUploadResult) error {
	adapter := &clipIndexerAdapter{inner: s.indexDisp}

	persistCmd := usecase.PersistClipCommand{
		ClipID:          clipID,
		Name:            md.Name,
		Filename:        driveFilename,
		Source:          md.Source,
		Category:        cmd.Category,
		Tags:            cmd.Tags,
		DurationSec:     md.Duration,
		LocalPath:       localPath,
		FileHash:        fileHash,
		Summary:         cmd.Summary,
		Topics:          cmd.Topics,
		Speakers:        cmd.Speakers,
		MentionedPeople: cmd.MentionedPeople,
		Hook:            cmd.Hook,
	}
	if uploadResult != nil {
		persistCmd.DriveLink = uploadResult.WebViewLink
		persistCmd.DriveFileID = uploadResult.FileID
	}

	if err := usecase.PersistClipAndIndex(ctx, adapter, persistCmd); err != nil {
		return fmt.Errorf("save clip via dispatcher: %w", err)
	}
	s.log.Info("saved clip to DB", "clip_id", clipID, "via_dispatcher", true)
	return nil
}

// dispatchEnrichment enqueues the media.enrich job. Returns whether indexing
// is enabled (used by the result builder).
func (s *Service) dispatchEnrichment(ctx context.Context, clipID, source, localPath string) bool {
	indexed := s.enrichment != nil && s.enrichment.IndexingEnabled()
	if indexed && s.enrichment != nil {
		if err := s.enrichment.DispatchPostRegister(ctx, clipID, source, localPath); err != nil {
			s.log.Warn("failed to enqueue media.enrich job; clip is saved (operator can reindex via POST /api/media/clips/:id/reindex)",
				"clip_id", clipID, "error", err)
		}
	}
	return indexed
}

// findRelated searches for clips related to the newly registered asset.
func (s *Service) findRelated(ctx context.Context, name, category string, tags []string) map[string]any {
	related := map[string]any{}
	if s.enrichment != nil {
		query := BuildRelatedClipsQuery(name, category, tags)
		if candidates, err := s.enrichment.SearchRelated(ctx, query, 5); err == nil && len(candidates) > 0 {
			related["search"] = map[string]any{
				"count":   len(candidates),
				"results": candidates,
			}
		}
	}
	return related
}

// buildResult assembles the final RegisterClipResult.
func (s *Service) buildResult(md *usecase.ResolvedMetadata, clipID, fileHash, driveFilename, localPath string, uploadResult *sourcing.DriveUploadResult, deliveryStatus asset.AssetPublishStatus, indexed bool, transcript, detectedLang string, related map[string]any, cmd sourcing.RegisterClipCommand) *sourcing.RegisterClipResult {
	res := &sourcing.RegisterClipResult{
		OK: true, ClipID: clipID, VideoID: md.VideoID,
		Name: md.Name, Filename: driveFilename, DurationSec: md.Duration,
		FileHash: fileHash, Source: md.Source, Category: cmd.Category,
		Tags: cmd.Tags, LocalPath: localPath,
		Indexed: indexed, IndexingStatus: IndexStatus(indexed),
		Transcribed: transcript != "", Language: detectedLang,
		RelatedClips: related,
	}
	if uploadResult != nil {
		res.DriveLink = uploadResult.WebViewLink
		res.DriveFileID = uploadResult.FileID
	}
	res.DeliveryStatus = deliveryStatus
	if deliveryStatus == asset.AssetPublishFailed {
		res.RetryScheduled = true
		res.Message = "asset registered locally; Drive upload failed — retry scheduled"
	}
	return res
}

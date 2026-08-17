// File register_helpers.go — Register-orchestration helper methods for
// the YouTubeRegistrar service. Extracted from service.go per AGENTS.md
// Pattern 5 v2 (1 concetto per file; code-motion pura, zero logica
// cambiata).
//
// Concept scope: every helper here participates in the canonical
// Register pipeline:
//
//   - dedupCheck               → pre-fetch dedup probe
//   - warnNameCollision        → name-collision warning
//   - buildDriveParams         → Drive filename / description / slug derivation
//   - processPublishResult     → Drive-publish outcome translation
//   - uploadCumulativeMetadata → aggregate clip metadata.json write
//   - saveClipToDB             → IndexDispatcherPort save
//   - dispatchEnrichment       → media.enrich job enqueue
//   - findRelated              → related-clip semantic search
//   - buildResult              → RegisterClipResult assembly
//
// godlike/06 SSOT one-canonical-owner-per-fact: these helpers live ONLY
// here. Register (in service.go) consumes them via package-local
// method receivers; same-package visibility rende i 9 helper
// accessibili senza import cycles.
//
// godlike/07 NO-FAKE-AVAILABILITY: each helper preserves the canonical
// typed-error contract (errors.Is probes on package-level sentinels;
// fmt.Errorf %w chains for context). best-effort vs required gates
// (P0.2 RequireDrive, QDRANT-isolation indexDisp REQUIRED) preserved
// verbatim.
package youtube

import (
	"context"
	"fmt"
	"strings"
	"time"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube/usecase"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Private helpers (called by Register) ─────────────────────────────────────

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
		// DoD #8 (July 2026): dedup-hit already has the canonical
		// folder metadata from the prior registration.
		DriveFolderID: existingClip.DriveFolderID,
		DrivePath:     existingClip.DrivePath,
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
//
// PR-YT-CLIP-SEMANTIC-LOCATION-FIX (July 2026): group now falls back
// through a 3-level cascade:
//
//  1. cmd.Group (explicit, highest priority)
//  2. cmd.Category (semantic category from the API payload)
//  3. cmd.Location.Category (canonical semantic-location DTO)
//
// This ensures that a payload with location={category:"Boxe"} but no
// explicit Group still routes through YouTubeClipPath correctly.
func (s *Service) buildDriveParams(cmd sourcing.RegisterClipCommand, md *usecase.ResolvedMetadata) (driveFilename, driveDesc, videoSlug, group string) {
	group = strings.TrimSpace(cmd.Group)
	if group == "" {
		group = strings.TrimSpace(cmd.Category)
	}
	if group == "" {
		group = strings.TrimSpace(cmd.Location.Category)
	}
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

// providerMetadataString reads a value from the yt-dlp raw provider
// metadata map (map[string]string). Reads are routed through this helper
// so callers do not index the raw wire-shape map inline: the canonical
// metadata-key gate (percheck_metadata_registry) governs Asset.Metadata
// only, but the scanner anchors on any field named Metadata[...] — the
// helper keeps the youtube/autotag surface free of bare-key residue
// without changing the downloader wire shape.
func providerMetadataString(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
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
	if title := providerMetadataString(fetched.Metadata, "youtube_title"); title != "" {
		entry["youtube_title"] = title
	}
	if uploader := providerMetadataString(fetched.Metadata, "youtube_uploader"); uploader != "" {
		entry["youtube_uploader"] = uploader
	}
	if uploadDate := providerMetadataString(fetched.Metadata, "youtube_upload_date"); uploadDate != "" {
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
		SourceURL:       md.RawURL,
		SourceProvider:  "youtube",
		SourceVideoID:   md.VideoID,
		StartSec:        md.StartSec,
		EndSec:          md.EndSec,
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
			s.log.Warn("failed to enqueue media.enrich job; clip is saved (operator can reindex via POST /api/assets/operator/assets/:id/reindex)",
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

// buildResultInput groups the parameters for buildResult.
//
// PR-YAGNI-BUILDRESULT (July 2026): replaces the 15 positional arguments
// with a single struct, removing the parameter-sprawl hazard while keeping
// the assembly logic identical.
type buildResultInput struct {
	MD             *usecase.ResolvedMetadata
	ClipID         string
	FileHash       string
	DriveFilename  string
	LocalPath      string
	UploadResult   *sourcing.DriveUploadResult
	DeliveryStatus asset.AssetPublishStatus
	Indexed        bool
	Transcript     string
	DetectedLang   string
	Related        map[string]any
	Cmd            sourcing.RegisterClipCommand
	TargetFolderID string
	Group          string
	VideoSlug      string
}

// buildResult assembles the final RegisterClipResult.
func (s *Service) buildResult(input buildResultInput) *sourcing.RegisterClipResult {
	res := &sourcing.RegisterClipResult{
		OK: true, ClipID: input.ClipID, VideoID: input.MD.VideoID,
		Name: input.MD.Name, Filename: input.DriveFilename, DurationSec: input.MD.Duration,
		FileHash: input.FileHash, Source: input.MD.Source, Category: input.Cmd.Category,
		Tags: input.Cmd.Tags, LocalPath: input.LocalPath,
		Indexed: input.Indexed, IndexingStatus: IndexStatus(input.Indexed),
		Transcribed: input.Transcript != "", Language: input.DetectedLang,
		RelatedClips: input.Related,
	}
	if input.UploadResult != nil {
		res.DriveLink = input.UploadResult.WebViewLink
		res.DriveFileID = input.UploadResult.FileID
	}
	// DoD #8 (July 2026): populate Drive folder metadata so API
	// callers see where the asset landed on Drive without re-querying
	// media_assets. targetFolderID comes from the Publisher (PublishClipToDrive
	// returns the resolved folder_id).
	res.DriveFolderID = input.TargetFolderID
	if input.TargetFolderID != "" && input.Group != "" && input.VideoSlug != "" {
		res.DrivePath = fmt.Sprintf("clips/%s/%s", input.Group, input.VideoSlug)
	}
	res.DeliveryStatus = input.DeliveryStatus
	if input.DeliveryStatus == asset.AssetPublishFailed {
		res.RetryScheduled = true
		res.Message = "asset registered locally; Drive upload failed — retry scheduled"
	}
	return res
}

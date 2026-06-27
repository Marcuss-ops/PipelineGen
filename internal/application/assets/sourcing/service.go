package sourcing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Service orchestrates media sourcing operations through narrow ports.
type Service struct {
	fetcher     FetchProviderPort
	drive       DrivePort
	clips       ClipStorePort
	jobs        JobsPort
	scanner     FileScannerPort
	hasher      HashPort
	transcriber TranscriptionPort
	assetTree   AssetTreePort
	search      SearchProviderPort
	config      ConfigPort
	enrichment  EnrichmentPort
	metadataUp  MetadataUploadPort
	indexDisp   IndexDispatcherPort
	log         Logger
}

// NewService creates a SourcingService. Nil ports cause the corresponding
// sub-operation to be skipped gracefully (best-effort).
func NewService(
	fetcher FetchProviderPort,
	drive DrivePort,
	clips ClipStorePort,
	jobs JobsPort,
	scanner FileScannerPort,
	hasher HashPort,
	transcriber TranscriptionPort,
	assetTree AssetTreePort,
	search SearchProviderPort,
	config ConfigPort,
	enrichment EnrichmentPort,
	metadataUp MetadataUploadPort,
	indexDisp IndexDispatcherPort,
	log Logger,
) *Service {
	return &Service{
		fetcher:     fetcher,
		drive:       drive,
		clips:       clips,
		jobs:        jobs,
		scanner:     scanner,
		hasher:      hasher,
		transcriber: transcriber,
		assetTree:   assetTree,
		search:      search,
		config:      config,
		enrichment:  enrichment,
		metadataUp:  metadataUp,
		indexDisp:   indexDisp,
		log:         log,
	}
}

// RegisterFromYouTube downloads a YouTube clip, uploads to Drive, saves to DB,
// and triggers enrichment/indexing. All sub-operations are best-effort.
func (s *Service) RegisterFromYouTube(ctx context.Context, cmd RegisterClipCommand) (*RegisterClipResult, error) {
	// ── 1. Sanitize URL + extract video ID ──────────────────────────
	rawURL := cmd.URL
	videoID := extractVideoIDFromURL(rawURL)
	if videoID == "" {
		videoID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// Rebuild clean URL
	if videoID != "" && !strings.HasPrefix(rawURL, "https://www.youtube.com/watch?v="+videoID) {
		rawURL = "https://www.youtube.com/watch?v=" + videoID
	}

	// Extract start/end from URL params if not already set
	if cmd.StartSec == 0 {
		cmd.StartSec = extractURLParam(rawURL, "start")
	}
	if cmd.EndSec == 0 {
		cmd.EndSec = extractURLParam(rawURL, "end")
	}

	// ── 2. Basic validation ─────────────────────────────────────────
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

	// ── 3. Dedup pre-check ──────────────────────────────────────────
	if !cmd.Force && s.clips != nil {
		if existing, err := s.clips.FindExisting(ctx, videoID, rawURL, cmd.StartSec, cmd.EndSec); err == nil && existing != "" {
			if existingClip, gerr := s.clips.GetClip(ctx, existing); gerr == nil && existingClip != nil {
				s.log.Debug("dedup hit", "existing_id", existing, "video_id", videoID)
				indexed := s.enrichment != nil
				return &RegisterClipResult{
					OK: true, Duplicate: true, ClipID: existingClip.ID, VideoID: videoID,
					Name: existingClip.Name, Filename: existingClip.Filename,
					DurationSec: int(existingClip.Duration.Seconds()),
					DriveLink:   existingClip.DriveLink, DriveFileID: existingClip.DriveFileID,
					FileHash: existingClip.FileHash, Source: existingClip.Source,
					Category: existingClip.Category, Tags: existingClip.Tags,
					LocalPath: existingClip.LocalPath, Indexed: indexed,
					IndexingStatus: indexStatus(indexed),
					Message:        "clip already registered for this YouTube video",
				}, nil
			}
		}
	}

	// ── 4. Fetch video via provider ─────────────────────────────────
	if s.fetcher == nil {
		return nil, fmt.Errorf("fetch provider not configured")
	}
	s.log.Info("fetching YouTube video", "video_id", videoID, "start", cmd.StartSec, "end", cmd.EndSec)
	fetched, err := s.fetcher.Fetch(ctx, FetchRequest{
		AssetID:      videoID,
		SourceRef:    rawURL,
		SegmentStart: time.Duration(cmd.StartSec * float64(time.Second)),
		SegmentEnd:   time.Duration(cmd.EndSec * float64(time.Second)),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch video: %w", err)
	}

	// ── 5. Populate metadata ────────────────────────────────────────
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

	// Post-fetch validation
	if durationSec > 0 {
		if cmd.StartSec > 0 && cmd.StartSec >= durationSec {
			return nil, fmt.Errorf("start (%.1f) exceeds video duration (%.1f)", cmd.StartSec, durationSec)
		}
		if cmd.EndSec > durationSec {
			s.log.Warn("end exceeds video duration, clip was truncated", "end", cmd.EndSec, "duration", durationSec)
		}
	}

	// Name collision warning
	if s.clips != nil {
		if existingNameID, _ := s.clips.FindByName(ctx, name); existingNameID != "" {
			s.log.Warn("name collision", "existing_id", existingNameID, "name", name)
		}
	}

	// ── 6. Resolve Drive target folder ──────────────────────────────
	group := strings.TrimSpace(cmd.Group)
	targetFolderID := strings.TrimSpace(cmd.FolderID)
	if targetFolderID == "" && s.config != nil {
		targetFolderID = s.config.ClipsFolder()
	}
	if targetFolderID == "" && s.config != nil {
		targetFolderID = s.config.RootFolder()
	}

	if group != "" && targetFolderID != "" && s.drive != nil {
		if existingName, err := s.drive.GetFolderName(ctx, targetFolderID); err == nil && cleanFolderName(existingName) == cleanFolderName(group) {
			// reuse
		} else {
			dirID, err := s.drive.GetOrCreateFolder(ctx, group, targetFolderID)
			if err != nil {
				s.log.Warn("failed to create group folder", "group", group, "error", err)
			} else {
				targetFolderID = dirID
			}
		}
	}
	// Per-video subfolder
	if targetFolderID != "" && videoID != "" && s.drive != nil {
		videoSlug := videoID
		if cmd.Name != "" {
			if titleSlug := textutil.SlugifyWithMax(cmd.Name, 60); titleSlug != "" {
				videoSlug = videoID + "-" + titleSlug
			}
		}
		videoFolderID, err := s.drive.GetOrCreateFolder(ctx, videoSlug, targetFolderID)
		if err != nil {
			s.log.Warn("failed to create video subfolder", "slug", videoSlug, "error", err)
		} else {
			targetFolderID = videoFolderID
		}
	}

	// ── 7. Compute MD5 hash ─────────────────────────────────────────
	fileHash := ""
	if s.hasher != nil {
		fileHash, err = s.hasher.MD5File(fetched.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("hash file: %w", err)
		}
	}
	clipID := fmt.Sprintf("yt_%s_%s", videoID, fileHash[:min(8, len(fileHash))])

	// ── 8. Transcribe audio (best-effort) ───────────────────────────
	var transcript, detectedLang string
	if s.transcriber != nil {
		transcript, detectedLang, _ = s.transcriber.Transcribe(ctx, fetched.LocalPath)
	}

	// ── 9. Upload to Google Drive ───────────────────────────────────
	ext := ".mp4"
	driveFilename := fmt.Sprintf("%s - %s%s", videoID, name, ext)
	var uploadResult *DriveUploadResult
	if s.drive != nil {
		driveDesc := buildDriveDescription(name, cmd.Description, description, cmd.Tags, cmd.Category, source, rawURL, videoID)
		result, err := s.drive.UploadFileWithDescription(ctx, fetched.LocalPath, targetFolderID, driveFilename, driveDesc)
		if err != nil {
			s.log.Warn("Drive upload failed, continuing with local file only", "error", err)
		} else {
			uploadResult = result
			s.log.Info("uploaded to Drive", "file_id", result.FileID, "link", result.WebViewLink)
		}
	}

	// ── 10. Upload cumulative metadata.json to Drive ────────────────
	if s.metadataUp != nil && targetFolderID != "" {
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
		_ = s.metadataUp.UpdateCumulativeJSON(ctx, "", targetFolderID, clipID, clipEntry)
	}

	// ── 11. Save to database ────────────────────────────────────────
	clip := &ExistingClip{
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

	viaDispatcher := false
	if s.indexDisp != nil {
		// QDRANT-002 canonical path: atomic UPSERT + outbox event via dispatcher.
		// The dispatcher writes media_assets and outbox_events in a single tx,
		// then the outbox pool picks up the event and runs IndexClip async.
		if err := s.indexDisp.EnqueueAndIndex(ctx, clip, fileHash); err != nil {
			return nil, fmt.Errorf("save clip via dispatcher: %w", err)
		}
		viaDispatcher = true
	} else if s.clips != nil {
		// Legacy path: raw UpsertClip without outbox event.
		// QDRANT-002: kept for backward compatibility when dispatcher is not wired.
		if err := s.clips.UpsertClip(ctx, clip); err != nil {
			return nil, fmt.Errorf("save clip: %w", err)
		}
	}
	if viaDispatcher || s.clips != nil {
		s.log.Info("saved clip to DB", "clip_id", clipID, "via_dispatcher", viaDispatcher)
	}

	// ── 12. Update Asset Tree ───────────────────────────────────────
	if s.assetTree != nil {
		node := AssetTreeNode{
			ID:     clipID,
			Name:   name,
			Source: source,
		}
		if uploadResult != nil {
			node.DriveLink = uploadResult.WebViewLink
		}
		_ = s.assetTree.UpsertNode(ctx, node)
	}

	// ── 13. Trigger async enrichment + indexing ─────────────────────
	indexed := s.enrichment != nil
	if indexed {
		concurrent.SafeGo("sourcing-enrich", func() {
			_ = s.enrichment.EnrichAndIndex(context.WithoutCancel(ctx), clipID, fetched.LocalPath, source)
		})
	}

	// ── 14. Related clips via search providers ──────────────────────
	relatedClips := map[string]any{}
	if s.search != nil {
		query := buildRelatedClipsQuery(name, cmd.Category, cmd.Tags)
		if candidates, err := s.search.Search(ctx, query, 5); err == nil && len(candidates) > 0 {
			relatedClips["search"] = map[string]any{
				"count":   len(candidates),
				"results": candidates,
			}
		}
	}

	// ── 15. Build result ────────────────────────────────────────────
	res := &RegisterClipResult{
		OK: true, ClipID: clipID, VideoID: videoID,
		Name: name, Filename: driveFilename, DurationSec: duration,
		FileHash: fileHash, Source: source, Category: cmd.Category,
		Tags: cmd.Tags, LocalPath: fetched.LocalPath,
		Indexed: indexed, IndexingStatus: indexStatus(indexed),
		Transcribed: transcript != "", Language: detectedLang,
		RelatedClips: relatedClips,
	}
	if uploadResult != nil {
		res.DriveLink = uploadResult.WebViewLink
		res.DriveFileID = uploadResult.FileID
	}
	return res, nil
}

// BatchRegisterFromYouTube processes a batch of clip registration commands
// sequentially. For each clip it calls RegisterFromYouTube and aggregates
// the results. This is the canonical service-level orchestrator — handlers
// call this single method instead of looping over clips themselves.
func (s *Service) BatchRegisterFromYouTube(ctx context.Context, commands []RegisterClipCommand) *BatchRegisterResult {
	if s == nil {
		return &BatchRegisterResult{
			OK:      false,
			Total:   len(commands),
			Failed:  len(commands),
			Results: make([]BatchClipResult, len(commands)),
		}
	}

	log := s.log
	results := make([]BatchClipResult, len(commands))
	var succeeded, failed int

	log.Info("starting batch registration", "service", "sourcing", "clips", len(commands))
	for i, cmd := range commands {
		res, err := s.RegisterFromYouTube(ctx, cmd)
		br := BatchClipResult{Name: cmd.Name}
		if err != nil {
			br.Error = err.Error()
			results[i] = br
			failed++
			log.Info("batch clip processed",
				"index", i+1,
				"total", len(commands),
				"name", cmd.Name,
				"ok", false,
				"error", err.Error(),
			)
			continue
		}
		if res == nil {
			br.Error = "empty registration result"
			results[i] = br
			failed++
			continue
		}
		br.OK = res.OK
		br.ClipID = res.ClipID
		br.Duplicate = res.Duplicate
		if res.Duplicate {
			br.OK = false
		}
		if !res.OK && res.Message != "" {
			br.Error = res.Message
		}
		results[i] = br
		if br.OK || br.Duplicate {
			succeeded++
		} else {
			failed++
		}
		log.Info("batch clip processed",
			"index", i+1,
			"total", len(commands),
			"name", cmd.Name,
			"ok", br.OK,
			"duplicate", br.Duplicate,
			"error", br.Error,
		)
	}

	log.Info("batch registration completed", "service", "sourcing", "succeeded", succeeded, "failed", failed)
	return &BatchRegisterResult{
		OK:        true,
		Total:     len(commands),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	}
}

// ── SyncDriveFolder ───────────────────────────────────────────────────

// SyncDriveFolder enqueues a catalog sync job for the given Drive folder.
func (s *Service) SyncDriveFolder(ctx context.Context, cmd SyncDriveFolderCommand) (*SyncDriveFolderResult, error) {
	folderID := strings.TrimSpace(cmd.DriveFolderID)
	if folderID == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}
	if s.jobs == nil {
		return nil, fmt.Errorf("jobs port not configured")
	}

	source := strings.TrimSpace(cmd.Source)
	if source == "" {
		source = "drive"
	}
	mediaType := strings.TrimSpace(cmd.MediaType)
	if mediaType == "" {
		mediaType = "clip"
	}

	s.log.Info("dispatching Drive folder sync",
		"folder_id", folderID, "source", source, "name", cmd.Name, "media_type", mediaType)

	job, err := s.jobs.Enqueue(ctx, EnqueueRequest{
		Type:       "drive.folder.sync",
		MaxRetries: 2,
		Payload: JobPayload{
			"drive_folder_id": folderID,
			"source":          source,
			"name":            cmd.Name,
			"media_type":      mediaType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue sync job: %w", err)
	}

	return &SyncDriveFolderResult{
		OK: true, JobID: job.ID, DriveFolderID: folderID,
		Source: source, Name: cmd.Name,
		Message: "Drive folder sync dispatched. Poll GET /api/jobs/" + job.ID + " for status.",
	}, nil
}

// ── LocalToDrive ──────────────────────────────────────────────────────

// LocalToDrive scans a local folder and enqueues a bulk upload job.
func (s *Service) LocalToDrive(ctx context.Context, cmd LocalToDriveCommand) (*LocalToDriveResult, error) {
	if s.scanner == nil {
		return nil, fmt.Errorf("file scanner not configured")
	}
	if strings.TrimSpace(cmd.DriveFolderID) == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}

	files, err := s.scanner.Scan(ctx, cmd.LocalFolder, cmd.Limit)
	if err != nil {
		return nil, fmt.Errorf("scan folder: %w", err)
	}

	// Group by first-level subdir name
	groups := make(map[string]bool)
	for _, f := range files {
		g := f.GroupName
		if g == "" {
			g = "uncategorized"
		}
		groups[g] = true
	}
	groupNames := make([]string, 0, len(groups))
	for g := range groups {
		groupNames = append(groupNames, g)
	}

	s.log.Info("scanned local folder", "files", len(files), "groups", len(groups), "dry_run", cmd.DryRun)

	if cmd.DryRun {
		return &LocalToDriveResult{
			OK: true, DryRun: true, LocalFound: len(files), Groups: groupNames,
		}, nil
	}

	if s.jobs == nil {
		return nil, fmt.Errorf("jobs port not configured")
	}

	source := cmd.Source
	if source == "" {
		source = "youtube-local"
	}
	conc := cmd.Concurrency
	if conc <= 0 {
		conc = 3
	}

	job, err := s.jobs.Enqueue(ctx, EnqueueRequest{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: JobPayload{
			"local_folder":           cmd.LocalFolder,
			"drive_folder_id":        strings.TrimSpace(cmd.DriveFolderID),
			"source":                 source,
			"subdir_as_drive_subdir": true,
			"recursive":              true,
			"concurrency":            conc,
			"limit":                  cmd.Limit,
			"file_patterns":          []string{"*.mp4"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue: %w", err)
	}

	return &LocalToDriveResult{
		OK: true, JobID: job.ID,
		Message:    fmt.Sprintf("job enqueued (%d files, %d groups)", len(files), len(groups)),
		LocalFound: len(files), Groups: groupNames,
	}, nil
}

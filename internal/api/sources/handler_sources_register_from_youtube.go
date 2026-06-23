package sources

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/clips"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// RegisterFromYouTubeRequest is the JSON body for registering a clip from a YouTube URL.
type RegisterFromYouTubeRequest struct {
	URL         string   `json:"url" binding:"required"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Source      string   `json:"source"`
	Category    string   `json:"category"`
	Group       string   `json:"group"`
	FolderID    string   `json:"folder_id"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Force       bool     `json:"force"`
}

// RegisterFromYouTube handles POST /api/media/register-from-youtube.
//
// Punto 9 migration: the yt-dlp download + segment extraction flow has
// been migrated to the YouTube FetchProvider (registered via
// providerRegistry.ByCapability(CapabilityFetch)). The handler now:
//  1. Extracts the video ID from the URL
//  2. Runs dedup + basic validation
//  3. Fetches the video via FetchProvider (download + metadata)
//  4. Fills name/description/duration from the fetched metadata
//  5. Continues with Drive upload, DB save, indexing (unchanged).
//
// If providerRegistry is unwired or no YouTube FetchProvider is
// registered, the handler returns an error.
func (h *Handler) RegisterFromYouTube(c *gin.Context) {
	req, ok := apiutil.BindJSON[RegisterFromYouTubeRequest](c)
	if !ok {
		return
	}

	ctx := c.Request.Context()

	// ── 1. Sanitize URL + extract video ID ──────────────────────────────
	{
		rawURL := req.URL
		videoID := ""
		if idx := strings.Index(rawURL, "v="); idx != -1 {
			rest := rawURL[idx+2:]
			for i, c := range rest {
				if c == '&' || c == '?' || c == '#' {
					videoID = rest[:i]
					break
				}
			}
			if videoID == "" {
				videoID = rest
			}
		}
		extractParam := func(key string) string {
			prefixes := []string{"&" + key + "=", "?" + key + "="}
			for _, pfx := range prefixes {
				if idx := strings.Index(rawURL, pfx); idx != -1 {
					rest := rawURL[idx+len(pfx):]
					for i, c := range rest {
						if c == '&' || c == '?' || c == '#' {
							return rest[:i]
						}
					}
					return rest
				}
			}
			return ""
		}
		if req.Start == 0 {
			if s := extractParam("start"); s != "" {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					req.Start = v
				}
			}
		}
		if req.End == 0 {
			if s := extractParam("end"); s != "" {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					req.End = v
				}
			}
		}
		if videoID != "" {
			req.URL = "https://www.youtube.com/watch?v=" + videoID
		}
	}

	log := h.log.With(
		zap.String("handler", "register-from-youtube"),
		zap.String("url", req.URL),
	)

	// ── 2. Extract video ID from URL ──────────────────────────────────
	videoID := extractVideoIDFromURL(req.URL)
	if videoID == "" {
		videoID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// ── 3. Basic validation (no metadata needed yet) ───────────────────
	if req.End > 0 && req.Start >= req.End {
		apiutil.BadRequest(c, fmt.Sprintf("invalid segment: start (%.1f) must be less than end (%.1f)", req.Start, req.End))
		return
	}
	if req.Start < 0 || req.End < 0 {
		apiutil.BadRequest(c, "start and end must be non-negative")
		return
	}

	// ── 4. Dedup pre-check ────────────────────────────────────────────
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "youtube-manual"
	}
	if !req.Force && h.clipsRepo != nil {
		if existing, dedupErr := h.findExistingYouTubeClip(ctx, videoID, req.URL, req.Start, req.End); dedupErr == nil && existing != "" {
			metrics.DedupHits.WithLabelValues(source, "precheck").Inc()
			log.Info("dedup hit: returning existing clip",
				zap.String("existing_clip_id", existing),
				zap.String("video_id", videoID))
			if existingClip, gerr := h.clipsRepo.GetClip(ctx, existing); gerr == nil && existingClip != nil {
				apiutil.OK(c, gin.H{
					"ok":            true,
					"duplicate":     true,
					"clip_id":       existingClip.ID,
					"video_id":      videoID,
					"name":          existingClip.Name,
					"filename":      existingClip.Filename,
					"duration_sec":  int64(existingClip.Duration.Seconds()),
					"drive_link":    existingClip.DriveLink(),
					"drive_file_id": existingClip.DriveFileID(),
					"file_hash":     existingClip.FileHash(),
					"source":        string(existingClip.Source),
					"category":      existingClip.Category,
					"tags":          existingClip.Tags,
					"local_path":    existingClip.LocalPath(),
					"indexed":       h.clipIndexer != nil || h.vectorStore != nil,
					"message":       "clip already registered for this YouTube video",
				})
				return
			}
		} else if dedupErr != nil {
			metrics.DedupMisses.WithLabelValues(source, "precheck_error").Inc()
			log.Warn("dedup pre-check failed, proceeding with registration",
				zap.String("video_id", videoID), zap.Error(dedupErr))
		} else {
			metrics.DedupMisses.WithLabelValues(source, "precheck").Inc()
		}
	}

	// ── 5. Fetch video via FetchProvider ──────────────────────────────
	log.Info("fetching YouTube video via FetchProvider",
		zap.String("video_id", videoID),
		zap.Float64("start", req.Start),
		zap.Float64("end", req.End))

	fetched, err := h.fetchYouTubeVideo(ctx, req.URL, req.Start, req.End, videoID)
	if err != nil {
		log.Error("FetchProvider failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to fetch YouTube video: %w", err))
		return
	}
	downloadedPath := fetched.LocalPath
	fetchedAsset := fetched.Asset

	log.Info("fetched video via provider",
		zap.String("local_path", downloadedPath),
		zap.String("title", fetchedAsset.Name),
		zap.Int64("bytes", fetched.Bytes))

	// ── 6. Populate metadata from fetched asset ───────────────────────
	// Name: prefer request → fetched YouTube title → video ID
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fetchedAsset.Name
	}
	if name == "" {
		name = videoID
	}

	// Description: prefer request → fetched YouTube description
	description := strings.TrimSpace(req.Description)
	if description == "" {
		desc := fetchedAsset.GetMetadataString("youtube_description")
		description = strings.TrimSpace(desc)
		description = textutil.Truncate(description, 1000)
	}

	// Duration: from fetched asset or segment bounds
	durationSec := 0.0
	if fetchedAsset.Duration > 0 {
		durationSec = fetchedAsset.Duration.Seconds()
	} else if req.End > req.Start {
		durationSec = req.End - req.Start
	}
	duration := int(durationSec)

	// Post-fetch validation (needs duration)
	if durationSec > 0 {
		if req.Start > 0 && req.Start >= durationSec {
			apiutil.BadRequest(c, fmt.Sprintf("start (%.1f) exceeds video duration (%.1f)", req.Start, durationSec))
			return
		}
		if req.End > durationSec {
			log.Warn("end exceeds video duration, clip was truncated",
				zap.Float64("end", req.End), zap.Float64("duration", durationSec))
		}
	}

	// Name collision warning
	if h.clipsRepo != nil {
		if existingNameID, _ := h.clipsRepo.FindByName(ctx, name); existingNameID != "" {
			log.Warn("name collision: another clip with same name exists",
				zap.String("existing_id", existingNameID), zap.String("name", name))
		}
	}

	// ── 7. Resolve Drive target folder ────────────────────────────────
	targetFolderID := clipsources.ExtractDriveFolderID(strings.TrimSpace(req.FolderID))
	if targetFolderID == "" {
		targetFolderID = h.cfg.Drive.ClipsFolder()
		if targetFolderID == "" {
			targetFolderID = h.cfg.Drive.RootFolder()
		}
	}
	group := strings.TrimSpace(req.Group)
	if group != "" && targetFolderID != "" {
		if existingName, err := h.driveUploader.GetFolderName(ctx, targetFolderID); err == nil && clipsources.CleanFolderName(existingName) == clipsources.CleanFolderName(group) {
			log.Info("folder_id already points to group folder, reusing it",
				zap.String("folder_id", targetFolderID),
				zap.String("name", existingName))
		} else {
			dirID, err := h.driveUploader.GetOrCreateFolder(ctx, group, targetFolderID)
			if err != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", group), zap.Error(err))
			} else {
				targetFolderID = dirID
			}
		}
	}

	// Create per-video subfolder
	if targetFolderID != "" && videoID != "" {
		videoSlug := videoID
		if req.Name != "" {
			titleSlug := textutil.SlugifyWithMax(req.Name, 60)
			if titleSlug != "" {
				videoSlug = videoID + "-" + titleSlug
			}
		}
		videoFolderID, err := h.driveUploader.GetOrCreateFolder(ctx, videoSlug, targetFolderID)
		if err != nil {
			log.Warn("failed to create video subfolder, using parent",
				zap.String("slug", videoSlug), zap.Error(err))
		} else {
			targetFolderID = videoFolderID
			log.Info("using per-video subfolder",
				zap.String("slug", videoSlug),
				zap.String("folder_id", videoFolderID))
		}
	}

	// ── 8. Compute MD5 hash ──────────────────────────────────────────
	fileHash, err := hashutil.MD5File(downloadedPath)
	if err != nil {
		log.Error("failed to hash file", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to hash file: %w", err))
		return
	}
	clipID := fmt.Sprintf("yt_%s_%s", videoID, fileHash[:8])

	// ── 9. Transcribe audio with Whisper (best-effort) ────────────────
	transcript, detectedLang := h.transcribeAudio(ctx, downloadedPath, log)
	if transcript != "" {
		h.saveTranscriptAndStage(downloadedPath, transcript, group, log)
	}

	// ── 10. Upload to Google Drive ────────────────────────────────────
	ext := ".mp4"
	driveFilename := fmt.Sprintf("%s - %s%s", videoID, name, ext)
	var uploadResult *clipsources.DriveUploadResult
	if h.driveUploader != nil {
		driveDescription := clipsources.BuildDriveDescription(name, req.Description, description, req.Tags, req.Category, req.Source, req.URL, videoID)
		result, err := h.driveUploader.UploadFileWithDescription(ctx, downloadedPath, targetFolderID, driveFilename, driveDescription)
		if err != nil {
			log.Warn("Drive upload failed, continuing with local file only",
				zap.Error(err))
		} else {
			uploadResult = &clipsources.DriveUploadResult{
				FileID:       result.FileID,
				WebViewLink:  result.WebViewLink,
				DownloadLink: result.DownloadLink,
			}
			log.Info("uploaded to Drive",
				zap.String("file_id", result.FileID),
				zap.String("drive_link", result.WebViewLink))
		}
	}

	// ── 11. Upload metadata.json to Drive ────────────────────────────
	if h.driveUploader != nil && targetFolderID != "" {
		clipEntry := map[string]interface{}{
			"clip_id":       clipID,
			"name":          name,
			"description":   description,
			"category":      req.Category,
			"source":        source,
			"group":         group,
			"tags":          req.Tags,
			"youtube_url":   req.URL,
			"youtube_id":    videoID,
			"filename":      driveFilename,
			"file_hash":     fileHash,
			"duration_sec":  duration,
			"created_at":    time.Now().UTC().Format(time.RFC3339),
			"drive_file_id": "",
			"drive_link":    "",
		}
		if title := fetchedAsset.GetMetadataString("youtube_title"); title != "" {
			clipEntry["youtube_title"] = title
		}
		if uploader := fetchedAsset.GetMetadataString("youtube_uploader"); uploader != "" {
			clipEntry["youtube_uploader"] = uploader
		}
		if uploadDate := fetchedAsset.GetMetadataString("youtube_upload_date"); uploadDate != "" {
			clipEntry["youtube_upload_date"] = uploadDate
		}
		if transcript != "" {
			clipEntry["clean_transcript"] = transcript
		}
		if detectedLang != "" {
			clipEntry["language"] = detectedLang
		}
		if req.Start > 0 {
			clipEntry["start_sec"] = req.Start
		}
		if req.End > 0 {
			clipEntry["end_sec"] = req.End
		}
		if uploadResult != nil {
			clipEntry["drive_file_id"] = uploadResult.FileID
			clipEntry["drive_link"] = uploadResult.WebViewLink
		}

		clipsources.UpdateCumulativeMetadataJSON(ctx, h.driveUploader, h.cfg.Storage.TempPath(), targetFolderID, clipID, clipEntry, log)
	}

	// ── 12. Create MediaAsset record ──────────────────────────────────
	now := time.Now().UTC()
	clip := &asset.Asset{
		ID:         clipID,
		Name:       name,
		Filename:   driveFilename,
		Source:     asset.Source(source),
		Category:   req.Category,
		Group:      group,
		MediaType:  asset.MediaType("video"),
		Tags:       req.Tags,
		SearchText: description,
		SourceURL:  req.URL,
		Duration:   time.Duration(duration) * time.Second,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	clip.SetLocalPath(downloadedPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(group)

	clip.SetMetadataString("youtube_video_id", videoID)
	clip.SetMetadataString("youtube_url", req.URL)
	clip.SetMetadataString("source_url", req.URL)
	if transcript != "" {
		clip.SetMetadataString("clean_transcript", transcript)
	}
	if detectedLang != "" {
		clip.SetMetadataString("language", detectedLang)
	} else if lang := fetchedAsset.GetMetadataString("youtube_language"); lang != "" {
		clip.SetMetadataString("language", lang)
	}
	if uploader := fetchedAsset.GetMetadataString("youtube_uploader"); uploader != "" {
		clip.SetMetadataString("youtube_uploader", uploader)
	}
	if uploadDate := fetchedAsset.GetMetadataString("youtube_upload_date"); uploadDate != "" {
		clip.SetMetadataString("youtube_upload_date", uploadDate)
	}
	if uploadResult != nil {
		clip.SetDriveLink(uploadResult.WebViewLink)
		clip.SetDownloadLink(uploadResult.DownloadLink)
		clip.SetDriveFileID(uploadResult.FileID)
	}
	if req.Start > 0 {
		clip.SetMetadataString("start", fmt.Sprintf("%.1f", req.Start))
	}
	if req.End > 0 {
		clip.SetMetadataString("end", fmt.Sprintf("%.1f", req.End))
	}

	// ── 13. Save to database ──────────────────────────────────────────
	if h.clipsRepo != nil {
		if err := h.clipsRepo.UpsertClip(ctx, clip); err != nil {
			log.Error("failed to save clip to DB", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to save clip: %w", err))
			return
		}
		log.Info("saved clip to DB", zap.String("clip_id", clip.ID))
	}

	// ── 14. Update Asset Tree ─────────────────────────────────────────
	if h.assetTreeSvc != nil {
		node := clipsources.ClipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// ── 15. Trigger async enrichment + Qdrant indexing ────────────────
	hasIndexer := h.clipIndexer != nil || h.vectorStore != nil || h.metaWriter != nil
	if hasIndexer {
		concurrent.SafeGo("yt-register-enrich", func() {
			if h.clips != nil {
				h.clips.EnrichAndIndexClip(context.WithoutCancel(ctx), clip, source)
			}
		})
		log.Info("triggered async Qdrant indexing and enrichment",
			zap.String("clip_id", clip.ID))
	}

	// ── 16. Surface related clips via providers.Registry ──────────────
	relatedByProvider := h.relatedClipsViaRegistry(ctx, clip, log)

	// ── 17. Return success ────────────────────────────────────────────
	indexingStatus := "not_configured"
	if hasIndexer {
		indexingStatus = "enqueued"
	}
	apiutil.OK(c, gin.H{
		"ok":              true,
		"clip_id":         clip.ID,
		"video_id":        videoID,
		"name":            clip.Name,
		"filename":        driveFilename,
		"duration_sec":    duration,
		"drive_link":      clip.DriveLink(),
		"drive_file_id":   clip.DriveFileID(),
		"file_hash":       fileHash,
		"source":          source,
		"category":        req.Category,
		"tags":            req.Tags,
		"local_path":      downloadedPath,
		"indexed":         hasIndexer,
		"indexing_status": indexingStatus,
		"transcribed":     transcript != "",
		"language":        detectedLang,
		"youtube_meta":    fetchedAsset != nil && fetchedAsset.Name != "",
		"related_clips":   relatedByProvider,
	})
}

// extractVideoIDFromURL extracts the YouTube video ID from a URL.
// Supports youtube.com/watch?v=ID and youtu.be/ID formats.
func extractVideoIDFromURL(rawURL string) string {
	// youtube.com/watch?v=ID
	for _, part := range strings.Split(rawURL, "&") {
		if strings.HasPrefix(part, "v=") || strings.Contains(part, "?v=") {
			if idx := strings.Index(part, "v="); idx != -1 {
				id := part[idx+2:]
				if len(id) > 11 {
					id = id[:11]
				}
				return id
			}
		}
	}
	// youtu.be/ID
	if idx := strings.LastIndex(rawURL, "youtu.be/"); idx != -1 {
		rest := rawURL[idx+len("youtu.be/"):]
		if end := strings.IndexAny(rest, "?&#"); end != -1 {
			rest = rest[:end]
		}
		return rest
	}
	return ""
}

// fetchYouTubeVideo resolves the YouTube FetchProvider from the
// provider registry and calls Fetch(). Returns the fetched asset
// with local path and metadata.
func (h *Handler) fetchYouTubeVideo(
	ctx context.Context,
	url string,
	startSec, endSec float64,
	assetID string,
) (*providers.FetchedAsset, error) {
	if h.providerRegistry == nil {
		return nil, fmt.Errorf("provider registry not wired")
	}

	// Find the YouTube FetchProvider
	var ytFP providers.FetchProvider
	for _, p := range h.providerRegistry.ByCapability(providers.CapabilityFetch) {
		if p.Name() == "youtube" {
			var ok bool
			ytFP, ok = p.(providers.FetchProvider)
			if !ok {
				continue
			}
			break
		}
	}
	if ytFP == nil {
		return nil, fmt.Errorf("youtube FetchProvider not registered in provider registry")
	}

	startDur := time.Duration(startSec * float64(time.Second))
	endDur := time.Duration(endSec * float64(time.Second))

	return ytFP.Fetch(ctx, providers.FetchRequest{
		AssetID:      assetID,
		SourceRef:    url,
		SegmentStart: startDur,
		SegmentEnd:   endDur,
	})
}

// relatedClipsViaRegistry fans out providerRegistry.ByCapability(CapabilitySearch)
// after a RegisterFromYouTube success and returns a {provider → top-N
// candidates} map. Best-effort: nil registry, nil SearchProvider type
// assertions, Search errors, and per-call context timeouts all
// resolve to "no entry for that provider" rather than aborting.
//
// Punto 9: the download portion now routes through FetchProvider;
// the post-registration related-clip lookup via SearchProvider is unchanged.
//
// Query heuristic rationale: a single registered-clip's title alone
// (eg. "Ben Shapiro vs Destiny - DEBATE") yields effectively zero
// hits across registered SearchProviders in production — artlist is
// term-based but its DB indexes curated stock, and youTube's adapter
// is live-search (it does not know about clips the local app has
// just registered). To improve recall, we broaden the query to
// include the clip's category + the first 2 tags. The broadening is
// consciously light-weight: spaces between tokens, no LLM rewriting.
// Operators reading an empty related_clips map in >95% of responses
// is expected behaviour; non-empty indicates a real semantic hit.
//
// Parallelism + timeout: pkg/concurrent.WithContext fans the per-
// provider Search calls out in parallel with first-error-wins and
// panic recovery. The 4s ceiling is total wall time, NOT per-provider
// (the previous serial loop was N×4s). Limit=5 keeps each
// provider's hit list small enough that a slow upstream does not
// flood the response.
func (h *Handler) relatedClipsViaRegistry(
	ctx context.Context,
	clip *asset.Asset,
	log *zap.Logger,
) gin.H {
	out := gin.H{}
	if h.providerRegistry == nil {
		return out
	}
	// NOTE: do NOT name this `providers` — the package import alias
	// `providers "internal/application/assets/providers"` is in scope
	// here, and a local variable with the same name would shadow it
	// and break every `providers.<Symbol>` reference below.
	searchProviders := h.providerRegistry.ByCapability(providers.CapabilitySearch)
	if len(searchProviders) == 0 {
		return out
	}
	query := buildRelatedClipsQuery(clip)
	g, gCtx := concurrent.WithContext(ctx)
	errCount := 0
	results := make([]gin.H, len(searchProviders))
	for i, p := range searchProviders {
		i, p := i, p
		sp, ok := p.(providers.SearchProvider)
		if !ok {
			continue
		}
		g.Go("yt-reg-related-"+p.Name(), func() error {
			perCtx, cancel := context.WithTimeout(gCtx, 4*time.Second)
			defer cancel()
			res, err := sp.Search(perCtx, providers.SearchRequest{
				Query: query,
				Limit: 5,
			})
			if err != nil {
				errCount++
				log.Debug("related-clip lookup failed",
					zap.String("provider", p.Name()),
					zap.Error(err))
				return nil
			}
			results[i] = gin.H{
				"count":   len(res.Candidates),
				"results": res.Candidates,
			}
			return nil
		})
	}
	g.Wait()
	hitCount := 0
	for i, p := range searchProviders {
		if results[i] != nil {
			out[p.Name()] = results[i]
			hitCount++
		}
	}
	out["__meta"] = gin.H{
		"providers_queried": len(searchProviders),
		"hits":              hitCount,
		"errors":            errCount,
	}
	return out
}

// buildRelatedClipsQuery composes a broadened query string from a
// freshly-registered clip for use in providerRegistry fan-out.
func buildRelatedClipsQuery(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	parts := []string{}
	if cat := strings.TrimSpace(clip.Category); cat != "" {
		parts = append(parts, cat)
	}
	maxTags := 2
	for _, t := range clip.Tags {
		if maxTags <= 0 {
			break
		}
		if tt := strings.TrimSpace(t); tt != "" {
			parts = append(parts, tt)
			maxTags--
		}
	}
	if n := strings.TrimSpace(clip.Name); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, " ")
}

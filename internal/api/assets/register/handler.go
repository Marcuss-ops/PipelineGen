// Package register provides thin HTTP handlers for YouTube clip registration.
package register

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/clips"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
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

// BatchRegisterRequest is the JSON body for batch registering clips from YouTube.
type BatchRegisterRequest struct {
	FolderID string                       `json:"folder_id"`
	Clips    []RegisterFromYouTubeRequest `json:"clips" binding:"required"`
}

// BatchClipResult is the result for a single clip in a batch registration.
type BatchClipResult struct {
	ClipID    string `json:"clip_id,omitempty"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// BatchRegisterResponse is the response for batch registration.
type BatchRegisterResponse struct {
	OK        bool              `json:"ok"`
	Total     int               `json:"total"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Results   []BatchClipResult `json:"results"`
}

// Handler manages YouTube clip registration (download + metadata + Drive + Qdrant).
type Handler struct {
	log              *zap.Logger
	cfg              *config.Config
	clipsRepo        *assets.ClipsRepository
	driveUploader    *drive.Uploader
	assetTreeSvc     *assettree.Service
	providerRegistry *providers.Registry
	clipIndexer      *clipindexer.Service
	vectorStore      *qdrant.Service
	metaWriter       *semantic.MetadataWriter
	clips            *clipsources.Handler // for EnrichAndIndexClip
}

// NewHandler creates a YouTube registration handler.
func NewHandler(
	cfg *config.Config,
	clipsRepo *assets.ClipsRepository,
	driveUploader *drive.Uploader,
	assetTreeSvc *assettree.Service,
	providerRegistry *providers.Registry,
	clipIndexer *clipindexer.Service,
	vectorStore *qdrant.Service,
	metaWriter *semantic.MetadataWriter,
	clipsHandler *clipsources.Handler,
	log *zap.Logger,
) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		cfg:              cfg,
		clipsRepo:        clipsRepo,
		driveUploader:    driveUploader,
		assetTreeSvc:     assetTreeSvc,
		providerRegistry: providerRegistry,
		clipIndexer:      clipIndexer,
		vectorStore:      vectorStore,
		metaWriter:       metaWriter,
		clips:            clipsHandler,
		log:              log,
	}
}

// RegisterRoutes registers the registration endpoints.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/register-from-youtube", h.RegisterFromYouTube)
	r.POST("/register-batch", h.BatchRegisterFromYouTube)
}

// RegisterFromYouTube handles POST /api/media/register-from-youtube.
func (h *Handler) RegisterFromYouTube(c *gin.Context) {
	req, ok := apiutil.BindJSON[RegisterFromYouTubeRequest](c)
	if !ok {
		return
	}

	ctx := c.Request.Context()

	// ── 1. Sanitize URL + extract video ID ──
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

	// ── 2. Extract video ID ──
	videoID := extractVideoIDFromURL(req.URL)
	if videoID == "" {
		videoID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// ── 3. Basic validation ──
	if req.End > 0 && req.Start >= req.End {
		apiutil.BadRequest(c, fmt.Sprintf("invalid segment: start (%.1f) must be less than end (%.1f)", req.Start, req.End))
		return
	}
	if req.Start < 0 || req.End < 0 {
		apiutil.BadRequest(c, "start and end must be non-negative")
		return
	}

	// ── 4. Dedup pre-check ──
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

	// ── 5. Fetch video via FetchProvider ──
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

	// ── 6. Populate metadata from fetched asset ──
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fetchedAsset.Name
	}
	if name == "" {
		name = videoID
	}

	description := strings.TrimSpace(req.Description)
	if description == "" {
		desc := fetchedAsset.GetMetadataString("youtube_description")
		description = strings.TrimSpace(desc)
		description = textutil.Truncate(description, 1000)
	}

	durationSec := 0.0
	if fetchedAsset.Duration > 0 {
		durationSec = fetchedAsset.Duration.Seconds()
	} else if req.End > req.Start {
		durationSec = req.End - req.Start
	}
	duration := int(durationSec)

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

	if h.clipsRepo != nil {
		if existingNameID, _ := h.clipsRepo.FindByName(ctx, name); existingNameID != "" {
			log.Warn("name collision: another clip with same name exists",
				zap.String("existing_id", existingNameID), zap.String("name", name))
		}
	}

	// ── 7. Resolve Drive target folder ──
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

	// ── 8. Compute MD5 hash ──
	fileHash, err := hashutil.MD5File(downloadedPath)
	if err != nil {
		log.Error("failed to hash file", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to hash file: %w", err))
		return
	}
	clipID := fmt.Sprintf("yt_%s_%s", videoID, fileHash[:8])

	// ── 9. Transcribe audio with Whisper (best-effort) ──
	transcript, detectedLang := h.transcribeAudio(ctx, downloadedPath, log)
	if transcript != "" {
		h.saveTranscriptAndStage(downloadedPath, transcript, group, log)
	}

	// ── 10. Upload to Google Drive ──
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

	// ── 11. Upload metadata.json to Drive ──
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

	// ── 12. Create MediaAsset record ──
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

	// ── 13. Save to database ──
	if h.clipsRepo != nil {
		if err := h.clipsRepo.UpsertClip(ctx, clip); err != nil {
			log.Error("failed to save clip to DB", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to save clip: %w", err))
			return
		}
		log.Info("saved clip to DB", zap.String("clip_id", clip.ID))
	}

	// ── 14. Update Asset Tree ──
	if h.assetTreeSvc != nil {
		node := clipsources.ClipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// ── 15. Trigger async enrichment + Qdrant indexing ──
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

	// ── 16. Surface related clips via providers.Registry ──
	relatedByProvider := h.relatedClipsViaRegistry(ctx, clip, log)

	// ── 17. Return success ──
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

// BatchRegisterFromYouTube handles POST /api/media/register-batch
func (h *Handler) BatchRegisterFromYouTube(c *gin.Context) {
	var req BatchRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if len(req.Clips) == 0 {
		apiutil.BadRequest(c, "clips list is empty")
		return
	}

	for i := range req.Clips {
		if req.Clips[i].FolderID == "" && req.FolderID != "" {
			req.Clips[i].FolderID = req.FolderID
		}
	}

	ctx := c.Request.Context()
	log := h.log.With(zap.String("handler", "batch-register"), zap.Int("total", len(req.Clips)))

	results := make([]BatchClipResult, len(req.Clips))
	var succeeded, failed int

	log.Info("starting batch registration", zap.Int("clips", len(req.Clips)))

	for i, clip := range req.Clips {
		result := h.processBatchClip(ctx, clip)
		results[i] = result
		if result.OK || result.Duplicate {
			succeeded++
		} else {
			failed++
		}

		log.Info("batch clip processed",
			zap.Int("index", i+1),
			zap.Int("total", len(req.Clips)),
			zap.String("name", clip.Name),
			zap.Bool("ok", result.OK),
			zap.Bool("duplicate", result.Duplicate),
			zap.String("error", result.Error))
	}

	log.Info("batch registration completed",
		zap.Int("succeeded", succeeded),
		zap.Int("failed", failed))

	apiutil.OK(c, BatchRegisterResponse{
		OK:        true,
		Total:     len(req.Clips),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	})
}

// processBatchClip processes a single clip by calling RegisterFromYouTube
// via a synthetic gin.Context and capturing the response.
func (h *Handler) processBatchClip(ctx context.Context, clip RegisterFromYouTubeRequest) BatchClipResult {
	result := BatchClipResult{
		Name: clip.Name,
	}

	body, err := json.Marshal(clip)
	if err != nil {
		result.Error = "failed to serialize clip: " + err.Error()
		return result
	}

	httpReq := &gin.Context{}
	httpReq.Request, _ = http.NewRequestWithContext(ctx, "POST", "/api/media/register-from-youtube", bytes.NewReader(body))
	httpReq.Request.Header.Set("Content-Type", "application/json")
	httpReq.Set("_batch_mode", true)
	httpReq.Keys = make(map[string]any)

	w := &batchResponseWriter{body: &bytes.Buffer{}}
	httpReq.Writer = w

	h.RegisterFromYouTube(httpReq)

	respBody, err := io.ReadAll(w.body)
	if err != nil {
		result.Error = "failed to read response"
		return result
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		result.Error = "failed to parse response"
		return result
	}

	if ok, exists := resp["ok"].(bool); exists && ok {
		result.OK = true
		result.ClipID, _ = resp["clip_id"].(string)
		if dup, exists := resp["duplicate"].(bool); exists && dup {
			result.Duplicate = true
			result.OK = false
		}
	} else if errMsg, exists := resp["error"].(string); exists {
		result.Error = errMsg
	} else if msg, exists := resp["message"].(string); exists {
		result.Error = msg
	}

	return result
}

// ── Helpers ──────────────────────────────────────────────────────────────

// extractVideoIDFromURL extracts the YouTube video ID from a URL.
func extractVideoIDFromURL(rawURL string) string {
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
	if idx := strings.LastIndex(rawURL, "youtu.be/"); idx != -1 {
		rest := rawURL[idx+len("youtu.be/"):]
		if end := strings.IndexAny(rest, "?&#"); end != -1 {
			rest = rest[:end]
		}
		return rest
	}
	return ""
}

// findExistingYouTubeClip checks the clips repo for an existing clip
// matching the given YouTube URL/video ID.
func (h *Handler) findExistingYouTubeClip(ctx context.Context, videoID, sourceURL string, startSec, endSec float64) (string, error) {
	if h.clipsRepo != nil && videoID != "" {
		hasSegment := endSec > startSec
		if id, err := h.clipsRepo.FindByYouTubeVideoID(ctx, videoID, hasSegment, startSec, endSec); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	if h.clipsRepo != nil && sourceURL != "" && !(endSec > startSec) {
		if id, err := h.clipsRepo.FindBySourceURL(ctx, sourceURL); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

// fetchYouTubeVideo resolves the YouTube FetchProvider and calls Fetch().
func (h *Handler) fetchYouTubeVideo(
	ctx context.Context,
	url string,
	startSec, endSec float64,
	assetID string,
) (*providers.FetchedAsset, error) {
	if h.providerRegistry == nil {
		return nil, fmt.Errorf("provider registry not wired")
	}

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

// relatedClipsViaRegistry fans out providerRegistry.ByCapability(CapabilitySearch).
func (h *Handler) relatedClipsViaRegistry(
	ctx context.Context,
	clip *asset.Asset,
	log *zap.Logger,
) gin.H {
	out := gin.H{}
	if h.providerRegistry == nil {
		return out
	}
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

// buildRelatedClipsQuery composes a broadened query string from a freshly-registered clip.
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

// ── Transcription ────────────────────────────────────────────────────────

// transcriptResult holds the parsed JSON output from transcribe_detect_lang.py.
type transcriptResult struct {
	Language             string  `json:"language"`
	Probability          float64 `json:"probability"`
	TranscriptFull       string  `json:"transcript_full"`
	TranscriptPreview    string  `json:"transcript_preview"`
	TranscriptLength     int     `json:"transcript_length"`
	NumSegments          int     `json:"num_segments"`
	TranscriptionTimeSec float64 `json:"transcription_time_seconds"`
	Error                string  `json:"error"`
}

// transcribeAudio runs Whisper transcription on a local video file.
func (h *Handler) transcribeAudio(ctx context.Context, localPath string, log *zap.Logger) (transcript string, language string) {
	if localPath == "" {
		return "", ""
	}

	if _, err := os.Stat(localPath); err != nil {
		log.Debug("transcribe: file not found, skipping", zap.String("path", localPath), zap.Error(err))
		return "", ""
	}

	pythonBin := "python3"
	scriptPath := filepath.Join(h.cfg.Paths.PythonScriptsDir, "tools", "transcribe_detect_lang.py")

	if _, err := os.Stat(scriptPath); err != nil {
		log.Debug("transcribe: script not found, skipping", zap.String("path", scriptPath), zap.Error(err))
		return "", ""
	}

	execResult, err := executil.RunSimple(ctx, pythonBin, scriptPath,
		"--transcribe", "--model", "tiny", "--json-only", localPath,
	)
	if err != nil {
		log.Warn("transcription failed for clip (non-fatal)",
			zap.String("path", localPath),
			zap.Error(err),
		)
		return "", ""
	}

	var tsResult transcriptResult
	if err := json.Unmarshal([]byte(execResult.Output), &tsResult); err != nil {
		log.Warn("failed to parse transcription JSON",
			zap.String("path", localPath),
			zap.Error(err),
		)
		return "", ""
	}

	if tsResult.Error != "" {
		log.Warn("transcription error from whisper",
			zap.String("path", localPath),
			zap.String("error", tsResult.Error),
		)
		return "", ""
	}

	transcript = strings.TrimSpace(tsResult.TranscriptFull)
	language = strings.TrimSpace(tsResult.Language)

	log.Info("clip transcribed",
		zap.String("path", localPath),
		zap.String("language", language),
		zap.Float64("probability", tsResult.Probability),
		zap.Int("transcript_len", tsResult.TranscriptLength),
		zap.Float64("time_sec", tsResult.TranscriptionTimeSec),
	)

	return transcript, language
}

// saveTranscriptAndStage writes the transcript text next to the video file
// and stages it for the embedding server.
func (h *Handler) saveTranscriptAndStage(localPath string, transcript string, group string, log *zap.Logger) {
	if transcript == "" {
		return
	}

	baseNoExt := strings.TrimSuffix(localPath, filepath.Ext(localPath))
	txtPath := baseNoExt + ".txt"
	if err := os.WriteFile(txtPath, []byte(transcript), 0644); err != nil {
		log.Warn("failed to write transcript .txt next to video",
			zap.String("txt_path", txtPath),
			zap.Error(err),
		)
	} else {
		log.Debug("transcript .txt saved next to video",
			zap.String("txt_path", txtPath),
		)
	}

	stageRoot := h.cfg.Storage.YoutubeClipsPath()
	if stageRoot == "" {
		stageRoot = filepath.Join(h.cfg.Storage.DataDir, "youtube-clips")
	}

	subBucket := strings.TrimSpace(group)
	if subBucket == "" || subBucket == "." {
		subBucket = "_manual"
	}
	subBucket = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, subBucket)

	stageDir := filepath.Join(stageRoot, subBucket)
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		log.Warn("failed to create transcript staging directory",
			zap.String("dir", stageDir),
			zap.Error(err),
		)
		return
	}

	stageFile := filepath.Base(baseNoExt) + ".txt"
	stagePath := filepath.Join(stageDir, stageFile)
	if err := os.WriteFile(stagePath, []byte(transcript), 0644); err != nil {
		log.Warn("failed to stage transcript for embedding server",
			zap.String("stage_path", stagePath),
			zap.Error(err),
		)
	} else {
		log.Debug("transcript staged for embedding server",
			zap.String("stage_path", stagePath),
		)
	}
}

// ── Batch response writer ───────────────────────────────────────────────

// batchResponseWriter is a minimal gin.ResponseWriter that captures the body.
type batchResponseWriter struct {
	body *bytes.Buffer
}

func (w *batchResponseWriter) Header() http.Header                  { return http.Header{} }
func (w *batchResponseWriter) Write(b []byte) (int, error)          { return w.body.Write(b) }
func (w *batchResponseWriter) WriteHeader(statusCode int)           {}
func (w *batchResponseWriter) WriteHeaderNow()                      {}
func (w *batchResponseWriter) Written() bool                        { return w.body.Len() > 0 }
func (w *batchResponseWriter) WriteString(s string) (int, error)    { return w.body.WriteString(s) }
func (w *batchResponseWriter) Size() int                            { return w.body.Len() }
func (w *batchResponseWriter) Status() int                          { return 200 }
func (w *batchResponseWriter) Flush()                               {}
func (w *batchResponseWriter) CloseNotify() <-chan bool             { return make(chan bool) }
func (w *batchResponseWriter) Pusher() http.Pusher                  { return nil }
func (w *batchResponseWriter) SetReadDeadline(_ interface{}) error  { return nil }
func (w *batchResponseWriter) SetWriteDeadline(_ interface{}) error { return nil }
func (w *batchResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

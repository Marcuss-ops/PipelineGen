// Package clips — Ingest sub-handler (Step 5 Split 2, June 2026).
//
// OVERRIDE ADR 0009 (clips.Handler capability-split) — user override
// recorded in commit messages; this commit extracts the 3 ingest
// routes (CreateClip + UpdateClip + UploadVideoClip) into a dedicated
// *IngestHandler receiver. IngestDeps carries only the 12 deps these
// methods consume (cluster × deps matrix §4):
//
//   - Dispatcher     (CreateClip + UpdateClip + UploadVideoClip — atomic UPSERT + outbox)
//   - AssetTreeSvc   (CreateClip + UpdateClip + UploadVideoClip — tree upsert)
//   - JobsSvc        (CreateClip + UploadVideoClip — media.enrich enqueue)
//   - SourceResolver (UpdateClip — repoForSource gate)
//   - ArtifactSvc    (UploadVideoClip — CreateAndVerify / LocalPath)
//   - DriveUploader  (UploadVideoClip — group folder + UploadFileWithDescription)
//   - ProcessRunner  (UploadVideoClip — probeDuration via ffprobe/mediainfo)
//   - Cfg            (UploadVideoClip — Drive.RootFolder, Storage.TempPath)
//   - ClipIndexer    (UploadVideoClip — null-check pre media.enrich gate)
//   - MetaWriter     (UploadVideoClip — null-check pre media.enrich gate)
//   - EnrichUC       (CreateClip + UploadVideoClip — null-check pre media.enrich gate)
//   - Log            (all methods)
//
// Pattern B (per-cluster RegisterRoutes with idem fn as parameter):
// the orchestrator Handler.RegisterRoutes single-calls
// ih.RegisterRoutes(r, h.idemWriter()). All 3 ingest routes have idem
// installed before the handler per AGENTS.md Pattern 8 (writes are
// atomic via Dispatcher + jobs media.enrich enqueue).
package clips

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// errClipDispatcherUnavailable is the fail-closed sentinel surfaced by
// every clips API writer in PR 6 (codex/qdrant-api-writers-fail-closed):
// when the canonical AssetMutationDispatcher is not wired at composition
// time (test fixtures, partial deploys), the four write endpoints
// (CreateClip, UploadVideoClip — ClipAction::ReuploadClip and
// sound_effect Generate are documented in their own clusters) return
// HTTP 503 with this message instead of silently falling back to a raw
// repo.Upsert write that would corrupt Qdrant semantics.
//
// Operational response: the operator should investigate why the
// composition root did not inject the dispatcher. The Wave 22 task-2
// (PR-6) gate `scripts/ci-bypass-audit.sh` ratchets this counter to
// zero — any new caller that bypasses AssetMutationDispatcher fails CI.
//
// Moved from clip_create.go (deleted in Split 2, June 2026).
var errClipDispatcherUnavailable = errors.New("clips API write unavailable: AssetMutationDispatcher not wired (QDRANT-asset-mutation isolation; production composition root must wire *outbox.Dispatcher via clipsDispatcherAdapter)")

// IngestDeps is the constructor bag for IngestHandler. The 12 fields
// below are exactly the deps the 3 moved methods touch — no more, no
// less. Cluster ownership follows the matrix in the Step 5 discovery
// report (June 2026, §4 Ingest cluster).
type IngestDeps struct {
	Dispatcher     appclips.ClipIndexDispatcherPort
	AssetTreeSvc   *assettree.Service
	JobsSvc        jobservice.Service
	SourceResolver *artifacts.SourceResolver
	ArtifactSvc    *artifacts.Service
	DriveUploader  *drive.Uploader
	ProcessRunner  appassets.ProcessRunner
	Cfg            *config.Config
	ClipIndexer    *clipindexer.Service
	MetaWriter     *semantic.MetadataWriter
	EnrichUC       *appclips.EnrichUseCase
	Log            *zap.Logger
}

// IngestHandler owns the 3 ingest routes. Receiver-on-pattern-B:
// constructed in NewHandler from an IngestDeps shape extracted from
// the orchestrator Deps.
type IngestHandler struct {
	dispatcher     appclips.ClipIndexDispatcherPort
	assetTreeSvc   *assettree.Service
	jobsSvc        jobservice.Service
	sourceResolver *artifacts.SourceResolver
	artifactSvc    *artifacts.Service
	driveUploader  *drive.Uploader
	processRunner  appassets.ProcessRunner
	cfg            *config.Config
	clipIndexer    *clipindexer.Service
	metaWriter     *semantic.MetadataWriter
	enrichUC       *appclips.EnrichUseCase
	log            *zap.Logger
}

// NewIngestHandler constructs an IngestHandler with the supplied
// IngestDeps. Nil fields are tolerated for test fixtures (each method
// does its own nil-check); production wiring supplies all 12 via the
// orchestrator Deps shape.
func NewIngestHandler(d IngestDeps) *IngestHandler {
	return &IngestHandler{
		dispatcher:     d.Dispatcher,
		assetTreeSvc:   d.AssetTreeSvc,
		jobsSvc:        d.JobsSvc,
		sourceResolver: d.SourceResolver,
		artifactSvc:    d.ArtifactSvc,
		driveUploader:  d.DriveUploader,
		processRunner:  d.ProcessRunner,
		cfg:            d.Cfg,
		clipIndexer:    d.ClipIndexer,
		metaWriter:     d.MetaWriter,
		enrichUC:       d.EnrichUC,
		log:            d.Log,
	}
}

// repoForSource resolves a clip source to its canonical repository
// via the shared SourceResolver. Used by UpdateClip; each cluster
// that needs source → repo mapping owns its own repoForSource method
// on its receiver (Split 1: Search; Split 2: Ingest; Split 3: Action;
// future Ops).
func (ih *IngestHandler) repoForSource(source string) *assets.ClipsRepository {
	if ih.sourceResolver == nil {
		return nil
	}
	return ih.sourceResolver.ResolveRepo(source)
}

// RegisterRoutes installs the 3 Ingest routes on the supplied gin
// router group. All routes are writes (idem-protected per PR8).
//
// Route table:
//
//	POST  /:source/clips           -> CreateClip      (write+idem)
//	PATCH /:source/clips/:id       -> UpdateClip      (write+idem)
//	POST  /upload-video            -> UploadVideoClip (write+idem)
func (ih *IngestHandler) RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc) {
	r.POST("/:source/clips", idem, ih.CreateClip)
	r.PATCH("/:source/clips/:id", idem, ih.UpdateClip)
	r.POST("/upload-video", idem, ih.UploadVideoClip)
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_create.go (deleted in Split 2, June 2026)
// ──────────────────────────────────────────────────────────────────────

// CreateClip creates a new clip and triggers semantic enrichment + vector indexing
// so the clip becomes immediately searchable via semantic search endpoints.
//
// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the synchronous
// SQLite UPSERT step now routes through dispatcher.EnqueueAndIndex —
// atomic UPSERT media_assets + INSERT outbox_events in a single
// transaction. A nil dispatcher is treated as a wiring error (HTTP 503),
// not as a fallback trigger to ih.assetRepo.Upsert. The downstream async
// enrichment path (media.enrich job enqueue) is preserved — it covers
// semantic enrichment (LLM tags) and is a separate concern from the
// Qdrant indexing path that the outbox event already triggers.
func (ih *IngestHandler) CreateClip(c *gin.Context) {
	source := c.Param("source")

	// Validate source param exists
	if source == "" {
		apiutil.BadRequest(c, "source is required")
		return
	}

	if ih.dispatcher == nil {
		// Fail-closed: PR 6 replaces the legacy
		// `if ih.assetRepo != nil { ih.assetRepo.Upsert }` path with an
		// explicit error. The composition root must always wire the
		// canonical *outbox.Dispatcher (via clipsDispatcherAdapter) in
		// production; reaching this branch means a wiring regression.
		ih.log.Error("CreateClip: dispatcher not wired \u2014 atomic UPSERT + outbox enqueue refused",
			zap.String("source", source))
		apiutil.Error(c, 503, errClipDispatcherUnavailable.Error())
		return
	}

	var clip asset.Asset
	if err := c.ShouldBindJSON(&clip); err != nil {
		apiutil.BadRequest(c, "invalid clip data: "+err.Error())
		return
	}

	// Ensure ID is generated if missing
	if clip.ID == "" {
		clip.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if clip.Source == "" {
		clip.Source = asset.Source(source)
	}

	ctx := c.Request.Context()

	// 1. Atomic UPSERT + outbox event via the canonical dispatcher.
	// The dispatcher's supersede-gate dedup uses the contentHash; fall
	// back to clip.ID when the bind-time payload omits it (the dispatcher
	// rejects empty asset.ID via the EnqueueAndIndex NewDispatcher wiring
	// pre-flight at outbox/repository.go:243-246).
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = clip.ID
	}
	if err := ih.dispatcher.EnqueueAndIndex(ctx, &clip, contentHash); err != nil {
		// Surface mutations.ErrDispatcherUnavailable verbatim so the
		// API caller can correlate with the upstream sentinel without
		// wrapping; any other error means the SQLite tx failed or the
		// outbox publish raced a malformed envelope — propagate.
		if errors.Is(err, mutations.ErrDispatcherUnavailable) {
			ih.log.Error("CreateClip: dispatcher unavailable", zap.String("clip_id", clip.ID), zap.Error(err))
			apiutil.Error(c, 503, errClipDispatcherUnavailable.Error())
			return
		}
		ih.log.Error("CreateClip: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("dispatcher.EnqueueAndIndex: %w", err))
		return
	}

	// 2. Update Asset Tree
	if ih.assetTreeSvc != nil {
		node := appclips.ClipToAssetNode(&clip)
		if err := ih.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			ih.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 3. Trigger async enrichment + indexing via canonical jobs system
	// (S1a, June 2026). The previous implementation used
	// `concurrent.SafeGo` + `context.WithoutCancel` to detach from the
	// HTTP handler ctx — but that simulates a background job from a
	// handler, which AGENTS.md §7 + Pattern 8 explicitly forbid.
	// Canonical path: enqueue a `media.enrich` job whose worker runs in
	// the local broker pool (or a remote worker via VELOX_BROKER_URL),
	// with the same 3-minute hard cap. The clip row is already saved
	// before this point so a failed enqueue does NOT roll back the HTTP
	// write — we log a WARN and let the operator re-trigger via
	// `POST /:source/clips/:id/reindex`.
	indexed := true
	if ih.enrichUC != nil && ih.jobsSvc != nil {
		_, err := ih.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clip.ID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clip.ID,
		})
		if err != nil {
			ih.log.Warn("failed to enqueue media.enrich job (clip is saved; reactive re-index required)",
				zap.String("clip_id", clip.ID), zap.Error(err))
			indexed = false
		}
	} else if ih.enrichUC != nil {
		// S1a (June 2026): jobs service NOT wired but enrichment deps
		// are. Pre-lift behaviour claimed `indexed: true` while doing
		// nothing — that was misleading. Truthful signal:
		// leave `indexed: false`. Production always wires jobsSvc; a
		// missing jobsSvc in test fixtures is the test author's
		// responsibility.
		ih.log.Warn("CreateClip: enrichment deps wired but jobsSvc nil \u2014 clip saved; index will lag until reactive re-index",
			zap.String("clip_id", clip.ID), zap.String("source", source))
		indexed = false
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"clip":    clip,
		"indexed": indexed,
	})
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_update.go (deleted in Split 2, June 2026)
// ──────────────────────────────────────────────────────────────────────

// UpdateClip updates an existing clip.
//
// QDRANT-002 (June 2026): Routes through dispatcher.EnqueueAndIndex for
// atomic UPSERT + outbox event. The raw repo.UpsertClip fallback is
// prohibited — a nil dispatcher is a wiring error and returns 503.
// Tests must inject a dispatcher stub.
func (ih *IngestHandler) UpdateClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := ih.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		apiutil.BadRequest(c, "invalid payload")
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Manual update of fields from payload
	if val, ok := payload["name"].(string); ok {
		clip.Name = val
	}
	if val, ok := payload["category"].(string); ok {
		clip.Category = val
	}
	if val, ok := payload["tags"].([]any); ok {
		tags := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				tags[i] = s
			}
		}
		clip.Tags = tags
	}
	if val, ok := payload["search_terms"].([]any); ok {
		terms := make([]string, len(val))
		for i, v := range val {
			if s, ok := v.(string); ok {
				terms[i] = s
			}
		}
		clip.SearchTerms = terms
	}
	if val, ok := payload["status"].(string); ok {
		clip.SetMetadataString("status", val)
	}
	if val, ok := payload["error"].(string); ok {
		clip.SetMetadataString("error", val)
	}
	if val, ok := payload["folder_id"].(string); ok {
		clip.SetFolderID(val)
	}
	if val, ok := payload["folder_path"].(string); ok {
		clip.SetFolderPath(val)
	}
	if val, ok := payload["drive_link"].(string); ok {
		clip.SetDriveLink(val)
	}
	if val, ok := payload["download_link"].(string); ok {
		clip.SetDownloadLink(val)
	}
	if val, ok := payload["thumb_url"].(string); ok {
		clip.ThumbnailURL = val
	}

	// QDRANT-002 closed (June 2026): dispatcher is mandatory.
	// Raw repo writes without outbox are prohibited — a nil
	// dispatcher is a wiring error, not a runtime fallback.
	if ih.dispatcher == nil {
		ih.log.Error("QDRANT-002: clip update rejected — dispatcher not wired (raw write without outbox is prohibited)",
			zap.String("clip_id", clipID))
		apiutil.Error(c, 503, "clip update unavailable: dispatcher not wired")
		return
	}
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = clipID
	}
	if err := ih.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Also update Asset Tree if service is available
	if ih.assetTreeSvc != nil {
		node := appclips.ClipToAssetNode(clip)
		if err := ih.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			ih.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clipID,
		"clip":    clip,
	})
}

// ──────────────────────────────────────────────────────────────────────
// MOVED FROM clip_upload.go (deleted in Split 2, June 2026)
// ──────────────────────────────────────────────────────────────────────

// UploadVideoClipResponse is returned after a successful video upload.
type UploadVideoClipResponse struct {
	OK          bool     `json:"ok"`
	ClipID      string   `json:"clip_id"`
	Name        string   `json:"name"`
	Filename    string   `json:"filename"`
	DriveLink   string   `json:"drive_link,omitempty"`
	DriveFileID string   `json:"drive_file_id,omitempty"`
	FileHash    string   `json:"file_hash"`
	Source      string   `json:"source"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	LocalPath   string   `json:"local_path"`
	Indexed     bool     `json:"indexed"`
	Duration    int      `json:"duration,omitempty"`
}

// UploadVideoClip handles POST /api/media/upload-video
// Accepts multipart form data with a video file and metadata fields.
//
// Form fields:
//   - file:       (required) the video file
//   - name:       clip name (defaults to filename without extension)
//   - description: description / search text for Qdrant indexing
//   - tags:       JSON array of tags, e.g. ["funny","interview"]
//   - source:     source identifier (default "manual")
//   - category:   category
//   - group:      Drive subfolder group name
//   - folder_id:  Drive folder ID (if omitted, uses configured default root)
func (ih *IngestHandler) UploadVideoClip(c *gin.Context) {
	// 1. Parse multipart form (max 500MB)
	if err := c.Request.ParseMultipartForm(500 << 20); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("failed to parse multipart form: %v", err))
		return
	}

	// 2. Get the video file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		apiutil.BadRequest(c, "file field is required: "+err.Error())
		return
	}
	defer file.Close()

	// 3. Parse metadata from form fields
	name := strings.TrimSpace(c.PostForm("name"))
	description := strings.TrimSpace(c.PostForm("description"))
	source := strings.TrimSpace(c.PostForm("source"))
	category := strings.TrimSpace(c.PostForm("category"))
	group := strings.TrimSpace(c.PostForm("group"))
	folderID := strings.TrimSpace(c.PostForm("folder_id"))

	// Parse tags as JSON array (fallback: comma-separated)
	var tags []string
	if tagsStr := c.PostForm("tags"); tagsStr != "" {
		if err := json.Unmarshal([]byte(tagsStr), &tags); err != nil {
			for _, t := range strings.Split(tagsStr, ",") {
				if trimmed := strings.TrimSpace(t); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}
	}

	if source == "" {
		source = "manual"
	}
	if name == "" {
		name = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}

	ctx := c.Request.Context()
	log := ih.log.With(
		zap.String("handler", "upload-video"),
		zap.String("filename", header.Filename),
		zap.String("name", name),
	)

	// 4. Stream uploaded file through artifact service (content-addressed storage)
	// This replaces os.Create + io.Copy + hashutil.MD5File with a single
	// Stage→Verify→Promote flow that computes SHA-256 and stores the blob
	// at a canonical content-addressed path.
	if ih.artifactSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("artifact service not available"))
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".mp4"
	}
	clipID := "manual_" + fmt.Sprintf("%d", time.Now().UnixNano())[:12]

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	artifact, err := ih.artifactSvc.CreateAndVerify(ctx, artifacts.CreateInput{
		ID:       clipID,
		Kind:     "video",
		MimeType: mimeType,
		Reader:   file,
	})
	if err != nil {
		log.Error("failed to store artifact", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to store file: %w", err))
		return
	}
	log.Info("artifact stored",
		zap.String("id", artifact.ID),
		zap.String("sha256", artifact.SHA256),
		zap.Int64("bytes", artifact.SizeBytes))

	// 5. Resolve local path for Drive upload and duration probing
	fileHash := artifact.SHA256
	// Re-derive clipID from content hash to preserve dedup-by-content behavior:
	// uploading the same file twice gets the same clip ID → upsert instead of insert.
	clipID = "manual_" + fileHash[:12]
	localPath, err := ih.artifactSvc.LocalPath(ctx, artifact.ID)
	if err != nil {
		log.Warn("could not resolve local path for artifact",
			zap.String("id", artifact.ID),
			zap.Error(err))
		// Fallback: use the artifact ID for Drive-less flows
		localPath = ""
	}

	// 6. Resolve Drive target folder
	targetFolderID := appclips.ExtractDriveFolderID(folderID)
	if targetFolderID == "" {
		// Use the MediaRootFolder as default root
		targetFolderID = ih.cfg.Drive.RootFolder()
		if group != "" && targetFolderID != "" {
			dirID, err := ih.driveUploader.GetOrCreateFolder(ctx, group, targetFolderID)
			if err != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", group), zap.Error(err))
			} else {
				targetFolderID = dirID
			}
		}
	} else if group != "" {
		// Check if the target folder already IS the group folder (avoid nested duplicates)
		if existingName, err := ih.driveUploader.GetFolderName(ctx, targetFolderID); err == nil && appclips.CleanFolderName(existingName) == appclips.CleanFolderName(group) {
			log.Info("folder_id already points to group folder, reusing it",
				zap.String("folder_id", targetFolderID),
				zap.String("name", existingName))
		} else {
			dirID, err := ih.driveUploader.GetOrCreateFolder(ctx, group, targetFolderID)
			if err != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", group), zap.Error(err))
			} else {
				targetFolderID = dirID
			}
		}
	}

	// 7. Upload file to Google Drive
	driveFilename := fmt.Sprintf("%s%s", name, ext)
	var uploadResult *DriveUploadResult
	if ih.driveUploader != nil && localPath != "" {
		driveDescription := appclips.BuildDriveDescription(name, description, "", tags, category, source, "", "")
		result, err := ih.driveUploader.UploadFileWithDescription(ctx, localPath, targetFolderID, driveFilename, driveDescription)
		if err != nil {
			log.Warn("Drive upload failed, continuing with local file only",
				zap.Error(err))
		} else {
			uploadResult = &DriveUploadResult{
				FileID:       result.FileID,
				WebViewLink:  result.WebViewLink,
				DownloadLink: result.DownloadLink,
			}
			log.Info("uploaded to Drive",
				zap.String("file_id", result.FileID),
				zap.String("drive_link", result.WebViewLink))
		}
	}

	// 7b. Upload cumulative metadata.json to Drive alongside the video
	if ih.driveUploader != nil && targetFolderID != "" {
		clipEntry := map[string]interface{}{
			"clip_id":     clipID,
			"name":        name,
			"description": description,
			"category":    category,
			"source":      source,
			"tags":        tags,
			"created_at":  time.Now().UTC().Format(time.RFC3339),
		}
		if uploadResult != nil {
			clipEntry["drive_file_id"] = uploadResult.FileID
			clipEntry["drive_link"] = uploadResult.WebViewLink
		}
		ih.updateCumulativeMetadataJSON(ctx, ih.cfg.Storage.TempPath(), targetFolderID, clipID, clipEntry, log)
	}

	// 8. Build the MediaAsset record
	now := time.Now().UTC()
	clip := &asset.Asset{
		ID:         clipID,
		Name:       name,
		Filename:   driveFilename,
		Source:     asset.Source(source),
		Category:   category,
		Group:      group,
		MediaType:  asset.MediaType("video"),
		Tags:       tags,
		SearchText: description,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	clip.SetLocalPath(localPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(group)

	if uploadResult != nil {
		clip.SetDriveLink(uploadResult.WebViewLink)
		clip.SetDownloadLink(uploadResult.DownloadLink)
		clip.SetDriveFileID(uploadResult.FileID)
	}

	// 9. Probe video duration from local file
	if localPath != "" {
		probeDuration(ctx, localPath, clip, log, ih.processRunner)
	}

	// 10. Save to database via canonical asset.Repository
	// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the legacy
	// soft-fallback `if ih.assetRepo != nil { ih.assetRepo.Upsert }` is
	// removed — a wiring regression that bypasses the outbox MUST NOT
	// silently succeed. The dispatcher's EnqueueAndIndex atomically
	// UPSERTs media_assets + emits asset.index.requested so Qdrant
	// semantics stay correct.
	if ih.dispatcher == nil {
		log.Error("UploadVideoClip: dispatcher not wired — atomic UPSERT + outbox enqueue refused",
			zap.String("clip_id", clip.ID))
		apiutil.Error(c, 503, errClipDispatcherUnavailable.Error())
		return
	}
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = fileHash
	}
	if err := ih.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		if errors.Is(err, mutations.ErrDispatcherUnavailable) {
			log.Error("UploadVideoClip: dispatcher unavailable", zap.String("clip_id", clip.ID), zap.Error(err))
			apiutil.Error(c, 503, errClipDispatcherUnavailable.Error())
			return
		}
		log.Error("UploadVideoClip: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("dispatcher.EnqueueAndIndex: %w", err))
		return
	}
	log.Info("saved clip via dispatcher (atomic UPSERT + outbox event)",
		zap.String("clip_id", clip.ID), zap.String("content_hash", contentHash))

	// 11. Update Asset Tree
	if ih.assetTreeSvc != nil {
		node := appclips.ClipToAssetNode(clip)
		if err := ih.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 12. Trigger async enrichment + Qdrant indexing via canonical jobs
	// system (S1a, June 2026). The previous implementation spawned a
	// goroutine via `concurrent.SafeGo` + detached the ctx via
	// `context.WithoutCancel` to simulate a background job — forbidden
	// by AGENTS.md §7 + Pattern 8 (handler goroutines must not
	// orchestrate business work). Canonical path: enqueue a
	// `media.enrich` job whose worker is registered in
	// `internal/application/clips/media_enrich_worker.go` and runs in
	// the local broker pool (or a remote worker via VELOX_BROKER_URL),
	// with the same 3-minute hard cap that the registry records.
	indexed := false
	if hasIndexer := ih.clipIndexer != nil || ih.enrichUC != nil || ih.metaWriter != nil; hasIndexer && ih.jobsSvc != nil {
		_, err := ih.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clip.ID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clip.ID,
		})
		if err != nil {
			log.Warn("failed to enqueue media.enrich job (clip is saved; reactive re-index required)",
				zap.String("clip_id", clip.ID), zap.Error(err))
		} else {
			indexed = true
		}
	} else if ih.clipIndexer != nil || ih.enrichUC != nil || ih.metaWriter != nil {
		// S1a (June 2026): same misleading-fallback fix as CreateClip —
		// jobs service not wired but enrichment deps are. Stay silent
		// (indexed stays false). Production always wires jobsSvc;
		// a missing jobsSvc in test fixtures is the test author's
		// responsibility. A WARN log surfaces the drift.
		log.Warn("UploadVideoClip: enrichment deps wired but jobsSvc nil — clip saved; index will lag until reactive re-index",
			zap.String("clip_id", clip.ID), zap.String("source", source))
	}
	if indexed {
		log.Info("triggered async enrichment + Qdrant indexing", zap.String("clip_id", clip.ID))
	}

	// 13. Return success response
	apiutil.OK(c, UploadVideoClipResponse{
		OK:          true,
		ClipID:      clip.ID,
		Name:        clip.Name,
		Filename:    driveFilename,
		DriveLink:   clip.DriveLink(),
		DriveFileID: clip.DriveFileID(),
		FileHash:    fileHash,
		Source:      source,
		Category:    category,
		Tags:        tags,
		LocalPath:   localPath,
		Indexed:     indexed,
		Duration:    int(clip.Duration.Milliseconds()),
	})
}

// DriveUploadResult is a simplified drive upload result, exported so
// the sibling sources package (handler_sources_register_from_youtube.go)
// can construct one without depending on clips package internals.
//
// Moved from clip_upload.go (deleted in Split 2, June 2026). Lives here
// on the IngestHandler receiver's domain.
type DriveUploadResult struct {
	FileID       string
	WebViewLink  string
	DownloadLink string
}

// updateCumulativeMetadataJSON is a best-effort helper used by the
// upload flow. The metadata file is maintained elsewhere; keep this
// call non-fatal so upload progress isn't blocked on sidecar JSON
// persistence.
//
// Moved from clip_ops_handlers.go (which it shared with Ops cluster
// methods). Now a method on *IngestHandler — semantically owned by
// the upload flow. Originally a no-op shim that just logged Debug;
// preserved as-is to keep PR-A doctrine of zero behaviour change.
func (ih *IngestHandler) updateCumulativeMetadataJSON(_ context.Context, _ string, _ string, _ string, _ map[string]interface{}, log *zap.Logger) {
	if log != nil {
		log.Debug("updateCumulativeMetadataJSON called")
	}
}

// ──────────────────────────────────────────────────────────────────────
// Helpers — process probing (ffprobe, mediainfo) for video duration.
// Moved from clip_upload.go (deleted in Split 2, June 2026).
// ──────────────────────────────────────────────────────────────────────

// probeDuration probes the video file for duration using ffprobe.
// Falls back to 0 if unavailable.
func probeDuration(ctx context.Context, localPath string, clip *asset.Asset, log *zap.Logger, runner appassets.ProcessRunner) {
	if clip == nil {
		return
	}

	// Try ffprobe
	probe := probeFFprobe(ctx, localPath, runner)
	if probe != nil && probe.Duration > 0 {
		clip.Duration = time.Duration(probe.Duration * float64(time.Second))
		return
	}

	// Fallback: try mediainfo if available
	dur := probeMediaInfo(ctx, localPath, runner)
	if dur > 0 {
		clip.Duration = time.Duration(dur) * time.Second
		return
	}

	log.Debug("could not probe video duration, leaving at 0",
		zap.String("path", localPath))
}

// probeFFprobe runs ffprobe on the file and returns duration.
func probeFFprobe(ctx context.Context, localPath string, runner appassets.ProcessRunner) *ffprobeResult {
	ffprobePath := "ffprobe"
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		localPath,
	}

	result, err := execCmd(ctx, ffprobePath, args, runner)
	if err != nil {
		return nil
	}

	output := strings.TrimSpace(result)
	if output == "" {
		return nil
	}

	var duration float64
	if _, err := fmt.Sscanf(output, "%f", &duration); err != nil {
		return nil
	}

	return &ffprobeResult{Duration: duration}
}

// ffprobeResult is the private probe response. Internal to ingest.go.
type ffprobeResult struct {
	Duration float64
}

// probeMediaInfo runs mediainfo as a fallback probe.
func probeMediaInfo(ctx context.Context, localPath string, runner appassets.ProcessRunner) int {
	result, err := execCmd(ctx, "mediainfo", []string{
		"--Inform=General;%Duration%",
		localPath,
	}, runner)
	if err != nil {
		return 0
	}

	output := strings.TrimSpace(result)
	if output == "" {
		return 0
	}

	var durationMs int
	if _, err := fmt.Sscanf(output, "%d", &durationMs); err != nil {
		return 0
	}

	return durationMs / 1000
}

// execCmd runs a command and returns stdout as a string.
func execCmd(ctx context.Context, name string, args []string, runner appassets.ProcessRunner) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("process runner not configured")
	}
	result, err := runner.RunSimple(ctx, name, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Output), nil
}

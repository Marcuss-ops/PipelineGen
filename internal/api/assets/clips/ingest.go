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
//   - ClipsRepo (UpdateClip — repoForSource gate)
//   - ArtifactSvc    (UploadVideoClip — CreateAndVerify / LocalPath)
//   - DriveAdmin  (UploadVideoClip — group folder + UploadFileWithDescription)
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
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
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
	Dispatcher    appclips.ClipIndexDispatcherPort
	AssetTreeSvc  *assettree.Service
	JobsSvc       jobservice.Service
	ClipsRepo     *assets.ClipsRepository
	ArtifactSvc   *artifacts.Service
	DriveAdmin    drive.Admin
	ProcessRunner appassets.ProcessRunner
	Cfg           *config.Config
	ClipIndexer   *clipindexer.Service
	MetaWriter    *semantic.MetadataWriter
	EnrichUC      *appclips.EnrichUseCase
	UploadUC      *appupload.UseCase
	Log           *zap.Logger
}

// IngestHandler owns the 3 ingest routes. Receiver-on-pattern-B:
// constructed in NewHandler from an IngestDeps shape extracted from
// the orchestrator Deps.
type IngestHandler struct {
	dispatcher    appclips.ClipIndexDispatcherPort
	assetTreeSvc  *assettree.Service
	jobsSvc       jobservice.Service
	clipsRepo     *assets.ClipsRepository
	artifactSvc   *artifacts.Service
	driveAdmin    drive.Admin
	processRunner appassets.ProcessRunner
	cfg           *config.Config
	clipIndexer   *clipindexer.Service
	metaWriter    *semantic.MetadataWriter
	enrichUC      *appclips.EnrichUseCase
	uploadUC      *appupload.UseCase
	log           *zap.Logger
}

// NewIngestHandler constructs an IngestHandler with the supplied
// IngestDeps. Nil fields are tolerated for test fixtures (each method
// does its own nil-check); production wiring supplies all 12 via the
// orchestrator Deps shape.
func NewIngestHandler(d IngestDeps) *IngestHandler {
	return &IngestHandler{
		dispatcher:    d.Dispatcher,
		assetTreeSvc:  d.AssetTreeSvc,
		jobsSvc:       d.JobsSvc,
		clipsRepo:     d.ClipsRepo,
		artifactSvc:   d.ArtifactSvc,
		driveAdmin:    d.DriveAdmin,
		processRunner: d.ProcessRunner,
		cfg:           d.Cfg,
		clipIndexer:   d.ClipIndexer,
		metaWriter:    d.MetaWriter,
		enrichUC:      d.EnrichUC,
		uploadUC:      d.UploadUC,
		log:           d.Log,
	}
}

// repoForSource resolves a clip source to its canonical repository
// via the shared ClipsRepository. All clip-type sources share the same
// concrete repo in production. Returns nil for voiceover/images.
func (ih *IngestHandler) repoForSource(source string) *assets.ClipsRepository {
	if ih.clipsRepo == nil {
		return nil
	}
	if !artifacts.IsClipsSource(source) {
		return nil
	}
	return ih.clipsRepo
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
// P1.5 CUTOVER (June 2026): the 10-step orchestration previously inlined
// here has been extracted into internal/application/clips/upload/UseCase.
// The handler is now thin transport only (AGENTS.md Pattern 8): it parses
// the multipart form, builds an UploadClipCommand, calls uploadUC.Execute,
// and maps the result to the JSON response.
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

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	// 4. P1.5 CUTOVER: delegate to upload.UseCase.Execute.
	// The use case absorbs the 10-step pipeline (artifact staging,
	// Drive folder resolve, upload, metadata, asset construction,
	// ffprobe, dispatcher, tree, job enqueue). Handler is thin
	// transport only — if the use case is nil (test fixture wiring
	// gap), surface a clear error.
	uc := ih.uploadUC
	if uc == nil {
		apiutil.InternalError(c, fmt.Errorf("upload use case not wired"))
		return
	}

	result, err := uc.Execute(c.Request.Context(), appupload.UploadClipCommand{
		File:        file,
		Filename:    header.Filename,
		MimeType:    mimeType,
		Name:        name,
		Description: description,
		Tags:        tags,
		Source:      source,
		Category:    category,
		Group:       group,
		FolderID:    folderID,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// 5. Map use-case result to legacy response envelope
	apiutil.OK(c, UploadVideoClipResponse{
		OK:          result.OK,
		ClipID:      result.ClipID,
		Name:        result.Name,
		Filename:    result.Filename,
		DriveLink:   result.DriveLink,
		DriveFileID: result.DriveFileID,
		FileHash:    result.FileHash,
		Source:      result.Source,
		Category:    result.Category,
		Tags:        result.Tags,
		LocalPath:   result.LocalPath,
		Indexed:     result.Indexed,
		Duration:    result.Duration,
	})
}

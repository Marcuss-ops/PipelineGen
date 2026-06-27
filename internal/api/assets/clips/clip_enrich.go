package clips

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// enrichAndIndexClip runs the full enrichment pipeline in background with a 3-minute timeout:
//  1. LLM semantic tagger → search_text, tags, subjects
//  2. Clip indexer → embedding computation
//  3. Vector store (Qdrant) upsert
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	h.enrichUC.EnrichAndIndex(ctx, clip, source)
}

// EnrichMedia triggers semantic enrichment + embedding for any media asset.
// Works with ALL media types (image, clip, stock, artlist, youtube, audio, voiceover).
// If search_text is missing, calls the semantic tagger to generate it.
// Then generates embedding via the Python embedding server, persists to DB.
// Finally upserts to Qdrant vector store.
//
// Usage:
//
//	POST /api/artlist/enrich    — uses path param :source from route
//	POST /api/enrich            — uses source from JSON body
//
// S1a (June 2026): the previous implementation called
// `h.enrichUC.EnrichMedia(...)` which spawned
// `concurrent.SafeGo(...) + context.WithoutCancel(...)` goroutines
// inside the application tier — the
// "handler-tier goroutine simulating a background job"
// anti-pattern that AGENTS.md §7 + Pattern 8 explicitly forbid.
// Canonical path: enqueue a `media.enrich` job so the
// `MediaEnrichWorker` runs the work in the broker pool / remote
// worker (same code path as CreateClip / UploadVideoClip /
// ReindexClip). Handler returns the job_id so the operator can
// poll `GET /api/jobs/:id/full`.
func (h *Handler) EnrichMedia(c *gin.Context) {
	var req struct {
		AssetID      string `json:"asset_id"`
		Source       string `json:"source"`
		SkipEmbedGen bool   `json:"skip_embed_gen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Fallback: use path param :source if body doesn't have source
	if req.Source == "" {
		req.Source = c.Param("source")
	}

	if req.AssetID == "" {
		apiutil.BadRequest(c, "asset_id is required")
		return
	}

	if h.jobsSvc == nil {
		// S1a (June 2026): jobs service unavailable — the previous
		// path spawned a goroutine via concurrent.SafeGo inside
		// the application tier; we no longer do that. Truthful
		// refusal: ask the operator to wire jobsSvc and retry.
		apiutil.Error(c, http.StatusServiceUnavailable,
			"EnrichMedia requires the jobs service (S1a removed the in-process SafeGo fallback); wire jobsSvc to use /api/media/enrich")
		return
	}

	h.log.Info("dispatching media.enrich via jobs system",
		zap.String("asset_id", req.AssetID),
		zap.String("source", req.Source),
		zap.Bool("skip_embed_gen", req.SkipEmbedGen),
	)

	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type: jobservice.TypeMediaEnrich,
		Payload: map[string]any{
			"asset_id":       req.AssetID,
			"source":         req.Source,
			"skip_embed_gen": req.SkipEmbedGen,
		},
		ActiveKey: "enrich_clip_" + req.AssetID,
	})
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to enqueue media.enrich job: %w", err))
		return
	}
	apiutil.OK(c, gin.H{
		"ok":         true,
		"action":     "enqueued",
		"job_id":     job.ID,
		"status_url": "/api/jobs/" + job.ID + "/full",
		"asset_id":   req.AssetID,
		"source":     req.Source,
		"method":     "media.enrich_worker_via_jobs",
		"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
	})
}

// ReindexClip triggers re-indexing of an existing clip (semantic enrichment + vector store).
// Useful after manually creating/updating a clip to make it searchable.
func (h *Handler) ReindexClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.repoForSource(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Run semantic enrichment first if search_text is empty but we have a name.
	// S1a (June 2026): replaced `concurrent.SafeGo` + detached ctx (a
	// forbidden "HTTP simulating background job" anti-pattern per
	// AGENTS.md §7 + Pattern 8) with a canonical `media.enrich` job
	// enqueue. The worker runs in the local broker pool / remote
	// worker, owns its own ctx (3-min hard cap from the registry), and
	// emits pipeline_stage_started/_completed logs.
	//
	// ActiveKey is the canonical coalesce key: a single in-flight
	// `enrich_clip_<id>` job per clip regardless of which route
	// triggered it. Sourcing service uses the local EnqueueRequest and
	// therefore has NO activeKey, but the jobs broker's claim path
	// inspects (job_type, payload.asset_id) so identical work via
	// different entry-points still collapses naturally.
	enrichNeeded := clip.SearchText == "" && clip.Name != "" && h.enrichUC != nil
	if enrichNeeded {
		if h.jobsSvc == nil {
			// Truthful refusal: we no longer run enrichment in the
			// request goroutine (the previous SafeGo path), so the only
			// honest answer is "service unavailable, the jobs system is
			// required". Test fixtures wire jobsSvc directly and skip
			// this branch.
			apiutil.Error(c, http.StatusServiceUnavailable,
				"reindex requires the jobs service (S1a removed the in-process SafeGo fallback); wire jobsSvc to use reindex")
			return
		}
		job, err := h.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clipID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clipID,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue media.enrich job: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"action":     "enqueued",
			"job_id":     job.ID,
			"status_url": "/api/jobs/" + job.ID + "/full",
			"clip_id":    clipID,
			"method":     "async_enrich+index_via_jobs",
			"message":    "enrichment + indexing dispatched to jobs system (worker will run)",
		})
		return
	}

	if h.clipIndexer != nil && h.clipIndexer.IsEnabled() {
		// Full pipeline: indexer generates embedding + upserts to vector store
		if err := h.clipIndexer.IndexClip(ctx, clipID); err != nil {
			apiutil.InternalError(c, fmt.Errorf("index failed: %w", err))
			return
		}
		apiutil.OK(c, gin.H{
			"ok":      true,
			"action":  "reindexed",
			"clip_id": clipID,
			"method":  "clip_indexer",
		})
		return
	}

	// direct-vector-store fallback removed — vector
	// capability deleted. The clip indexer is the canonical
	// semantic-search backend.

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "skipped",
		"clip_id": clipID,
		"reason":  "no indexer configured and no search_text available",
	})
}

// BatchReindex finds all assets missing embeddings and re-indexes them via the job system.
// Returns immediately with a job_id that can be polled via GET /api/jobs/:id/full.
//
// POST /api/media/enrich/batch
// Body: {"source": "artlist", "media_type": "video", "limit": 100}
func (h *Handler) BatchReindex(c *gin.Context) {
	var req struct {
		Source    string `json:"source"`
		MediaType string `json:"media_type"`
		Limit     int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if h.clipIndexer == nil || !h.clipIndexer.IsEnabled() {
		apiutil.InternalError(c, fmt.Errorf("clip indexer not available"))
		return
	}

	// Enqueue as a job so callers can poll progress via GET /api/jobs/:id/full
	if h.jobsSvc != nil {
		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type: "media.reindex",
			Payload: map[string]any{
				"source":     req.Source,
				"media_type": req.MediaType,
				"limit":      req.Limit,
			},
			ActiveKey: fmt.Sprintf("batch_reindex_%s_%s", req.Source, req.MediaType),
		})
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"action":     "batch_reindex_enqueued",
			"job_id":     job.ID,
			"status_url": "/api/jobs/" + job.ID + "/full",
			"message":    "Batch reindex job enqueued",
		})
		return
	}

	// Fallback: fire-and-forget goroutine if jobs service not available
	ctx := c.Request.Context()
	result, err := h.clipIndexer.BatchReindex(ctx, req.Source, req.MediaType, req.Limit)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"action":  "batch_reindex_started",
		"total":   result.Total,
		"message": fmt.Sprintf("%d assets queued for re-indexing (background)", result.Total),
	})
}

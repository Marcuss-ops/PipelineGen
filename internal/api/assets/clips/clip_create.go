package clips

import (
	"errors"
	"fmt"
	"time"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// errClipDispatcherUnavailable is the fail-closed sentinel surfaced by
// every clips API writer in PR 6 (codex/qdrant-api-writers-fail-closed):
// when the canonical AssetMutationDispatcher is not wired at composition
// time (test fixtures, partial deploys), the four write endpoints
// (CreateClip, UploadVideoClip, ClipAction::ReuploadClip, sound_effect
// Generate) return HTTP 503 with this message instead of silently
// falling back to a raw h.assetRepo.Upsert write that would corrupt
// Qdrant semantics.
//
// Operational response: the operator should investigate why the
// composition root did not inject the dispatcher. The Wave 22 task-2
// (PR-6) gate `scripts/ci-bypass-audit.sh` ratchets this counter to
// zero — any new caller that bypasses AssetMutationDispatcher fails CI.
var errClipDispatcherUnavailable = errors.New("clips API write unavailable: AssetMutationDispatcher not wired (QDRANT-asset-mutation isolation; production composition root must wire *outbox.Dispatcher via clipsDispatcherAdapter)")

// CreateClip creates a new clip and triggers semantic enrichment + vector indexing
// so the clip becomes immediately searchable via semantic search endpoints.
//
// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the synchronous
// SQLite UPSERT step now routes through dispatcher.EnqueueAndIndex —
// atomic UPSERT media_assets + INSERT outbox_events in a single
// transaction. A nil dispatcher is treated as a wiring error (HTTP 503),
// not as a fallback trigger to h.assetRepo.Upsert. The downstream async
// enrichment path (media.enrich job enqueue) is preserved — it covers
// semantic enrichment (LLM tags) and is a separate concern from the
// Qdrant indexing path that the outbox event already triggers.
func (h *Handler) CreateClip(c *gin.Context) {
	source := c.Param("source")

	// Validate source param exists
	if source == "" {
		apiutil.BadRequest(c, "source is required")
		return
	}

	if h.dispatcher == nil {
		// Fail-closed: PR 6 replaces the legacy
		// `if h.assetRepo != nil { h.assetRepo.Upsert }` path with an
		// explicit error. The composition root must always wire the
		// canonical *outbox.Dispatcher (via clipsDispatcherAdapter) in
		// production; reaching this branch means a wiring regression.
		h.log.Error("CreateClip: dispatcher not wired \u2014 atomic UPSERT + outbox enqueue refused",
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
	if err := h.dispatcher.EnqueueAndIndex(ctx, &clip, contentHash); err != nil {
		// Surface mutations.ErrDispatcherUnavailable verbatim so the
		// API caller can correlate with the upstream sentinel without
		// wrapping; any other error means the SQLite tx failed or the
		// outbox publish raced a malformed envelope — propagate.
		if errors.Is(err, mutations.ErrDispatcherUnavailable) {
			h.log.Error("CreateClip: dispatcher unavailable", zap.String("clip_id", clip.ID), zap.Error(err))
			apiutil.Error(c, 503, errClipDispatcherUnavailable.Error())
			return
		}
		h.log.Error("CreateClip: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("dispatcher.EnqueueAndIndex: %w", err))
		return
	}

	// 2. Update Asset Tree
	if h.assetTreeSvc != nil {
		node := appclips.ClipToAssetNode(&clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			h.log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
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
	if h.enrichUC != nil && h.jobsSvc != nil {
		_, err := h.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clip.ID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clip.ID,
		})
		if err != nil {
			h.log.Warn("failed to enqueue media.enrich job (clip is saved; reactive re-index required)",
				zap.String("clip_id", clip.ID), zap.Error(err))
			indexed = false
		}
	} else if h.enrichUC != nil {
		// S1a (June 2026): jobs service NOT wired but enrichment deps
		// are. Pre-lift behaviour claimed `indexed: true` while doing
		// nothing — that was misleading. Truthful signal:
		// leave `indexed: false`. Production always wires jobsSvc; a
		// missing jobsSvc in test fixtures is the test author's
		// responsibility.
		h.log.Warn("CreateClip: enrichment deps wired but jobsSvc nil \u2014 clip saved; index will lag until reactive re-index",
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

package clips

import (
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	jobmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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
		ih.log.Error("CreateClip: dispatcher not wired — atomic UPSERT + outbox enqueue refused",
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
	contentHash := clip.LegacyFileMD5()
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
	// `POST /api/assets/operator/assets/:id/reindex`.
	indexed := true
	if ih.enrichUC != nil && ih.jobsSvc != nil {
		_, err := ih.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
			Type: jobmedia.TypeEnrich,
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
		ih.log.Warn("CreateClip: enrichment deps wired but jobsSvc nil — clip saved; index will lag until reactive re-index",
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

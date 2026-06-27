// Package app — QDRANT-004 mediasearch adapters extracted from registry.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the inline adapter types that WireRegistry uses to bridge the mediasearch
// service surface with concrete infrastructure types, plus the canonical
// mutations.AssetMutationDispatcher adapter (Wave 22 task 1 of 5).
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/delivery"

	"go.uber.org/zap"
)

// ── QDRANT-004 adapters ────────────────────────────────────────────────────

// mediasearchVectorAdapter implements mediasearch.VectorSearchPort by
// combining an OllamaClient for embedding with a search.VectorStorePort
// for vector-store operations.
type mediasearchVectorAdapter struct {
	embedder interface {
		Embed(ctx context.Context, text string) ([]float32, error)
	}
	store assetsearch.VectorStorePort
}

func (a *mediasearchVectorAdapter) EmbedTextForVector(ctx context.Context, text, _ string) ([]float32, error) {
	return a.embedder.Embed(ctx, text)
}

func (a *mediasearchVectorAdapter) VectorStore() assetsearch.VectorStorePort {
	return a.store
}

// mediasearchReadAdapter implements mediasearch.MediaReadRepository using
// the existing ClipsRepository for batched SQLite reads.
type mediasearchReadAdapter struct {
	clips *assets.ClipsRepository
}

// GetMany implements the 4-arg mediasearch.MediaReadRepository contract
// (QDRANT-004 PR3, June 2026). The allowStates list is honoured at the
// memory layer because asset.Filter does not currently expose a
// lifecycle_state IN (...) predicate; a future wave that adds it should
// push this filter into SQL so cross-tenant + cross-lifecycle reads are
// blocked at the database layer (defence-in-depth).
//
// TODO(QDRANT-004 follow-up): push allowStates into asset.Filter once
// ClipsRepository.List supports lifecycle_state IN (...) so the SQL layer
// enforces the lifecycle boundary alongside the existing workspace_id
// filter (defence-in-depth at the database; the current implementation
// only guarantees the boundary at the memory layer after List returns).
//
// Empty LifecycleState rows are let through regardless of allowStates:
// the service's post-query guard `isLifecycleSearchable` treats empty
// state as let-through (SQL adapter has already filtered, or column is
// unwritten). Honour the same contract here so the two layers agree.
func (a *mediasearchReadAdapter) GetMany(ctx context.Context, workspace mediasearch.WorkspaceContext, assetIDs []string, allowStates []string) ([]mediasearch.MediaAsset, error) {
	if a.clips == nil || len(assetIDs) == 0 {
		return nil, nil
	}
	// Normalise the allowlist to lower-case once so the per-row filter
	// below doesn't need to repeat the ToLower conversion (and can't
	// drift on case). Empty entries are dropped. Empty allowStates →
	// no per-row lifecycle filter beyond the existing soft-delete
	// exclusion (preserves pre-PR3 behaviour for callers that pass
	// nil/empty).
	allowSet := make(map[string]struct{}, len(allowStates))
	for _, s := range allowStates {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			allowSet[s] = struct{}{}
		}
	}

	// Batch query via ClipsRepository.List with filter.IDs — single
	// SQL statement with WHERE id IN (?, ?, ...).
	// QDRANT-001 (June 2026) closure: the workspace_id predicate is
	// pushed down into SQL via asset.Filter so cross-tenant reads
	// are blocked at the database layer (defence-in-depth: the auth
	// middleware retains the auth-context check; SQL is the last
	// line of defence). workspace.WorkspaceID empty == no tenant
	// filter (legacy callers; the service-layer Search already
	// rejects empty/`default` workspaces before this is called).
	clips, err := a.clips.List(ctx, asset.Filter{
		IDs:         assetIDs,
		WorkspaceID: workspace.WorkspaceID,
		IsAdmin:     workspace.IsAdmin,
	})
	if err != nil {
		return nil, err
	}
	out := make([]mediasearch.MediaAsset, 0, len(clips))
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		// Exclude soft-deleted rows per MediaReadRepository contract.
		ls := strings.ToLower(string(clip.LifecycleState))
		if ls == "deleted" {
			continue
		}
		// QDRANT-004 PR3 lifecycle allowlist: when the service passes
		// a non-empty allowStates list AND the row's LifecycleState
		// is non-empty, drop any row whose state is NOT in the
		// allowlist. Empty LifecycleState is let through (matches
		// the post-query guard `isLifecycleSearchable` contract).
		// When allowStates is empty, the soft-delete exclusion above
		// is the only lifecycle gate (preserves pre-PR3 behaviour).
		if clip.LifecycleState != "" && len(allowSet) > 0 {
			if _, ok := allowSet[ls]; !ok {
				continue
			}
		}
		lang, _ := clip.Metadata["language"].(string)
		width, _ := clip.Metadata["width"].(float64)
		height, _ := clip.Metadata["height"].(float64)
		out = append(out, mediasearch.MediaAsset{
			ID:         clip.ID,
			Name:       clip.Name,
			Source:     string(clip.Source),
			MediaType:  string(clip.MediaType),
			Category:   clip.Category,
			Tags:       clip.Tags,
			Language:   lang,
			DurationMs: int(clip.Duration.Milliseconds()),
			Width:      int(width),
			Height:     int(height),
			SearchText: clip.SearchText,
		})
	}
	return out, nil
}

// mediasearchDeliveryAdapter wraps the result of a missing HMAC
// signer. QDRANT-004 regression: the previous implementation was a
// silent noop ("", nil) that the service dropped without surfacing
// the missing-config cause. The new implementation returns an
// explicit error so the failure mode shows up in logs and dashboards.
//
// QDRANT-004 acceptance criterion "Delivery URL protetto": any
// production deployment with mediasearch.Enabled=true MUST have a
// configured delivery signer; the configuration loader enforces
// this at startup via the cfg.Validate() HMAC-secret check. This
// adapter is the last line of defense in case the validator is
// bypassed (e.g. future VELOX_ALLOW_INSECURE_DEV escape leak).
type mediasearchDeliveryAdapter struct{}

var errDeliveryNotConfigured = errors.New(
	"mediasearch: delivery URL signing not configured " +
		"(set security.delivery_hmac_secret with >=32 bytes; " +
		"VELOX_ALLOW_INSECURE_DEV=true is a dev-only escape)",
)

func (a *mediasearchDeliveryAdapter) BuildAuthorizedURL(_ context.Context, _ mediasearch.WorkspaceContext, _ string) (string, error) {
	return "", errDeliveryNotConfigured
}

// mediasearchLogger adapts zap.Logger to mediasearch.Logger.
type mediasearchLogger struct {
	sugar *zap.SugaredLogger
}

func (l mediasearchLogger) Info(msg string, kv ...any)  { l.sugar.Infow(msg, kv...) }
func (l mediasearchLogger) Warn(msg string, kv ...any)  { l.sugar.Warnw(msg, kv...) }
func (l mediasearchLogger) Debug(msg string, kv ...any) { l.sugar.Debugw(msg, kv...) }

// ── QDRANT-004 delivery signer helper ───────────────────────────────────────

// buildMediasearchDeliverySvc returns a delivery.Signer when the HMAC secret
// is configured (≥32 chars). Returns an error when the secret is missing or
// too short — QDRANT-004 closed: the server must fail startup rather than
// silently accept a no-op delivery (which would return results without
// authorized download URLs).
func buildMediasearchDeliverySvc(cfgDeliveryHMACSecret, veloxBaseURL string, deliveryReplayWindowSec int, log *zap.Logger) (mediasearch.AssetDeliveryService, error) {
	secret := strings.TrimSpace(cfgDeliveryHMACSecret)
	if len(secret) < 32 {
		return nil, fmt.Errorf("QDRANT-004: delivery_hmac_secret must be ≥32 bytes (got %d); set in config.yaml or VELOX_DELIVERY_HMAC_SECRET env var. Mediasearch requires signed delivery URLs.", len(secret))
	}
	deliveryBaseURL := strings.TrimSpace(veloxBaseURL)
	replayWindow := time.Duration(deliveryReplayWindowSec) * time.Second
	if replayWindow <= 0 {
		replayWindow = 5 * time.Minute
	}
	signer, err := delivery.NewSigner(
		[]byte(secret),
		replayWindow,
		deliveryBaseURL,
		"/internal/v1/deliver",
	)
	if err != nil {
		return nil, fmt.Errorf("QDRANT-004: delivery signer init failed: %w", err)
	}
	log.Info("QDRANT-004: delivery signer wired with HMAC secret")
	return signer, nil
}

// ── Wave 22 task 1 of 5: AssetMutationDispatcher adapter ────────────────────
//
// mutationsDispatcherAdapter wraps the canonical *outbox.Dispatcher so the
// application layer can depend on the SSOT mutations.AssetMutationDispatcher
// surface (rather than the 9+ ad-hoc narrow dispatcher ports the codebase has
// accumulated over time). The adapter is a thin delegation layer — no
// business logic, no SQL, no outbox_event payload synthesis. The dispatcher
// does all of that.
//
// Compile-time assertion placement rationale (Pattern 0):
//
//   The dispatcher (in internal/infrastructure/...) SHOULD NOT import
//   mutations (in internal/application/...) directly — that would violate
//   the canonical application ← infrastructure layering. Instead, the
//   assertion lives HERE in the composition root, which is the ONE place
//   allowed to import both sides. Drift in either the interface or the
//   dispatcher's tx-bound implementation surfaces as a build error in
//   the composition root, not as a runtime panic.
//
// Signature-drift detection: break any of the 3 methods' signatures
// (e.g. change `contentHash string` to `contentHash int`) and observe
// the build fail at this compile-time assertion. That is the
// verification gate the user spec called out.
type mutationsDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// var _ mutations.AssetMutationDispatcher = (*mutationsDispatcherAdapter)(nil)
//
// is the canonical compile-time assertion. Per AGENTS.md Pattern 0 / 8
// (compile-time over runtime checks), the assertion lives at the adapter
// home rather than the dispatcher home to preserve layering.
var _ mutations.AssetMutationDispatcher = (*mutationsDispatcherAdapter)(nil)

// newMutationsDispatcherAdapter wraps a non-nil dispatcher. Returns
// an explicit error when disp is nil so the wiring at the composition
// root surfaces misconfiguration loudly (mirror of artlist's typed-
// sentinel fail-closed convention).
func newMutationsDispatcherAdapter(disp *outbox.Dispatcher) (mutations.AssetMutationDispatcher, error) {
	if disp == nil {
		return nil, fmt.Errorf("app.newMutationsDispatcherAdapter: outbox.Dispatcher is required (QDRANT-asset-mutation isolation, Wave 22 task 1; composition root must wire the canonical *outbox.Dispatcher)")
	}
	return &mutationsDispatcherAdapter{disp: disp}, nil
}

// EnqueueAndIndex delegates to the dispatcher's tx-bound UPSERT +
// outbox enqueue. No per-call mutation (the interface is the SSOT;
// the dispatcher owns the SQL + envelope + state stamp).
func (a *mutationsDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

// EnqueueAndRestore delegates to the dispatcher's tx-bound state
// stamp + outbox enqueue (event_type='asset.index.restore_requested').
// Handler lands in task 3 of 5.
func (a *mutationsDispatcherAdapter) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndRestore(ctx, assetID)
}

// EnqueueAndDelete delegates to the dispatcher's tx-bound DELETE_PENDING
// state stamp + outbox enqueue (event_type='asset.index.delete_requested').
// Handler already exists from QDRANT-002 PR7.
func (a *mutationsDispatcherAdapter) EnqueueAndDelete(ctx context.Context, assetID string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndDelete(ctx, assetID)
}

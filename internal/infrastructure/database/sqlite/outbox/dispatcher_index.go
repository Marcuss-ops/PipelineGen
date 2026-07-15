package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// EnqueueAndIndex performs UPSERT media_assets + INSERT of the canonical
// index-request event in a single atomic transaction,
// then commits. After commit, the outboxevents Pool will see the new
// pending event and run IndexClip on it asynchronously via the
// IndexingHandler.
//
// Callers MUST NOT subsequently run SafeGoFunc(IndexClip(...)) — the
// outbox event IS the indexing trigger.
//
// contentHash should be the canonical content fingerprint. Used to build
// the event_key for deduplication, so duplicate ingestions are safe.
//
// Folders (clip.IsFolder == true) MUST be filtered by the caller before
// calling — vector indexing of folders is meaningless. The folder branch
// is checked BEFORE the contentHash guard because folders have no
// FileHash by design (godlike/07 no-fake-availability: a folder does
// not have a content fingerprint, and we MUST NOT reject folder upserts
// with "contentHash is required" — that would be a fail-loud error for
// a valid input shape).
func (d *Dispatcher) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.clips == nil {
		return errors.New("outbox.Dispatcher: clips repo not configured")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}
	// Folders are not vector-indexable. This branch MUST come before
	// the contentHash guard (folders do not have a file_hash by
	// design — rejecting them on contentHash="" would be a fail-loud
	// error for a valid input shape). The folder path writes ONLY to
	// media_assets and explicitly skips the outbox INSERT (per
	// godlike/07 no-fake-availability: we MUST NOT produce indexing
	// events for assets that the indexer cannot process).
	if clip.IsFolder() {
		return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
			if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
				return fmt.Errorf("dispatcher upsert folder %s: %w", clip.ID, err)
			}
			if d.log != nil {
				d.log.Debug("dispatcher skipped outbox enqueue for folder",
					zap.String("asset_id", clip.ID),
				)
			}
			return nil
		})
	}
	// Non-folder clips: contentHash is REQUIRED for the v1 envelope's
	// event_key (supersede gate cannot function without a content
	// fingerprint). This guard intentionally lives AFTER the IsFolder
	// branch so folder upserts are not rejected with a contentHash error.
	if contentHash == "" {
		return fmt.Errorf("outbox.Dispatcher.EnqueueAndIndex: contentHash is required for non-folder clip %s (supersede gate cannot function without a content fingerprint — callers must set file_hash before dispatching)", clip.ID)
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}

		eventID := uuid.NewString()
		// Fase 5 / Commit 2 (July 2026) — use the canonical OutboxKey
		// (eventType:provider:clipID:sourceVersion) for the outbox
		// event_key. The previous indexEventKey helper produced a
		// 5-segment shape that included infra-level fields
		// (model/version/collection). The 4-segment OutboxKey is
		// infrastructure-independent: if the embedding model changes,
		// the event_key stays the same, so the outbox UNIQUE INDEX
		// dedup continues to work across model upgrades. The wire-shape
		// break is safe because outbox events are ephemeral (processed
		// then deleted); old events with the 5-segment shape will
		// process and vanish naturally.
		eventKey, ekErr := idempotency.OutboxKey(
			outboxevents.EventAssetIndexRequested,
			string(clip.Source),
			clip.ID,
			contentHash,
		)
		if ekErr != nil {
			return fmt.Errorf("dispatcher.EnqueueAndIndex(%q): build outbox event_key: %w", clip.ID, ekErr)
		}
		payload := indexRequestV1{
			SchemaVersion:      outboxevents.ReindexEnvelopeV1Schema,
			EventID:            eventID,
			AssetID:            clip.ID,
			Operation:          "UPSERT",
			SourceVersion:      contentHash,
			TargetIndexVersion: clipindexer.CollectionVersion(),
			RequestedVectors:   []string{"text", "transcript"},
			RequestedAt:        timeutil.FormatRFC3339(time.Now()),
			EmbeddingModel:     clipindexer.EmbeddingModel(),
			EmbeddingVersion:   clipindexer.EmbeddingModelVersion(),
			IdempotencyKey:     eventKey,
		}
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 index payload %s: %w", clip.ID, err)
		}

		if _, err := d.outboxEventsRepo.Enqueue(

			ctx, tx,
			outboxevents.EventAssetIndexRequested,
			clip.ID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox event %s: %w", clip.ID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events indexing (v1 envelope)",
				zap.String("asset_id", clip.ID),
				zap.String("outbox_event_id", eventID),
				zap.String("source", string(clip.Source)),
				zap.String("source_version", contentHash),
				zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
			)
		}
		return nil
	})
}

// SaveDiscoveredAsset is the discovery-only upsert path. It writes the
// clip row into media_assets with the supplied lifecycle_state + index_state
// (canonical signature from artlist: StateStaging + StateDiscovered) but
// does NOT enqueue any outbox event. The canonical asset.index.requested
// event arrives later, AFTER the artlist.run processing post-processing
// finalizer produces a fully-populated clip (real hash, Drive file id,
// upload completed). See artlist.Dispatcher.SaveDiscoveredAsset for the
// rationale.
//
// Behavioural invariants:
//  1. Atomic single-tx UPSERT — commits or returns the wrapped error;
//     no partial-row state is observable on failure.
//  2. NO outbox_events row is written — explicit invariant for the
//     "discovery does not prematurely index" property. Tests assert this
//     via the integration_test.go fixture (no row appears in
//     outbox_events after a SaveDiscoveredAsset call).
//  3. lifecycle_state + index_state are stamped on the clip BEFORE the
//     UpsertClipTx call so the dispatcher ships a coherent row to the
//     infra writer; callers do not need to pre-set them.
//  4. Empty clip.ID is rejected up-front, mirroring EnqueueAndIndex's
//     pre-tx guard so failures never reach the SQL layer.
//
// Future evolvability: a future "Re-Save Discovered" path (e.g. for
// metadata reconciliation) can extend this method to optionally emit a
// metadata_export.requested outbox event WITHOUT emitting
// asset.index.requested — keeping the discovery/processing split
// intact. For now the bare-Unbox path is the only thing SearchLiveAndSave
// needs.
func (d *Dispatcher) SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.clips == nil {
		return errors.New("outbox.Dispatcher: clips repo not configured")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}
	if !lifecycle.Valid() {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): lifecycle_state %q is not canonical (Valid()=false)", clip.ID, lifecycle)
	}
	if !idx.Valid() {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): index_state %q is not canonical (Valid()=false)", clip.ID, idx)
	}
	// Stamp lifecycle_state + index_state directly on the clip so
	// UpsertClipTx persists them in the same UPSERT. The two typed setters
	// above guarantee Valid(); further invariants (LifecycleState column
	// NOT NULL, index_state stored in metadata_json column) are enforced
	// by clips_transactions.go::UpsertClipTx — no additional wiring here.
	clip.LifecycleState = lifecycle
	clip.SetMetadataString("index_state", string(idx))

	// Fase 5 / Commit 2 (July 2026) — stamp the canonical JobKey into
	// metadata_json. The 3-tuple (provider, clipID, sourceVersion) is the
	// canonical idempotency key for the job/outbox surface (per user spec
	// literal "provider+clip_id+source_version per i job/outbox"). At
	// discovery time we don't have a real source_version (the file isn't
	// downloaded yet), so we use the literal sentinel "discovered" as
	// the source_version placeholder. The key is re-stamped with the
	// real contentHash at EnqueueAndIndex time (the outbox event_key
	// itself carries the canonical OutboxKey). The sentinel is
	// greppable ("discovered" appears in metadata_json.job_key on every
	// freshly-discovered row) and is impossible to confuse with a real
	// SHA-256 hash (which is always 64 hex chars or "sha256:<hex>").
	//
	// godlike/07 fail-closed: an empty source_version is rejected by
	// the canonical JobKey constructor with ErrEmptySourceVersion. The
	// sentinel "discovered" sidesteps that without leaking the empty
	// sentinel to the SQL layer.
	jobKey, jkErr := idempotency.JobKey(string(clip.Source), clip.ID, "discovered")
	if jkErr != nil {
		return fmt.Errorf("dispatcher.SaveDiscoveredAsset(%q): stamp job_key: %w", clip.ID, jkErr)
	}
	clip.SetMetadataString("job_key", jobKey)

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}
		if d.log != nil {
			d.log.Debug("dispatcher saved discovered asset (no outbox event)",
				zap.String("asset_id", clip.ID),
				zap.String("lifecycle_state", string(lifecycle)),
				zap.String("index_state", string(idx)),
			)
		}
		return nil
	})
}

// EnqueueIndexEvent emits the canonical asset.index.requested.v1
// envelope INSIDE a caller-owned *sql.Tx. PR-VO-A3 (Outbox-based
// Qdrant indexing, June 2026): this method is the Voiceover path's
// narrow entry point.
func (d *Dispatcher) EnqueueIndexEvent(ctx context.Context, tx *sql.Tx, assetID, contentHash string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("outbox.Dispatcher.EnqueueIndexEvent: assetID is required")
	}
	if contentHash == "" {
		return errors.New("outbox.Dispatcher.EnqueueIndexEvent: contentHash is required (supersede gate cannot function without a content fingerprint)")
	}

	eventID := uuid.NewString()
	// Fase 5 / Commit 2 (July 2026) — use the canonical OutboxKey for
	// the event_key. See EnqueueAndIndex for the rationale (4-segment
	// infrastructure-independent shape replaces the 5-segment
	// indexEventKey helper).
	//
	// EnqueueIndexEvent doesn't have a *asset.Asset in scope (the
	// caller supplies just the assetID + contentHash), so we infer the
	// provider from the assetID prefix via the domain-layer
	// DetectSourceFromAssetID helper. This keeps the wire-in minimal:
	// the canonical constructor is still the single source of truth for
	// the key shape, and the provider-inference helper is the
	// domain-level mirror of clipindexer.sourceFromClipID (see
	// internal/domain/asset/clip_identity.go for the divergence
	// documentation).
	provider := asset.DetectSourceFromAssetID(assetID)
	eventKey, ekErr := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested,
		provider,
		assetID,
		contentHash,
	)
	if ekErr != nil {
		return fmt.Errorf("dispatcher.EnqueueIndexEvent(%q): build outbox event_key: %w", assetID, ekErr)
	}
	payload := buildIndexRequestV1(eventID, assetID, contentHash, eventKey)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dispatcher marshal v1 index payload %s: %w", assetID, err)
	}

	if _, err := d.outboxEventsRepo.Enqueue(

		ctx, tx,
		outboxevents.EventAssetIndexRequested,
		assetID,
		"media_asset",
		string(payloadJSON),
		eventKey,
	); err != nil {
		return fmt.Errorf("dispatcher enqueue outbox event %s: %w", assetID, err)
	}

	if d.log != nil {
		d.log.Debug("dispatcher enqueued asset for outbox_events indexing via EnqueueIndexEvent (v1 envelope, caller-owned tx)",
			zap.String("asset_id", assetID),
			zap.String("outbox_event_id", eventID),
			zap.String("source_version", contentHash),
			zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
		)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────

// buildIndexRequestV1 is the canonical v1 envelope builder shared
// between EnqueueAndIndex and EnqueueIndexEvent.
func buildIndexRequestV1(eventID, assetID, contentHash, eventKey string) indexRequestV1 {
	return indexRequestV1{
		SchemaVersion:      outboxevents.ReindexEnvelopeV1Schema,
		EventID:            eventID,
		AssetID:            assetID,
		Operation:          "UPSERT",
		SourceVersion:      contentHash,
		TargetIndexVersion: clipindexer.CollectionVersion(),
		RequestedVectors:   []string{"text", "transcript"},
		RequestedAt:        timeutil.FormatRFC3339(time.Now()),
		EmbeddingModel:     clipindexer.EmbeddingModel(),
		EmbeddingVersion:   clipindexer.EmbeddingModelVersion(),
		IdempotencyKey:     eventKey,
	}
}

// shortHashPrefix returns a short log-friendly prefix; the empty
// string yields "" so log readers do not see a misleading
// "(empty)" marker.
func shortHashPrefix(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// Package finalizer — asset_finalizer_tx.go: orchestrator for
// AssetTxFinalizer (canonical impl of finalization.AssetFinalizerTx).
//
// This file owns:
//
//   - struct AssetTxFinalizer (log + fanout fields)
//   - NewAssetTxFinalizer (constructor)
//   - WithFanOut (fluent setter for post-commit fan-out)
//   - FirePostCommitHooks (post-commit fan-out, NOT tx-bound)
//   - FinalizeAsset (the canonical orchestrator — caller-owned tx,
//     executes 5 helpers in order: media_assets → asset_versions →
//     asset_locations → asset_renditions (loop) → outbox_events)
//   - kindToMediaType (free helper, used by FinalizeAsset AND
//     asset_finalizer_asset.go::upsertMediaAsset — co-located here
//     because both helpers depend on the same enum-to-string map)
//
// The 5 SQL helpers live in sibling files (one per canonical table)
// for atomic-discipline readability:
//
//   - asset_finalizer_asset.go       → upsertMediaAsset (media_assets)
//   - asset_finalizer_versions.go    → insertAssetVersion (asset_versions)
//   - asset_finalizer_locations.go   → upsertAssetLocation (asset_locations, primary)
//   - asset_finalizer_renditions.go  → upsertRenditionLocation (asset_renditions + asset_locations per rendition)
//   - asset_finalizer_outbox.go      → insertOutboxEvent (outbox_events)
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): this file does NOT own BeginTx / Commit /
// Rollback. The CALLER (JobFinalizer at
// internal/application/jobs/finalizer/job_finalizer.go) opens the
// *sql.Tx, passes it via finalizer.WrapTx(tx), then calls Commit /
// Rollback after FinalizeAsset returns. AssetTxFinalizer participates
// in the same tx as a typed-narrow writer via finalization.Transaction
// (ExecContext + QueryRowContext only — Commit/Rollback are NOT
// exposed on the interface by design).
//
// MAPPING NOTE: the per-prompt spec mentions "tracks" (presumed
// from the clip_atomic_writer sister pattern), but this finalizer
// does NOT write to any text tracks table — the actual canonical
// tables are media_assets / asset_versions / asset_locations /
// asset_renditions / outbox_events. The "versions" file is the
// faithful mapping for the canonical DB schema; this preserves
// the atomic discipline while aligning with reality.
//
// Mechanical split from the pre-PR7 monolithic surface. Zero
// behavior change. The FinalizeAsset helper-call order
// (media_assets → asset_versions → asset_locations →
// asset_renditions (loop) → outbox_events) MUST stay byte-for-byte
// — a re-order would silently break invariants:
//
//  1. (asset, versions) — asset_versions has FK to media_assets;
//     out-of-order MAX+1 would surface UNIQUE collisions or
//     dangling FK rows under concurrent writers.
//  2. (locations, renditions) — renditions reference
//     asset_locations.id by FK + the timeseries primary key
//     resolution depends on the primary location row being
//     committed first.
//  3. (renditions, outbox) — the IndexingHandler Qdrant payload
//     reads asset_renditions metadata so the outbox event MUST
//     be the LAST DB write to surface a consistent fingerprint.
package finalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
)

// AssetTxFinalizer is the concrete implementation of
// finalization.AssetFinalizerTx.
//
// It writes the canonical media_assets, asset_versions, asset_locations,
// asset_renditions (optional, per published rendition), and outbox_events
// records inside the caller's transaction. It participates in the
// JobFinalizer's transaction via the finalization.Transaction narrow
// interface — it never opens its own transaction.
//
// For each PublishedArtifact, it produces:
//   - ArtifactRef (lightweight reference for downstream consumers)
//   - []OutboxEvent (indexing requests for Qdrant)
//
// Schema tables written (inside the caller's tx):
//
//	media_assets    — INSERT ... ON CONFLICT(id) DO UPDATE (canonical asset,
//	                   index_state INTENTIONALLY excluded from DO UPDATE
//	                   so the clipindexer's state-transition SURVIVES
//	                   re-finalization)
//	asset_versions  — INSERT with MAX(version_number)+1 (sequential version)
//	asset_locations — INSERT ... ON CONFLICT(asset_id, location_kind) DO UPDATE
//	                  (primary = 1 for the canonical location; renditions use
//	                  distinct location_kind per (provider, kind) tuple)
//	asset_renditions — INSERT ... ON CONFLICT(asset_id, kind) DO UPDATE
//	                   (per technical variant supplied by the caller)
//	outbox_events   — INSERT ... ON CONFLICT(event_key) WHERE event_key != ''
//	                   DO NOTHING (idempotent re-finalization)
//
// Canonical reference: Piano d'Azione Completo § 5.1–5.2.
type AssetTxFinalizer struct {
	log    *zap.Logger
	fanout *texttracks.MaterializeFanOut // optional nil-safe post-publish fan-out helper
}

// NewAssetTxFinalizer creates an AssetTxFinalizer.
//
// godlike/07 backward-compat: the constructor signature is
// unchanged from pre-Fase-4 (log only). The fanout helper is
// optionally attached via WithFanOut. Composition roots that
// need post-publish fan-out MUST call WithFanOut AFTER
// NewAssetTxFinalizer + AFTER the MaterializeFanOut is built
// (which requires the JobsBundle to be assembled first).
// This sequencing is the canonical SSOT order.
func NewAssetTxFinalizer(log *zap.Logger) *AssetTxFinalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetTxFinalizer{log: log}
}

// WithFanOut attaches the post-publish fan-out helper. Returns
// the receiver for fluent chaining at composition root. nil-safe
// (passing nil clears the fan-out hook but does not delete the
// finalizer itself; FirePostCommitHooks short-circuits to no-op
// when fanout is nil).
//
// godlike/06 SSOT: this is the SOLE canonical extension seam
// for adding fan-out to AssetTxFinalizer. Composition roots
// MUST NOT inline the fan-out call inside FinalizeAsset (the
// caller owns the tx; the fan-out must fire AFTER commit).
func (s *AssetTxFinalizer) WithFanOut(fanout *texttracks.MaterializeFanOut) *AssetTxFinalizer {
	if s == nil {
		return s
	}
	s.fanout = fanout
	return s
}

// FirePostCommitHooks is the canonical post-commit fan-out hook
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4, July 2026).
//
// Callers (the JobFinalizer + every AssetFinalizerTx caller —
// Stock, Artlist, Soundeffect, SlideWorker, future Video
// re-render) MUST invoke this AFTER tx.Commit returns nil.
// Inside the tx-bound context, the materialize job would be
// observable to workers BEFORE the asset row is durable — a
// TOCTOU race; firing post-commit guarantees the source row is
// visible to the materialize handler when the worker picks up
// the enqueued job.
//
// Activation gate (godlike/07 fail-closed):
//   - artifact.SourceTextHash == "" → no fan-out (pre-Fase-4
//     assets without source text skip silently).
//   - artifact.SourceLanguage == "" → no fan-out (no BCP-47).
//   - s.fanout == nil → no fan-out (disabled-mode wiring).
//   - Any non-nil error from EnqueueMaterializeOne is logged at
//     Warn level and swallowed — the canonical asset row +
//     asset.index.requested outbox event are already durable;
//     the materialize enqueue failure MUST NOT roll them back.
//     The broker has its own retry policy for the resulting
//     job; a future reconciliation pass can backstop missed
//     fan-outs.
func (s *AssetTxFinalizer) FirePostCommitHooks(
	ctx context.Context,
	artifact finalization.PublishedArtifact,
) {
	if s == nil {
		// Nil-receiver guard (mirrors WithFanOut). Required
		// because field access on a nil pointer receiver
		// panics in Go. Composition roots that build a nil
		// AssetTxFinalizer by mistake (e.g., a test seam
		// without Log wiring) MUST NOT crash the post-commit
		// caller path.
		return
	}
	if s.fanout == nil {
		// Disabled-mode wiring — no fan-out (godlike/07
		// NO-FAKE-AVAILABILITY: this is observable to
		// composition root configs that opt out of the
		// texttracks pipeline; operators see no asset.text.materialize
		// jobs enqueued for these assets).
		return
	}
	if artifact.SourceTextHash == "" || artifact.SourceLanguage == "" {
		// No source text available — this is the canonical
		// fan-out precondition. Pure-audio / pure-image
		// assets without a text source skip silently.
		return
	}
	// Fire the canonical post-publish enqueue. We use the
	// canonical kinds slice so fan-out covers transcript +
	// description + summary (the 3 textual kinds that benefit
	// most from translation; Title + Keywords are already
	// short, deterministic, and don't need translation).
	kinds := []asset.TextTrackKind{
		asset.TextTrackTranscript,
		asset.TextTrackDescription,
		asset.TextTrackSummary,
	}
	if err := s.fanout.EnqueueMaterializeOne(
		ctx,
		artifact.ArtifactID,
		artifact.SourceLanguage,
		artifact.SourceTextHash,
		kinds,
	); err != nil {
		// godlike/07 NO-FAKE-AVAILABILITY: log + swallow. The
		// canonical asset row + outbox event are already
		// committed; rolling back the tx would be wrong (the
		// tx is closed). The recovery path is operator-runs
		// `pipelinegen-admin text-tracks-backfill` which
		// discovers the just-published asset and fans out
		// translation fan-out for any target languages. We
		// deliberately do NOT escalate to FAILED — the caller
		// (StockFinalizeStep / Artlist stagePersistResults /
		// etc.) needs the tx-Commit-success verdict clean for
		// its own verdict-stamping path.
		if s.log != nil {
			s.log.Warn("AssetTxFinalizer.FirePostCommitHooks: fan-out enqueue failed (canonical asset row preserved; operator backfill will recover)",
				zap.String("artifact_id", artifact.ArtifactID),
				zap.String("source_language", artifact.SourceLanguage),
				zap.String("source_text_hash", artifact.SourceTextHash),
				zap.Error(err))
		}
		return
	}
	if s.log != nil {
		s.log.Info("AssetTxFinalizer.FirePostCommitHooks: asset.text.materialize enqueued",
			zap.String("artifact_id", artifact.ArtifactID),
			zap.String("source_language", artifact.SourceLanguage),
			zap.String("source_text_hash", artifact.SourceTextHash),
			zap.Int("kinds_count", len(kinds)),
		)
	}
}

// Compile-time assertion.
var _ finalization.AssetFinalizerTx = (*AssetTxFinalizer)(nil)

// FinalizeAsset writes the canonical asset, version, location,
// rendition and outbox records for a published artifact inside the
// caller's transaction.
//
// The caller (JobFinalizer) owns the transaction lifecycle —
// AssetTxFinalizer only executes SQL via the Transaction surface.
//
// Helper-call order (MUST-stay byte-for-byte, see package doc for
// the rationale):
//  1. upsertMediaAsset        (asset_finalizer_asset.go)
//  2. insertAssetVersion      (asset_finalizer_versions.go)
//  3. upsertAssetLocation     (asset_finalizer_locations.go, primary location)
//  4. upsertRenditionLocation (asset_finalizer_renditions.go, loop on
//     artifact.Renditions; per rendition writes both
//     asset_locations + asset_renditions)
//  5. insertOutboxEvent       (asset_finalizer_outbox.go)
func (s *AssetTxFinalizer) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	if artifact.ArtifactID == "" {
		return finalization.ArtifactRef{}, nil,
			fmt.Errorf("asset finalizer: ArtifactID is empty")
	}

	nowStr := timeutil.FormatRFC3339(time.Now())

	// 1. UPSERT media_assets — canonical asset row.
	if err := s.upsertMediaAsset(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 2. INSERT asset_versions — new version row.
	versionNum, err := s.insertAssetVersion(ctx, tx, &artifact, nowStr)
	if err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 3. UPSERT asset_locations — canonical location row (primary).
	if err := s.upsertAssetLocation(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 4. UPSERT rendition locations + asset_renditions for each
	// additional technical variant supplied by the caller.
	for i := range artifact.Renditions {
		if err := s.upsertRenditionLocation(ctx, tx, &artifact, &artifact.Renditions[i], nowStr); err != nil {
			return finalization.ArtifactRef{}, nil, err
		}
	}

	// 5. Build ArtifactRef and outbox events.
	ref := finalization.ArtifactRef{
		ArtifactID:    artifact.ArtifactID,
		AssetID:       artifact.ArtifactID, // AssetID = ArtifactID (logical identity)
		Kind:          artifact.Kind,
		SourceVersion: int64(versionNum),
		ContentHash:   artifact.SHA256,
		Location:      artifact.Location,
	}

	// Outbox event: index this asset in Qdrant.
	// Canonical v1 envelope matching the IndexingHandler contract
	// (schema_version, event_id, asset_id, source_version,
	// idempotency_key are REQUIRED by the handler).
	eventID := uuid.NewString()
	eventKey := fmt.Sprintf("index:%s:%s", artifact.ArtifactID, artifact.SHA256)
	// Compute source + media_type for the outbox payload, mirroring
	// the fallback logic used in upsertMediaAsset for the media_assets
	// row. The gate04 outbox test asserts these fields are populated
	// in the JSON envelope consumed by the dispatcher worker + Qdrant
	// indexer; without this fix the JSON silently omitted them and the
	// test failed with payload["source"]=nil, payload["media_type"]=nil.
	sourceStr := artifact.Source
	if sourceStr == "" {
		sourceStr = string(artifact.Location.Action)
	}
	mediaTypeStr := kindToMediaType(artifact.Kind)
	indexPayload, err := json.Marshal(map[string]any{
		"schema_version":  outboxevents.ReindexEnvelopeV1Schema,
		"event_id":        eventID,
		"asset_id":        artifact.ArtifactID,
		"operation":       "UPSERT",
		"source":          sourceStr,
		"media_type":      mediaTypeStr,
		"source_version":  artifact.SHA256,
		"idempotency_key": eventKey,
	})
	if err != nil {
		return finalization.ArtifactRef{}, nil,
			fmt.Errorf("asset finalizer: marshal index payload: %w", err)
	}
	events := []finalization.OutboxEvent{
		{
			EventType:   outboxevents.EventAssetIndexRequested,
			AggregateID: artifact.ArtifactID,
			EventKey:    eventKey,
			Payload:     json.RawMessage(indexPayload),
		},
	}

	// Persist the outbox event inside the same transaction so the
	// IndexingHandler can pick it up atomically after commit.
	if err := s.insertOutboxEvent(ctx, tx, events[0], nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	s.log.Debug("asset finalised in tx",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.Int("version", versionNum),
		zap.String("media_type", kindToMediaType(artifact.Kind)),
	)

	return ref, events, nil
}

// kindToMediaType maps a domain ArtifactKind to a media_type string
// suitable for the media_assets.media_type column.
//
// Kept in this file (orchestrator) rather than
// asset_finalizer_asset.go because FinalizeAsset ALSO needs the
// media_type for the outbox payload. Co-locating the helper with
// the orchestrator avoids a cross-file free-function reference —
// every helper file in this package already depends on the
// orchestrator via the *AssetTxFinalizer receiver, so a
// free-function in this file is the cheapest, most readable home.
func kindToMediaType(k finalization.ArtifactKind) string {
	switch k {
	case finalization.KindVideo:
		return "video"
	case finalization.KindImage:
		return "image"
	case finalization.KindAudio, finalization.KindVoiceover, finalization.KindSoundEffect:
		return "audio"
	case finalization.KindDocument:
		return "document"
	case finalization.KindScript:
		return "text"
	case finalization.KindMetadata:
		return "metadata"
	case finalization.KindArchive:
		return "archive"
	default:
		return "other"
	}
}

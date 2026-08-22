// Package outbox — index.go carries the consumer handler for
// `asset.index.requested` events (QDRANT-002 PR4, closes ticket items
// F "Worker outbox: supersede obsolete events", I "versioned event
// schema", and the metrics half of M).
//
// The handler mirrors IndexDeleteHandler's contract:
//
//   - IndexDeleteHandler  removes the asset → Qdrant delete +
//     media_assets soft-delete.
//   - IndexingHandler     upserts the asset  → media_assets row +
//     Qdrant upsert, gated by source_version equality.
//
// Idempotency contract:
//
//   - Qdrant's PUT /collections/{alias}/points is natively idempotent
//     when the point id is deterministic (uuid5(assetID)). IndexClip
//     re-reading the current asset on every worker pass is a no-op when
//     indexed_content_hash already equals current content_hash; the
//     supersede gate in this handler short-circuits even earlier (the
//     event is obsoleted by a newer aggregate version BEFORE embedding
//     work, so a stale Qdrant upsert never runs).
//
// Index-revision supersede policy:
//
//   - Read asset by id; missing → treat as success (a separate event
//     will arrive for the new aggregate or this one is already covered).
//   - If the asset's current canonical index_revision
//     (media_assets.metadata_json.$.index_revision, with legacy
//     content_hash fallback via SourceVersionFor) differs from the
//     event's index_revision (legacy source_version alias falls back at
//     parse time), return a *SupersedeError. The Pool routes the row to
//     MarkSuperseded (status='superseded') without burning max_attempts.
//     The newer event in the queue (or the already-indexed state)
//     provides the canonical current point. Byte identity (content_sha256)
//     is NEVER the comparison value (godlike/06).
//
// Schema versioning (QDRANT-002 item I):
//
//   - Strict v1 envelope — schema_version literal must match
//     IndexRequestSchemaVersion. Mismatch is TERMINAL via
//     outboxevents.NewTerminalError so producers upgrade instead of
//     retrying into a repair loop.
//   - Required fields: schema_version, event_id, asset_id, source_version.
//   - Optional: requested_vectors, requested_at, embedding_*,
//     idempotency_key, target_index_version, operation. Defaulted in
//     payload decoding so handlers can rely on the enriched names.
//
// PR-SPLIT-OUTBOX-INDEXING (Fase 3.4 of
// LONG-FILES-DECOMPOSITION-V2-2026-07-06, July 2026): the 504 LoC
// monolithic file was split into 3 sibling files per AGENTS.md
// Pattern 5 godlike/06 SSOT. The 3 files are in the same Go
// package (internal/application/jobs/outbox) so cross-file symbol
// resolution is via package-scope visibility.
//
//   - indexing.go (this file, slim, ~190 LoC): package goddoc +
//     imports + 2 schema constants + 2 interface declarations +
//     indexRequestV1 envelope struct + IndexingHandler struct +
//     NewIndexingHandler ctor + WithStateUpdater fluent setter +
//     EventType accessor + the closing PR-11 follow-up comment block
//     (the producer-side migration note).
//   - indexing_validate.go (~150 LoC, NEW): parseAndValidateRequest
//     function — the SOLE canonical entry point for v1 envelope
//     parse + strict envelope validation. Extracted from the
//     original Handle method's first 7 validation blocks
//     (parse + schema_version + event_id + asset_id + source_version +
//     idempotency_key + operation). Terminal classification is
//     preserved EXACTLY (the same outboxevents.NewTerminalError wrap,
//     the same Warn-level log lines, the same zap fields).
//   - indexing_handle.go (~230 LoC, NEW): Handle method — the main
//     entry point. Calls parseAndValidateRequest (sibling), then
//     performs the source_version supersede gate, then delegates
//     to IndexClip (with sentinel-driven retry-pending + CAS-miss
//     supersede classification). The deferred MediaIndexDuration
//     observation + outcome propagation is preserved EXACTLY.
//
// Lookup paths preserved: outbox.IndexingHandler /
// outbox.IndexRequestSchemaVersion / outbox.IndexRequestOperationUPSERT /
// outbox.parseAndValidateRequest all canonical.
package outbox

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// IndexRequestSchemaVersion is the canonical schema version the
// IndexingHandler accepts. Producers and consumers share the definition
// owned by outboxevents; a mismatch is TERMINAL.
// Mirrors the DeleteRequestSchemaVersion pattern in index_delete.go.
const IndexRequestSchemaVersion = outboxevents.ReindexEnvelopeV1Schema

// IndexRequestOperationUPSERT is the only operation value the v1
// envelope carries for asset.index.requested events. DELETE flows
// through the separate `asset.index.delete_requested.v1` event
// family — consumers of a future UPSERT-or-DELETE handler should
// switch on Operation, but today's IndexingHandler rejects anything
// ≠ "UPSERT" as terminal (a future enum extension enum point).
const IndexRequestOperationUPSERT = "UPSERT"

// IndexClipper is the minimum surface the IndexingHandler needs from
// the clipindexer.Service. Declared locally so the handler does not
// import clipindexer directly (avoids a circular dependency when the
// clipindexer package itself produces outbox events).
type IndexClipper interface {
	IndexClip(ctx context.Context, clipID string) error
}

// SourceVersionQuerier is the pre-flight idempotency surface needed
// for the source_version supersede gate (QDRANT-002 item F).
//
// PR 11 follow-up (June 2026): the previous AssetSourceChecker
// interface exposed `GetClip(ctx, id) (*asset.Asset, error)` so this
// handler could load the full aggregate and walk Go accessors over
// it. That pattern DRIFTED against the producer-side SQL helper in
// cmd/admin/reconcile_qdrant.go — the producer walked a 3-tier
// COALESCE chain (content_hash → metadata.file_hash → top-level
// file_hash column) while the consumer walked *asset.Asset
// accessor methods whose LegacyFileMD5() returns the SAME metadata slot
// as the JSON file_hash tier. To unify the two, the upstream port
// is replaced by SourceVersionQuerier which is a direct, narrow
// method that returns the same value the producer computes.
//
// Production concrete is *assets.ClipsRepository from
// internal/infrastructure/database/sqlite/assets, which delegates
// to assets.SourceVersionFor (the canonical SQL helper — same
// function cmd/admin/reconcile_qdrant.go imports). A single
// function owns the priority chain semantics so future drift is
// structurally impossible.
type SourceVersionQuerier interface {
	SourceVersionFor(ctx context.Context, id string) (string, error)
}

// indexRequestV1 is the canonical v1 envelope for
// asset.index.requested events.
//
// Required fields (handler fails-fast with TerminalError on
// missing-or-malformed):
//
//   - schema_version  (literal IndexRequestSchemaVersion)
//   - event_id        (RFC4122 UUID or producer-chosen opaque token)
//   - asset_id        (canonical media_assets.id)
//   - source_version  (ingest-time content_hash; the supersede gate
//     compares this against
//     media_assets.metadata_json.$.content_hash)
//
// Optional:
//
//   - operation           (default "UPSERT"; anything else → terminal)
//   - target_index_version (collection_version in old payload; logged
//     for audit, not used in supersede logic)
//   - requested_vectors   (default ["text", "transcript"]; passed
//     to IndexClip downstream)
//   - requested_at        (RFC3339 UTC; logged for audit only)
//   - embedding_model,
//     embedding_version   (legacy fields preserved so older
//     producers don't break)
//   - idempotency_key     (mirrors event_key for audit); empty is
//     terminal
//   - force               (Card 7.1, July 2026; bool, default false).
//     When true, the source_version supersede gate in Handle is
//     bypassed and IndexClip is invoked unconditionally. Producers
//     that want forced reindex must use the canonical
//     outboxevents.BuildReindexEnvelopeV1Force variant (which sets
//     this field to true AND appends the ":force" suffix to
//     event_key so the SQLite UNIQUE(event_key) dedup does not
//     collapse a force reindex with a prior non-force reindex).
//     Production ingest (asset_committer) and the normal reconciler
//     repair path (service_projection) MUST NOT set force — the
//     supersede dedup is part of their contract.
//
// Producers MUST NOT include embeddings, raw search vectors, or any
// payload that would make the event bloom to MBs. The handler
// reaches back into SQLite (asset_id lookup) to fetch current state.
type indexRequestV1 struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	AssetID       string `json:"asset_id"`
	Operation     string `json:"operation,omitempty"`
	SourceVersion string `json:"source_version"`
	// IndexRevision is the canonical supersede fingerprint — the
	// indexable-snapshot revision the supersede gate compares against the
	// current asset index_revision (via SourceVersionFor, which reads
	// metadata_json.$.index_revision first). Producers write both this and
	// the legacy SourceVersion (same value); legacy events without
	// index_revision fall back to source_version at parse time.
	IndexRevision      string   `json:"index_revision,omitempty"`
	TargetIndexVersion string   `json:"target_index_version,omitempty"`
	RequestedVectors   []string `json:"requested_vectors,omitempty"`
	RequestedAt        string   `json:"requested_at,omitempty"`
	EmbeddingModel     string   `json:"embedding_model,omitempty"`
	EmbeddingVersion   string   `json:"embedding_version,omitempty"`
	IdempotencyKey     string   `json:"idempotency_key"`
	// Force (Card 7.1, July 2026): when true, the source_version
	// supersede gate is bypassed. See package doc above for the
	// producer-side contract + the BuildReindexEnvelopeV1Force
	// variant.
	Force bool `json:"force,omitempty"`
}

// IndexingHandler is the canonical handler for asset.index.requested.v1.
//
// indexer is required for production wiring (BuildOutboxBundle
// populates it from *clipindexer.Service). sourceQuerier is required
// for the supersede gate; nil is allowed in tests that exercise only
// the parse-time / schema-validation branch. stateUpdater is OPTIONAL
// (PR-QDRANT-INDEXCLIP-GUARD, July 2026) — nil is the test-only
// "ignore IndexerStateUpdater" path; production wires via the
// fluent WithStateUpdater setter (no caller of NewIndexingHandler is
// broken). When stateUpdater is nil, the handler logs the
// sentinel-driven state-update FAILURE at Warn level and still
// returns the retryable error so the outbox event is NOT silently
// lost (godlike/07 fail-closed).
//
// All three ports nil-safe: the handler guards each call site with
// a nil-check so partial wiring degrades to "skip the gate" rather
// than crashing — but production callers MUST wire all three.
type IndexingHandler struct {
	indexer       IndexClipper
	sourceQuerier SourceVersionQuerier
	stateUpdater  clipindexer.IndexerStateUpdater
	log           *zap.Logger
}

// NewIndexingHandler wires the producer-side dependencies. log nil →
// nop logger. Both indexer and sourceQuerier may be nil in tests; the
// real wire path (BuildOutboxBundle) passes non-nil for both.
//
// stateUpdater is wired separately via WithStateUpdater (fluent
// setter — preserves the existing 3-arg constructor signature per
// godlike/07 minimum-blast-radius; new PRs SHOULD prefer the
// setter for new optional dependencies instead of growing the
// constructor parameter list).
func NewIndexingHandler(indexer IndexClipper, sourceQuerier SourceVersionQuerier, log *zap.Logger) *IndexingHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &IndexingHandler{
		indexer:       indexer,
		sourceQuerier: sourceQuerier,
		log:           log.Named("index"),
	}
}

// WithStateUpdater attaches an IndexerStateUpdater so the handler can
// stamp transient retry-pending states onto media_assets.index_state
// without coupling to internal SQLite helpers.
//
// PR-QDRANT-INDEXCLIP-GUARD (July 2026): required so the
// ErrIndexClipDisabledButEventRequested path records
// INDEXING_SKIPPED_NO_INDEXER on media_assets surface (observable
// on dashboards). The production wire path (BuildOutboxBundle) MUST
// pass the SAME *clipindexer.Service concrete that powers the
// IndexClipper port — godlike/06 SSOT (single concrete for both
// ports; no second adapter layer).
//
// godlike/07 minimum-blast-radius: returns the receiver to preserve
// the fluent-construction idiom. Nil updater is permitted (test
// path); production wires non-nil.
func (h *IndexingHandler) WithStateUpdater(u clipindexer.IndexerStateUpdater) *IndexingHandler {
	h.stateUpdater = u
	return h
}

// EventType returns the canonical outboxevents constant.
func (h *IndexingHandler) EventType() string {
	return outboxevents.EventAssetIndexRequested
}

// IdempotencyKey declares the canonical handler-level idempotency
// identity for asset.index.requested events. Static — derived from
// the schema_version literal — so the HandlerRegistry.Register
// fail-closed panic can fire at init time if a future refactor
// forgets the declaration.
//
// godlike/06 SSOT: this string shape is the canonical handler-class
// identity surfaced via outbox.IdempotencyKey; per-event idempotency
// keys live in the envelope's IdempotencyKey field (parsed at
// Handle-time) and DO NOT replace this static declaration.
func (h *IndexingHandler) IdempotencyKey() string {
	return outboxevents.EventAssetIndexRequested + "." + IndexRequestSchemaVersion
}

// Historical note: the legacy readSourceVersion helper was removed in
// PR 11 (June 2026). Both producer and consumer route through
// assets.SourceVersionFor (internal/infrastructure/database/sqlite/assets/source_version.go).

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
// Source-version supersede policy:
//
//   - Read asset by id; missing → treat as success (a separate event
//     will arrive for the new aggregate or this one is already covered).
//   - If asset.metadata_json.$.content_hash differs from the event
//     payload's source_version, return a *SupersedeError. The Pool
//     routes the row to MarkSuperseded (status='superseded') without
//     burning max_attempts. The newer event in the queue (or the
//     already-indexed state) provides the canonical current point.
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
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// IndexRequestSchemaVersion is the canonical, EXACT string the
// IndexingHandler accepts. Producers MUST send
// "asset.index.requested.v1" literally. Mismatch is TERMINAL.
// Mirrors the DeleteRequestSchemaVersion pattern in index_delete.go.
const IndexRequestSchemaVersion = "asset.index.requested.v1"

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

// AssetSourceChecker is the pre-flight idempotency surface needed
// for the source_version supersede gate (QDRANT-002 item F). Same
// shape as AssetDeleter.GetClip in index_delete.go — production
// concrete is *assets.ClipsRepository from
// internal/infrastructure/database/sqlite/assets, which already
// satisfies both IndexingHandler + IndexDeleteHandler contracts.
//
// We expose a focused one-method interface here (rather than reusing
// AssetDeleter) so the semantics are explicit at every call site:
// "I need to read the current aggregate state to compare source
// versions" vs. "I need to soft-delete the aggregate". Tests pass a
// stub that returns a pre-built *asset.Asset.
type AssetSourceChecker interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
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
//
// Producers MUST NOT include embeddings, raw search vectors, or any
// payload that would make the event bloom to MBs. The handler
// reaches back into SQLite (asset_id lookup) to fetch current state.
type indexRequestV1 struct {
	SchemaVersion      string   `json:"schema_version"`
	EventID            string   `json:"event_id"`
	AssetID            string   `json:"asset_id"`
	Operation          string   `json:"operation,omitempty"`
	SourceVersion      string   `json:"source_version"`
	TargetIndexVersion string   `json:"target_index_version,omitempty"`
	RequestedVectors   []string `json:"requested_vectors,omitempty"`
	RequestedAt        string   `json:"requested_at,omitempty"`
	EmbeddingModel     string   `json:"embedding_model,omitempty"`
	EmbeddingVersion   string   `json:"embedding_version,omitempty"`
	IdempotencyKey     string   `json:"idempotency_key"`
}

// IndexingHandler is the canonical handler for asset.index.requested.v1.
//
// indexer is required for production wiring (BuildOutboxBundle
// populates it from *clipindexer.Service). sourceChecker is required
// for the supersede gate; nil is allowed in tests that exercise only
// the parse-time / schema-validation branch.
//
// Both ports nil-safe: the handler guards each call site with a
// nil-check so partial wiring degrades to "skip the gate" rather
// than crashing — but production callers MUST wire both.
type IndexingHandler struct {
	indexer       IndexClipper
	sourceChecker AssetSourceChecker
	log           *zap.Logger
}

// NewIndexingHandler wires the producer-side dependencies. log nil →
// nop logger. Both indexer and sourceChecker may be nil in tests; the
// real wire path (BuildOutboxBundle) passes non-nil for both.
func NewIndexingHandler(indexer IndexClipper, sourceChecker AssetSourceChecker, log *zap.Logger) *IndexingHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &IndexingHandler{
		indexer:       indexer,
		sourceChecker: sourceChecker,
		log:           log.Named("index"),
	}
}

// EventType returns the canonical outboxevents constant.
func (h *IndexingHandler) EventType() string {
	return outboxevents.EventAssetIndexRequested
}

// Handle parses the v1 envelope, performs the source_version
// supersede gate, and delegates to IndexClip. Validation failures
// and unsatisfiable payloads return typed terminal errors (PR1's
// outboxevents.NewTerminalError) so the pool's IsTerminal classifier
// dead-letters them immediately rather than burning max_attempts in
// a repair loop. Transient IndexClip failures return non-terminal
// errors so the pool retries per its exponential backoff. A
// source_version mismatch returns *SupersedeError so the pool's
// IsSupersede classifier routes the row to MarkSuperseded.
//
// Outcome label propagation: a closure-local variable `outcome` is
// reassigned in each branch (default "parse_err"). The deferred
// MediaIndexDuration observation reads it once at exit. This is the
// simplest pattern that survives early returns and panic without
// context-value indirection. The function does NOT use named returns —
// every branch returns its error explicitly — so future edits adding
// a branch cannot accidentally overwrite an earlier `err =` assignment
// via a bare `return` (a maintenance landmine named returns introduce
// when mixed with bare returns).
func (h *IndexingHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	start := time.Now()
	metrics.MediaIndexAttemptsTotal.WithLabelValues(evt.EventType).Inc()
	outcome := "parse_err"
	defer func() {
		metrics.MediaIndexDuration.WithLabelValues(evt.EventType, outcome).Observe(time.Since(start).Seconds())
	}()

	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	var p indexRequestV1
	if jerr := json.Unmarshal([]byte(evt.PayloadJSON), &p); jerr != nil {
		log.Warn("asset.index.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(jerr),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested payload parse: %w", jerr),
		)
	}

	// Strict v1 envelope validation. Each missing/mismatched field is
	// TERMINAL — retrying won't bring the field into existence.
	if p.SchemaVersion != IndexRequestSchemaVersion {
		log.Warn("asset.index.requested schema_version mismatch (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("got_version", p.SchemaVersion),
			zap.String("want_version", IndexRequestSchemaVersion),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(fmt.Errorf(
			"asset.index.requested: schema_version mismatch (terminal — got %q, want %q)",
			p.SchemaVersion, IndexRequestSchemaVersion,
		))
	}
	if p.EventID == "" {
		log.Warn("asset.index.requested: missing event_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: event_id is required (terminal)"),
		)
	}
	if p.AssetID == "" {
		log.Warn("asset.index.requested: empty asset_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: empty asset_id (terminal — retry cannot conjure an id)"),
		)
	}
	if p.SourceVersion == "" {
		// Empty source_version is the canonical supersede amibiguity
		// signal — we cannot verify the event is current, so retrying
		// won't fix it. Producers MUST send the ingest-time content
		// hash. Terminal so producers upgrade.
		log.Warn("asset.index.requested: missing source_version (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: source_version is required for the supersede gate (terminal — retry cannot conjure a fingerprint)"),
		)
	}
	if p.IdempotencyKey == "" {
		log.Warn("asset.index.requested: missing idempotency_key (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: idempotency_key is required (terminal)"),
		)
	}
	if p.Operation != "" && p.Operation != IndexRequestOperationUPSERT {
		log.Warn("asset.index.requested: unsupported operation (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("operation", p.Operation),
		)
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: unsupported operation %q (terminal — only %q is supported in v1)", p.Operation, IndexRequestOperationUPSERT),
		)
	}

	reqLog := []zap.Field{
		zap.String("asset_id", p.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", p.EventID),
		zap.String("source_version", p.SourceVersion),
		zap.Int("attempt", evt.AttemptCount),
	}
	if p.RequestedAt != "" {
		reqLog = append(reqLog, zap.String("requested_at", p.RequestedAt))
	}
	if p.Operation != "" {
		reqLog = append(reqLog, zap.String("operation", p.Operation))
	}

	// Source-version supersede gate (QDRANT-002 item F — "Se l'evento
	// è obsoleto, marcarlo SUPERSEDED senza indicizzare dati vecchi").
	// Read the current asset via sourceChecker; compare its source
	// fingerprint against the event's source_version. If they differ,
	// another (newer) ingest already covered this aggregate; we MUST
	// NOT burn a Qdrant upsert on stale data.
	//
	// Source-version key priority (handler-side defense in depth):
	//   1. metadata_json.$.content_hash  ← primarily written by the
	//                                      Dispatcher inside the same
	//                                      tx as the outbox event, so
	//                                      atomic with the producer-side
	//                                      source_version stamp.
	//   2. metadata_json.$.file_hash     ← ingest.service fallback for
	//                                      sources that write a different
	//                                      key (an asset that arrived via
	//                                      a path the Dispatcher did NOT
	//                                      touch, e.g. direct CLI command).
	//   3. media_assets.file_hash        ← legacy top-level column,
	//                                      populated by older ingest
	//                                      paths.
	// The dispatcher writes key #1 atomically inside EnqueueAndIndex,
	// so the gate is reliable for canonical ingest paths; keys #2 and
	// #3 keep the gate meaningful for non-canonical ingest paths.
	//
	// When MULTIPLE keys are populated and DISAGREE, content_hash wins
	// (it represents the same write boundary as the event's
	// source_version stamp so the two are guaranteed to be consistent
	// within a single ingest). DO NOT reorder these keys without
	// thinking it through — see readSourceVersion for the load-bearing
	// rules.
	//
	// The gate is skipped when sourceChecker is nil (test path only)
	// so we don't break tests that wire only the indexer.
	if h.sourceChecker != nil {
		current, gerr := h.sourceChecker.GetClip(ctx, p.AssetID)
		if gerr != nil {
			// GetClip failure is retryable — it could be a transient
			// SQLite lock or a network blip on a remote DB.
			log.Warn("asset.index.requested: GetClip for supersede gate failed (retryable)",
				append(reqLog, zap.Error(gerr))...,
			)
			outcome = "retryable"
			return fmt.Errorf("asset.index.requested GetClip(%s): %w", p.AssetID, gerr)
		}
		if current != nil {
			curVersion := readSourceVersion(current)
			if curVersion != "" && curVersion != p.SourceVersion {
				// Stamp the metric before returning so dashboards
				// surface the supersede delta even when the handler
				// short-circuits before the duration observation
				// captures the rest of the path.
				metrics.MediaIndexSupersededTotal.WithLabelValues(evt.EventType).Inc()
				outcome = "superseded"
				log.Info("asset.index.requested: event superseded by newer aggregate version",
					append(reqLog,
						zap.String("current_source_version", curVersion),
					)...,
				)
				return outboxevents.NewSupersede(p.AssetID, curVersion, p.SourceVersion)
			}
		}
	}

	// IndexClip delegation. The clipindexer is responsible for
	// embedding generation + Qdrant upsert + SQLite indexed_at stamp.
	log.Info("asset.index.requested: delegating to IndexClip", reqLog...)
	if h.indexer == nil {
		outcome = "terminal"
		return outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: indexer not wired (terminal — production misconfiguration)"),
		)
	}
	if ierr := h.indexer.IndexClip(ctx, p.AssetID); ierr != nil {
		// Retryable — embedding-server transient failures (timeouts,
		// 502/503/504), network blips, and Qdrant conn drops ride
		// the existing exponential-backoff path; max_attempts is their
		// natural dead-letter cap.
		outcome = "retryable"
		log.Warn("asset.index.requested: IndexClip failed (retryable)",
			append(reqLog, zap.Error(ierr))...,
		)
		return fmt.Errorf("asset.index.requested IndexClip(%s): %w", p.AssetID, ierr)
	}

	outcome = "success"
	log.Info("asset.index.requested: indexing complete", reqLog...)
	return nil
}

// readSourceVersion returns the asset's current source_version
// fingerprint by walking a deterministic priority list of fields
// (see the IndexingHandler source_version key-priority comment for
// the canonical-vs-fallback rationale).
//
// Priority invariants — these are load-bearing, do NOT reorder:
//
//  1. metadata_json.$.content_hash is the dispatcher-aware write
//     boundary (the Dispatcher writes it atomically inside the same
//     tx as the outbox event). When MULTIPLE keys are populated and
//     DISAGREE, content_hash wins — it represents the same write
//     boundary as the event's source_version stamp so the two are
//     guaranteed to be consistent within a single ingest.
//
//  2. metadata_json.$.file_hash is a fallback for non-dispatcher
//     ingest paths (e.g. CLI direct upserts, older YouTube sync
//     paths) where the Dispatcher was not in the write path.
//
//  3. Asset.FileHash() is the legacy top-level column; populated by
//     pre-metadata-json ingest tooling.
//
// Returns "" when none of the candidate keys are populated — the
// handler treats empty as "no current fingerprint to compare
// against", so the gate is BYPASSED (IndexClip's own internal
// indexed_content_hash check still runs as a fallback).
func readSourceVersion(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if v := a.GetMetadataString("content_hash"); v != "" {
		return v
	}
	if v := a.GetMetadataString("file_hash"); v != "" {
		return v
	}
	// Top-level file_hash column on the Asset struct; populated
	// by older ingest paths that did not write metadata_json.file_hash.
	if v := a.FileHash(); v != "" {
		return v
	}
	return ""
}

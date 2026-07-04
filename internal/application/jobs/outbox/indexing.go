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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
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
// accessor methods whose FileHash() returns the SAME metadata slot
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
	// Read the current asset's source fingerprint via the
	// SourceVersionQuerier port (PR 11 follow-up: this replaced the
	// previous AssetSourceChecker.GetClip pattern, which had a
	// producer-/consumer-side priority-chain drift — see
	// internal/infrastructure/database/sqlite/assets/source_version.go
	// for the canonical priority list and the regression test that
	// pins it).
	//
	// Three outcomes from the helper:
	//   (a) (value, nil)             — fingerprint present. If differs
	//                                  from the event's source_version,
	//                                  return *SupersedeError so the
	//                                  pool routes the row to
	//                                  MarkSuperseded without burning
	//                                  a Qdrant upsert.
	//   (b) ("", nil)                — row exists but no fingerprint;
	//                                  fall through to IndexClip.
	//   (c) ("", sql.ErrNoRows)      — row missing; fall through to
	//                                  IndexClip (its own idempotency
	//                                  check handles the ghost case).
	// All OTHER errors are SQL failures (lock, I/O, drift) — retryable.
	//
	// The gate is skipped when sourceQuerier is nil (test path only)
	// so we don't break tests that wire only the indexer.
	if h.sourceQuerier != nil {
		curVersion, qerr := h.sourceQuerier.SourceVersionFor(ctx, p.AssetID)
		if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
			// Generic SQL failure (lock, network blip, schema
			// drift) is retryable — the pool's exponential backoff
			// retries per its config.
			log.Warn("asset.index.requested: SourceVersionFor failed (retryable)",
				append(reqLog, zap.Error(qerr))...,
			)
			outcome = "retryable"
			return fmt.Errorf("asset.index.requested SourceVersionFor(%s): %w", p.AssetID, qerr)
		}
		// sql.ErrNoRows (row missing) — fall through.
		// (value, nil) — proceed; supersede if value differs.
		if qerr == nil && curVersion != "" && curVersion != p.SourceVersion {
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
		// PR-QDRANT-INDEXCLIP-GUARD (July 2026): the indexer-offline
		// path. clipindexer.Service.IndexClip returns the typed
		// sentinel ErrIndexClipDisabledButEventRequested when
		// cfg.Enabled=false (the indexer is disabled at runtime but
		// an asset.index.requested event arrived anyway). Detection
		// branch fires BEFORE the existing CAS-supersede check so
		// the typed sentinel takes precedence over generic
		// transient-error classification.
		//
		// Outcome: stamp INDEXING_SKIPPED_NO_INDEXER on
		// media_assets via the IndexerStateUpdater port (best-effort
		// — log+continue if the updater is nil or fails), then
		// return a NON-nil retryable error so the outbox pool does
		// NOT mark the event COMPLETED and the event is re-emitted
		// when the operator re-enables the indexer (pending+retry
		// per godlike/07 fail-closed).
		if errors.Is(ierr, clipindexer.ErrIndexClipDisabledButEventRequested) {
			metrics.MediaIndexSkippedTotal.WithLabelValues(evt.EventType).Inc()
			outcome = "skipped_no_indexer"
			log.Warn("asset.index.requested: indexer disabled, retry pending until re-enabled",
				append(reqLog, zap.Error(ierr))...,
			)
			if h.stateUpdater != nil {
				if suErr := h.stateUpdater.MarkIndexingSkippedNoIndexer(ctx, p.AssetID); suErr != nil {
					// godlike/07 fail-closed: a state-update
					// failure MUST NOT abort the retry path — the
					// asset row stays in its previous state, the
					// outbox event is still re-emitted on retry,
					// and the operator gets a Warn line to
					// investigate. Returning nil here would
					// silently lose the retry signal.
					log.Warn("asset.index.requested: MarkIndexingSkippedNoIndexer failed; retry path continues unchanged",
						append(reqLog, zap.Error(suErr))...,
					)
				}
			}
			// Wrap the typed sentinel in a retryable error
			// (NOT a TerminalError) so the outbox pool's
			// IsTerminal classifier routes the event back to
			// the pending bucket. The sentinel itself remains
			// errors.Is-probe-able for downstream consumers.
			return fmt.Errorf("asset.index.requested: %w", ierr)
		}
		// CAS miss after Qdrant upsert (BLOCKER #2): setIndexedAt's
		// source_version + file_hash + index_state='INDEXING' fence
		// matched zero rows — the asset was superseded while embeddings
		// were being generated. Route to SUPERSEDED so the outbox does
		// NOT retry and does NOT mark the stale event as SUCCESS.
		var superseded *clipindexer.ErrIndexSuperseded
		if errors.As(ierr, &superseded) {
			metrics.MediaIndexSupersededTotal.WithLabelValues(evt.EventType).Inc()
			outcome = "superseded"
			log.Info("asset.index.requested: IndexClip CAS miss — event superseded (routing to MarkSuperseded)",
				append(reqLog,
					zap.String("stale_source_version", superseded.SourceVersion),
				)...,
			)
			return outboxevents.NewSupersede(superseded.ClipID, "<post-upsert-race>", superseded.SourceVersion)
		}
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

// NOTE (PR 11 follow-up, June 2026): the previous readSourceVersion
// helper above was REMOVED. Both the producer-side
// (cmd/admin/reconcile_qdrant.go) and this consumer-side handler now
// route through assets.SourceVersionFor in
// internal/infrastructure/database/sqlite/assets/source_version.go.
// The legacy reader walked *Asset accessor methods, which DROPPED
// tier 3 (legacy top-level media_assets.file_hash column) — the new
// SQL helper honours all three tiers so backfilled rows from
// pre-metadata-json tooling are correctly fingerprinted.
//
// The migration path for any future reader that needs to operate on
// a pre-loaded *Asset should call into a thin adapter rather than
// re-creating the priority chain — see the doc-comment on the
// upstream package-level function for the canonical rules.

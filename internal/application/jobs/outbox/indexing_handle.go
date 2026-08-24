package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// Handle is the canonical handler for asset.index.requested.v1 events.
//
// Parses the v1 envelope via parseAndValidateRequest (sibling file),
// performs the source_version supersede gate, and delegates to
// IndexClip. Validation failures and unsatisfiable payloads return
// typed terminal errors (outboxevents.NewTerminalError) so the
// pool's IsTerminal classifier dead-letters them immediately rather
// than burning max_attempts in a repair loop. Transient IndexClip
// failures return non-terminal errors so the pool retries per its
// exponential backoff. A source_version mismatch returns
// *SupersedeError so the pool's IsSupersede classifier routes the
// row to MarkSuperseded.
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

	// Parse + strict v1 envelope validation. Each missing/mismatched
	// field is TERMINAL — retrying won't bring the field into
	// existence. The parse+validate logic lives in indexing_validate.go
	// (godlike/06 SSOT one-canonical-owner-per-fact).
	p, vErr := parseAndValidateRequest(evt.PayloadJSON, evt, log)
	if vErr != nil {
		outcome = "terminal"
		return vErr
	}

	reqLog := []zap.Field{
		zap.String("asset_id", p.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", p.EventID),
		zap.String("index_revision", p.IndexRevision),
		zap.Int("attempt", evt.AttemptCount),
	}
	if p.RequestedAt != "" {
		reqLog = append(reqLog, zap.String("requested_at", p.RequestedAt))
	}
	if p.Operation != "" {
		reqLog = append(reqLog, zap.String("operation", p.Operation))
	}

	// Index-revision supersede gate (QDRANT-002 item F — "Se l'evento
	// è obsoleto, marcarlo SUPERSEDED senza indicizzare dati vecchi").
	// Compare the event's index_revision (the indexable-snapshot revision;
	// legacy source_version alias falls back at parse time) against the
	// CURRENT canonical index_revision read via the SourceVersionQuerier
	// port (PR 11 follow-up: replaced the AssetSourceChecker.GetClip pattern
	// — see internal/infrastructure/database/sqlite/assets/source_version.go
	// for the canonical priority list, which reads metadata_json.$.index_revision
	// FIRST and only falls back to content_hash/file_hash for legacy rows).
	// The gate NEVER compares byte identity (content_sha256) — the index
	// revision is the separate snapshot fingerprint (godlike/06).
	//
	// Three outcomes from the helper:
	//   (a) (value, nil)             — fingerprint present. If differs
	//                                  from the event's index_revision,
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
		if qerr == nil && curVersion != "" && curVersion != p.IndexRevision && !p.Force {
			// Stamp the metric before returning so dashboards
			// surface the supersede delta even when the handler
			// short-circuits before the duration observation
			// captures the rest of the path.
			metrics.MediaIndexSupersededTotal.WithLabelValues(evt.EventType).Inc()
			outcome = "superseded"
			log.Info("asset.index.requested: event superseded by newer aggregate version",
				append(reqLog,
					zap.String("current_index_revision", curVersion),
				)...,
			)
			return outboxevents.NewSupersede(p.AssetID, curVersion, p.IndexRevision)
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

// Package media — outbox_worker.go: the PostgreSQL outbox consumption
// lifecycle and the canonical asset.index.requested worker.
//
// Demolition contract (media cutover, September 2026): this worker is the
// ONLY consumer of media index-request events in the PostgreSQL media SSOT
// mode. It replaces the SQLite outbox → Qdrant projection chain: the
// embedding is written to media_embeddings (pgvector) inside the SAME
// database that owns media_assets, the asset index_state is flipped to
// INDEXED in the same transaction as the vector upsert, and the outbox
// event is completed only after both commits. There is no Qdrant hop.
//
// Lease fencing mirrors internal/platform/sqlite/outboxevents exactly
// (same status set, same lease columns, same exponential-backoff
// MarkFailed semantics) — one outbox fact family, two engine adapters.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrLeaseLost is returned when a lifecycle mutator's lease fence fails:
// IndexEventPayload is the asset.index.requested envelope
// (ReindexEnvelopeV1Schema parity with the SQLite dispatcher).
type IndexEventPayload struct {
	SchemaVersion  string `json:"schema_version"`
	AssetID        string `json:"asset_id"`
	Source         string `json:"source,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
}

// AssetEmbedder produces the text-channel embedding for one asset.
// Production concrete: embeddings.HTTPTextEmbedder (E5 sidecar contract —
// the SAME embedder family that produced the indexed document vectors,
// so query and document spaces cannot drift).
type AssetEmbedder interface {
	EmbedAssetText(ctx context.Context, assetID string) ([]float32, error)
}

// BatchAssetEmbedder is the OPTIONAL batch surface of the embedding port.
// When the wired AssetEmbedder implements it, the worker resolves a whole
// claim batch (search_text read + provider leg) in one pass instead of one
// SELECT per event; the per-asset path stays the contract for embedders
// that cannot batch, and remains the fallback whenever the batch leg fails.
//
// Implementations MUST omit (never fabricate) an asset they could not
// resolve: the worker then re-runs that event through the per-asset path so
// the failure is attributed, retried and dead-lettered per event.
type BatchAssetEmbedder interface {
	EmbedAssetTexts(ctx context.Context, assetIDs []string) (map[string][]float32, error)
}

// PostgresIndexWorker is the canonical consumer of asset.index.requested
// events in the PostgreSQL media SSOT mode.
type PostgresIndexWorker struct {
	repo     *Repository
	vectors  *VectorSurfaceWriter
	embedder AssetEmbedder
	// ModelID pins the production embedding family. The worker fails
	// closed when the family is unregistered — the embedding is never
	// written under an unknown model identity.
	ModelID string
	// EmbeddingType is the canonical channel ("text").
	EmbeddingType string

	handlersMu  sync.RWMutex
	handlers    map[string]OutboxHandler
	metrics     OutboxStatusMetrics
	metricTypes map[string]struct{}
}

// NewPostgresIndexWorker constructs the worker. Every dependency is
// required — a nil slot is a composition error and panics loudly at boot
// (godlike/07: fail closed, never fake availability).
func NewPostgresIndexWorker(repo *Repository, vectors *VectorSurfaceWriter, embedder AssetEmbedder, modelID string) *PostgresIndexWorker {
	switch {
	case repo == nil:
		panic("media.NewPostgresIndexWorker: outbox repository is required")
	case vectors == nil:
		panic("media.NewPostgresIndexWorker: vector surface writer is required")
	case embedder == nil:
		panic("media.NewPostgresIndexWorker: embedder is required")
	case modelID == "":
		panic("media.NewPostgresIndexWorker: model id is required")
	}
	return &PostgresIndexWorker{
		repo:          repo,
		vectors:       vectors,
		embedder:      embedder,
		ModelID:       modelID,
		EmbeddingType: "text",
		handlers:      make(map[string]OutboxHandler),
		metricTypes:   make(map[string]struct{}),
	}
}

// WithOutboxStatusMetrics enables status projection for the supplied event
// types. A nil metrics port disables the projection without affecting the
// delivery worker. Event types are bounded by composition-time registration.
func (w *PostgresIndexWorker) WithOutboxStatusMetrics(metrics OutboxStatusMetrics, eventTypes ...string) *PostgresIndexWorker {
	if w == nil {
		return w
	}
	w.metrics = metrics
	for _, eventType := range eventTypes {
		if eventType != "" {
			w.metricTypes[eventType] = struct{}{}
		}
	}
	return w
}

// RefreshOutboxStatusMetrics reads the PostgreSQL outbox status counts for
// the event types registered through WithOutboxStatusMetrics. It is exported
// so the live PostgreSQL acceptance test can exercise the same projection the
// production Run loop uses.
func (w *PostgresIndexWorker) RefreshOutboxStatusMetrics(ctx context.Context) error {
	if w == nil || w.metrics == nil || len(w.metricTypes) == 0 {
		return nil
	}
	for eventType := range w.metricTypes {
		var pending, deadLetter, backlog int64
		var oldestEventAgeSeconds float64
		// ONE round-trip per event type: the drain loop must never pay a
		// COUNT(*) FILTER probe per claim. backlog = pending + processing
		// (all uncompleted work); oldest_age = age of the oldest pending
		// row, 0 when the queue is empty.
		if err := w.repo.db.QueryRowContext(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE status = 'pending'),
				COUNT(*) FILTER (WHERE status = 'dead_letter'),
				COUNT(*) FILTER (WHERE status IN ('pending', 'processing')),
				COALESCE(EXTRACT(EPOCH FROM (now() - MIN(created_at_ts) FILTER (WHERE status = 'pending'))), 0)
			FROM outbox_events
			WHERE event_type = $1
		`, eventType).Scan(&pending, &deadLetter, &backlog, &oldestEventAgeSeconds); err != nil {
			return fmt.Errorf("media outbox status metrics %q: %w", eventType, err)
		}
		w.metrics.ObserveOutboxStatus(eventType, "pending", pending)
		w.metrics.ObserveOutboxStatus(eventType, "dead_letter", deadLetter)
		w.metrics.ObserveOutboxBacklog(eventType, backlog, oldestEventAgeSeconds)
	}
	return nil
}

// RegisterHandler adds a durable consumer for a PostgreSQL media outbox
// event type. Registration happens during composition, before Run starts, but
// the mutex also makes the contract safe for tests and future hot wiring.
func (w *PostgresIndexWorker) RegisterHandler(eventType string, handler OutboxHandler) error {
	if w == nil {
		return errors.New("media outbox worker: worker is required")
	}
	if eventType == "" {
		return errors.New("media outbox worker: event type is required")
	}
	if handler == nil {
		return fmt.Errorf("media outbox worker: handler for %q is required", eventType)
	}
	w.handlersMu.Lock()
	defer w.handlersMu.Unlock()
	if _, exists := w.handlers[eventType]; exists {
		return fmt.Errorf("media outbox worker: handler already registered for %q", eventType)
	}
	w.handlers[eventType] = handler
	return nil
}

func (w *PostgresIndexWorker) handlerFor(eventType string) OutboxHandler {
	w.handlersMu.RLock()
	defer w.handlersMu.RUnlock()
	return w.handlers[eventType]
}

// Handle processes one claimed asset.index.requested event:
//
//	embed(search_text) → upsert media_embeddings → index_state=INDEXED
//	  (same tx) → outbox completed.
//
// Idempotent by construction: the embedding upsert is keyed on
// (asset_id, embedding_type, model_id) and SetIndexed is a no-op when the
// state/content already match — a redelivered event converges to the same
// terminal state.
func (w *PostgresIndexWorker) Handle(ctx context.Context, claim *OutboxClaim) error {
	if claim == nil {
		return nil
	}
	evt := claim.Event
	if evt.EventType != EventAssetIndexRequested {
		handler := w.handlerFor(evt.EventType)
		if handler == nil {
			return w.failOrFail(ctx, claim, fmt.Errorf("media outbox worker: no handler registered for event type %q", evt.EventType))
		}
		if err := handler.Handle(ctx, claim); err != nil {
			return w.failOrFail(ctx, claim, err)
		}
		if err := w.repo.MarkCompleted(ctx, evt.ID, claim.LeaseID); err != nil {
			return fmt.Errorf("media outbox worker: complete event %d: %w", evt.ID, err)
		}
		return nil
	}

	// Index events go through the canonical INDEXED transition. A nil vec
	// means "resolve it here"; the batch fast-path in processClaims passes a
	// prefetched vector instead, so the per-asset SELECT/Embed is skipped.
	return w.handleIndexEvent(ctx, claim, nil)
}

// handleIndexEvent applies the canonical INDEXED transition for one
// asset.index.requested claim. A non-empty vec (prefetched by the batch
// leg) skips the per-asset embed; nil falls back to the single-asset path.
//
// Idempotent by construction: the embedding upsert is keyed on
// (asset_id, embedding_type, model_id) and the index_state flip is a no-op
// when the state/content already match — a redelivered event converges to
// the same terminal state.
func (w *PostgresIndexWorker) handleIndexEvent(ctx context.Context, claim *OutboxClaim, vec []float32) error {
	evt := claim.Event
	var payload IndexEventPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err != nil {
		// A malformed envelope is terminal — retrying cannot fix bytes.
		if markErr := w.repo.MarkCompleted(ctx, evt.ID, claim.LeaseID); markErr != nil {
			return fmt.Errorf("media index worker: malformed payload + complete: %w", markErr)
		}
		return fmt.Errorf("media index worker: malformed payload for event %d: %w", evt.ID, err)
	}
	assetID := payload.AssetID
	if assetID == "" {
		assetID = evt.AggregateID
	}
	if assetID == "" {
		if err := w.repo.MarkCompleted(ctx, evt.ID, claim.LeaseID); err != nil {
			return fmt.Errorf("media index worker: empty asset + complete: %w", err)
		}
		return fmt.Errorf("media index worker: event %d carries no asset identity", evt.ID)
	}

	if len(vec) == 0 {
		resolved, err := w.embedder.EmbedAssetText(ctx, assetID)
		if err != nil {
			return w.failOrFail(ctx, claim, fmt.Errorf("media index worker: embed asset %q: %w", assetID, err))
		}
		vec = resolved
	}
	if len(vec) == 0 {
		return w.failOrFail(ctx, claim, fmt.Errorf("media index worker: zero-length embedding for asset %q", assetID))
	}

	// One transaction: vector upsert + index_state flip. Rollback leaves
	// the event processing (lease will expire) and zero partial state.
	tx, err := w.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media index worker: begin tx: %w", err)
	}
	if err := w.vectors.UpsertEmbeddingTx(ctx, tx, assetID, w.EmbeddingType, w.ModelID, vec); err != nil {
		_ = tx.Rollback()
		return w.failOrFail(ctx, claim, fmt.Errorf("media index worker: embedding upsert asset %q: %w", assetID, err))
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET index_state = 'INDEXED', index_state_updated_at = $1
		WHERE id = $2
	`, nowRFC3339(), assetID); err != nil {
		_ = tx.Rollback()
		return w.failOrFail(ctx, claim, fmt.Errorf("media index worker: index_state flip asset %q: %w", assetID, err))
	}
	if err := tx.Commit(); err != nil {
		return w.failOrFail(ctx, claim, fmt.Errorf("media index worker: commit asset %q: %w", assetID, err))
	}

	if err := w.repo.MarkCompleted(ctx, evt.ID, claim.LeaseID); err != nil {
		return fmt.Errorf("media index worker: complete event %d: %w", evt.ID, err)
	}
	return nil
}

// indexAssetID resolves the asset identity an asset.index.requested event
// addresses: payload.asset_id wins over the aggregate id (exactly as
// handleIndexEvent resolves it). Returns "" for any other event type or an
// unparseable envelope, which keeps the batch leg conservative.
func indexAssetID(evt OutboxEvent) string {
	if evt.EventType != EventAssetIndexRequested {
		return ""
	}
	var payload IndexEventPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err != nil {
		return ""
	}
	if payload.AssetID != "" {
		return payload.AssetID
	}
	return evt.AggregateID
}

// prefetchBatchVectors resolves the text-channel vector for every distinct
// asset in the claimed batch with ONE search_text read when the wired
// embedder exposes the optional BatchAssetEmbedder surface. It returns nil
// whenever batching is unavailable, the batch has fewer than two distinct
// assets (no round-trip to amortise), or the batch leg fails — in all three
// cases each event falls back to the per-asset path, so a batch-leg error
// can never change delivery, retry or dead-letter semantics.
func (w *PostgresIndexWorker) prefetchBatchVectors(ctx context.Context, claims []*OutboxClaim) map[string][]float32 {
	batch, ok := w.embedder.(BatchAssetEmbedder)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(claims))
	seen := make(map[string]struct{}, len(claims))
	for _, claim := range claims {
		if claim == nil {
			continue
		}
		id := indexAssetID(claim.Event)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		return nil
	}
	vectors, err := batch.EmbedAssetTexts(ctx, ids)
	if err != nil {
		return nil
	}
	return vectors
}

// processClaims drains one claimed batch. Index events whose vector was
// resolved by the batch leg skip the per-asset embed; every other event (and
// every asset the batch leg could not resolve) goes through Handle. The loop
// never aborts on a per-event failure — the event owns its retry/dead-letter
// lifecycle.
func (w *PostgresIndexWorker) processClaims(ctx context.Context, claims []*OutboxClaim, log Logger) {
	vectors := w.prefetchBatchVectors(ctx, claims)
	for _, claim := range claims {
		if claim == nil {
			continue
		}
		var err error
		if vec, ok := vectors[indexAssetID(claim.Event)]; ok {
			err = w.handleIndexEvent(ctx, claim, vec)
		} else {
			err = w.Handle(ctx, claim)
		}
		if err != nil {
			w.logf(log, "media index worker: event "+fmt.Sprint(claim.Event.ID)+" failed", err)
			continue
		}
		if w.metrics != nil {
			w.metrics.ObserveOutboxProcessed(claim.Event.EventType)
		}
	}
}

// failOrFail records the failure with exponential backoff and surfaces
// the error so the worker loop can log it. The event is retried until
// max_attempts, then dead-lettered (never silently dropped).
func (w *PostgresIndexWorker) failOrFail(ctx context.Context, claim *OutboxClaim, cause error) error {
	backoff := time.Duration(1<<min(claim.Event.AttemptCount, 6)) * time.Second
	if err := w.repo.MarkFailed(ctx, claim.Event.ID, claim.LeaseID, cause.Error(), time.Now().Add(backoff)); err != nil {
		return errors.Join(cause, fmt.Errorf("media index worker: mark failed: %w", err))
	}
	return cause
}

// DefaultPollInterval is the production ClaimNext cadence when the
// wiring does not override it.
const DefaultPollInterval = 2 * time.Second

// DefaultLeaseTTL is the production claim lease. A worker that crashes
// mid-handle loses the lease after this window and the event is
// reclaimable (attempt_count already incremented — no infinite loop).
const DefaultLeaseTTL = 5 * time.Minute

// Run drains asset.index.requested events until ctx is cancelled: claim a
// batch → process → repeat, sleeping pollInterval whenever the outbox is
// empty. A batch shares one lease token and, when the wired embedder exposes
// the optional BatchAssetEmbedder surface, one search_text read (N→1)
// instead of one SELECT per event.
// It is the production entry point (launched via SafeGo from the
// composition root's start closure) and is also exercisable directly in
// tests with a short interval.
//
// Guarantee: Handle is invoked for every claimable event exactly once per
// claim; errors are recorded on the event (retry/dead-letter) and logged
// here — the loop NEVER exits on a per-event failure, only on ctx.Done.
func (w *PostgresIndexWorker) Run(ctx context.Context, pollInterval, leaseTTL time.Duration, log Logger) {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	if leaseTTL <= 0 {
		leaseTTL = DefaultLeaseTTL
	}
	// Metrics ride their own cadence, decoupled from claims: the loop below
	// drains flat out while the outbox has work and idles on this ticker
	// only once it is empty.
	metricsTicker := time.NewTicker(pollInterval)
	defer metricsTicker.Stop()

	workerID := "pg-media-index-worker:" + w.ModelID
	if err := w.RefreshOutboxStatusMetrics(ctx); err != nil {
		w.logf(log, "media outbox: status metrics refresh failed", err)
	}
	for {
		// Opportunistic metrics refresh while work is flowing (non-blocking).
		select {
		case <-metricsTicker.C:
			if err := w.RefreshOutboxStatusMetrics(ctx); err != nil {
				w.logf(log, "media outbox: status metrics refresh failed", err)
			}
		default:
		}
		claims, err := w.repo.ClaimBatch(ctx, workerID, leaseTTL, outboxDrainBatchSize)
		if err != nil {
			w.logf(log, "media index worker: claim failed", err)
			if !w.waitForNextTick(ctx, metricsTicker, log) {
				return
			}
			continue
		}
		if len(claims) == 0 {
			// Outbox drained: wait for the next tick instead of busy-polling.
			if !w.waitForNextTick(ctx, metricsTicker, log) {
				return
			}
			continue
		}
		w.processClaims(ctx, claims, log)
	}
}

// waitForNextTick blocks until the next poll interval (refreshing the outbox
// status gauges on the same tick) and reports whether the loop should
// continue: false means ctx was cancelled.
func (w *PostgresIndexWorker) waitForNextTick(ctx context.Context, metricsTicker *time.Ticker, log Logger) bool {
	select {
	case <-ctx.Done():
		return false
	case <-metricsTicker.C:
		if err := w.RefreshOutboxStatusMetrics(ctx); err != nil {
			w.logf(log, "media outbox: status metrics refresh failed", err)
		}
		return true
	}
}

// Logger is the narrow logging port so the worker stays infrastructure-
// agnostic (the wiring passes a zap-backed adapter; tests may pass nil —
// logging is best-effort, never load-bearing).
type Logger interface {
	Info(msg string, fields ...any)
	Error(msg string, fields ...any)
}

func (w *PostgresIndexWorker) logf(log Logger, msg string, err error) {
	if log == nil {
		return
	}
	log.Error(msg, map[string]any{"error": err.Error()})
}

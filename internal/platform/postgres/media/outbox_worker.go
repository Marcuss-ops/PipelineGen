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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// ErrLeaseLost is returned when a lifecycle mutator's lease fence fails:
// the event was re-assigned or is already terminal. Mirrors the SQLite
// outbox error so worker code is engine-agnostic.
var ErrLeaseLost = errors.New("outbox lease lost")

// OutboxEvent is the consumption projection of one outbox_events row
// (SQLite outboxevents.Event parity).
type OutboxEvent struct {
	ID          int64
	EventType   string
	AggregateID string
	// AggregateType mirrors the SQLite envelope ("asset").
	AggregateType string
	PayloadJSON   string
	Status        string
	AttemptCount  int
	MaxAttempts   int
	LastError     string
	EventKey      string
	Priority      int
	CreatedAt     string
	UpdatedAt     string
}

// OutboxClaim is one claimed event plus its lease identity.
type OutboxClaim struct {
	Event    OutboxEvent
	WorkerID string
	LeaseID  string
}

// ClaimNext claims the oldest pending event atomically (CTE claim with
// row-level fencing — PostgreSQL UPDATE ... WHERE status='pending' is
// atomic under concurrent workers). Ordering: priority DESC,
// next_attempt_at ASC, id ASC (migration 186 parity).
func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*OutboxClaim, error) {
	now := timeutil.FormatRFC3339(time.Now())
	leaseID := uuid.NewString()
	leaseExpiry := timeutil.FormatRFC3339(time.Now().Add(leaseTTL))

	var id int64
	err := r.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending'
			  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
			ORDER BY priority DESC, next_attempt_at ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_events
		SET status = 'processing',
		    attempt_count = attempt_count + 1,
		    worker_id = $2, lease_id = $3, lease_expiry = $4,
		    updated_at = $1
		WHERE id = (SELECT id FROM candidate)
		  AND status = 'pending'
		RETURNING id
	`, now, workerID, leaseID, leaseExpiry).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media outbox ClaimNext: %w", err)
	}

	var evt OutboxEvent
	err = r.db.QueryRowContext(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_type, payload_json,
		       status, attempt_count, max_attempts, last_error, event_key,
		       priority, created_at, updated_at
		FROM outbox_events WHERE id = $1
	`, id).Scan(&evt.ID, &evt.EventType, &evt.AggregateID, &evt.AggregateType, &evt.PayloadJSON,
		&evt.Status, &evt.AttemptCount, &evt.MaxAttempts, &evt.LastError, &evt.EventKey,
		&evt.Priority, &evt.CreatedAt, &evt.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("media outbox ClaimNext refetch(%d): %w", id, err)
	}
	return &OutboxClaim{Event: evt, WorkerID: workerID, LeaseID: leaseID}, nil
}

// MarkCompleted completes a claimed event (lease-fenced).
func (r *Repository) MarkCompleted(ctx context.Context, eventID int64, leaseID string) error {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'completed', completed_at = $1, updated_at = $1
		WHERE id = $2 AND status = 'processing' AND lease_id = $3
	`, now, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("media outbox MarkCompleted(%d): %w", eventID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("media outbox MarkCompleted(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

// MarkFailed records a failed attempt. Attempts remaining → back to
// pending with exponential backoff; exhausted → dead_letter.
func (r *Repository) MarkFailed(ctx context.Context, eventID int64, leaseID, errMsg string, nextAttemptAt time.Time) error {
	var attemptCount, maxAttempts int
	if err := r.db.QueryRowContext(ctx,
		`SELECT attempt_count, max_attempts FROM outbox_events WHERE id = $1`, eventID,
	).Scan(&attemptCount, &maxAttempts); err != nil {
		return fmt.Errorf("media outbox MarkFailed read(%d): %w", eventID, err)
	}
	now := timeutil.FormatRFC3339(time.Now())

	if attemptCount >= maxAttempts {
		result, err := r.db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = 'dead_letter', last_error = $1, updated_at = $2,
			    worker_id = '', lease_id = '', lease_expiry = NULL
			WHERE id = $3 AND lease_id = $4 AND status = 'processing'
		`, errMsg, now, eventID, leaseID)
		if err != nil {
			return fmt.Errorf("media outbox MarkFailed dead_letter(%d): %w", eventID, err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("media outbox MarkFailed dead_letter(%d): %w", eventID, ErrLeaseLost)
		}
		return nil
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'pending', last_error = $1, next_attempt_at = $2,
		    updated_at = $3, worker_id = '', lease_id = '', lease_expiry = NULL
		WHERE id = $4 AND lease_id = $5 AND status = 'processing'
	`, errMsg, timeutil.FormatRFC3339(nextAttemptAt), now, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("media outbox MarkFailed retry(%d): %w", eventID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("media outbox MarkFailed retry(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

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
	}
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

	vec, err := w.embedder.EmbedAssetText(ctx, assetID)
	if err != nil {
		return w.failOrFail(ctx, claim, fmt.Errorf("media index worker: embed asset %q: %w", assetID, err))
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

// Run drains asset.index.requested events until ctx is cancelled: claim →
// handle → repeat, sleeping pollInterval whenever the outbox is empty.
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
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	workerID := "pg-media-index-worker:" + w.ModelID
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		claim, err := w.repo.ClaimNext(ctx, workerID, leaseTTL)
		if err != nil {
			w.logf(log, "media index worker: claim failed", err)
			continue
		}
		if claim == nil {
			continue // outbox drained
		}
		if err := w.Handle(ctx, claim); err != nil {
			w.logf(log, "media index worker: event "+fmt.Sprint(claim.Event.ID)+" failed", err)
		}
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

// EmbedAssetTextAdapter adapts the kernel asset.Embedder (HTTPTextEmbedder)
// to the worker's AssetEmbedder port: the asset's search_text is fetched
// from the media SSOT and embedded with the canonical text-channel model.
type EmbedAssetTextAdapter struct {
	db      *sql.DB
	embeder coreasset.Embedder
}

// NewEmbedAssetTextAdapter constructs the adapter. Both deps required.
func NewEmbedAssetTextAdapter(db *sql.DB, embedder coreasset.Embedder) *EmbedAssetTextAdapter {
	if db == nil {
		panic("media.NewEmbedAssetTextAdapter: db is required")
	}
	if embedder == nil {
		panic("media.NewEmbedAssetTextAdapter: embedder is required")
	}
	return &EmbedAssetTextAdapter{db: db, embeder: embedder}
}

// EmbedAssetText reads search_text from media_assets and embeds it.
func (a *EmbedAssetTextAdapter) EmbedAssetText(ctx context.Context, assetID string) ([]float32, error) {
	var text string
	if err := a.db.QueryRowContext(ctx,
		`SELECT search_text FROM media_assets WHERE id = $1`, assetID,
	).Scan(&text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("embed asset %q: asset not found in media SSOT", assetID)
		}
		return nil, fmt.Errorf("embed asset %q: read search_text: %w", assetID, err)
	}
	res, err := a.embeder.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	return res.Vector, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

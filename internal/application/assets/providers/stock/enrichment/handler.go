// Package enrichment — handler.go (PR-011A, July 2026).
//
// EnrichmentHandler is the canonical broker entry-point for the
// stock post-publish RLM/LLM enrichment pass. The handler:
//
//  1. Parses the job payload (chunk_id).
//  2. Reads the media_assets row by id.
//  3. Calls EnrichmentLLMClient.Enrich (currently a stub in PR-011A).
//  4. PR-011B will replace the stub with the real ollama call + UPDATE
//     media_assets.metadata_json with the EnrichedFields.
//  5. PR-011C will replace the noop finalization step with the
//     asset.published v1 outbox event emit so the IndexingHandler
//     re-upserts the chunk to Qdrant with the enriched fields.
//
// PR-011A scope (this file): ship the canonical handler skeleton +
// composition-root wiring + the typed-error contract. The LLM
// call is a STUB that returns ErrEnrichmentLLMUnavailable so the
// worker retry path is exercised end-to-end without a real
// ollama call. PR-011B is the next-PR in the chain.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - EnrichmentHandler struct + HandleJob live ONLY in this file.
//   - 5 typed-error sentinels live ONLY in errors.go.
//   - EnrichmentLLMClient port + StubEnrichmentLLMClient live ONLY in llm_client.go.
//   - AssetRepository port lives ONLY in this file (the canonical
//     narrow seam for "read media_assets row by id" — the
//     composition root wires the production concrete).
//
// godlike/07 fail-closed contracts:
//   - NewEnrichmentHandler returns (nil, ErrEnrichmentHandlerNotConfigured)
//     when LLMClient or AssetRepo is nil. Composition root MUST propagate
//     the error (NOT silently default to a no-op handler).
//   - HandleJob returns a typed sentinel from the 5-sentinel taxonomy
//     on every failure mode. Callers can probe via errors.Is() to
//     decide between retry / terminal / re-think.
//   - The handler respects ctx cancellation: every LLM call passes
//     through ctx, and the SELECT-by-id query is bounded by ctx
//     deadline via the underlying sql.DB driver.
//
// godlike/07 minimum-blast-radius: the handler is additive — the
// stock pipeline (PR-001..PR-009 chain) does not call this
// handler. The composition root wires the handler ONLY when
// cfg.External.StockEnrichmentEnabled=true (godlike/07 fail-closed:
// no-enrichment-configured = no-handler-registered = no-job-enqueued).
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// AssetRepository is the canonical Pattern-0 typed port for
// "read media_assets row by id + update metadata_json".
// godlike/06 SSOT: this interface is the SOLE definition of
// the asset-repository contract for the enrichment pass.
//
// Implementations:
//   - enrichment.sqliteAssetRepository (PR-011A — wraps
//     *sql.DB, reads media_assets by id, updates metadata_json)
//   - The future production concrete (already in
//     internal/infrastructure/database/sqlite/assets/repository.go)
//     is wired via composition root fluent setter per AGENTS.md
//     Pattern 0.
//
// The 2 methods are the minimal surface for the enrichment pass:
//   - GetByID returns the canonical media_assets row needed
//     to build the EnrichmentRequest (SourceURL / Title /
//     Description / StartSec / EndSec / SourceProvider).
//   - UpdateEnrichedMetadata persists the EnrichedFields into
//     media_assets.metadata_json (PR-011B will call this).
type AssetRepository interface {
	// GetByID returns the media_assets row by canonical id, or
	// (nil, ErrEnrichmentChunkNotFound) when the row is absent.
	// godlike/07 typed-error contract: ErrEnrichmentChunkNotFound
	// is the canonical terminal sentinel; other errors wrap
	// WrapPersistFailed for SQL-side failures.
	GetByID(ctx context.Context, id string) (*AssetRow, error)

	// UpdateEnrichedMetadata persists the EnrichedFields into
	// the media_assets row's metadata_json column. The implementation
	// MUST be idempotent on retry (UPDATE is naturally idempotent
	// given the same EnrichedFields input). Returns nil on
	// success; typed sentinel on failure.
	//
	// PR-011A: this method is declared but NOT YET CALLED by the
	// handler — the stub adapter returns ErrEnrichmentLLMUnavailable
	// BEFORE the handler reaches the update step. PR-011B will
	// add the call site.
	UpdateEnrichedMetadata(ctx context.Context, id string, fields EnrichedFields) error
}

// AssetRow is the typed read-back envelope from GetByID.
// The 7 fields are the canonical projection of the media_assets
// columns the enrichment pass needs to build the EnrichmentRequest.
//
// godlike/06 SSOT: AssetRow lives ONLY in this file. The
// production concrete (sqliteAssetRepository) populates these
// fields from the media_assets row; the handler projects them
// into EnrichmentRequest. Future LLM-driven enrichment passes
// MUST extend this struct (NOT introduce a parallel envelope).
type AssetRow struct {
	ID             string
	SourceURL      string
	Title          string
	Description    string
	StartSec       float64
	EndSec         float64
	SourceProvider string
}

// EnrichmentHandler is the canonical broker entry-point for the
// stock post-publish RLM/LLM enrichment pass. Constructed via
// NewEnrichmentHandler (fail-closed on nil deps per godlike/07).
type EnrichmentHandler struct {
	// LLMClient is the canonical Pattern-0 typed port. The
	// composition root injects the production concrete
	// (forward-pointer PR-011B ollama adapter) or the stub
	// (PR-011A — returns ErrEnrichmentLLMUnavailable).
	LLMClient EnrichmentLLMClient

	// AssetRepo is the canonical narrow port for media_assets
	// row read + metadata_json update. The composition root
	// injects the production concrete.
	AssetRepo AssetRepository

	// Log is the canonical zap logger. godlike/07 nil-tolerance:
	// nil-logger falls back to zap.NewNop() (defense-in-depth).
	Log *zap.Logger
}

// NewEnrichmentHandler constructs the canonical handler with
// fail-closed nil-deps gate per godlike/07 typed-error contract.
// Returns (nil, ErrEnrichmentHandlerNotConfigured) when LLMClient
// or AssetRepo is nil; composition root MUST propagate the error.
func NewEnrichmentHandler(llmClient EnrichmentLLMClient, assetRepo AssetRepository, log *zap.Logger) (*EnrichmentHandler, error) {
	if llmClient == nil {
		return nil, WrapHandlerNotConfigured("llmClient")
	}
	if assetRepo == nil {
		return nil, WrapHandlerNotConfigured("assetRepo")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &EnrichmentHandler{
		LLMClient: llmClient,
		AssetRepo: assetRepo,
		Log:       log,
	}, nil
}

// RegisterHandler binds the EnrichmentHandler.HandleJob to the
// canonical job type string ("media.stock_rlm_enrich") on the
// appjobs.Service dispatcher. Returns error on registration
// failure (duplicate handler, frozen registry). Composition root
// MUST propagate the error (NOT silently default to a no-op
// registration).
func (h *EnrichmentHandler) RegisterHandler(jobsSvc *appjobs.Service) error {
	if h == nil {
		return WrapHandlerNotConfigured("handler")
	}
	if jobsSvc == nil {
		return WrapHandlerNotConfigured("jobsSvc")
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeMediaStockRLMEnrich, appjobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("enrichment.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeMediaStockRLMEnrich, err)
	}
	h.Log.Info("registered media.stock_rlm_enrich job handler", zap.String("type", appjobs.TypeMediaStockRLMEnrich))
	return nil
}

// HandleJob is the canonical broker entry-point. Parses the
// job payload (chunk_id), reads the media_assets row, calls
// the LLM client, and (PR-011B+C) persists the enrichment + emits
// the asset.published outbox event.
//
// PR-011A scope: the LLM call is a STUB that returns
// ErrEnrichmentLLMUnavailable (worker's exponential backoff
// retries this sentinel up to DefaultMaxRetries=3 before
// flipping terminal). The handler does NOT call the
// UpdateEnrichedMetadata path or the outbox emit in PR-011A
// — those land in PR-011B and PR-011C respectively.
//
// Job payload shape:
//
//	{
//	  "chunk_id": "stock:<run_fingerprint>:chunk:<i>" (canonical media_assets.id)
//	}
//
// godlike/07 typed-error contract: every error path returns a
// typed sentinel from the 5-sentinel taxonomy. Dual-%w wraps
// (Go 1.20+) preserve the chain for both errors.Is (sentinel)
// and errors.As (typed envelope) probes.
func (h *EnrichmentHandler) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h == nil {
		return nil, ErrEnrichmentHandlerNotConfigured
	}

	// ── Step 1: parse the job payload (chunk_id) ──────────────
	var payload struct {
		ChunkID string `json:"chunk_id"`
	}
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			// godlike/07 typed-error contract: the JSON unmarshal
			// error path returns a typed sentinel (terminal — the
			// producer must fix the enqueue call). Distinct from
			// ErrEnrichmentInvalidLLMResponse (LLM-side parse
			// failure, retryable up to 3 times).
			return nil, WrapPayloadInvalid(err)
		}
	}
	if payload.ChunkID == "" {
		return nil, WrapChunkNotFound("")
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(10, "Reading media_assets row")
	}

	// ── Step 2: read the media_assets row ─────────────────────
	row, err := h.AssetRepo.GetByID(ctx, payload.ChunkID)
	if err != nil {
		// Pass typed sentinels through (ChunkNotFound is terminal;
		// PersistFailed wraps the SQL-side error).
		return nil, err
	}
	if row == nil {
		return nil, WrapChunkNotFound(payload.ChunkID)
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(40, "Calling LLM for enrichment")
	}

	// ── Step 3: call the LLM (stub in PR-011A; real in PR-011B) ─
	_, err = h.LLMClient.Enrich(ctx, EnrichmentRequest{
		ChunkID:        row.ID,
		SourceURL:      row.SourceURL,
		Title:          row.Title,
		Description:    row.Description,
		StartSec:       row.StartSec,
		EndSec:         row.EndSec,
		SourceProvider: row.SourceProvider,
	})
	if err != nil {
		// PR-011A: the stub adapter returns ErrEnrichmentLLMUnavailable
		// on every call. The worker's exponential backoff retries this
		// sentinel up to DefaultMaxRetries=3 before flipping terminal.
		// PR-011B will additionally surface ErrEnrichmentInvalidLLMResponse
		// on parse failures (also retryable up to 3, then terminal).
		return nil, err
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(80, "PR-011B/C: persist + emit outbox (forward-pointer)")
	}

	// ── Step 4 (PR-011B): UPDATE media_assets.metadata_json ──
	// Forward-pointer: PR-011B will call
	// h.AssetRepo.UpdateEnrichedMetadata(ctx, row.ID, enriched.Fields)
	// here with the LLM response. The method is declared on the
	// AssetRepository port but the call site is gated on the
	// future adapter returning a non-nil *EnrichmentResponse.
	//
	// ── Step 5 (PR-011C): emit asset.published v1 outbox ─────
	// Forward-pointer: PR-011C will emit
	// outboxevents.NewEnvelope(EventAssetPublished, SchemaVersionAssetPublished,
	//     assetPublishedRequestV1{...enriched fields...})
	// so the IndexingHandler re-upserts the chunk to Qdrant with
	// the enriched fields.

	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "PR-011A stub handler: LLM call exercised the retry path (PR-011B/C forward-pointer)")
	}

	// PR-011A: the handler does not yet produce a non-trivial
	// result map (the persistence + outbox emit land in PR-011B+C).
	// Return a minimal result envelope so the broker's mark-SUCCEEDED
	// seam is reachable end-to-end.
	return map[string]any{
		"chunk_id":      row.ID,
		"handler_stage": "pr011a_stub_llm_called",
		"model":         h.LLMClient.Model(),
	}, nil
}

// SQLiteAssetRepository is the PR-011A production concrete
// AssetRepository. Wraps *sql.DB and reads media_assets by id
// + updates metadata_json (PR-011B will call the update path).
//
// godlike/06 SSOT (one canonical owner per fact):
// SQLiteAssetRepository lives ONLY in this file. The composition
// root wires this concrete via fluent setter; future production
// concretes (e.g. a sharded repository for high-throughput
// deployments) MUST implement the same AssetRepository port and
// be injected via the same fluent setter.
type SQLiteAssetRepository struct {
	// DB is the canonical *sql.DB handle. The composition root
	// injects the same DB handle the broker uses (canonical
	// SSOT for "which DB does the system read from").
	DB *sql.DB
}

// NewSQLiteAssetRepository constructs the canonical concrete
// with fail-closed nil-DB gate per godlike/07 typed-error
// contract. Returns (nil, ErrEnrichmentHandlerNotConfigured)
// when DB is nil.
func NewSQLiteAssetRepository(db *sql.DB) (*SQLiteAssetRepository, error) {
	if db == nil {
		return nil, WrapHandlerNotConfigured("db")
	}
	return &SQLiteAssetRepository{DB: db}, nil
}

// GetByID reads the canonical media_assets row by id. Returns
// (nil, WrapChunkNotFound(id)) when the row is absent
// (sql.ErrNoRows) — the canonical terminal sentinel.
// Other SQL errors wrap WrapPersistFailed (SQL-side diagnostic).
func (r *SQLiteAssetRepository) GetByID(ctx context.Context, id string) (*AssetRow, error) {
	if r == nil || r.DB == nil {
		return nil, WrapHandlerNotConfigured("repo")
	}
	row := r.DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(source_url, ''), COALESCE(title, ''),
		       COALESCE(description, ''), COALESCE(start_sec, 0.0),
		       COALESCE(end_sec, 0.0), COALESCE(source_provider, '')
		FROM media_assets
		WHERE id = ?
	`, id)
	var out AssetRow
	if err := row.Scan(&out.ID, &out.SourceURL, &out.Title, &out.Description, &out.StartSec, &out.EndSec, &out.SourceProvider); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, WrapChunkNotFound(id)
		}
		return nil, WrapPersistFailed(err)
	}
	return &out, nil
}

// UpdateEnrichedMetadata persists the EnrichedFields into
// media_assets.metadata_json. PR-011A: declared for future
// use; the handler does NOT yet call this method (the stub
// LLM client returns ErrEnrichmentLLMUnavailable before the
// call site is reached). PR-011B will replace the call site.
//
// godlike/07 minimum-blast-radius: the implementation is
// idempotent on retry (UPDATE is naturally idempotent given
// the same EnrichedFields input). The metadata_json column
// shape mirrors the PR-001..PR-009 wire-format (JSON
// encoding of the 6 LLM-only fields).
func (r *SQLiteAssetRepository) UpdateEnrichedMetadata(ctx context.Context, id string, fields EnrichedFields) error {
	if r == nil || r.DB == nil {
		return WrapHandlerNotConfigured("repo")
	}
	metaJSON, err := json.Marshal(fields)
	if err != nil {
		return WrapInvalidLLMResponse(err)
	}
	_, err = r.DB.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, string(metaJSON), id)
	if err != nil {
		return WrapPersistFailed(err)
	}
	return nil
}

// Compile-time assertion: *SQLiteAssetRepository satisfies the
// AssetRepository port. Catches signature drift at compile time
// per AGENTS.md Pattern 0 / godlike/06 SSOT.
//
// Note: there is no compile-time pin for *EnrichmentHandler →
// appjobs.Handler because the appjobs surface uses a HandlerFunc
// adapter (not a named interface) for broker registration. The
// adapter handles the signature conversion from
// `func(ctx, *appjobs.Job, *appjobs.JobTools) (map[string]any, error)`
// to the domain Handler type. The RegisterHandler call site
// validates the signature at registration time.
var _ AssetRepository = (*SQLiteAssetRepository)(nil)

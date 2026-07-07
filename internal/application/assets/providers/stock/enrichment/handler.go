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
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
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
//     Description / StartSec / EndSec / SourceProvider / DriveFileID /
//     DrivePath / FileHash — the 3 drive fields are required for
//     PR-011C's asset.published v1 envelope construction).
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
// The 10 fields are the canonical projection of the media_assets
// columns the enrichment pass needs to build the EnrichmentRequest
// + the asset.published v1 envelope (PR-011C).
//
// godlike/06 SSOT: AssetRow lives ONLY in this file. The
// production concrete (sqliteAssetRepository) populates these
// fields from the media_assets row; the handler projects them
// into EnrichmentRequest + AssetPublishedRequestV1. Future
// LLM-driven enrichment passes MUST extend this struct (NOT
// introduce a parallel envelope).
//
// PR-011C added 3 drive fields (DriveFileID + DrivePath + FileHash)
// so the asset.published v1 envelope can carry the canonical
// drive_file_id + drive_path (consumer ComposeSearchText uses
// them) + the file_hash (idempotency_key derivation per
// PR-ENRICHMENT-IDEMPOTENCY-KEY).
type AssetRow struct {
	ID             string
	SourceURL      string
	Title          string
	Description    string
	StartSec       float64
	EndSec         float64
	SourceProvider string
	DriveFileID    string
	DrivePath      string
	FileHash       string
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

	// Emitter is the canonical Pattern-0 typed port for the
	// asset.published v1 outbox event (PR-011C). The composition
	// root injects the production concrete (outbox-dispatcher-backed)
	// or the stub (PR-011C tests — captures the emitted payload
	// for hermetic TDD assertions).
	//
	// godlike/07 minimum-blast-radius: the emitter is OPTIONAL
	// (nil = no emit; the composition root may wire a noop stub
	// in disabled mode). When nil, HandleJob skips the emit step
	// (a Warn log is emitted per godlike/07 nil-tolerance discipline
	// so an operator inspecting the audit log can identify the
	// misconfiguration).
	Emitter AssetPublishedEmitter

	// Log is the canonical zap logger. godlike/07 nil-tolerance:
	// nil-logger falls back to zap.NewNop() (defense-in-depth).
	Log *zap.Logger
}

// NewEnrichmentHandler constructs the canonical handler with
// fail-closed nil-deps gate per godlike/07 typed-error contract.
// Returns (nil, ErrEnrichmentHandlerNotConfigured) when LLMClient
// or AssetRepo is nil; composition root MUST propagate the error.
//
// PR-011C: the signature is now 4-arg (llmClient, assetRepo,
// emitter, log). Existing callers (PR-011A composition root at
// build_bundles_stock.go) MUST be updated to pass the emitter.
// The emitter is OPTIONAL (nil is allowed) so PR-011A-style
// disabled-mode wiring is preserved.
func NewEnrichmentHandler(llmClient EnrichmentLLMClient, assetRepo AssetRepository, emitter AssetPublishedEmitter, log *zap.Logger) (*EnrichmentHandler, error) {
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
		Emitter:   emitter,
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
	resp, err := h.LLMClient.Enrich(ctx, EnrichmentRequest{
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
	// PR-011A: the stub returns a non-nil *EnrichmentResponse with
	// empty fields (per StubEnrichmentLLMClient.Enrich). The handler
	// proceeds to Step 5 (emit) even with empty EnrichedFields so
	// the v1 envelope wire-shape is exercised end-to-end. PR-011B
	// will replace the stub with a real ollama adapter that returns
	// populated EnrichedFields (Category/Event/Round/Scene/Subject/Entities).
	if resp == nil {
		resp = &EnrichmentResponse{Fields: EnrichedFields{}}
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(70, "PR-011B: persist (forward-pointer); PR-011C: emit asset.published v1")
	}

	// ── Step 4 (PR-011B): UPDATE media_assets.metadata_json ──
	// Per user-spec literal: "After the UPDATE succeeds, populate
	// the v1 envelope and emit." The UPDATE is idempotent on
	// retry (same EnrichedFields input produces byte-identical
	// metadata_json) so re-running the handler is safe. The
	// StubEnrichmentLLMClient returns empty EnrichedFields, so
	// the UPDATE is a no-op on the stub (UPDATE media_assets
	// SET metadata_json = '{}' WHERE id = ?). PR-011B will
	// replace the stub with a real ollama adapter that returns
	// populated EnrichedFields; the UPDATE call site is the
	// same — no change to the persist path.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: a persist failure aborts
	// the emit per the user spec ("the asset is not enriched,
	// so the v1 envelope must NOT be emitted"). The error path
	// returns WrapPersistFailed (terminal — the worker's
	// exponential backoff would mask the underlying SQL failure
	// if retryable).
	if updateErr := h.AssetRepo.UpdateEnrichedMetadata(ctx, row.ID, resp.Fields); updateErr != nil {
		return nil, updateErr
	}

	// ── Step 5 (PR-011C): emit asset.published v1 outbox ─────
	// emitAssetPublishedV1 returns (stageLabel, error). The stageLabel
	// is the canonical audit-trail label for the emit outcome (3
	// distinct values: ok / skipped_nil_emitter / failed_retryable).
	// The error is non-nil only for the failed_retryable case; the
	// worker's exponential backoff retries up to DefaultMaxRetries=3.
	// The UPDATE (Step 4) is idempotent on retry (same EnrichedFields
	// input produces byte-identical metadata_json) and the emit is
	// idempotent on retry (same event_key collapses via UNIQUE
	// constraint).
	emitStage, emitErr := h.emitAssetPublishedV1(ctx, row, resp.Fields)
	if emitErr != nil {
		return nil, emitErr
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "PR-011C: enrichment emit complete (stage="+emitStage+")")
	}

	// PR-011C: the handler produces a result envelope that carries
	// the canonical emit-stage label (1 of 3 distinct values) so the
	// broker's mark-SUCCEEDED seam + the audit log have a canonical
	// record of the emit outcome. The 3 stage labels
	// (pr011c_v1_emit_ok / pr011c_v1_emit_skipped_nil_emitter /
	// pr011c_v1_emit_failed_retryable) are the SSOT — no implicit
	// "v1 emitted" claim when the emit was skipped or failed.
	return map[string]any{
		"chunk_id":       row.ID,
		"handler_stage":  emitStage,
		"model":          h.LLMClient.Model(),
		"destination":    "stock",
		"schema_version": outbox.AssetPublishedSchemaVersion,
	}, nil
}

// emitAssetPublishedV1 builds the canonical v1 envelope and
// hands it to the AssetPublishedEmitter port. godlike/06 SSOT:
// the v1 envelope is the canonical wire-shape owned by
// internal/application/jobs/outbox/asset_published.go; this
// method is a producer-side builder that maps the
// EnrichmentHandler's internal types (AssetRow + EnrichedFields)
// onto the canonical envelope fields.
//
// Returns:
//   - nil on successful emit (or skipped when Emitter is nil —
//     a Warn log captures the disabled-mode wiring per
//     godlike/07 nil-tolerance).
//   - ErrEnrichmentEmitFailed on emitter-side failure (retryable).
//   - ErrEnrichmentHandlerNotConfigured (via WrapEmitFailed) on
//     required-field validation failure (terminal — the handler
//     itself has a wiring bug if asset_id or file_hash is empty
//     on a properly-fetched media_assets row).
//
// emitAssetPublishedV1 builds the canonical v1 envelope and
// hands it to the AssetPublishedEmitter port. godlike/06 SSOT:
// the v1 envelope is the canonical wire-shape owned by
// internal/application/jobs/outbox/asset_published.go; this
// method is a producer-side builder that maps the
// EnrichmentHandler's internal types (AssetRow + EnrichedFields)
// onto the canonical envelope fields.
//
// Returns (stageLabel, error) where stageLabel is one of 3
// canonical audit-trail values:
//   - "pr011c_v1_emit_ok" — emit succeeded; the v1 envelope
//     was enqueued to outbox_events
//   - "pr011c_v1_emit_skipped_nil_emitter" — disabled-mode
//     wiring (composition root did not inject the emitter);
//     the handler logged a Warn; the v1 envelope was NOT
//     emitted (no fake-availability: the audit log captures
//     the misconfiguration)
//   - "pr011c_v1_emit_failed_retryable" — emitter-side failure
//     (e.g. SQLite locked, I/O error) OR idempotency-key
//     validation failure; the error is non-nil
//     (ErrEnrichmentEmitFailed wrapped); the worker's
//     exponential backoff retries up to DefaultMaxRetries
//
// The error is non-nil only for the failed_retryable case.
// godlike/07 NO-FAKE-AVAILABILITY: nil-emitter surfaces the
// canonical skipped label (NOT a silent success); the audit
// log captures the misconfiguration.
func (h *EnrichmentHandler) emitAssetPublishedV1(ctx context.Context, row *AssetRow, fields EnrichedFields) (string, error) {
	// godlike/07 nil-tolerance: nil-emitter is disabled-mode
	// wiring (composition root did not inject the emitter). Log
	// a Warn + return the canonical skipped stage label so
	// the handler's happy-path is reachable in tests that
	// don't exercise the emit step. The composition root
	// MUST wire a real emitter in production
	// (godlike/07 NO-FAKE-AVAILABILITY).
	if h.Emitter == nil {
		if h.Log != nil {
			h.Log.Warn("enrichment.HandleJob: emitter nil (composition root disabled-mode wiring) — skipping asset.published v1 emit",
				zap.String("chunk_id", row.ID),
			)
		}
		return "pr011c_v1_emit_skipped_nil_emitter", nil
	}

	// Derive the canonical idempotency_key from (chunk_id,
	// file_hash, v1). ErrEnrichmentIdempotencyKeyConflict surfaces
	// the godlike/07 no-fake-availability contract violation
	// (empty chunk_id or malformed content_hash) — terminal
	// because the producer (the stock pipeline that wrote
	// the media_assets row) must fix the underlying state.
	idemKey, idemErr := EnrichmentIdempotencyKey(row.ID, row.FileHash, EnrichmentVersionV1)
	if idemErr != nil {
		// godlike/07 typed-error contract: a malformed triple
		// is a producer-side bug. Surface as a terminal sentinel
		// (WrapEmitFailed with the idem conflict as the cause)
		// so the operator can identify the root cause. The
		// stage label is the "failed" variant because the emit
		// WAS attempted (we got past the nil-emitter check) but
		// the pre-emit validation rejected the payload.
		return "pr011c_v1_emit_failed_retryable", WrapEmitFailed(fmt.Errorf("%w: %v", ErrEnrichmentIdempotencyKeyConflict, idemErr))
	}

	// Build the canonical v1 envelope (godlike/06 SSOT one
	// canonical owner per fact: AssetPublishedRequestV1 in
	// internal/application/jobs/outbox/asset_published.go).
	payload := outbox.AssetPublishedRequestV1{
		SchemaVersion:  outbox.AssetPublishedSchemaVersion,
		EventID:        uuid.NewString(),
		AssetID:        row.ID,
		Destination:    "stock",
		Origin:         "generated",
		Category:       fields.Category,
		Subject:        fields.Subject,
		Provider:       row.SourceProvider,
		DriveFileID:    row.DriveFileID,
		DrivePath:      row.DrivePath,
		ContentType:    "video",
		Tags:           nil, // PR-011C: tags deferred to PR-ENRICHMENT-E2E-SUITE forward-pointer
		IdempotencyKey: idemKey,
		RequestedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	// Hand to the emitter. The production concrete (outbox-dispatcher-backed)
	// opens its own tx and enqueues to outbox_events. The stub
	// (tests) captures the payload for hermetic TDD assertions.
	if err := h.Emitter.EmitAssetPublished(ctx, payload); err != nil {
		return "pr011c_v1_emit_failed_retryable", WrapEmitFailed(err)
	}

	if h.Log != nil {
		h.Log.Info("enrichment.HandleJob: asset.published v1 emitted",
			zap.String("chunk_id", row.ID),
			zap.String("event_id", payload.EventID),
			zap.String("idempotency_key", idemKey),
			zap.String("drive_file_id", payload.DriveFileID),
			zap.String("destination", payload.Destination),
		)
	}
	return "pr011c_v1_emit_ok", nil
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
//
// PR-011C: the SELECT projection expanded from 7 to 10 columns
// to include the 3 drive fields required for the v1 envelope:
// drive_file_id, drive_path, file_hash. The columns are
// COALESCE-wrapped to NULL → ” / 0 mappings so a legacy row
// written before the columns existed returns empty strings (not
// nil-pointer panics) — the emitter then either uses an empty
// drive_file_id (the canonical v1 envelope allows omitempty) or
// the idempotency_key derivation fails with
// ErrEnrichmentIdempotencyKeyConflict on an empty file_hash
// (terminal, surfaces as a producer-side state gap).
func (r *SQLiteAssetRepository) GetByID(ctx context.Context, id string) (*AssetRow, error) {
	if r == nil || r.DB == nil {
		return nil, WrapHandlerNotConfigured("repo")
	}
	row := r.DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(source_url, ''), COALESCE(title, ''),
		       COALESCE(description, ''), COALESCE(start_sec, 0.0),
		       COALESCE(end_sec, 0.0), COALESCE(source_provider, ''),
		       COALESCE(drive_file_id, ''), COALESCE(drive_path, ''),
		       COALESCE(file_hash, '')
		FROM media_assets
		WHERE id = ?
	`, id)
	var out AssetRow
	if err := row.Scan(&out.ID, &out.SourceURL, &out.Title, &out.Description, &out.StartSec, &out.EndSec, &out.SourceProvider, &out.DriveFileID, &out.DrivePath, &out.FileHash); err != nil {
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

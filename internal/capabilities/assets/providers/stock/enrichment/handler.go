// Package enrichment — canonical broker entry-point for the stock
// post-publish RLM/LLM enrichment pass.
//
// Invariant: parse payload → read media_assets row → call LLM →
// persist enriched metadata → emit asset.published v1 outbox event.
//
// godlike/06 SSOT: EnrichmentHandler + HandleJob live ONLY here;
// typed sentinels live ONLY in errors.go; LLM client port lives
// ONLY in llm_client.go; AssetRepository concrete lives ONLY in
// handler_repository.go; emitAssetPublishedV1 lives ONLY in handler_emit.go.
//
// godlike/07 fail-closed: NewEnrichmentHandler errors on nil LLMClient
// or AssetRepo; HandleJob returns typed sentinels on every failure;
// the handler respects ctx cancellation.
package enrichment

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"encoding/json"
	"fmt"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"go.uber.org/zap"
)

// AssetRepository is the canonical Pattern-0 typed port for
// "read media_assets row by id + update metadata_json".
// godlike/06 SSOT: this interface is the SOLE definition of
// the asset-repository contract for the enrichment pass.
//
// The 2 methods are the minimal surface for the enrichment pass:
//   - GetByID returns the canonical media_assets row needed
//     to build the EnrichmentRequest (SourceURL / Title /
//     Description / StartSec / EndSec / SourceProvider / DriveFileID /
//     DrivePath / LegacyFileMD5 — the 3 drive fields are required for
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

// AssetMetadataUpdater is the canonical metadata mutation port. Production
// wires the SQLite MediaCommitter implementation at the composition root;
// this application package never owns media_assets SQL.
type AssetMetadataUpdater interface {
	UpdateAssetMetadata(ctx context.Context, assetID, metadataJSON string) error
}

// AssetRow is the typed read-back envelope from GetByID.
// The 10 fields are the canonical projection of the media_assets
// columns the enrichment pass needs to build the EnrichmentRequest
// + the asset.published v1 envelope (PR-011C).
//
// godlike/06 SSOT: AssetRow lives ONLY in this file. The
// production concrete (SQLiteAssetRepository in handler_repository.go)
// populates these fields from the media_assets row; the handler
// projects them into EnrichmentRequest + AssetPublishedRequestV1.
// Future LLM-driven enrichment passes MUST extend this struct (NOT
// introduce a parallel envelope).
//
// PR-011C added 3 drive fields (DriveFileID + DrivePath + LegacyFileMD5)
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
	LegacyFileMD5  string
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
// jobs.Service dispatcher. Returns error on registration
// failure (duplicate handler, frozen registry). Composition root
// MUST propagate the error (NOT silently default to a no-op
// registration).
func (h *EnrichmentHandler) RegisterHandler(jobsSvc *jobs.Service) error {
	if h == nil {
		return WrapHandlerNotConfigured("handler")
	}
	if jobsSvc == nil {
		return WrapHandlerNotConfigured("jobsSvc")
	}
	if err := jobsSvc.RegisterHandler(jobs.TypeMediaStockRLMEnrich, jobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("enrichment.RegisterHandler: bind %q to dispatcher: %w", jobs.TypeMediaStockRLMEnrich, err)
	}
	h.Log.Info("registered media.stock_rlm_enrich job handler", zap.String("type", jobs.TypeMediaStockRLMEnrich))
	return nil
}

// HandleJob is the canonical broker entry-point. Parses the
// job payload (chunk_id), reads the media_assets row, calls
// the LLM client, persists the enrichment and emits
// the asset.published outbox event.
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
func (h *EnrichmentHandler) HandleJob(ctx context.Context, job *jobs.Job, tools *jobs.JobTools) (map[string]any, error) {
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
		"schema_version": jobs.AssetPublishedSchemaVersion,
	}, nil
}

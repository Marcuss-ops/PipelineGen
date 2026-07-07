// Package enrichment — llm_client.go (PR-011A, July 2026).
//
// Pattern-0 typed port for the stock RLM/LLM enrichment LLM client.
// The canonical port per godlike/06 SSOT: EnrichmentLLMClient lives
// ONLY in this file; future adapters (ollama, vllm, nvidia, mock)
// implement this interface and are injected via composition root
// fluent setters per AGENTS.md Pattern 0.
//
// PR-011A scope: declares the typed contract + a stub adapter that
// returns ErrEnrichmentLLMUnavailable (so the worker retry path
// is exercised end-to-end without a real ollama call). PR-011B
// will replace the stub with a real ollama-backed adapter that
// parses the response into EnrichedFields and surfaces parse
// errors via ErrEnrichmentInvalidLLMResponse.
//
// godlike/07 typed-error contract: every error returned from
// Enrich() must be a typed sentinel reachable via errors.Is().
// The stub adapter returns ErrEnrichmentLLMUnavailable verbatim;
// the future ollama adapter will return
// ErrEnrichmentInvalidLLMResponse on parse failures and
// ErrEnrichmentLLMUnavailable on network errors.
//
// godlike/07 minimum-blast-radius: the port is additive — the
// stock pipeline (PR-001..PR-009 chain) does not call this
// port directly. The EnrichmentHandler consumes it; the
// EnrichmentHandler is the canonical SOLE owner of
// "when do we call the LLM and what do we do with the
// response". The port exposes only the minimal surface needed
// (input + output) so the LLM call site has one canonical
// entry point.
package enrichment

import (
	"context"
	"errors"
	"time"
)

// EnrichedFields is the typed result envelope returned by
// EnrichmentLLMClient.Enrich. The 6 LLM-only fields mirror the
// ChunkMetadataEntry extension shipped in PR-007 (LLM enrichment
// plumbing) — PR-008 wire-shape projected them as IndexingStatus
// literal; PR-011B+C will populate them from the LLM response.
//
// godlike/06 SSOT (one canonical owner per fact): EnrichedFields
// lives ONLY in this file. The 6 fields are the canonical set
// per user spec (Category / Event / Round / Scene / Subject /
// Entities); future enrichment passes MUST extend this struct
// (NOT introduce a parallel envelope) to preserve one-canonical-
// owner-per-fact discipline.
//
// JSON tags mirror the metadata.json shape from PR-001..PR-009;
// the canonical SSOT pair (EnrichedFields + ChunkMetadataEntry)
// share the same JSON tag namespace so the ollama JSON-mode
// response deserializes byte-equivalently into either envelope.
type EnrichedFields struct {
	// Category is the LLM-inferred content category
	// (e.g. "Boxe", "Sport", "Documentario"). Free-form string —
	// the LLM prompt asks for the single most specific category
	// from the canonical taxonomy.
	Category string `json:"category"`

	// Event is the LLM-inferred event / fight / match name
	// (e.g. "Pacquiao vs Broner"). Empty when the chunk is not
	// associated with a specific event.
	Event string `json:"event,omitempty"`

	// Round is the LLM-inferred round number (e.g. "5") for
	// boxing / MMA content. Empty when the content is not
	// round-annotated.
	Round string `json:"round,omitempty"`

	// Scene is the LLM-inferred scene description
	// (e.g. "Combination in the corner"). Free-form — the
	// LLM prompt asks for a 5-15 word description of the
	// visible action.
	Scene string `json:"scene,omitempty"`

	// Subject is the LLM-inferred primary subject / protagonist
	// (e.g. "Mike Tyson", "Manny Pacquiao"). Empty when the
	// content has no specific subject.
	Subject string `json:"subject,omitempty"`

	// Entities is the LLM-extracted list of named entities
	// (people, places, organizations). The list is bounded
	// at 5 entries per the canonical prompt template
	// (matches Artlist phrases cap precedent).
	Entities []string `json:"entities,omitempty"`
}

// EnrichmentRequest is the typed input envelope for the LLM
// enrichment call. The metadata fields are sourced from the
// media_assets row the handler reads; the LLM uses them as
// context to produce the EnrichedFields output.
type EnrichmentRequest struct {
	// ChunkID is the canonical media_assets.id (caller's job
	// payload chunk_id). Echoed back in the response for
	// downstream audit logging.
	ChunkID string `json:"chunk_id"`

	// SourceURL is the original stock source URL (e.g. pexels
	// clip URL). Optional — the LLM uses it as context for
	// category inference.
	SourceURL string `json:"source_url,omitempty"`

	// Title is the chunk title (mirrors ChunkState.Title from
	// PR-007). Optional — the LLM uses it as context.
	Title string `json:"title,omitempty"`

	// Description is the chunk description (operator-supplied
	// or empty). Optional.
	Description string `json:"description,omitempty"`

	// StartSec is the clip start timestamp in seconds.
	// Optional.
	StartSec float64 `json:"start_sec,omitempty"`

	// EndSec is the clip end timestamp in seconds.
	// Optional.
	EndSec float64 `json:"end_sec,omitempty"`

	// SourceProvider is the canonical source identifier
	// (e.g. "pexels", "pixabay"). The LLM uses it to
	// adjust category vocabulary per provider conventions.
	SourceProvider string `json:"source_provider,omitempty"`
}

// EnrichmentResponse is the typed output envelope from the LLM
// enrichment call. The handler projects the response into
// media_assets.metadata_json + re-emits the asset.published
// outbox event (PR-011C).
type EnrichmentResponse struct {
	// ChunkID mirrors the input request (canonical SSOT
	// echo for downstream audit log correlation).
	ChunkID string `json:"chunk_id"`

	// Fields is the LLM-inferred enrichment. The 6 fields
	// are populated by the LLM prompt template.
	Fields EnrichedFields `json:"fields"`

	// Model is the actual model identifier used (mirrors
	// cfg.External.ParseArenaLLM at call time, or the
	// fallback cfg.External.OllamaModel when empty).
	Model string `json:"model"`

	// Elapsed is the wall-clock duration of the LLM call.
	// Operator-facing observability — surfaces on the
	// dashboard alongside the job's terminal status.
	Elapsed time.Duration `json:"elapsed_ms,omitempty"`
}

// EnrichmentLLMClient is the canonical Pattern-0 typed port
// for the LLM client used by the stock RLM/LLM enrichment pass.
// godlike/06 SSOT: this interface is the SOLE definition of
// the LLM-client contract; no other package redefines the
// signature.
//
// Implementations:
//   - enrichment.stubEnrichmentLLMClient (PR-011A — returns
//     ErrEnrichmentLLMUnavailable to drive the worker retry path)
//   - enrichment.ollamaEnrichmentLLMClient (PR-011B — wraps
//     internal/infrastructure/ai/ollama/client.Client.Chat)
//
// The future ollama adapter is FORWARD-POINTED to PR-011B;
// this PR ships only the typed contract + the stub.
type EnrichmentLLMClient interface {
	// Enrich sends the request to the LLM and returns the
	// typed EnrichedFields. Implementations MUST return a
	// typed sentinel on any failure mode (LLM unreachable →
	// ErrEnrichmentLLMUnavailable; response parse failure →
	// ErrEnrichmentInvalidLLMResponse).
	Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error)

	// Model returns the LLM model identifier currently
	// configured (mirrors cfg.External.ParseArenaLLM with
	// cfg.External.OllamaModel fallback). Used by the
	// handler for audit logging and dashboard rendering.
	Model() string
}

// StubEnrichmentLLMClient is the PR-011A stub adapter. Every
// Enrich() call returns ErrEnrichmentLLMUnavailable so the
// worker retry path is exercised end-to-end without a real
// ollama call. PR-011B will replace this stub with a real
// ollama-backed adapter; the typed contract is byte-stable
// across the migration.
//
// godlike/07 minimum-blast-radius: the stub is the
// composition-root default when cfg.External.StockEnrichmentEnabled
// is true but no real ollama adapter is wired (dev / test
// environments). Production deployments wire the real
// ollama adapter via fluent setter per AGENTS.md Pattern 0.
//
// godlike/06 SSOT (one canonical owner per fact):
// StubEnrichmentLLMClient lives ONLY in this file.
type StubEnrichmentLLMClient struct {
	// modelName is the model identifier the stub reports
	// via Model(). Mirrors the future real adapter's getter
	// contract so handlers can log the model without
	// special-casing stub vs real. Unexported to avoid
	// Go's field/method name collision with the Model()
	// method below.
	modelName string
}

// NewStubEnrichmentLLMClient constructs a stub adapter with
// the canonical model identifier. When model is empty,
// defaults to "stub:enrichment-unavailable" so audit logs
// can distinguish stub-originated responses from real-llm
// responses in the dashboard.
func NewStubEnrichmentLLMClient(model string) *StubEnrichmentLLMClient {
	if model == "" {
		model = "stub:enrichment-unavailable"
	}
	return &StubEnrichmentLLMClient{modelName: model}
}

// Enrich returns ErrEnrichmentLLMUnavailable verbatim per
// godlike/07 typed-error contract. The chunk_id is preserved
// in the wrapped error message for operator log correlation.
func (s *StubEnrichmentLLMClient) Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error) {
	if req.ChunkID == "" {
		return nil, WrapHandlerNotConfigured("chunk_id")
	}
	return nil, WrapLLMUnavailable(errors.New("stub adapter: PR-011B will wire the real ollama call"))
}

// Model returns the configured stub model identifier.
func (s *StubEnrichmentLLMClient) Model() string {
	return s.modelName
}

// Compile-time assertion: *StubEnrichmentLLMClient satisfies
// EnrichmentLLMClient. Catches signature drift at compile
// time per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ EnrichmentLLMClient = (*StubEnrichmentLLMClient)(nil)

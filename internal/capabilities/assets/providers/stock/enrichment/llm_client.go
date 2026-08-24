// Package enrichment — llm_client.go (PR-SPLIT-LLM-CLIENT, 2026-08-08).
//
// Slim orchestrator: the canonical Pattern-0 typed port
// (EnrichmentLLMClient) + the StubEnrichmentLLMClient adapter.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - EnrichmentLLMClient interface lives ONLY in this file.
//   - StubEnrichmentLLMClient (PR-011A) lives ONLY in this file.
//   - the real ollama-backed adapter lives in llm_client_ollama.go.
//   - the request/response wire shape lives in llm_client_request.go.
//
// PR-011A scope: the stub adapter returns ErrEnrichmentLLMUnavailable
// to drive the worker retry path end-to-end without a real ollama
// call. PR-011B migrates the composition root from stub → ollama
// without touching this file (the typed contract is byte-stable
// across the migration).
package assets

import (
	"context"
	"errors"
)

// EnrichmentLLMClient is the canonical Pattern-0 typed port for the
// LLM client used by the stock RLM/LLM enrichment pass.
//
// godlike/06 SSOT: this interface is the SOLE definition of the
// LLM-client contract; no other package redefines the signature.
type EnrichmentLLMClient interface {
	// Enrich sends the request to the LLM and returns the typed
	// EnrichedFields. Implementations MUST return a typed sentinel
	// on any failure mode (LLM unreachable → ErrEnrichmentLLMUnavailable;
	// response parse failure → ErrEnrichmentInvalidLLMResponse).
	Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error)

	// Model returns the LLM model identifier currently configured.
	// Used by the handler for audit logging and dashboard rendering.
	Model() string
}

// StubEnrichmentLLMClient is the PR-011A stub adapter. Every Enrich()
// call returns ErrEnrichmentLLMUnavailable so the worker retry path
// is exercised end-to-end without a real ollama call. PR-011B will
// replace this stub with a real ollama-backed adapter.
//
// godlike/06 SSOT: StubEnrichmentLLMClient lives ONLY in this file.
type StubEnrichmentLLMClient struct {
	// modelName is the model identifier the stub reports
	// via Model(). Mirrors the future real adapter's getter
	// contract so handlers can log the model without
	// special-casing stub vs real.
	modelName string
}

// NewStubEnrichmentLLMClient constructs a stub adapter with the
// canonical model identifier. When model is empty, defaults to
// "stub:enrichment-unavailable" so audit logs can distinguish
// stub-originated responses from real-llm responses.
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
// EnrichmentLLMClient per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ EnrichmentLLMClient = (*StubEnrichmentLLMClient)(nil)

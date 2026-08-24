// Package enrichment — llm_client_request.go (PR-SPLIT-LLM-CLIENT, 2026-08-08).
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// 6-field EnrichedFields envelope + the wire-shape request/response
// types + the user-prompt builder + the log-preview helper live
// ONLY in this file. Future enrichment passes that need to evolve
// the wire shape MUST extend this file (NOT introduce a parallel
// envelope) to preserve one-canonical-owner-per-fact.
package assets

import (
	"fmt"
	"strings"
	"time"
)

// EnrichedFields is the typed result envelope returned by
// EnrichmentLLMClient.Enrich. The 6 LLM-only fields mirror the
// ChunkMetadataEntry extension shipped in PR-007 (LLM enrichment
// plumbing).
//
// JSON tags mirror the metadata.json shape from PR-001..PR-009;
// the canonical SSOT pair (EnrichedFields + ChunkMetadataEntry)
// share the same JSON tag namespace so the ollama JSON-mode
// response deserializes byte-equivalently into either envelope.
type EnrichedFields struct {
	// Category is the LLM-inferred content category
	// (e.g. "Boxe", "Sport", "Documentario").
	Category string `json:"category"`

	// Event is the LLM-inferred event / fight / match name.
	Event string `json:"event,omitempty"`

	// Round is the LLM-inferred round number (boxing / MMA).
	Round string `json:"round,omitempty"`

	// Scene is the LLM-inferred scene description.
	Scene string `json:"scene,omitempty"`

	// Subject is the LLM-inferred primary subject / protagonist.
	Subject string `json:"subject,omitempty"`

	// Entities is the LLM-extracted list of named entities
	// (people, places, organizations). Bounded at 5 entries.
	Entities []string `json:"entities,omitempty"`
}

// EnrichmentRequest is the typed input envelope for the LLM
// enrichment call. The metadata fields are sourced from the
// media_assets row the handler reads; the LLM uses them as
// context to produce the EnrichedFields output.
type EnrichmentRequest struct {
	// ChunkID is the canonical media_assets.id.
	ChunkID string `json:"chunk_id"`

	// SourceURL is the original stock source URL.
	SourceURL string `json:"source_url,omitempty"`

	// Title is the chunk title (mirrors ChunkState.Title from PR-007).
	Title string `json:"title,omitempty"`

	// Description is the chunk description (operator-supplied or empty).
	Description string `json:"description,omitempty"`

	// StartSec is the clip start timestamp in seconds.
	StartSec float64 `json:"start_sec,omitempty"`

	// EndSec is the clip end timestamp in seconds.
	EndSec float64 `json:"end_sec,omitempty"`

	// SourceProvider is the canonical source identifier.
	SourceProvider string `json:"source_provider,omitempty"`
}

// EnrichmentResponse is the typed output envelope from the LLM
// enrichment call. The handler projects the response into
// media_assets.metadata_json + re-emits the asset.published
// outbox event (PR-011C).
type EnrichmentResponse struct {
	// ChunkID mirrors the input request (canonical SSOT echo).
	ChunkID string `json:"chunk_id"`

	// Fields is the LLM-inferred enrichment.
	Fields EnrichedFields `json:"fields"`

	// Model is the actual model identifier used.
	Model string `json:"model"`

	// Elapsed is the wall-clock duration of the LLM call.
	Elapsed time.Duration `json:"elapsed_ms,omitempty"`
}

// buildEnrichmentUserPrompt constructs the user-message text
// from the EnrichmentRequest fields. Mirrors the canonical
// metadata-input shape (Title / Description / SourceProvider /
// SourceURL / StartSec / EndSec). Pure function (no side
// effects) for hermetic testability.
func buildEnrichmentUserPrompt(req EnrichmentRequest) string {
	var b strings.Builder
	b.WriteString("Enrich this video clip with the canonical 6-field metadata:\n\n")
	if req.Title != "" {
		b.WriteString("Title: ")
		b.WriteString(req.Title)
		b.WriteString("\n")
	}
	if req.Description != "" {
		b.WriteString("Description: ")
		b.WriteString(req.Description)
		b.WriteString("\n")
	}
	if req.SourceProvider != "" {
		b.WriteString("Source provider: ")
		b.WriteString(req.SourceProvider)
		b.WriteString("\n")
	}
	if req.SourceURL != "" {
		b.WriteString("Source URL: ")
		b.WriteString(req.SourceURL)
		b.WriteString("\n")
	}
	if req.StartSec != 0 || req.EndSec != 0 {
		b.WriteString(fmt.Sprintf("Time range: %.1fs - %.1fs\n", req.StartSec, req.EndSec))
	}
	return b.String()
}

// previewForLog returns a bounded-length preview of the LLM
// response for log inclusion in parse-failure error messages.
// Bounded at 200 chars to keep audit logs compact.
func previewForLog(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

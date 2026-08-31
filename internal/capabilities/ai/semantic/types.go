// Package semantic — types.go: the canonical DTO surface + port
// interface for the metadata writer capability (P0-#2, July 2026).
//
// P0-#2 closure (July 2026) retired the Phase 1.2 "DISABLED stub"
// `semantic.MetadataWriterPort` concrete. The previous design constructed
// an empty struct via `semantic.NewMetadataWriter(...)` that returned
// `ErrSemanticMetadataWriterDisabled` on every method call — a fake
// concrete that the composition root threaded through 22+ call sites
// as if it were a real service. This file replaces that with:
//
//  1. The canonical DTOs (WriteRequest, WriteResult, Payload,
//     AssetSemanticInput, ExtensionEntry) — extracted out of the
//     previous stub file so they live in a non-stub module and
//     remain importable when the real implementation is reintroduced.
//
//  2. The `MetadataWriterPort` interface — the canonical narrow
//     typed surface per AGENTS.md Pattern 0. Composition roots
//     depend on this port (NOT the concrete nopWriter in nop.go),
//     so a future real implementation is a drop-in replacement
//     without touching the consumers.
//
//  3. The `ErrSemanticMetadataWriterDisabled` typed sentinel — the
//     canonical "this capability is currently a noop" signal that
//     callers branch on via `errors.Is`.
//
// The previous `MetadataWriter` struct + `NewMetadataWriter`
// constructor are DELETED. Callers now either:
//
//   - Receive `nil` and nil-check at the call site (composition root
//     does not construct any nop when the backend is absent), OR
//
//   - Receive `semantic.NewNopMetadataWriter(log)` (an explicit nop
//     that logs the disabled marker + returns the typed sentinel
//     on every method call).
//
// The choice between nil and NewNopMetadataWriter is the
// composition root's: pass nil when no consumer can observe the
// sentinel (e.g., the production composition), and pass the nop
// when the consumer relies on the sentinel for graceful degradation
// (e.g., the soundeffect handler that logs and continues on the
// sentinel).
//
// godlike/07 NO-FAKE-AVAILABILITY: this file does NOT define any
// concrete implementation. The nop lives in nop.go (explicit
// degradation), and the real implementation is a future wave.
package semantic

import (
	"context"
	"errors"
)

// ErrSemanticMetadataWriterDisabled is the canonical typed sentinel
// returned by every MetadataWriterPort method when the implementation
// is a noop (the current state — no real Ollama/Python semantic
// tagger has been reintroduced per P0-#2 + P0.18).
//
// Per godlike/07 (no-fake-availability), callers can branch on
// `errors.Is(err, ErrSemanticMetadataWriterDisabled)` to detect the
// "real semantic tagger has not been reintroduced yet" condition and
// either surface a typed `metadata_disabled` API surface, route the
// request to the new enrichment port (P0.18), or fail loud at the
// composition root if a capability requires a real model.
var ErrSemanticMetadataWriterDisabled = errors.New("semantic: MetadataWriter is DISABLED / NOT_CONFIGURED (P0-#2 nop — real Ollama/Python semantic tagger has not been reintroduced; callers must branch on errors.Is(err, ErrSemanticMetadataWriterDisabled) per godlike/07)")

// MetadataWriterPort is the canonical narrow typed surface for the
// metadata writer capability (P0-#2, July 2026). Per AGENTS.md
// Pattern 0 (port abstraction layer, June 2026), composition roots
// and use cases depend on this port, NOT on any concrete.
//
// Implementations:
//
//   - `semantic.NewNopMetadataWriter(log)` — explicit nop that
//     logs the disabled marker on construction and returns
//     `ErrSemanticMetadataWriterDisabled` on every method call.
//
//   - Future: a real `*ollama.TaggerAdapter` (or equivalent) that
//     implements both methods against the canonical Ollama / Python
//     semantic-tagger pipeline. Drop-in replacement: no caller
//     changes required when the real implementation lands.
//
// Both methods are required (not split into separate ports) because
// the two production consumers (soundeffect handler + clips
// EnrichUseCase) historically consumed both shapes. Splitting the
// port would force both consumers to take two narrower ports,
// adding boilerplate without reducing the surface area meaningfully.
// The real implementation MUST implement both.
type MetadataWriterPort interface {
	// GeneratePayload produces the semantic metadata payload
	// (search_text / concept_tags / subjects / mood / …) for the
	// given WriteRequest. The string return value is reserved
	// for future forward-compat (was "" in the disabled stub
	// shape) and is currently ALWAYS "" for nop implementations.
	//
	// Errors: returns ErrSemanticMetadataWriterDisabled when
	// the implementation is a nop. Real implementations may return
	// a wrapped upstream error (TTS / Python / Ollama failure).
	GeneratePayload(ctx context.Context, req WriteRequest) (*Payload, string, error)

	// Write produces the semantic metadata payload AND persists it
	// to the canonical local artifact (the WriteResult.LocalPath
	// echoes req.LocalPath for the historical shape). Pre-fix the
	// method returned a synthetic WriteResult with a fabricated
	// LocalPath echo — godlike/07 violation. The current nop
	// returns (nil, ErrSemanticMetadataWriterDisabled); real
	// implementations return the populated WriteResult on success.
	Write(ctx context.Context, req WriteRequest) (*WriteResult, error)
}

// ── WriteRequest ──────────────────────────────────────────────────────

// WriteRequest carries the inputs for semantic metadata generation.
// The shape is the canonical pre-fix contract; 22+ production
// callers construct WriteRequest literals at the boundary to the
// nop. P0-#2 closure keeps the type unchanged for forward-compat
// with the future real tagger reintroduction (the same fields will
// be consumed by the real implementation's port adapter).
type WriteRequest struct {
	AssetID    string
	AssetType  string
	MediaType  string
	Source     string
	SourceType string
	Generator  string
	Retriever  string
	PageURL    string
	ImageURL   string
	License    string
	Author     string
	Style      string
	Prompt     string
	LocalPath  string
	TempDir    string
	Extensions []map[string]any
	GroupID    string
	Assets     []map[string]any
}

// ── WriteResult ───────────────────────────────────────────────────────

// WriteResult carries the output of a MetadataWriterPort.Write call.
// P0-#2 closure keeps the type unchanged for forward-compat;
// production callers reading WriteResult.LocalPath MUST branch on the
// returned err (errors.Is(err, ErrSemanticMetadataWriterDisabled))
// rather than dereferencing a nil WriteResult. The pre-fix WriteResult
// payload was a synthetic surface; the field signature is preserved
// so existing struct literal sites in caller code compile unchanged.
type WriteResult struct {
	LocalPath string
	Payload   *Payload
}

// ── Payload ────────────────────────────────────────────────────────────

// Payload holds the semantic metadata produced by the tagger. Type
// preserved unchanged for forward-compat; no production caller
// constructs Payload literals directly outside the nop and its
// tests. The fields mirror the canonicalis semantic-tagger output
// shape for the future real implementation.
type Payload struct {
	AssetID             string
	PromptOriginal      string
	Style               []string
	Tags                []string
	Subjects            []string
	SearchText          string
	AssetType           string
	SemanticDescription string
	ConceptTags         []string
	Mood                []string
	Categories          []string
	VisualObjects       []string
	EmotionalTone       []string
	RetrievalScore      *float64
}

// ── AssetSemanticInput ────────────────────────────────────────────────

// AssetSemanticInput carries the fields used to build a unified
// metadata map. The struct is consumed by BuildAssetMetadata (in
// metadata_builder.go) and is part of the canonical shared surface
// that production callers thread through the nop. Pre-fix callers
// from internal/capabilities/clips/enrich.go +
// internal/capabilities/assets/providers/artlist/semantic_enricher.go
// + others persisted this struct to wire payloads; the P0-#2
// closure keeps the type for forward-compat (the real semantic
// tagger pipeline reintroduced per P0.18 will reuse the same input
// shape — same fields, same JSON tags).
type AssetSemanticInput struct {
	AssetID             string
	AssetType           string
	Source              string
	MediaType           string
	Generator           string
	PromptOriginal      string
	SemanticDescription string
	SearchText          string
	Subjects            []string
	SubjectSlugs        []string
	Tags                []string
	Categories          []string
	Mood                []string
	Style               []string
	Confidence          float64
	EmbeddingStatus     string
	VisualEmbeddingJSON string
	PHash               string
	VisualDimensions    int
	Assets              []map[string]any
	Extra               map[string]any
}

// ── ExtensionEntry ────────────────────────────────────────────────────

// ExtensionEntry is the typed envelope some callers (soundeffect /
// video extensions via BuildVideoExtension) use to thread per-asset
// metadata-extension data. Kept in the shared surface for type-pinning
// symmetry with the extension builders in metadata_builder.go.
type ExtensionEntry map[string]any

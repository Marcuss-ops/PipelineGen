// Package semantic — semantic_stub.go: explicit DISABLED stub for the
// canonical MetadataWriter surface (Phase 1.2 closure, July 2026).
//
// The pre-closure semantic.NewMetadataWriter returned an empty struct
// whose Write / GeneratePayload methods produced synthetic Payload
// shells without invoking any LLM, Ollama, or stored canonical
// metadata. This was a silent fake-availability violation (godlike/07):
// callers were handed an apparently-valid Payload that the system
// accepted as if a real semantic tagger had run, while the audit chain
// confirmed no model was ever consulted.
//
// Phase 1.2 closure replaces this with:
//
//   - An explicit DISABLED / NOT_CONFIGURED log marker at constructor
//     time, so operators can grep for "semantic.MetadataWriter" in init
//     logs to confirm the no-op is the expected surface (NOT a missing
//     wiring).
//   - A typed sentinel ErrSemanticMetadataWriterDisabled (godlike/07
//     typed-error contract, errors.Is-able) returned by every Write /
//     GeneratePayload call. The methods now return (nil, "", sentinel)
//     — no synthetic Payload is fabricated.
//
// The canonical shared types (WriteRequest, WriteResult, Payload,
// AssetSemanticInput, ExtensionEntry) live in this file because they
// are CONSUMED by 22+ production callers across `internal/application/`,
// `internal/app/`, and `internal/api/`. Removing this package entirely
// would break those callers; the canonical migration path is a future
// wave that replaces `*semantic.MetadataWriter` with the new
// enrichment port documented in architecture/current.yaml (P0.18 +
// forward-pointer from P1.2).
package semantic

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// ErrSemanticMetadataWriterDisabled is the canonical typed sentinel
// returned by every MetadataWriter method on the Phase 1.2 disabled
// stub. Per godlike/07 (no-fake-availability), callers can branch on
// errors.Is(err, ErrSemanticMetadataWriterDisabled) to detect the
// "real semantic tagger has not been reintroduced yet" condition and
// either surface a typed `metadata_disabled` API surface, route the
// request to the new enrichment port (P0.18), or fail loud at the
// composition root if a capability requires a real model.
var ErrSemanticMetadataWriterDisabled = errors.New("semantic: MetadataWriter is DISABLED / NOT_CONFIGURED (Phase 1.2 stub — real Ollama/Python semantic tagger has not been reintroduced; callers must branch on errors.Is(err, ErrSemanticMetadataWriterDisabled) per godlike/07)")

// ── NewMetadataWriter (DISABLED constructor) ──────────────────────────

// NewMetadataWriter is the canonical constructor for *MetadataWriter.
// Pre-fix callers (composition root: app/module_media.go::WireArtlist +
// app/module_media.go::WireStockPipeline + app/build_bundles_domain.go
// + app/build_bundles_voiceover.go + others) call
// semantic.NewMetadataWriter(...) with five args; the constructor
// accepts the args (kept for forward-compat when the real semantic
// tagger is reintroduced per P0.18) and returns the disabled stub.
//
// Phase 1.2 contract (godlike/07): the init log carries the explicit
// DISABLED / NOT_CONFIGURED marker so operators can grep for the
// canonical string from observability + audit log surfaces and confirm
// the no-op is the expected surface, NOT a missing wiring.
//
// Args are intentionally accepted (not validated / not errored): the
// 22+ caller sites already pass string args from cfg.External.OllamaURL
// + cfg.External.OllamaModel + cfg.Paths.PythonScriptsDir +
// cfg.Storage.TempPath(); rejecting them now would force a coordinated
// migration across ALL call sites. The Phase 1.2 closure deliberately
// preserves the signature so existing wiring remains a compile-time
// no-op (the args are ignored; Write/GeneratePayload return the typed
// sentinel regardless).
func NewMetadataWriter(pythonScriptsDir, tempDir, ollamaURL, ollamaModel string, log *zap.Logger) *MetadataWriter {
	if log == nil {
		log = zap.NewNop()
	}
	log.Warn("semantic.MetadataWriter DISABLED / NOT_CONFIGURED (Phase 1.2 stub) — real Ollama/Python semantic tagger has not been reintroduced; every Write/GeneratePayload call returns ErrSemanticMetadataWriterDisabled",
		zap.String("python_scripts_dir", pythonScriptsDir),
		zap.String("temp_dir", tempDir),
		zap.String("ollama_url", ollamaURL),
		zap.String("ollama_model", ollamaModel),
	)
	return &MetadataWriter{}
}

// ── AssetSemanticInput ────────────────────────────────────────────────

// AssetSemanticInput carries the fields used to build a unified metadata
// map. The struct is consumed by BuildAssetMetadata (in
// metadata_builder.go) and is part of the canonical shared surface
// that production callers thread through the disabled stub. Pre-fix
// called from internal/application/clips/enrich.go +
// internal/application/assets/providers/artlist/semantic_enricher.go
// + others persisted this struct to wire payloads; the Phase 1.2
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

// ── MetadataWriter (DISABLED) ──────────────────────────────────────────

// MetadataWriter is the canonical DISABLED stub surface. Pre-fix the
// struct produced synthetic Payload shells; Phase 1.2 closure replaces
// this with a strict no-op that returns the typed sentinel for every
// semantic-richness operation.
//
// The struct is intentionally empty (no fields). A future wave that
// reintroduces the real semantic tagger (P0.18) will replace this
// type with a structured-port concrete (e.g. *ollama.TaggerAdapter)
// threaded via a typed SemanticMetadataWriterPort (mirrors
// sfxports.SemanticMetadataWriterPort precedent).
type MetadataWriter struct{}

// GeneratePayload is the canonical DISABLED stub method. Returns
// (nil, "", ErrSemanticMetadataWriterDisabled) per godlike/07
// no-fake-availability — the previous shape
// `(*Payload, "", nil)` with a synthetic Payload shell is retired.
// Callers branch on the returned sentinel.
//
// The "string" return value is reserved for future forward-compat
// (was "" in the synthetic-payload shape) and is now ALWAYS "".
func (w *MetadataWriter) GeneratePayload(_ context.Context, _ WriteRequest) (*Payload, string, error) {
	return nil, "", ErrSemanticMetadataWriterDisabled
}

// Write is the canonical DISABLED stub method. Returns
// (nil, ErrSemanticMetadataWriterDisabled) per godlike/07. Pre-fix the
// method returned (&WriteResult{Payload: synthesisedShell,
// LocalPath: req.LocalPath}, nil) — the LocalPath echo was a
// particularly insidious fake-availability (callers persisted the
// echo-path as if a real write happened). Retired.
func (w *MetadataWriter) Write(_ context.Context, _ WriteRequest) (*WriteResult, error) {
	return nil, ErrSemanticMetadataWriterDisabled
}

// ── WriteRequest ──────────────────────────────────────────────────────

// WriteRequest carries the inputs for semantic metadata generation.
// The shape is the canonical pre-fix contract; 22+ production callers
// construct WriteRequest literals at the boundary to the disabled
// stub. Phase 1.2 closure keeps the type unchanged for forward-compat
// with the future real tagger reintroduction (the same fields will be
// consumed by the P0.18 typed SemanticMetadataWriterPort adapter).
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

// WriteResult carries the output of a MetadataWriter.Write call.
// Phase 1.2 closure keeps the type unchanged for forward-compat;
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
// constructs Payload literals directly outside the disabled stub and
// its tests. The fields mirror the canonicalis semantic-tagger
// output shape for the future P0.18 typed-port reintroduction.
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

// ── ExtensionEntry ────────────────────────────────────────────────────

// ExtensionEntry is the typed envelope some callers (soundeffect /
// video extensions via BuildVideoExtension) use to thread per-asset
// metadata-extension data. Kept in the shared surface for type-pinning
// symmetry with the extension builders in metadata_builder.go.
type ExtensionEntry map[string]any

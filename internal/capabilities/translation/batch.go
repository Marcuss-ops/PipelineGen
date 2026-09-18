// Package translation — batch.go: optional batched-translation capability of
// the application-layer port surface.
//
// Motivation (translation bottleneck, Sept 2026): the per-cue subtitle path
// translates ONE cue per provider round-trip. For a 100-cue track in 10 target
// languages that is ~1000 LLM calls, each dominated by the fixed system
// prompt. Batching them is a pure wire-level optimisation: every segment stays
// an independent unit with its own id, so a batched provider cannot silently
// merge, reorder or drop a cue.
//
// The port is OPTIONAL by design (godlike/07 NO-FAKE-AVAILABILITY): a provider
// that cannot batch simply does not implement it, callers detect the capability
// with a type assertion, and any batch failure degrades to the per-segment path
// that already exists. No caller may assume batching is available.
package translation

import "context"

// BatchTranslationSegment is one independent text unit of a batched request.
// ID is the caller's stable correlation key (a cue index, a scene id); it is
// echoed by the provider and validated on the way back, so the response order
// is never trusted.
type BatchTranslationSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// BatchTranslationCommand is the application-layer DTO for
// BatchTranslationPort.TranslateBatch. ModelPolicy mirrors TranslationCommand's
// (nil = server default).
type BatchTranslationCommand struct {
	// SourceLang is the BCP-47 language tag of the source text.
	SourceLang string `json:"source_lang,omitempty"`
	// TargetLang is the BCP-47 language tag to translate into. Required.
	TargetLang string `json:"target_lang,omitempty"`
	// Segments are the independent units to translate. Order is the caller's
	// contract; ids must be unique and non-empty.
	Segments []BatchTranslationSegment `json:"segments,omitempty"`
	// ModelPolicy is the optional provider+model control (nil = default).
	ModelPolicy *ModelPolicy `json:"model_policy,omitempty"`
	// ChunkSize caps how many segments one provider request may carry.
	// 0 = provider default. It exists so the caller — which alone knows how
	// large its segments are — can keep a prompt inside the resident context
	// bucket instead of having a transport-level default overrule it.
	ChunkSize int `json:"chunk_size,omitempty"`
}

// BatchTranslationResult is the application-layer DTO returned by
// BatchTranslationPort.TranslateBatch. Segments mirrors the command order and
// carries the same ids.
type BatchTranslationResult struct {
	Segments     []BatchTranslationSegment `json:"segments,omitempty"`
	UsedModel    string                    `json:"used_model,omitempty"`
	UsedProvider string                    `json:"used_provider,omitempty"`
	// CacheStatus is "hit" | "miss" | "partial" (opaque observability signal,
	// same vocabulary as TranslationResult.CacheStatus).
	CacheStatus string `json:"cache_status,omitempty"`
}

// BatchTranslationPort is the optional batched variant of TranslationPort.
//
// Contract:
//   - the returned Segments slice mirrors cmd.Segments 1:1 (same order, same
//     ids, same length); a provider that cannot honour that MUST return an
//     error instead of a short/reordered slice;
//   - an error is "this batch produced nothing usable" — callers fall back to
//     TranslationPort.Translate per segment.
type BatchTranslationPort interface {
	TranslateBatch(ctx context.Context, cmd BatchTranslationCommand) (BatchTranslationResult, error)
}

// Compile-time assertion: the Ollama adapter implements the batched capability.
var _ BatchTranslationPort = (*OllamaTranslator)(nil)

package scriptcore

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// ComputeFingerprint creates a deterministic hash of the input to detect
// stale cache entries when clip metadata, transcripts, prompts, models
// or generation parameters change.
//
// The fingerprint is the cache key for the memory gate. It mixes:
//   - clip IDs, titles, summaries, transcript word counts (input content)
//   - user-facing options (language, tone, target words, type, style)
//   - per-phase prompt versions (planner, writer, normalizer, scene builder,
//     type registry, output schema) — see FingerprintVersionContext
//   - per-phase model identifiers and generation parameters
//     (planner + writer model, temperature, num_predict)
//
// When versionCtx is nil, DefaultFingerprintContext() is used so that
// prompt-version changes still invalidate the cache even when the caller
// did not provide the context.
func (b *ClipSourceBuilder) ComputeFingerprint(clipIDs []string, pack *ClipSourcePack, opts *ClipGenerationOptions, versionCtx *FingerprintVersionContext) string {
	// Use a copy with empty fields filled in. Never mutate the caller's
	// struct: callers may reuse the same context across multiple calls
	// and would be surprised if subsequent calls saw the filled values.
	filled := versionCtx.fillMissingCopy()

	sort.Strings(clipIDs)

	h := sha256.New()
	for _, id := range clipIDs {
		h.Write([]byte(id))
	}
	for _, c := range pack.Clips {
		h.Write([]byte(c.Title))
		h.Write([]byte(c.Summary))
		h.Write([]byte(fmt.Sprintf("%d", c.TranscriptWords)))
	}
	h.Write([]byte(opts.Language))
	h.Write([]byte(opts.Tone))
	if opts.TargetWords > 0 {
		h.Write([]byte(fmt.Sprintf("%d", opts.TargetWords)))
	}
	h.Write([]byte(opts.TranscriptPolicy))
	h.Write([]byte(opts.OrderingStrategy))
	if opts.MaxCharsPerScene > 0 {
		h.Write([]byte(fmt.Sprintf("%d", opts.MaxCharsPerScene)))
	}
	if opts.SourceText != "" {
		h.Write([]byte(opts.SourceText))
	}
	if opts.StyleInstructions != "" {
		h.Write([]byte(opts.StyleInstructions))
	}
	if opts.Type != "" {
		h.Write([]byte(opts.Type))
	}

	// ── Per-phase prompt versions (cache invalidation) ─────────────
	h.Write([]byte("planner_v:" + filled.PlannerPromptVersion))
	h.Write([]byte("writer_v:" + filled.WriterPromptVersion))
	h.Write([]byte("normalizer_v:" + filled.NormalizerVersion))
	h.Write([]byte("scene_builder_v:" + filled.SceneBuilderVersion))
	h.Write([]byte("type_registry_v:" + filled.TypeRegistryVersion))
	h.Write([]byte("output_schema_v:" + filled.OutputSchemaVersion))

	// ── Per-phase model identifiers (cache invalidation) ────────────
	h.Write([]byte("planner_model:" + filled.PlannerModel))
	h.Write([]byte("planner_temp:"))
	h.Write([]byte(fmt.Sprintf("%g", PlannerTemperature)))
	h.Write([]byte("planner_n_predict:"))
	h.Write([]byte(fmt.Sprintf("%d", PlannerNumPredict)))
	h.Write([]byte("writer_model:" + filled.WriterModel))

	return fmt.Sprintf("cs_%x", h.Sum(nil)[:16])
}

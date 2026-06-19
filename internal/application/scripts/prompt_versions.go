package scripts

// ── Prompt versions ────────────────────────────────────────────────────
//
// Every phase of the clip-to-script pipeline has its own prompt template.
// When the template changes, the output changes. To prevent stale
// fingerprint matches, each phase exposes a version constant that is
// mixed into ComputeFingerprint.
//
// Bumping policy: bump the version whenever the prompt template, the
// output schema, or the model identifier changes. This is the single
// source of truth for invalidation.
//
// ── Version registry ──────────────────────────────────────────────────

// PlannerPromptVersion is bumped when the narrative planning system
// prompt, planner user prompt, or plan JSON schema change.
//
// v2 (PR4): ordered_clips now carries purpose / comedic_angle /
// target_words per clip; the prompt asks for them explicitly so the
// writer can lean on per-clip intent instead of guessing.
const PlannerPromptVersion = "v2"

// WriterPromptVersion is bumped when the writer user prompt, output
// contract, or post-processing rules change. The writer is engine.WriteScript
// in write_script.go.
//
// v2 (PR5): strengthened OUTPUT CONTRACT to require `[Clip: <id>]` markers
// as the FIRST line of every clip scene. Adds an explicit good/bad example
// inline so smaller models (qwen2.5:1.5b, gemma2:2b) follow the structure
// reliably. Pair with BuildScenesWithMarkers in clip_scenes.go which now
// parses emitted markers and falls back to round-robin for orphans.
const WriterPromptVersion = "v2"

// NormalizerVersion is bumped when the expand / compress prompts, the
// tolerance curve, or the iteration count in NormalizeLength change.
const NormalizerVersion = "v1"

// SceneBuilderVersion is bumped when the BuildClipScenes heuristics
// change (intro / outro detection, round-robin distribution, etc.).
const SceneBuilderVersion = "v1"

// OutputSchemaVersion is bumped when the JSON shape of ClipScriptResult,
// NarrativePlan, or any persistence schema changes. Keeping it separate
// from the prompt versions makes it safe to re-run generations after
// schema migrations without invalidating the entire cache.
const OutputSchemaVersion = "v1"

// ── Generation parameters (used by planner, mixed into fingerprint) ──

// PlannerTemperature and PlannerNumPredict are the deterministic defaults
// for the narrative planning LLM call. They are exposed so callers can
// mix the same values into the fingerprint — changing them invalidates
// the memory gate cache automatically.
const (
	PlannerTemperature = 0.2
	PlannerNumPredict  = 2048
)

// ── Fingerprint version context ────────────────────────────────────────

// FingerprintVersionContext carries the per-phase versions and per-phase
// model identifiers that ComputeFingerprint mixes into the cache key.
//
// Without this context the fingerprint only hashes inputs (clip IDs,
// transcripts, options). If a prompt or model changes, the cache would
// return stale hits for hours. By hashing the versions too we ensure
// that any change to the generation stack automatically invalidates
// the cache.
//
// This is a separate struct rather than extra fields on
// ClipGenerationOptions because:
//   - The context is optional: callers that don't know the model can
//     pass nil and get a "v_unknown" sentinel for the affected fields
//     without breaking the fingerprint format.
//   - It keeps ClipGenerationOptions focused on user-facing knobs
//     (language, tone, type, ...).
//
// Generation parameters (temperature, num_predict) are intentionally
// NOT included here. They are constant for the planner
// (PlannerTemperature / PlannerNumPredict) — see prompt_versions.go
// — and the writer's temperature/num_predict are plumbed through
// WriteScriptRequest separately, not the fingerprint. Bumping the
// prompt versions is sufficient to invalidate the cache when the
// writer's behavior changes. If writer generation parameters ever
// need to flow into the fingerprint, add them to ClipGenerationOptions
// and pass them via NewFingerprintContext.
type FingerprintVersionContext struct {
	// Per-phase prompt versions (required for proper cache invalidation)
	PlannerPromptVersion string
	WriterPromptVersion  string
	NormalizerVersion    string
	SceneBuilderVersion  string
	TypeRegistryVersion  string
	OutputSchemaVersion  string

	// Per-phase model identifiers
	PlannerModel string
	WriterModel  string
}

// UnknownVersion is the sentinel used for missing version / model fields.
// It is intentionally distinct from empty string so that the cache
// contains a clear "we don't know yet" marker instead of silently
// collapsing with the empty string case.
const UnknownVersion = "v_unknown"

// DefaultFingerprintContext returns a FingerprintVersionContext populated
// with all known versions and empty model fields. Use this when the
// caller does not have access to the model configuration but still wants
// the cache to be invalidated by prompt changes.
func DefaultFingerprintContext() *FingerprintVersionContext {
	return &FingerprintVersionContext{
		PlannerPromptVersion: PlannerPromptVersion,
		WriterPromptVersion:  WriterPromptVersion,
		NormalizerVersion:    NormalizerVersion,
		SceneBuilderVersion:  SceneBuilderVersion,
		TypeRegistryVersion:  NarrativeStrategyVersion,
		OutputSchemaVersion:  OutputSchemaVersion,
		// Models are left empty (UnknownVersion) because the caller
		// may not know them at fingerprint time.
	}
}

// NewFingerprintContext is a convenience constructor for callers that
// know both model names. Pass the same model twice when planner and
// writer share it (the common case today).
func NewFingerprintContext(plannerModel, writerModel string) *FingerprintVersionContext {
	ctx := DefaultFingerprintContext()
	ctx.PlannerModel = plannerModel
	ctx.WriterModel = writerModel
	return ctx
}

// fillMissingCopy returns a copy of c with empty string fields replaced
// by UnknownVersion. It never mutates the receiver. Used internally by
// ComputeFingerprint so the hashed value is always well-defined without
// surprising callers who reuse the same context across multiple calls.
//
// When called on a nil receiver, it returns DefaultFingerprintContext()
// so that callers who pass nil get the same hash as callers who pass
// DefaultFingerprintContext() explicitly. This is the principle of
// least surprise: "nil" means "I don't know the model, use whatever
// defaults you have".
func (c *FingerprintVersionContext) fillMissingCopy() FingerprintVersionContext {
	var out FingerprintVersionContext
	if c == nil {
		// nil receiver → behave like DefaultFingerprintContext so the
		// resulting hash is identical to a caller that explicitly passes
		// DefaultFingerprintContext(). We then run the same fill-missing
		// pass as the non-nil branch so model fields are also v_unknown.
		defaultCtx := DefaultFingerprintContext()
		out = *defaultCtx
	} else {
		out = *c
	}
	if out.PlannerPromptVersion == "" {
		out.PlannerPromptVersion = UnknownVersion
	}
	if out.WriterPromptVersion == "" {
		out.WriterPromptVersion = UnknownVersion
	}
	if out.NormalizerVersion == "" {
		out.NormalizerVersion = UnknownVersion
	}
	if out.SceneBuilderVersion == "" {
		out.SceneBuilderVersion = UnknownVersion
	}
	if out.TypeRegistryVersion == "" {
		out.TypeRegistryVersion = UnknownVersion
	}
	if out.OutputSchemaVersion == "" {
		out.OutputSchemaVersion = UnknownVersion
	}
	if out.PlannerModel == "" {
		out.PlannerModel = UnknownVersion
	}
	if out.WriterModel == "" {
		out.WriterModel = UnknownVersion
	}
	return out
}

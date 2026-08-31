// Package scripts — generation_normalizer.go is the canonical
// normalization layer for the unified script-generation pipeline.
// It applies the single precedence chain to every GenerationItemV2:
//
//	caller explicit > preset > config > safety default
//
// Normalization is idempotent: calling NormalizeItem twice with the
// same inputs produces the same output. The normalizer never mutates
// fields that the caller explicitly set.
//
// This file replaces the duplicated default-coercion logic scattered
// across:
//   - generate_batch_usecase.go::Run (language, tone, duration, model,
//     prompt versions, ChannelID, drive folder)
//   - pipeline_handlers.go::handleClipPathTextOnly (min words, model,
//     language defaults)
//   - engine.go::Generate (language, tone fallbacks)
//   - ollama/types/defaults.go (DefaultLanguage, DefaultTone,
//     DefaultDuration constants)
package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// NormalizationConfig carries the configuration values that the
// normalizer needs. It is a focused snapshot — the caller extracts
// these from *config.Config so the normalizer has no import on
// internal/platform/config.
type NormalizationConfig struct {
	// ── Identity defaults ────────────────────────────────────────────
	DefaultLanguage        string // e.g. "it"
	DefaultTone            string // e.g. "documentary"
	WordsPerMinute         int    // resolved speech-rate default
	SafetyLanguage         string // resolved safety language
	DefaultDurationSeconds int    // e.g. 600
	OllamaModel            string // e.g. "llama3.2"
	ChannelID              string // memory-gate channel

	// ── Sizing defaults ───────────────────────────────────────────────
	MinWordFloor int // minimum word count floor (e.g. 200)

	// ── Prompt version defaults ───────────────────────────────────────
	PromptVersion       string // e.g. "v1"
	EditorPromptVersion string // e.g. "v1"
	QAPromptVersion     string // e.g. "v1"

	// ── Output defaults ───────────────────────────────────────────────
	DefaultSentencesPerImage int // e.g. 10
	DefaultImagesPerScene    int // e.g. 2

	// ── Batch defaults ────────────────────────────────────────────────
	MaxBatchWorkers int // default: 4; 0 → default; <0 → unbounded

	// ── Source text logging redaction ────────────────────────────────
	// LogSourceTextPreview controls whether a short preview of the
	// source_text is included in structured logs.
	LogSourceTextPreview bool
	// SourceTextPreviewChars caps the preview length in characters.
	SourceTextPreviewChars int

	// WordsPerSecondClipEvidence is the max source_text words supported
	// per second of resolved clip evidence duration. 0 disables the
	// SOURCE_TEXT_EXCEEDS_CLIP_EVIDENCE check.
	WordsPerSecondClipEvidence float64

	// ScriptDocsFolderID is the canonical default Google Drive folder for
	// script documents (PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID). It is the
	// configured default resolved when a payload omits docs.folder_id.
	// Resolution uses the single canonical resolver (one owner per fact):
	// caller override > this default > fail closed when docs.enabled=true.
	ScriptDocsFolderID string
}

// NormalizeItem applies the precedence chain to a single
// GenerationItemV2. The item is modified in-place.
//
// Step 1: Apply preset overrides (only to zero-valued fields).
// Step 2: Apply config defaults (only to fields still at zero).
// Step 3: Apply safety defaults (only to fields still at zero).
func NormalizeItem(item *scriptpkg.GenerationItemV2, preset scriptpkg.Preset, cfg NormalizationConfig) {
	if item == nil {
		return
	}

	// ── Step 1: preset overrides ───────────────────────────────────
	ApplyPreset(item, preset)
	applyMediaDensity(&item.ScriptParams)

	// ── Step 2: config defaults ────────────────────────────────────
	applyConfigDefaults(item, cfg)

	// ── Step 3: safety defaults ────────────────────────────────────
	applySafetyDefaults(item, cfg)
}

// applyMediaDensity expands the operator-facing cadence preset into the
// canonical numeric knobs. Explicit numeric values win independently, so a
// caller can select a preset and override only one side of the cadence.
func applyMediaDensity(spec *scriptpkg.ScriptSpec) {
	if spec == nil {
		return
	}
	var sentences, images int
	switch strings.ToLower(strings.TrimSpace(spec.MediaDensity)) {
	case "sparse":
		sentences, images = 12, 1
	case "balanced":
		sentences, images = 8, 2
	case "dense":
		sentences, images = 4, 2
	default:
		return
	}
	if spec.SentencesPerImage <= 0 {
		spec.SentencesPerImage = sentences
	}
	if spec.ImagesPerScene <= 0 {
		spec.ImagesPerScene = images
	}
}

// NormalizeEnvelope applies NormalizeItem to every item in the
// envelope. The envelope's Preset is passed to each item.
// The envelope is NOT modified (items are processed in a new slice).
func NormalizeEnvelope(env *scriptpkg.GenerationEnvelopeV2, cfg NormalizationConfig) []scriptpkg.GenerationItemV2 {
	if env == nil || len(env.Items) == 0 {
		return nil
	}
	normalized := make([]scriptpkg.GenerationItemV2, len(env.Items))
	for i := range env.Items {
		item := env.Items[i] // shallow copy
		NormalizeItem(&item, env.Preset, cfg)
		normalized[i] = item
	}
	return normalized
}

// ── Step 2 helpers ───────────────────────────────────────────────

func applyConfigDefaults(item *scriptpkg.GenerationItemV2, cfg NormalizationConfig) {
	// Identity.
	if strings.TrimSpace(item.Language) == "" {
		item.Language = cfg.DefaultLanguage
	}
	if strings.TrimSpace(item.Tone) == "" {
		item.Tone = cfg.DefaultTone
	}
	if strings.TrimSpace(item.Model) == "" {
		item.ModelAuto = true
		item.Model = cfg.OllamaModel
	}

	// Script sizing. Derive TargetWords from whichever duration is
	// non-zero, respecting the precedence chain: caller > config.
	if item.ScriptParams.TargetWords <= 0 {
		dur := item.ScriptParams.Duration
		if dur <= 0 {
			dur = cfg.DefaultDurationSeconds
		}
		if dur > 0 {
			// Canonical script-generation WPM. Multiply first to avoid
			// integer truncation for short durations. Reads from the
			// SSOT (pkg/defaults::ScriptConfig.WordsPerMinute = 150 as
			// of June 2026 — promoted from the legacy 140 default to
			// match the active path truth). Pre-unification this was
			// a hardcoded `150` literal; the SSOT now matches and
			// the test TestNormalizeItemDurationToWords (300s × 150wpm
			// / 60 = 750 words) continues to pass.
			wordsPerMinute := cfg.WordsPerMinute
			if wordsPerMinute <= 0 {
				wordsPerMinute = defaults.DefaultScriptConfig().WordsPerMinute
			}
			item.ScriptParams.TargetWords = (dur * wordsPerMinute) / 60
		}
	}
	if item.ScriptParams.Duration <= 0 && cfg.DefaultDurationSeconds > 0 {
		item.ScriptParams.Duration = cfg.DefaultDurationSeconds
	}
	if item.ScriptParams.MinWords <= 0 && cfg.MinWordFloor > 0 {
		item.ScriptParams.MinWords = cfg.MinWordFloor
	}

	// Prompt versions.
	if strings.TrimSpace(item.ScriptParams.PromptVersion) == "" {
		item.ScriptParams.PromptVersion = cfg.PromptVersion
	}
	if strings.TrimSpace(item.ScriptParams.EditorPromptVersion) == "" {
		item.ScriptParams.EditorPromptVersion = cfg.EditorPromptVersion
	}
	if strings.TrimSpace(item.ScriptParams.QAPromptVersion) == "" {
		item.ScriptParams.QAPromptVersion = cfg.QAPromptVersion
	}

	// Scene image defaults.
	if item.ScriptParams.SentencesPerImage <= 0 {
		item.ScriptParams.SentencesPerImage = cfg.DefaultSentencesPerImage
	}
	if item.ScriptParams.ImagesPerScene <= 0 {
		item.ScriptParams.ImagesPerScene = cfg.DefaultImagesPerScene
	}

	// Resolve the voiceover capability once. Legacy payloads that only set a
	// group/folder remain compatible, but routing is no longer reinterpreted
	// by downstream processors. An explicit disabled toggle always wins.
	if item.Output.VoiceoverEnabled != scriptpkg.ToggleEnabled &&
		item.Output.VoiceoverEnabled != scriptpkg.ToggleDisabled &&
		(strings.TrimSpace(item.Output.VoiceoverGroup) != "" || strings.TrimSpace(item.Output.VoiceoverFolderID) != "") {
		item.Output.VoiceoverEnabled = scriptpkg.ToggleEnabled
	}

	// Source defaults: NumClips derives from MaxClips.
	if item.Source.NumClips <= 0 && item.Source.MaxClips > 0 {
		item.Source.NumClips = item.Source.MaxClips
	}
	// Derive NumClips from Segments count when not explicitly set.
	if item.Source.NumClips <= 0 && len(item.ScriptParams.Segments) > 0 {
		item.Source.NumClips = len(item.ScriptParams.Segments)
	}
	if item.ScriptParams.SegmentWords <= 0 && item.ScriptParams.TargetWords > 0 && item.Source.NumClips > 0 {
		item.ScriptParams.SegmentWords = item.ScriptParams.TargetWords / item.Source.NumClips
	}
	if item.ScriptParams.TargetWords <= 0 && item.ScriptParams.SegmentWords > 0 && item.Source.NumClips > 0 {
		item.ScriptParams.TargetWords = item.ScriptParams.SegmentWords * item.Source.NumClips
	}

	// Source text defaults to topic when empty.
	if item.Source.IsText() {
		if strings.TrimSpace(item.Source.SourceText) == "" {
			item.Source.SourceText = strings.TrimSpace(item.Source.Topic)
		}
	}

	// Script docs destination — single canonical resolution. The folder ID
	// follows: explicit docs.folder_id > PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID
	// (configured default) > fail closed when docs.enabled=true. The
	// resolver is the ONLY owner of this decision; downstream plan/plan
	// builders, the document publisher, and handlers never re-derive it.
	// The normalizer has no error channel, so an enabled request with no
	// resolvable folder stays empty and is rejected by ValidateItem
	// (docs enabled but no script docs folder configured).
	if resolvedFolderID, _ := scriptpkg.ResolveScriptDocsFolderID(
		item.Docs.Enabled, item.Docs.FolderID, cfg.ScriptDocsFolderID,
	); resolvedFolderID != "" {
		item.Docs.FolderID = resolvedFolderID
	}
}

// ── Step 3 helpers ───────────────────────────────────────────────

func applySafetyDefaults(item *scriptpkg.GenerationItemV2, cfg NormalizationConfig) {
	// Hard floor: every field that would break the engine if left at
	// zero gets a safety default here.

	if strings.TrimSpace(item.Language) == "" {
		// Safety floor: when caller + preset + config are all unset,
		// read from the SSOT (pkg/defaults::ScriptConfig.SafetyLanguage
		// = "en" as of June 2026 — the V1 contract language).
		// Semantically distinct from DefaultLanguage ("it") which is
		// the Step-2 config-default fallback; the safety floor must
		// remain exploitable even when per-locale overrides change
		// DefaultLanguage. Test TestNormalizeItemPrecedenceConfigBeatsHardDefault
		// pins this precedence contract.
		item.Language = cfg.SafetyLanguage
		if strings.TrimSpace(item.Language) == "" {
			item.Language = defaults.DefaultScriptConfig().SafetyLanguage // compatibility for manually assembled test configs
		}
	}
	if strings.TrimSpace(item.Tone) == "" {
		item.Tone = cfg.DefaultTone
		if strings.TrimSpace(item.Tone) == "" {
			item.Tone = defaults.DefaultScriptConfig().DefaultTone // compatibility for manually assembled test configs
		}
	}
	if strings.TrimSpace(item.Model) == "" {
		item.ModelAuto = true
		item.Model = "llama3.2"
	}

	// Ensure at least the SSOT WordsPerMinute floor (defaults to
	// 150 as of June 2026 — promoted from the legacy literal to
	// match the pkg/defaults registry). The 150 ceiling is the
	// floor for the case where caller + config + duration ALL
	// landed at zero; the normalizer still emits a non-empty
	// bundle so downstream postprocessors don't trip Required-empty.
	if item.ScriptParams.TargetWords <= 0 {
		item.ScriptParams.TargetWords = cfg.WordsPerMinute
		if item.ScriptParams.TargetWords <= 0 {
			item.ScriptParams.TargetWords = defaults.DefaultScriptConfig().WordsPerMinute // compatibility for manually assembled test configs
		}
	}

	// Title defaults to topic.
	if strings.TrimSpace(item.Title) == "" {
		item.Title = strings.TrimSpace(item.Source.Topic)
	}
	if strings.TrimSpace(item.Title) == "" {
		item.Title = "Untitled Script"
	}

	// ── Postprocessor flags (P0.1, July 2026) ─────
	//
	// SaveToDB is a safety default because:
	//   1. The "custom" preset is pass-through (ApplyPreset touches
	//      nothing).
	//   2. applyConfigDefaults only fills identity/sizing/prompt
	//      fields.
	//   3. Without this safety default, buildPostprocessorList never
	//      adds ProcessorPersistence to the postprocessor list, so
	//      scripts are never persisted.
	//
	// SaveToDB is a safety default because persistence is required
	// for downstream processors to read the script.
	if !item.Output.SaveToDB {
		item.Output.SaveToDB = true
	}

	// Output format (P0.1, June 2026).
	//
	// Canonical script-generation now mandates the structured V1
	// JSON contract (engine.go::EngineResult.Output of type
	// ModelScriptOutputV1). The previous default of "prose" caused a
	// silent decoder regression: when OutputFmt=="prose" the engine
	// suppressed the V1 output instruction suffix, the model produced
	// free-form prose, and the JSON decoder rejected the payload
	// with ErrModelOutputMalformed.
	//
	// The default is now "json". Callers who explicitly opt into
	// the legacy free-form path are rejected by the validator
	// (see generation_validator.go::validateOutput) so the only
	// way "prose" can reach the canonical pipeline is through a
	// downstream migration we're not supporting.
	if item.Output.OutputFmt == "" {
		item.Output.OutputFmt = "json"
	}

	// Source defaults.
	if item.Source.TranscriptPolicy == "" {
		item.Source.TranscriptPolicy = scriptpkg.TranscriptPolicyStrict
	}
	if item.Source.OrderingStrategy == "" {
		item.Source.OrderingStrategy = "relevance"
	}
}

// ApplyPreset applies a generation preset's documented overrides to
// the item, before the config-defaults and safety-defaults layers
// run. Per §6 "Required preset semantics" of
// docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md, each
// preset has a single canonical row in the table; this function is
// the body of that row.
//
// Caller fields are NEVER overwritten: the preset only fills in
// zero-valued fields when its doc row says so. The full precedence
// chain (this function is just the "preset" step) is:
//
//	caller explicit > preset > config > safety default
//
// The function is idempotent (re-running it with the same inputs
// produces no further change) and nil-safe (a nil item returns
// without mutating anything).
//
// Summary by case (see doc §6 for the canonical rows):
//
//	custom     → pass-through (no overrides)
//	with_images → SentencesPerImage=8, ImagesPerScene=2 only when caller
//	               left them at zero
//	full_media → scoping-only
//	catalog    → pass-through (handler binds source.kind=catalog upstream)
//	search     → pass-through (handler binds source.kind=search upstream)
//	batch / unknown / empty → pass-through
func ApplyPreset(item *scriptpkg.GenerationItemV2, preset scriptpkg.Preset) {
	if item == nil {
		return
	}
	switch preset {
	case scriptpkg.PresetCustom:
		// §6 row 1: custom | none | none.
		// Caller filled every flag explicitly; preset must not touch.
	case scriptpkg.PresetWithImages:
		// §6 row 2: with_images | none | images sizing defaults.
		if item.ScriptParams.SentencesPerImage <= 0 {
			item.ScriptParams.SentencesPerImage = 8
		}
		if item.ScriptParams.ImagesPerScene <= 0 {
			item.ScriptParams.ImagesPerScene = 2
		}
	case scriptpkg.PresetFullMedia:
		// §6 row 3: full_media enables voiceover when the caller did not
		// explicitly choose a voiceover capability.
		if item.Output.VoiceoverEnabled != scriptpkg.ToggleEnabled && item.Output.VoiceoverEnabled != scriptpkg.ToggleDisabled {
			item.Output.VoiceoverEnabled = scriptpkg.ToggleEnabled
		}
	case scriptpkg.PresetCatalog:
		// §6 row 4: catalog | source.kind=catalog | none.
		//
		// Pass-through. The HTTP handler (`handler_legacy_adapters.go`
		// §6 catalog endpoints) binds source.kind=SourceCatalog on the
		// item before ApplyPreset runs; the preset carries the intent
		// without altering the field again.
	case scriptpkg.PresetSearch:
		// §6 row 5: search | source.kind=search | none.
		//
		// Pass-through. The HTTP handler (`handler_legacy_adapters.go`
		// §6 search endpoints) binds source.kind=SourceSearch on the
		// item before ApplyPreset runs; the preset carries the intent
		// without altering the field again.
	default:
		// batch, unknown, empty Preset → pass-through.
		// `TestApplyPresetBatchPassThrough` exercises path with
		// Preset("batch") as a plain string; an empty Preset also
		// arrives here from callers that did not bind one.
	}
}

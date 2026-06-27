// Package scripts — generation_normalizer.go is the canonical
// normalization layer for the unified script-generation pipeline.
// It applies the single precedence chain to every GenerationItemV2:
//
//   caller explicit > preset > config > safety default
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
//   - engine.go::WriteScript (language, tone fallbacks)
//   - ollama/types/defaults.go (DefaultLanguage, DefaultTone,
//     DefaultDuration constants)
package scripts

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// NormalizationConfig carries the configuration values that the
// normalizer needs. It is a focused snapshot — the caller extracts
// these from *config.Config so the normalizer has no import on
// internal/platform/config.
type NormalizationConfig struct {
	// ── Identity defaults ────────────────────────────────────────────
	DefaultLanguage        string // e.g. "it"
	DefaultTone            string // e.g. "documentary"
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

	// ── Step 2: config defaults ────────────────────────────────────
	applyConfigDefaults(item, cfg)

	// ── Step 3: safety defaults ────────────────────────────────────
	applySafetyDefaults(item)
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
			// ~150 words per minute. Multiply first to avoid
			// integer truncation for short durations.
			item.ScriptParams.TargetWords = (dur * 150) / 60
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

	// Voiceover group default.
	if item.Output.GenerateVoiceover && strings.TrimSpace(item.Output.VoiceoverGroup) == "" {
		item.Output.VoiceoverGroup = cfg.ChannelID
	}

	// Source defaults: NumClips derives from MaxClips.
	if item.Source.NumClips <= 0 && item.Source.MaxClips > 0 {
		item.Source.NumClips = item.Source.MaxClips
	}

	// Source text defaults to topic when empty.
	if item.Source.Type == scriptpkg.SourceText {
		if strings.TrimSpace(item.Source.SourceText) == "" {
			item.Source.SourceText = strings.TrimSpace(item.Source.Topic)
		}
	}
}

// ── Step 3 helpers ───────────────────────────────────────────────

func applySafetyDefaults(item *scriptpkg.GenerationItemV2) {
	// Hard floor: every field that would break the engine if left at
	// zero gets a safety default here.

	if strings.TrimSpace(item.Language) == "" {
		item.Language = "en"
	}
	if strings.TrimSpace(item.Tone) == "" {
		item.Tone = "documentary"
	}
	if strings.TrimSpace(item.Model) == "" {
		item.Model = "llama3.2"
	}

	// Ensure at least 100 target words.
	if item.ScriptParams.TargetWords <= 0 {
		item.ScriptParams.TargetWords = 150
	}

	// Title defaults to topic.
	if strings.TrimSpace(item.Title) == "" {
		item.Title = strings.TrimSpace(item.Source.Topic)
	}
	if strings.TrimSpace(item.Title) == "" {
		item.Title = "Untitled Script"
	}

	// Output format.
	if item.Output.OutputFmt == "" {
		item.Output.OutputFmt = "prose"
	}

	// Source defaults.
	if item.Source.TranscriptPolicy == "" {
		item.Source.TranscriptPolicy = "auto"
	}
	if item.Source.OrderingStrategy == "" {
		item.Source.OrderingStrategy = "relevance"
	}
}

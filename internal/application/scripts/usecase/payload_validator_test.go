package usecase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestPayloadValidator_ValidEnvelope(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:       "valid-1",
				Title:    "Valid",
				Language: "en",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					Topic:      "valid topic",
					SourceText: "short source text",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	require.NoError(t, v.ValidateEnvelope(env))
}

func TestPayloadValidator_SourceTextTooManyChars(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{MaxSourceTextChars: 10})
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "too-long",
				Title: "Too Long",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					SourceText: "this source text is way too long",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "SOURCE_TEXT_TOO_LARGE", pve.Code)
	assert.Equal(t, 32, pve.Extra["actual_chars"])
	assert.Equal(t, 10, pve.Extra["max_chars"])
}

func TestPayloadValidator_SourceTextTooManyBytes(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{MaxSourceTextBytes: 10})
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "too-long-bytes",
				Title: "Too Long Bytes",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					SourceText: "this source text is way too long",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "SOURCE_TEXT_TOO_LARGE", pve.Code)
	assert.Equal(t, 32, pve.Extra["actual_bytes"])
	assert.Equal(t, 10, pve.Extra["max_bytes"])
}

func TestPayloadValidator_SourceTextExceedsTargetRatio(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{MaxSourceTextToTargetWordsRatio: 2.0})
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "ratio",
				Title: "Ratio",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					SourceText: "one two three four five six seven eight nine ten eleven twelve",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 5},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "SOURCE_TEXT_EXCEEDS_TARGET_RATIO", pve.Code)
	assert.Equal(t, 12, pve.Extra["source_words"])
	assert.Equal(t, 5, pve.Extra["target_words"])
	assert.Equal(t, 2.0, pve.Extra["max_ratio"])
}

func TestPayloadValidator_TokenEstimate(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{MaxSourceTextTokens: 2})
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "tokens",
				Title: "Tokens",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					SourceText: "this is a test of token estimation",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "SOURCE_TEXT_TOO_LARGE", pve.Code)
	assert.NotNil(t, pve.Extra["actual_tokens"])
	assert.Equal(t, 2, pve.Extra["max_tokens"])
}

func TestPayloadValidator_DuplicateClipIDs(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "dup",
				Title: "Duplicate",
				Source: scriptpkg.SourceSpec{
					Type:    scriptpkg.SourceClips,
					ClipIDs: []string{"clip-1", "clip-1", "clip-2"},
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "duplicate clip_id")
}

func TestPayloadValidator_TargetWordsMustBePositive(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "zero-target",
				Title: "Zero Target",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "topic",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 0},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "INVALID_TARGET_WORDS", pve.Code)
	assert.Equal(t, "target_words must be > 0", pve.Message)
	assert.Equal(t, 0, pve.Extra["actual_target_words"])
}

func TestPayloadValidator_UnsupportedLanguage(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:       "bad-lang",
				Title:    "Bad Lang",
				Language: "xx",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "topic",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "unsupported language")
}

func TestPayloadValidator_InvalidGroundingPolicy(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "bad-grounding",
				Title: "Bad Grounding",
				Source: scriptpkg.SourceSpec{
					Type:            scriptpkg.SourceClips,
					ClipIDs:         []string{"clip-1"},
					GroundingPolicy: "invalid",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "invalid grounding_policy")
}

func TestPayloadValidator_IncompatibleFallbackPolicy(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "bad-fallback",
				Title: "Bad Fallback",
				Source: scriptpkg.SourceSpec{
					Type:           scriptpkg.SourceText,
					Topic:          "topic",
					FallbackPolicy: scriptpkg.FallbackPolicyAllowProse,
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "fallback_policy is only compatible with source.type=clips")
}

func TestPayloadValidator_UnknownSourceType(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "unknown-source",
				Title: "Unknown Source",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceType("unknown"),
					Topic: "topic",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "unknown source type")
}

func TestPayloadValidator_ClipsSourceWithoutClipIDs(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "no-clips",
				Title: "No Clips",
				Source: scriptpkg.SourceSpec{
					Type: scriptpkg.SourceClips,
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "clips source requires at least one clip_id")
}

func TestPayloadValidator_LongSourceTextWithinLimit(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{MaxSourceTextChars: 100})
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "within-limit",
				Title: "Within Limit",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					SourceText: strings.Repeat("a", 100),
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			},
		},
	}

	require.NoError(t, v.ValidateEnvelope(env))
}

package usecase

import (
	"fmt"
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
	assert.Equal(t, 32, pve.Extra.ActualChars)
	assert.Equal(t, 10, pve.Extra.MaxChars)
	assert.Contains(t, pve.Extra.Limits, "chars")
	assert.NotContains(t, pve.Error(), "this source text is way too long")
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
	assert.Equal(t, 32, pve.Extra.ActualBytes)
	assert.Equal(t, 10, pve.Extra.MaxBytes)
	assert.Contains(t, pve.Extra.Limits, "bytes")
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
	assert.Equal(t, 12, pve.Extra.SourceWords)
	assert.Equal(t, 5, pve.Extra.TargetWords)
	assert.Equal(t, 2.0, pve.Extra.MaxRatio)
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
	assert.Greater(t, pve.Extra.ActualTokens, 0)
	assert.Equal(t, 2, pve.Extra.MaxTokens)
	assert.Contains(t, pve.Extra.Limits, "tokens")
}

func TestPayloadValidator_SourceTextExceedsMultipleLimits(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{
		MaxSourceTextChars:  5,
		MaxSourceTextBytes:  5,
		MaxSourceTextTokens: 1,
	})
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "multi-limit",
				Title: "Multi Limit",
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
	limits := pve.Extra.Limits
	require.NotEmpty(t, limits)
	assert.Contains(t, limits, "chars")
	assert.Contains(t, limits, "bytes")
	assert.Contains(t, limits, "tokens")
}

func TestPayloadValidator_SourceTextTooLargeDoesNotLeakText(t *testing.T) {
	v := NewPayloadValidator(config.ScriptsConfig{MaxSourceTextChars: 5})
	secret := "this source text contains a secret 12345"
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "leak-check",
				Title: "Leak Check",
				Source: scriptpkg.SourceSpec{
					Type:       scriptpkg.SourceText,
					SourceText: secret,
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
	assert.NotContains(t, pve.Error(), secret)
	assert.NotContains(t, fmt.Sprintf("%+v", pve.Extra), secret)
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
	assert.Equal(t, 0, pve.Extra.ActualTargetWords)
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

// ── PR-CS-1 / FASE 6 (DoD #8): ScriptSegment validation ─────────────

func TestPayloadValidator_TargetWordsZeroWithSegmentsAllowed(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "zero-with-segments",
				Title: "Zero With Segments",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "topic",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 0,
					Segments: []scriptpkg.ScriptSegment{
						{Topic: "intro", TargetWords: 80},
						{Topic: "body", TargetWords: 200},
					},
				},
			},
		},
	}
	require.NoError(t, v.ValidateEnvelope(env))
}

func TestPayloadValidator_SegmentsEmpty(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "segments-empty",
				Title: "Segments Empty",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 100,
					// Explicit present-empty (caller wrote `segments: []`);
					// distinct from absent which silently defaults.
					Segments: []scriptpkg.ScriptSegment{},
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "segments must not be empty")
}

func TestPayloadValidator_SegmentsAndSegmentTopicsMutex(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "mutex",
				Title: "Mutex",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords:   100,
					SegmentTopics: []string{"a", "b"},
					Segments: []scriptpkg.ScriptSegment{
						{Topic: "x"},
					},
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "cannot both be set")
}

func TestPayloadValidator_SegmentTopicEmpty(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "topic-empty",
				Title: "Topic Empty",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 100,
					Segments: []scriptpkg.ScriptSegment{
						{Topic: "intro"},
						{Topic: ""},    // blank → fail
						{Topic: "   "}, // whitespace-only also fails (TrimSpace check)
					},
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pie *scriptpkg.PlanInvalidError
	require.ErrorAs(t, err, &pie)
	assert.Contains(t, pie.Details[0], "topic is required")
	// Index 1 is the first blank one — operator-clarity invariant.
	assert.Contains(t, pie.Details[0], "[1]")
}

func TestPayloadValidator_TooManySegments(t *testing.T) {
	t.Parallel()
	// Default cap comes from WithDefaults → MaxSegmentsCap=50.
	v := NewDefaultPayloadValidator()
	segments := make([]scriptpkg.ScriptSegment, 51)
	for i := range segments {
		segments[i] = scriptpkg.ScriptSegment{Topic: fmt.Sprintf("topic_%d", i)}
	}
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "too-many",
				Title: "Too Many",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "x",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					TargetWords: 100,
					Segments:    segments,
				},
			},
		},
	}
	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "TOO_MANY_SEGMENTS", pve.Code)
	assert.Equal(t, 51, pve.Extra.ActualSegments)
	assert.Equal(t, 50, pve.Extra.MaxSegmentsCap)
}

func TestPayloadValidator_HappyPathFourSegments(t *testing.T) {
	t.Parallel()
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "happy",
				Title: "Happy Path",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "Pacquiao vs Broner",
				},
				ScriptParams: scriptpkg.ScriptSpec{
					// TargetWords omitted (=0) — valid because each
					// segment carries its own per-block budget.
					Segments: []scriptpkg.ScriptSegment{
						{Topic: "Introduzione", TargetWords: 80},
						{Topic: "Contesto", TargetWords: 200},
						{Topic: "Evento", TargetWords: 400},
						{Topic: "Conclusione", TargetWords: 120},
					},
				},
			},
		},
	}
	require.NoError(t, v.ValidateEnvelope(env))
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

func TestPayloadValidator_VideoMetadataEmpty(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "empty-metadata",
				Title: "Empty Metadata",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "topic",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
				VideoMetadata: &scriptpkg.VideoMetadata{
					Title:       "",
					Description: "",
					Tags:        []string{},
				},
			},
		},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "EMPTY_VIDEO_METADATA", pve.Code)
}

func TestPayloadValidator_VideoMetadataWhitespaceOnly(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{{
			ID:    "whitespace-metadata",
			Title: "Whitespace Metadata",
			Source: scriptpkg.SourceSpec{
				Type:  scriptpkg.SourceText,
				Topic: "topic",
			},
			ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
			VideoMetadata: &scriptpkg.VideoMetadata{
				Language:    "   ",
				Title:       "\t",
				Description: "\n",
				Tags:        []string{" ", "\t"},
			},
		}},
	}

	err := v.ValidateEnvelope(env)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	require.Equal(t, "EMPTY_VIDEO_METADATA", pve.Code)
	require.Equal(t, "request.validation", pve.Stage)
	require.False(t, pve.Retryable)
}

func TestPayloadValidator_VideoMetadataValid(t *testing.T) {
	v := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID:    "valid-metadata",
				Title: "Valid Metadata",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "topic",
				},
				ScriptParams: scriptpkg.ScriptSpec{TargetWords: 150},
				VideoMetadata: &scriptpkg.VideoMetadata{
					Title: "Some Video Title",
				},
			},
		},
	}

	require.NoError(t, v.ValidateEnvelope(env))
}

func TestPayloadValidator_RejectsEmptyVideoMetadata(t *testing.T) {
	validator := NewDefaultPayloadValidator()
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{
			{
				ID: "test-item",
				Source: scriptpkg.SourceSpec{
					Type:  scriptpkg.SourceText,
					Topic: "topic",
				},
				ScriptParams:  scriptpkg.ScriptSpec{TargetWords: 150},
				VideoMetadata: &scriptpkg.VideoMetadata{},
			},
		},
	}

	err := validator.ValidateEnvelope(env)

	require.Error(t, err)

	var validationError *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &validationError)
	require.Equal(t, "EMPTY_VIDEO_METADATA", validationError.Code)
}

package usecase

import (
	"context"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"testing"
)

// TestGenerateOneUseCase_LogsAndReturnsTypedError_OnEngineNil pins the
// SCRIPT-T03-USECASE (P0, 2026-07-15) godlike/07 typed-error gate:
// when the usecase hits a phase failure, it MUST log the diagnostic
// context (reason/phase, error) at the boundary AND return a typed
// error that satisfies errors.Is for the canonical sentinel.
//
// Strategy: use observer.New(zap.WarnLevel) to capture log entries,
// construct a usecase with engine=nil (triggers the
// "engine not configured" constructor failure), execute a real item,
// and assert:
//
//	(a) errors.Is(err, scriptpkg.ErrGenerationFailed) is true
//	(b) exactly 1 Warn log entry was emitted with reason="engine_nil"
//	    + the typed error chain
//
// godlike/07 NO_FAKE_AVAILABILITY: the typed error is the canonical
// propagation surface; the log is the canonical diagnostic surface.
// Both surfaces are asserted in this test.
func TestGenerateOneUseCase_LogsAndReturnsTypedError_OnEngineNil(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, // SourceRegistry nil
		nil, // Engine nil → triggers ErrGenerationFailed (typed sentinel)
		nil, // ppReg nil
		log,
	)

	item := scriptpkg.GenerationItemV2{
		ID:    "script-t03-usecase-test",
		Title: "SCRIPT-T03-USECASE test",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "test",
		},
	}

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err, "Execute with engine=nil must return error")
	require.True(t, errors.Is(err, scriptpkg.ErrGenerationFailed),
		"godlike/07 typed-error gate: errors.Is(err, ErrGenerationFailed) must be true, got err=%v", err)

	require.Equal(t, 1, recorded.Len(),
		"SCRIPT-T03-USECASE: exactly 1 Warn log entry must be emitted (constructor-failure path)")
	entry := recorded.All()[0]
	assert.Equal(t, "generate-one: construction failed", entry.Message,
		"SCRIPT-T03-USECASE: log message must indicate construction failure")
	fields := entry.ContextMap()
	assert.Equal(t, "engine_nil", fields["reason"],
		"SCRIPT-T03-USECASE: log must include the reason 'engine_nil' for the construction failure")
}

// TestGenerateOneUseCase_LogsAndReturnsTypedError_OnValidateFailure
// pins the godlike/07 typed-error gate for a real phase failure
// (validate, not just constructor). The validate phase runs AFTER
// the constructor checks, so the item_id is populated in the log.
//
// Strategy: construct a usecase with a real Engine (stubbed ollama)
// + SourceRegistry nil + nil ppReg. Pass an item with an empty
// Source (no Type set) which trips ValidateItem. Assert:
//
//	(a) errors.Is(err, scriptpkg.ErrPlanInvalid) is true (typed)
//	(b) exactly 1 Warn log entry was emitted with item_id +
//	    phase="validate" + the typed error
func TestGenerateOneUseCase_LogsAndReturnsTypedError_OnValidateFailure(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	// Real Engine with stubbed ollama (no memory gate) — won't be
	// invoked because ValidateItem short-circuits BEFORE engine.
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, // SourceRegistry nil
		e,
		nil, // ppReg nil
		log,
	)

	// Item with empty Source: Type="" — ValidateItem will return
	// an error (the canonical "no source specified" path).
	item := scriptpkg.GenerationItemV2{
		ID:     "validate-fail-item",
		Title:  "Validate Fail Test",
		Source: scriptpkg.SourceSpec{
			// No Type set → ValidateItem rejects.
		},
	}

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err, "Execute with empty Source must return error")
	require.True(t, errors.Is(err, scriptpkg.ErrPlanInvalid),
		"godlike/07 typed-error gate: errors.Is(err, ErrPlanInvalid) must be true, got err=%v", err)

	require.Equal(t, 1, recorded.Len(),
		"SCRIPT-T03-USECASE: exactly 1 Warn log entry must be emitted (validate phase)")
	entry := recorded.All()[0]
	assert.Equal(t, "generate-one: phase failed", entry.Message,
		"SCRIPT-T03-USECASE: log message must indicate phase failure")
	fields := entry.ContextMap()
	assert.Equal(t, "validate-fail-item", fields["item_id"],
		"SCRIPT-T03-USECASE: log must include the item_id")
	assert.Equal(t, "validate", fields["phase"],
		"SCRIPT-T03-USECASE: log must include the phase 'validate'")
}

// ── Clip evidence text support (P1) ──

func TestEnforceClipEvidenceTextSupport_AllowedWithinBudget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 10000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	if err := enforceClipEvidenceTextSupport(plan, cfg); err != nil {
		t.Fatalf("expected no error for source_text within clip evidence budget, got %v", err)
	}
}

func TestEnforceClipEvidenceTextSupport_ExceedsBudget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five six seven eight nine ten eleven twelve",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 2000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	err := enforceClipEvidenceTextSupport(plan, cfg)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "SOURCE_TEXT_EXCEEDS_CLIP_EVIDENCE", pve.Code)
	assert.Equal(t, 12, pve.Extra.ActualWords)
	assert.Equal(t, 5, pve.Extra.MaxWords)
	assert.Equal(t, 2.0, pve.Extra.EvidenceSeconds)
	assert.Equal(t, 2.5, pve.Extra.WordsPerSecond)
}

func TestEnforceClipEvidenceTextSupport_DisabledWhenZero(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five six seven eight nine ten eleven twelve",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 1000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 0}
	require.NoError(t, enforceClipEvidenceTextSupport(plan, cfg))
}

func TestEnforceClipEvidenceTextSupport_SumsMultipleClips(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five six seven eight nine ten",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 2000},
				"clip-2": {StartMs: 0, EndMs: 2000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	if err := enforceClipEvidenceTextSupport(plan, cfg); err != nil {
		t.Fatalf("expected no error when total clip duration supports source_text, got %v", err)
	}
}

func TestEnforceClipEvidenceTextSupport_IgnoresTextSource(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceText),
		SourceText: "one two three four five six seven eight nine ten eleven twelve",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 1000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	require.NoError(t, enforceClipEvidenceTextSupport(plan, cfg))
}

func TestEnforceClipEvidenceTextSupport_NoSourceText(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 1000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	require.NoError(t, enforceClipEvidenceTextSupport(plan, cfg))
}

// ── Event emission coverage (July 2026) ──

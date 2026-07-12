// Package voiceover — usecase/process_segment_construction_test.go
//
// Construction-phase tests for the SHARED per-item pipeline runner
// (usecase/process_segment.go). These tests pin the canonical
// construction-time contracts — fail-fast guards (godlike/07) and
// source-level delegation invariants (godlike/06 SSOT) — WITHOUT
// exercising any runtime Execute path.
//
// godlike/06 SSOT: tests 1-2 lock the canonical construction invariants:
//
//  1. TestNewProcessSegmentUseCase_PanicsOnMandatoryDeps — the constructor
//     fail-fast panics (4 mandatory deps) are the canonical surface
//     that enforces "partial wire-up surfaces at composition time, not
//     at first job dispatch" (AGENTS.md WireUp pattern).
//
//  2. TestProcessSegmentUseCase_DRY_Wiring_SourceGrep — the source-grep
//     inverse-regression guard pins the cross-file delegation invariant:
//     BOTH usecase.go (batch) AND process_voiceover_item.go (per-item)
//     delegate to the shared ProcessSegmentUseCase.Execute. A future
//     refactor that inlines either body silently collapses the
//     one-canonical-owner invariant; this test catches the regression
//     at build-time by reading the source files for `processSeg.Execute(`.
//
// godlike/07 minimum-blast-radius: zero production code changes.
// Construction tests do not exercise Execute — only the constructor
// + the source-grep read surface.
package voiceover

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 1: NewProcessSegmentUseCase fail-fast guards (table-driven)
// ─────────────────────────────────────────────────────────────────────────

// TestNewProcessSegmentUseCase_PanicsOnMandatoryDeps asserts the canonical
// fail-fast semantics from the constructor: TTSProvider / Publisher /
// VoiceoverRepository / Finalizer are MANDATORY (panic on nil);
// AudioPostProcessor + Logger are nil-safe (NOT asserted here).
//
// Per AGENTS.md WireUp pattern (godlike/07 fail-fast), a partial
// wire-up surfaces at composition time (panic), NOT at first-job
// dispatch. A regression that relaxes any of the 4 mandatory guards
// would let nil-deps reach Execute and surface as NPE-style
// panics mid-job — exactly the silent-success class godlike/07
// forbids.
func TestNewProcessSegmentUseCase_PanicsOnMandatoryDeps(t *testing.T) {
	// validDeps is the canonical happy-path struct literal; each
	// subtest mutates ONE field to nil and asserts panic on the
	// matching constructor message.
	validDeps := ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{},
		Publisher:           &stubProcessPublisher{},
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{},
	}

	tests := []struct {
		name        string
		mutate      func(d *ProcessSegmentDeps)
		wantPanicRe string
	}{
		{
			name:        "nil TTSProvider",
			mutate:      func(d *ProcessSegmentDeps) { d.TTSProvider = nil },
			wantPanicRe: "TTSProvider is required",
		},
		{
			name:        "nil Publisher",
			mutate:      func(d *ProcessSegmentDeps) { d.Publisher = nil },
			wantPanicRe: "Publisher is required",
		},
		{
			name:        "nil VoiceoverRepository",
			mutate:      func(d *ProcessSegmentDeps) { d.VoiceoverRepository = nil },
			wantPanicRe: "VoiceoverRepository is required",
		},
		{
			name:        "nil Finalizer",
			mutate:      func(d *ProcessSegmentDeps) { d.Finalizer = nil },
			wantPanicRe: "Finalizer is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := validDeps
			tt.mutate(&deps)
			// Production uses `panic("string literal")` — Testify's
			// PanicsWithError requires the panic value to implement
			// `error`; a string panic value fails the type assertion
			// inside Testify. Use recover + assert.Contains on the
			// formatted panic value instead (godlike/07 typed-error
			// contract spirit: assert WHICH guard triggered, not
			// just "something panicked").
			var gotPanic any
			func() {
				defer func() { gotPanic = recover() }()
				NewProcessSegmentUseCase(deps)
			}()
			require.NotNil(t, gotPanic,
				"%s: NewProcessSegmentUseCase must panic on this nil mandatory dep (godlike/07 fail-fast at composition)", tt.name)
			assert.Contains(t, fmt.Sprint(gotPanic), tt.wantPanicRe,
				"%s: panic message must contain canonical '%s' substring (production regression guard)", tt.name, tt.wantPanicRe)
		})
	}

	// Bonus: assert the nil-safe deps do NOT panic when nil.
	t.Run("nil AudioPostProcessor OK (nil-safe)", func(t *testing.T) {
		deps := validDeps
		deps.AudioPostProcessor = nil
		assert.NotPanics(t, func() { NewProcessSegmentUseCase(deps) })
	})
	t.Run("nil Logger OK (zap.NewNop() fallback)", func(t *testing.T) {
		deps := validDeps
		deps.Logger = nil
		assert.NotPanics(t, func() { NewProcessSegmentUseCase(deps) })
	})
}

// ─────────────────────────────────────────────────────────────────────────
// Test 2: DRY-wiring source-grep INVERSE regression guard
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_DRY_Wiring_SourceGrep pins the godlike/06
// one-canonical-owner invariant at the source level: BOTH the batch
// use case (usecase.go) AND the per-item use case (process_voiceover_item.go)
// MUST delegate to the shared ProcessSegmentUseCase via `processSeg.Execute(`.
//
// A hermetic version of this test would construct both use cases with
// the same shared deps and assert they produce equivalent results —
// but that passes even if production wiring silently inlines either
// body. The source-grep variant is robust against that regression:
// inlining either body removes `processSeg.Execute(` from the
// source file, surfacing the failure here as a build-time test.
//
// Pre-DRY the bodies had ~120 lines of inline 4-stage code each (TTS /
// AudioPost / Publish / BeginTx + Finalize + Commit). Post-DRY the
// bodies shrink to a thin wrapper around ProcessSegmentCommand construction
// + Execute delegation. The grep proves the bodies did not regress.
func TestProcessSegmentUseCase_DRY_Wiring_SourceGrep(t *testing.T) {
	delegationRe := regexp.MustCompile(`processSeg\.Execute\(`)

	files := []struct {
		path    string
		context string
	}{
		{"usecase.go", "batch use case (GenerateVoiceoversUseCase.processOneLanguage)"},
		{"process_voiceover_item.go", "per-item use case (ProcessVoiceoverItemUseCase.Execute)"},
	}

	for _, f := range files {
		t.Run(f.context, func(t *testing.T) {
			content, err := os.ReadFile(f.path)
			require.NoError(t, err, "must read %s (canonical DRY delegation source)", f.path)
			assert.Regexp(t, delegationRe, string(content),
				"PR-VO-USECASE-PROCESS-DRY DRY invariant: %s MUST delegate to processSeg.Execute("+
					" (godlike/06 SSOT one-canonical-owner). A regression that inlines the per-item body breaks this guard.",
				f.path)
		})
	}

	// Cross-reference: assert the SHARED ProcessSegmentUseCase is the
	// binding target (not a separate canonical body). The string
	// `processSeg` field is declared on both use case structs per
	// their godlike/06 SSOT doc comments.
	for _, f := range files {
		content, err := os.ReadFile(f.path)
		require.NoError(t, err)
		// Two independent substring checks (whitespace-tolerant: production
		// uses `processSeg    *ProcessSegmentUseCase` with 4-space canonical
		// struct-field formatting, so a literal single-space Contains would
		// silently fail on production's correct shape).
		assert.Contains(t, string(content), "processSeg",
			"%s MUST hold a processSeg field (godlike/06 delegation anchor)", f.path)
		assert.Contains(t, string(content), "*ProcessSegmentUseCase",
			"%s MUST hold a *ProcessSegmentUseCase field (godlike/06 delegation anchor)", f.path)
	}
}

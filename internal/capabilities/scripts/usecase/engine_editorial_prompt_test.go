// Package usecase — engine_editorial_prompt_test.go
//
// Kept in the parent package (2026-09-12) when the generation core moved to
// usecase/gencore: this test asserts on the editorial prompt built by
// scripts/generation together with BuildClipFingerprint, which still lives in
// this package. It uses no gencore internals, so it stays here rather than
// creating an import cycle from gencore's tests back into usecase.
package usecase

import (
	"testing"

	"github.com/stretchr/testify/assert"

	generationpkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/generation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestBuildEditorialPrompt_DoesNotIncludeFingerprint asserts that the
// editorial prompt never contains the item identity fingerprint hash.
// Pre-PR 2, buildPrompt returned BuildItemIdentity(item) — a SHA-256
// digest sent to the model as the prompt, which is wrong on every
// front (no editorial content, hides intent, leaks identity).
func TestBuildEditorialPrompt_DoesNotIncludeFingerprint(t *testing.T) {
	t.Parallel()
	item := scriptpkg.GenerationItemV2{
		ID:    "fp-test",
		Title: "FP Test",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "Deterministic assembly test",
			SourceText: "alpha beta gamma",
			Guidelines: "Documentary tone.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 250,
		},
		Style:    "cinematic",
		Language: "en",
		Tone:     "neutral",
	}
	editorial := generationpkg.BuildPlan(item).RenderedPrompt
	// P0 #1 (June 2026): BuildClipFingerprint replaces the Phase 1b
	// stub BuildItemIdentity. The editorial prompt must never contain
	// the source fingerprint — that was the pre-PR 2 anti-pattern
	// where the model prompt WAS the fingerprint hash.
	fp := BuildClipFingerprint(item.Source, nil)
	assert.NotEmpty(t, fp, "fingerprint must be non-empty after P0 #1 fix")
	assert.NotContains(t, editorial, fp, "RenderedPrompt must NOT contain the item fingerprint hash")
	assert.Contains(t, editorial, "Documentary tone.", "editorial prompt should include source guidelines")
	assert.Contains(t, editorial, "250", "editorial prompt should include target words")
}

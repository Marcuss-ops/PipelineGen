// Package script_test — cache_key_test.go covers the canonical
// memory-gate cache key derivation acceptance criteria from PR 2:
//
//   - fingerprint-not-in-prompt: source fingerprint is a cache-key
//     input but must never appear in plan.RenderedPrompt
//   - cache-key-stable: same plan -> same key
//   - language-changes-key: en vs it -> different keys
//   - force-refresh-bypasses-read: ForceRefresh is a control flag,
//     not an identity input; changing it must NOT change the key
//   - output/document/image/voiceover flags: excluded from identity
package script_test

import (
	"strings"
	"testing"

	script "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/stretchr/testify/assert"
)

// basePlan returns a fully-populated plan matching the PR 2
// "everything filled in" case so per-test edits only mutate one
// axis at a time.
func basePlan() script.ResolvedGenerationPlan {
	return script.ResolvedGenerationPlan{
		Title:               "Cache Key Test",
		Topic:               "Test Topic",
		Language:            "en",
		Tone:                "documentary",
		Model:               "llama3:8b",
		Style:               "cinematic",
		SourceKind:          "text",
		Guidelines:          "Documentary tone.",
		SourceFingerprint:   "fp-abc123",
		TargetWords:         500,
		PromptVersion:       "v1",
		EditorPromptVersion: "v1",
		QAPromptVersion:     "v1",
		PromptProfile:       "default-v1",
	}
}

func TestBuildCacheKey_Deterministic(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	k1 := script.BuildCacheKey(&p1)
	k2 := script.BuildCacheKey(&p2)
	assert.NotEmpty(t, k1)
	assert.Equal(t, k1, k2, "same plan must produce same cache key")
	assert.Len(t, k1, 16, "cache key is first 16 hex chars of SHA-256, matching BuildItemIdentity")
}

func TestBuildCacheKey_DifferentLanguageChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.Language = "it"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentFingerprintChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.SourceFingerprint = "fp-different"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentStyleChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.Style = "narrative"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentModelChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.Model = "qwen2:7b"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentSourceKindChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p1.SourceKind = "text"
	p2.SourceKind = "clip"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentPromptVersionChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.PromptVersion = "v2"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentPromptProfileChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.PromptProfile = "experimental-v3"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentEditorPromptVersionChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.EditorPromptVersion = "v2"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentQAPromptVersionChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.QAPromptVersion = "v2"
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentGuidelinesChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.Guidelines = "Comedic tone."
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DifferentTargetWordsChanges(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.TargetWords = 800
	assert.NotEqual(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_SegmentSizingFieldsExcluded(t *testing.T) {
	t.Parallel()
	// Segment sizing fields NumClips and SegmentWords must not
	// change the text-identity fingerprint. SegmentTopics is
	// intentionally not part of this exclusion any more.
	p1 := basePlan()
	p2 := basePlan()
	p1.NumClips = 2
	p1.SegmentWords = 120
	p2.NumClips = 3
	p2.SegmentWords = 180
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2),
		"NumClips and SegmentWords must not change the canonical cache key")
}

// ── Excludes (control flags / postprocessors must not change key) ────

func TestBuildCacheKey_ForceRefreshExcluded(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.ForceRefresh = true
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2),
		"ForceRefresh must not be in cache key (control flag, not identity)")
}

func TestBuildCacheKey_SaveToDBExcluded(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.SaveToDB = true
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_DriveFolderIDExcluded(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p2 := basePlan()
	p2.DriveFolderID = "drive-folder-xyz"
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_OutputFmtExcluded(t *testing.T) {
	t.Parallel()
	// PR 2 acceptance: "immagini on/off non cambiano la cache key
	// del testo". OutputFmt is the prose/json switch — output flag.
	p1 := basePlan()
	p2 := basePlan()
	p2.OutputFmt = "json"
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_LanguagesExcluded(t *testing.T) {
	t.Parallel()
	// Even though Language (single) is in the key, Languages
	// (translation multi-list) is a postprocessing-only flag.
	p1 := basePlan()
	p2 := basePlan()
	p2.Languages = []string{"it", "es"}
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2))
}

func TestBuildCacheKey_NilPlanReturnsEmpty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", script.BuildCacheKey(nil))
}

// ── Hash identity hygiene ─────────────────────────────────────────────

func TestBuildCacheKey_OnlyHexChars(t *testing.T) {
	t.Parallel()
	p := basePlan()
	k := script.BuildCacheKey(&p)
	assert.Equal(t, 16, len(k), "cache key is 16 hex chars (first 16 from SHA-256)")
	for _, c := range k {
		assert.True(t,
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"every char must be 0-9 or a-f, got %q in %q", c, k)
	}
}

// Pre-PR 2 the prompt WAS the hash. Verify that hash bytes (a
// fingerprint-style value) don't somehow leak back into the
// rendered prompt through BuildCacheKey. This is a regression
// guard: if BuildCacheKey grew fields that include the prompt
// body, this test would catch it.

// ── PR-CS-1 / FASE 7 (DoD #9): ScriptSegment fingerprint sensitivity ──
// 7 user-spec tests pinning the contract that every ScriptSegment
// mutation (topic / source_text / target_words / order / count)
// produces a different fingerprint. The 7th test pins the canonical
// "same input → same fingerprint" cache-hit invariant so the order-
// sensitivity work doesn't accidentally produce collisions.

// baseFingerprintInput returns a fully-populated 2-segment plan
// fingerprint input (single canonical baseline for the 7 tests).
// PR-CS-1 / FASE 7: Slices/struct fields are intentionally NOT
// shared between base- and mutation-test plans so a regression in
// the defensive clone (cloneFingerprintInput) cannot accidentally
// produce equality-under-mutation.
func baseFingerprintInput() script.GenerationFingerprintInput {
	return script.GenerationFingerprintInput{
		ContractVersion: 1,
		SourceType:      "text",
		Language:        "en",
		Tone:            "documentary",
		Style:           "cinematic",
		TargetWords:     500,
		Model:           "llama3:8b",
		Segments: []script.ScriptSegment{
			{Topic: "intro", SourceText: "first source.", TargetWords: 80},
			{Topic: "body", SourceText: "second source.", TargetWords: 200},
		},
	}
}

func TestFingerprint_DifferentTopic_DifferentFP(t *testing.T) {
	t.Parallel()
	base := baseFingerprintInput()
	mutated := baseFingerprintInput()
	mutated.Segments[0].Topic = "intro_REORDERED"
	baseFP := script.BuildFingerprint(base)
	mutFP := script.BuildFingerprint(mutated)
	if baseFP == mutFP {
		t.Fatalf("topic mutation MUST change fingerprint; both %q", baseFP)
	}
}

func TestFingerprint_DifferentSourceText_DifferentFP(t *testing.T) {
	t.Parallel()
	base := baseFingerprintInput()
	mutated := baseFingerprintInput()
	mutated.Segments[1].SourceText = "DIFFERENT second source."
	baseFP := script.BuildFingerprint(base)
	mutFP := script.BuildFingerprint(mutated)
	if baseFP == mutFP {
		t.Fatalf("source_text mutation MUST change fingerprint; both %q", baseFP)
	}
}

func TestFingerprint_DifferentTargetWords_DifferentFP(t *testing.T) {
	t.Parallel()
	base := baseFingerprintInput()
	mutated := baseFingerprintInput()
	mutated.Segments[0].TargetWords = 81
	baseFP := script.BuildFingerprint(base)
	mutFP := script.BuildFingerprint(mutated)
	if baseFP == mutFP {
		t.Fatalf("target_words mutation MUST change fingerprint; both %q", baseFP)
	}
}

func TestFingerprint_DifferentOrder_DifferentFP(t *testing.T) {
	t.Parallel()
	base := baseFingerprintInput()
	mutated := baseFingerprintInput()
	mutated.Segments[0], mutated.Segments[1] = mutated.Segments[1], mutated.Segments[0]
	baseFP := script.BuildFingerprint(base)
	mutFP := script.BuildFingerprint(mutated)
	if baseFP == mutFP {
		t.Fatalf("segment reorder MUST change fingerprint (DoD #9 reverse); both %q", baseFP)
	}
}

func TestFingerprint_AddedSegment_DifferentFP(t *testing.T) {
	t.Parallel()
	base := baseFingerprintInput()
	mutated := baseFingerprintInput()
	mutated.Segments = append(mutated.Segments, script.ScriptSegment{Topic: "outro", TargetWords: 60})
	baseFP := script.BuildFingerprint(base)
	mutFP := script.BuildFingerprint(mutated)
	if baseFP == mutFP {
		t.Fatalf("adding a segment MUST change fingerprint; both %q", baseFP)
	}
}

func TestFingerprint_RemovedSegment_DifferentFP(t *testing.T) {
	t.Parallel()
	base := baseFingerprintInput()
	mutated := baseFingerprintInput()
	mutated.Segments = mutated.Segments[:1]
	baseFP := script.BuildFingerprint(base)
	mutFP := script.BuildFingerprint(mutated)
	if baseFP == mutFP {
		t.Fatalf("removing a segment MUST change fingerprint; both %q", baseFP)
	}
}

func TestFingerprint_SameInput_SameFP(t *testing.T) {
	t.Parallel()
	a := baseFingerprintInput()
	b := baseFingerprintInput()
	fpA := script.BuildFingerprint(a)
	fpB := script.BuildFingerprint(b)
	if fpA != fpB {
		t.Fatalf("identical inputs MUST produce equal fingerprints (cache hit); %q vs %q", fpA, fpB)
	}
	if len(fpA) != 16 {
		t.Fatalf("fingerprint must be 16 hex chars; got %q (len=%d)", fpA, len(fpA))
	}
}

func TestBuildCacheKey_KeyAndRenderedPromptIndependent(t *testing.T) {
	t.Parallel()
	p1 := basePlan()
	p1.RenderedPrompt = "A long, naturalist prompt body."
	k1 := script.BuildCacheKey(&p1)
	p2 := basePlan()
	p2.RenderedPrompt = "A very different, fictionalist prompt body."
	k2 := script.BuildCacheKey(&p2)
	assert.Equal(t, k1, k2, "different RenderedPrompt must NOT change the cache key")
	// Sanity check the key shape isn't suspicious.
	assert.False(t, strings.Contains(k1, " "), "cache key must not contain prompt-body whitespace")
}

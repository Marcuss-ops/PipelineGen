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
		Title:             "Cache Key Test",
		Topic:             "Test Topic",
		Language:          "en",
		Tone:              "documentary",
		Model:             "llama3:8b",
		Style:             "cinematic",
		SourceKind:        "text",
		Guidelines:        "Documentary tone.",
		SourceFingerprint: "fp-abc123",
		TargetWords:       500,
		PromptVersion:     "v1",
		PromptProfile:     "default-v1",
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

func TestBuildCacheKey_SegmentFieldsExcluded(t *testing.T) {
	t.Parallel()
	// The canonical GenerationFingerprintInput does not include
	// segment sizing fields; they affect scene planning but are not
	// part of the text-identity fingerprint.
	p1 := basePlan()
	p2 := basePlan()
	p1.NumClips = 2
	p1.SegmentWords = 120
	p1.SegmentTopics = []string{"A", "B"}
	p2.NumClips = 3
	p2.SegmentWords = 180
	p2.SegmentTopics = []string{"A", "C"}
	assert.Equal(t, script.BuildCacheKey(&p1), script.BuildCacheKey(&p2),
		"segment sizing fields must not change the canonical cache key")
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

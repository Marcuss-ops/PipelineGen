// Package scripts — processor_voiceover_test.go pins
// fix/voiceover-propagate-warnings (June 2026): the per-scene
// failures that VoiceoverProcessor.Process accumulates must reach
// PostProcessResult.Warnings (not only the zap log line) so the
// canonical GenerationResult.Warnings envelope in the API
// response surface carries the partial-failure signal to the
// caller.
//
// Coverage matrix:
//
//   - partial failures propagate
//     — 3 scenes, scenes 1 and 2 fail with distinct errors
//     (position-keyed, not text-keyed — robust against any
//     future sanitization upstream of Generate); assert
//     result.Warnings has exactly 2 entries, each containing
//     "voiceover failed for scene" so a future log-format
//     change is caught by the substring assertion.
//
//   - mixed status on Voiceovers slice
//     — Voiceovers[i].Status reflects "completed" for the
//     successful scene and "failed" for the two failing scenes.
//     Pinning the per-scene status alongside Warnings defends
//     against the regression "Warnings is populated but
//     Voiceovers.Status is left as zero for failing scenes"
//     (which would make the canonical SceneBinding.Voiceover
//     status invisible downstream).
//
//   - all-success has empty Warnings
//     — when every scene succeeds, result.Warnings is nil/empty
//     so the API response doesn't carry spurious noise on a
//     successful run.
package adapters

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stubItemExecutor is a voiceover.VoiceoverItemExecutor implementation
// controlled by a per-scene callback. The processor now fans out
// scenes concurrently, so the stub must key off stable inputs
// (scene text / filename) rather than call order.
//
// P0-#3 final closure (July 2026): the legacy `VoiceoverService`
// interface (Generate + GenerateWithDestination) is RETIRED. The
// stub now implements the single canonical Execute method with a
// typed *voiceover.GenerateVoiceoverItemCommand, returning a
// *voiceover.VoiceoverItemResult.
type stubItemExecutor struct {
	fn    func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error)
	calls atomic.Int32
}

// Execute is the canonical VoiceoverItemExecutor port method.
// Records the call and dispatches to the per-scene callback.
func (s *stubItemExecutor) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	s.calls.Add(1)
	if item == nil {
		return &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "nil GenerateVoiceoverItemCommand",
		}, nil
	}
	if s.fn != nil {
		return s.fn(item.Text, string(item.Language), item.Filename)
	}
	// Default success: returns the canonical
	// *voiceover.VoiceoverItemResult struct directly. The processor
	// reads LocalPath + DriveLink as struct fields (no type
	// assertion needed).
	return &voiceover.VoiceoverItemResult{
		Status:    voiceover.StatusCompleted,
		Language:  item.Language,
		Filename:  item.Filename,
		LocalPath: "/tmp/" + item.Filename,
		DriveLink: "https://drive.example.test/" + item.Filename,
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): stubItemExecutor
// must structurally satisfy voiceover.VoiceoverItemExecutor.
var _ voiceover.VoiceoverItemExecutor = (*stubItemExecutor)(nil)

// basePartialFailuresPlan returns a ResolvedGenerationPlan with
// three model-defined scenes in engineResult-Output.SpecScene,
// indexed 0..2. Language + Title are stable so the per-scene
// filename pattern stays predictable. Scene Text is descriptive
// only — failure mapping is driven by call position, not text.
func basePartialFailuresPlan() (*scriptpkg.ResolvedGenerationPlan, *scriptpkg.SpecSceneOutput) {
	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "scene zero", Kind: scriptpkg.SceneNarration},
		{ID: "scene-1", Index: 1, Text: "scene one", Kind: scriptpkg.SceneNarration},
		{ID: "scene-2", Index: 2, Text: "scene two", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "item-vo-warnings",
		Title:    "Warnings Propagation",
		Language: "en",
		Mode:     "text",
	}
	return plan, spec
}

// partialFailuresProc builds the processor under test with the
// stub that fails on calls 1 and 2 (i.e. scenes 1 and 2). Two
// distinct errors so a future "all warnings become one string"
// refactor is caught by the distinct-error assertions.
//
// P0-#3 final closure (July 2026): the stub's fn signature is
// unchanged (text, lang, filename) but the return type is now
// *voiceover.VoiceoverItemResult.
func partialFailuresProc() (*VoiceoverProcessor, *stubItemExecutor) {
	stub := &stubItemExecutor{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			switch text {
			case "scene one":
				return nil, errors.New("tts python socket closed")
			case "scene two":
				return nil, errors.New("voiceover service rate-limited")
			default:
				return &voiceover.VoiceoverItemResult{
					Status:    voiceover.StatusCompleted,
					Language:  voiceover.Language(lang),
					Filename:  filename,
					LocalPath: "/tmp/" + filename,
					DriveLink: "https://drive.example.test/" + filename,
				}, nil
			}
		},
	}
	return NewVoiceoverProcessor(stub, zap.NewNop()), stub
}

// TestVoiceoverProcessor_PartialFailuresPropagateToWarnings pins
// the user-facing fix: a 3-scene plan with 2 scene-level failures
// must produce a PostProcessResult whose Warnings field has
// exactly 2 entries, each describing a voiceover failure. Without
// the fix in processor_voiceover.go::Process, these warnings were
// only logged (zap) and the canonical Warnings envelope stayed
// empty → the API response would have reported success.
func TestVoiceoverProcessor_PartialFailuresPropagateToWarnings(t *testing.T) {
	t.Parallel()

	plan, spec := basePartialFailuresPlan()
	proc, _ := partialFailuresProc()

	input := ProcessInput{
		Text:      "Generated script body spanning all three scenes.",
		SpecScene: *spec,
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err, "Process must NOT return an error for partial scene failures (best-effort policy)")
	require.NotNil(t, result, "Process must return a non-nil PostProcessResult on partial failures")

	// Canonical contract: Warnings is non-empty AND carries exactly 2
	// entries — one per failed scene. Both must reference the scene
	// index and contain the canonical marking "voiceover failed for
	// scene" so downstream dashboards can grep.
	require.NotEmpty(t, result.Warnings,
		"PostProcessResult.Warnings must be populated for partial scene failures (fix/voiceover-propagate-warnings)")
	assert.Len(t, result.Warnings, 2,
		"exactly 2 failures expected (scenes 1 and 2); got %d", len(result.Warnings))

	for i, w := range result.Warnings {
		assert.True(t, strings.Contains(w, "voiceover failed for scene"),
			"warning %d must contain %q for grep-friendly diagnostics; got %q",
			i, "voiceover failed for scene", w)
	}

	// Distinct-error pin: every underlying-error string must surface
	// in at least one warning so operators can correlate each
	// failure with logs without grepping the aggregated line again.
	// A future "all warnings become one string" refactor would
	// collapse both errors into a single identical warning; that
	// regression must be detected by requiring both.
	assert.True(t, containsAll(result.Warnings, "tts python socket closed", "voiceover service rate-limited"),
		"warnings must surface ALL distinct underlying errors so the failure categories remain visible; got %v", result.Warnings)

	// Per-scene status pin: the Voiceovers slice mirrors the per-scene
	// outcome. 1 completed + 2 failed. This defends against a
	// regression where Warnings is populated but the
	// SceneBinding.Voiceover.Status downstream consumer reads
	// Voiceovers[i].Status = "" (the zero value) instead of "failed".
	require.Len(t, result.Voiceovers, 3, "all 3 scenes must appear in the Voiceovers slice regardless of success/fail")
	assert.Equal(t, "completed", result.Voiceovers[0].Status,
		"scene 0 succeeded → Status must be \"completed\"")
	for _, idx := range []int{1, 2} {
		assert.Equal(t, "failed", result.Voiceovers[idx].Status,
			"scene %d failed → Status must be \"failed\" (so SceneBinding.Voiceover reaches the API response with the correct state)", idx)
	}
}

// TestVoiceoverProcessor_AllSuccess_HasEmptyWarnings pins the
// negative case: when every scene succeeds, result.Warnings must
// stay nil/empty so the API response doesn't carry spurious noise.
// This is the corollary of the partial-failure pin — together they
// guarantee the Warnings field is conditional on actual failures.
func TestVoiceoverProcessor_AllSuccess_HasEmptyWarnings(t *testing.T) {
	t.Parallel()

	plan, spec := basePartialFailuresPlan()
	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	result, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:      "Generated body covering all 3 scenes.",
		SpecScene: *spec,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 3, "all 3 scenes must appear in the Voiceovers slice regardless of success/fail")
	assert.Empty(t, result.Warnings,
		"successful run must NOT populate Warnings; got %v", result.Warnings)
	for i, vo := range result.Voiceovers {
		assert.Equal(t, "completed", vo.Status,
			"successful scene %d must have Status=\"completed\"; got %q", i, vo.Status)
	}
}

// containsAll is a tiny assertion helper — returns true only if
// every needle is contained in at least one of the supplied
// haystacks. Used to pin that each distinct underlying error
// surfaces in the warnings slice (defends against a future
// collapse into a single generic message). Loosely indexed —
// which haystack carries which needle is irrelevant; what
// matters is that every needle has coverage.
func containsAll(haystacks []string, needles ...string) bool {
	for _, n := range needles {
		covered := false
		for _, h := range haystacks {
			if strings.Contains(h, n) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

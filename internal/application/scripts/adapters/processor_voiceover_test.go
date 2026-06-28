// Package scripts — processor_voiceover_test.go pins
// fix/voiceover-propagate-warnings (June 2026): the per-scene
// failures that VoiceoverProcessor.Process accumulates must reach
// PostProcessResult.Warnings.
//
// PR 5 (June 2026): stubVoiceoverGenerator replaces the legacy
// stubVoiceoverService. The stub now implements the typed
// VoiceoverGenerator port — returns *domain.VoiceoverResult,
// not interface{} or map[string]any.
package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// stubVoiceoverGenerator implements the typed VoiceoverGenerator port.
// The processor iterates scenes in index order (0..N-1), so the N-th
// entry in failOnCall is the error returned for scene index N.
type stubVoiceoverGenerator struct {
	failOnCall map[int]error
	callCount  int
}

func (s *stubVoiceoverGenerator) Generate(_ context.Context, cmd domain.GenerateVoiceoverCommand) (*domain.VoiceoverResult, error) {
	s.callCount++
	if err, ok := s.failOnCall[s.callCount-1]; ok && err != nil {
		return nil, err
	}
	return &domain.VoiceoverResult{
		Voice:       string(cmd.Locale),
		Locale:      string(cmd.Locale),
		DriveLink:   "https://drive.example.test/" + cmd.Reference.SceneID + ".mp3",
		LocalPath:   "/tmp/" + cmd.Reference.SceneID + ".mp3",
		ID:          domain.BuildID(cmd),
		Filename:    domain.BuildFilename(cmd, domain.BuildID(cmd)),
		ScriptID:    cmd.Reference.ScriptID,
		SceneID:     cmd.Reference.SceneID,
	}, nil
}

// basePartialFailuresPlan returns a ResolvedGenerationPlan with
// three model-defined scenes.
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

func partialFailuresProc() (*VoiceoverProcessor, *stubVoiceoverGenerator) {
	stub := &stubVoiceoverGenerator{
		failOnCall: map[int]error{
			1: errors.New("tts python socket closed"),
			2: errors.New("voiceover service rate-limited"),
		},
	}
	return NewVoiceoverProcessor(stub, zap.NewNop()), stub
}

func TestVoiceoverProcessor_PartialFailuresPropagateToWarnings(t *testing.T) {
	t.Parallel()

	plan, spec := basePartialFailuresPlan()
	proc, _ := partialFailuresProc()

	input := ProcessInput{
		Text:      "Generated script body spanning all three scenes.",
		SpecScene: *spec,
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.NotEmpty(t, result.Warnings)
	assert.Len(t, result.Warnings, 2)

	for i, w := range result.Warnings {
		assert.True(t, strings.Contains(w, "voiceover failed for scene"),
			"warning %d must contain %q; got %q", i, "voiceover failed for scene", w)
	}

	assert.True(t, containsAll(result.Warnings, "tts python socket closed", "voiceover service rate-limited"),
		"warnings must surface ALL distinct underlying errors; got %v", result.Warnings)

	require.Len(t, result.Voiceovers, 3)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	for _, idx := range []int{1, 2} {
		assert.Equal(t, "failed", result.Voiceovers[idx].Status)
	}
}

func TestVoiceoverProcessor_AllSuccess_HasEmptyWarnings(t *testing.T) {
	t.Parallel()

	plan, spec := basePartialFailuresPlan()
	stub := &stubVoiceoverGenerator{failOnCall: map[int]error{}}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	result, err := proc.Process(context.Background(), plan, ProcessInput{
		Text:      "Generated body covering all 3 scenes.",
		SpecScene: *spec,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 3)
	assert.Empty(t, result.Warnings)
	for i, vo := range result.Voiceovers {
		assert.Equal(t, "completed", vo.Status,
			"successful scene %d must have Status=\"completed\"; got %q", i, vo.Status)
	}
}

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

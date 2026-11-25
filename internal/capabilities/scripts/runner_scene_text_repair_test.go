package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// repairingTextGenerator implements both TextGenerator and SceneTextRepairer.
// GenerateSceneText is intentionally unused in these tests; the repair seam is
// the behavior under test.
type repairingTextGenerator struct {
	texts []string
	calls int
}

func (g *repairingTextGenerator) GenerateSceneText(context.Context, GenerateRequest) ([]Scene, error) {
	return nil, errors.New("GenerateSceneText must not be called by the repair path")
}

func (g *repairingTextGenerator) RepairSceneText(_ context.Context, _ GenerateRequest, scene Scene, _ int, attempt int) (Scene, error) {
	g.calls++
	if len(g.texts) == 0 {
		return Scene{}, errors.New("no repair text configured")
	}
	index := attempt - 1
	if index >= len(g.texts) {
		index = len(g.texts) - 1
	}
	out := scene
	if out.Text == nil {
		out.Text = make(map[Language]string)
	}
	out.Text["it"] = g.texts[index]
	return out, nil
}

func repairTestRequest() GenerateRequest {
	return GenerateRequest{
		Source:         Source{Type: SourceClips},
		SourceLanguage: "it",
		ScriptParams: scriptpkg.ScriptSpec{
			Segments: []scriptpkg.ScriptSegment{{
				ID:          "scene-1",
				Topic:       "una storia",
				TargetWords: 650,
				MinWords:    600,
			}},
		},
	}
}

func repeatedWords(count int) string {
	return strings.TrimSpace(strings.Repeat("parola ", count))
}

func TestRepairShortClipScenesExpandsToFloor(t *testing.T) {
	gen := &repairingTextGenerator{texts: []string{repeatedWords(650)}}
	runner := &Runner{textGen: gen}
	req := repairTestRequest()
	scenes := []Scene{{
		ID:    "scene-1",
		Index: 0,
		Text:  map[Language]string{"it": repeatedWords(435)},
	}}

	attempts := runner.repairShortClipScenes(context.Background(), "run-repair-ok", req, scenes, nil)

	if got := attempts["scene-1"]; got != 1 {
		t.Fatalf("repair attempts = %d, want 1", got)
	}
	if got := scriptpkg.WordCount(scenes[0].Text["it"]); got != 650 {
		t.Fatalf("repaired words = %d, want 650", got)
	}
	if err := clipSceneValidationError(scenes[0], req, 0, attempts); err != nil {
		t.Fatalf("repaired scene still invalid: %v", err)
	}
	if got := gen.calls; got != 1 {
		t.Fatalf("repair calls = %d, want 1", got)
	}
}

func TestRepairShortClipScenesStopsAfterTwoFailedRepairs(t *testing.T) {
	gen := &repairingTextGenerator{texts: []string{repeatedWords(435), repeatedWords(435)}}
	runner := &Runner{textGen: gen}
	req := repairTestRequest()
	scenes := []Scene{{
		ID:    "scene-1",
		Index: 0,
		Text:  map[Language]string{"it": repeatedWords(435)},
	}}

	attempts := runner.repairShortClipScenes(context.Background(), "run-repair-fail", req, scenes, nil)

	if got := attempts["scene-1"]; got != maxSceneTextRepairAttempts {
		t.Fatalf("repair attempts = %d, want %d", got, maxSceneTextRepairAttempts)
	}
	if got := scriptpkg.WordCount(scenes[0].Text["it"]); got != 435 {
		t.Fatalf("scene words = %d, want the unchanged 435", got)
	}
	err := clipSceneValidationError(scenes[0], req, 0, attempts)
	if err == nil {
		t.Fatal("short scene must still fail validation")
	}
	if !strings.Contains(err.Error(), "SCRIPT_SCENE_TEXT_INVALID") {
		t.Fatalf("error = %v, want SCRIPT_SCENE_TEXT_INVALID", err)
	}
	if !strings.Contains(err.Error(), "repair_attempts=2") {
		t.Fatalf("error = %v, want repair_attempts=2", err)
	}
	if got := gen.calls; got != maxSceneTextRepairAttempts {
		t.Fatalf("repair calls = %d, want %d", got, maxSceneTextRepairAttempts)
	}
}

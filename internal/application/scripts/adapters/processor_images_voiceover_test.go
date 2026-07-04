// Package adapters_test — processor_images_voiceover_test.go exercises
// the ImageProcessor and VoiceoverProcessor.
//
// PR 3 (June 2026): the processors now return results through
// PostProcessResult rather than mutating the model in-place.
// Tests assert on the returned SceneImage / SceneVoiceover slices.
//
// Step 1 (June 2026 drift fix): corrected import alias from bare
// `scripts` (root package, which has none of these types) to
// adapterspkg (the adapters subpackage where PostProcessResult,
// NewImageProcessor, NewVoiceoverProcessor, ImageGenService,
// VoiceoverService, ImageResult all live). Updated Process()
// call signatures to match the canonical (ctx, plan, ProcessInput)
// shape. Replaced PostProcessArtifact (nonexistent type) with
// PostProcessResult. Updated fakeImageGen return type to
// *adapterspkg.ImageResult.
package adapters_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Image processor fakes ─────────────────────────────────────────

type fakeImageGen struct {
	results []*adapterspkg.ImageResult
	errs    []error
	calls   atomic.Int32
}

func (f *fakeImageGen) SearchAndDownload(_ context.Context, _, _, _, _ string, _ interface{}) (*adapterspkg.ImageResult, error) {
	i := int(f.calls.Add(1) - 1)
	if i >= len(f.results) {
		return nil, errors.New("unexpected call index")
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.results[i], nil
}

// ── Voiceover processor fakes ─────────────────────────────────────

type fakeVoiceoverGen struct {
	fn    func(text, lang, filename string) (*voiceover.VoiceoverResult, error)
	calls atomic.Int32
}

func (f *fakeVoiceoverGen) Generate(_ context.Context, text, lang, filename string) (*voiceover.VoiceoverResult, error) {
	return f.GenerateWithDestination(context.Background(), text, lang, filename, nil)
}

func (f *fakeVoiceoverGen) GenerateWithDestination(_ context.Context, text, lang, filename string, _ *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error) {
	f.calls.Add(1)
	if f.fn != nil {
		return f.fn(text, lang, filename)
	}
	return &voiceover.VoiceoverResult{DriveLink: "http://default.example/" + filename, Path: "/tmp/" + filename}, nil
}

// ── Helpers ───────────────────────────────────────────────────────

func textOnlyPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{ID: "item-text", Language: "en"}
}

func planWithLanguage(lang string) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{ID: "item-" + lang, Language: lang}
}

func nScenesModel(n int) *scriptpkg.ModelScriptOutputV1 {
	scenes := make([]scriptpkg.SpecScene, n)
	for i := 0; i < n; i++ {
		scenes[i] = scriptpkg.SpecScene{
			ID:    "scene-" + itoaSimple(i),
			Index: i,
			Text:  scenePool[i%len(scenePool)],
			Kind:  scriptpkg.SceneImage,
		}
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Generated script.",
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}
}

var scenePool = []string{
	"First scene narration.",
	"Second scene narration.",
	"Third scene narration.",
	"Fourth scene narration.",
}

func itoaSimple(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoaSimple(-i)
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// processInputFromModel builds a ProcessInput from a ModelScriptOutputV1.
// The SpecScene is shared by reference (slice header copy) so the
// caller can still read model.SpecScene.Scenes after the processor runs.
func processInputFromModel(model *scriptpkg.ModelScriptOutputV1) adapterspkg.ProcessInput {
	if model == nil {
		return adapterspkg.ProcessInput{}
	}
	return adapterspkg.ProcessInput{
		Text:      model.Text,
		SpecScene: model.SpecScene,
	}
}

// ── Test: ImageProcessor ──────────────────────────────────────────

func TestImageProcessorNilGen(t *testing.T) {
	t.Parallel()
	proc := adapterspkg.NewImageProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), textOnlyPlan(), processInputFromModel(nScenesModel(2)))
	if err == nil {
		t.Fatal("expected error when ImageGenService is nil")
	}
}

func TestImageProcessorNoScenes(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*adapterspkg.ImageResult{{SourceURL: "http://img1"}}}
	proc := adapterspkg.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(0)
	_, err := proc.Process(context.Background(), textOnlyPlan(), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, int32(0), gen.calls.Load(), "gen should not be called when SpecScene is empty")
	assert.Len(t, model.SpecScene.Scenes, 0)
}

func TestImageProcessorNilModel(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*adapterspkg.ImageResult{{SourceURL: "http://img1"}}}
	proc := adapterspkg.NewImageProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), textOnlyPlan(), processInputFromModel(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should be non-nil")
	}
}

func TestImageProcessorSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*adapterspkg.ImageResult{{SourceURL: "http://img1.jpg"}}}
	proc := adapterspkg.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(1)
	result, err := proc.Process(context.Background(), planWithLanguage("en"), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, result)
	require.Len(t, result.SceneImages, 1)
	assert.Equal(t, "http://img1.jpg", result.SceneImages[0].URL)
}

func TestImageProcessorPartialFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{
		results: []*adapterspkg.ImageResult{{SourceURL: "http://img1.jpg"}, nil},
		errs:    []error{nil, errors.New("timeout")},
	}
	proc := adapterspkg.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(2)
	result, err := proc.Process(context.Background(), planWithLanguage("en"), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error (partial failures should not abort): %v", err)
	}
	require.NotNil(t, result)
	require.Len(t, result.SceneImages, 2)
	assert.Equal(t, "http://img1.jpg", result.SceneImages[0].URL)
	assert.Equal(t, "", result.SceneImages[1].URL)
	// ImageProcessor logs partial failures but does not populate
	// PostProcessResult.Warnings (unlike VoiceoverProcessor which
	// does). The log output is the canonical warning surface.
}

// ── Test: VoiceoverProcessor ──────────────────────────────────────

func TestVoiceoverProcessorNilGen(t *testing.T) {
	t.Parallel()
	proc := adapterspkg.NewVoiceoverProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), textOnlyPlan(), processInputFromModel(nScenesModel(2)))
	if err == nil {
		t.Fatal("expected error when VoiceoverService is nil")
	}
}

func TestVoiceoverProcessorNoScenes(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{}
	proc := adapterspkg.NewVoiceoverProcessor(gen, zap.NewNop())
	model := nScenesModel(0)
	result, err := proc.Process(context.Background(), textOnlyPlan(), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, result)
	assert.Equal(t, int32(0), gen.calls.Load(), "gen should not be called when SpecScene is empty")
}

func TestVoiceoverProcessorNilModel(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{}
	proc := adapterspkg.NewVoiceoverProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), textOnlyPlan(), processInputFromModel(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should be non-nil")
	}
}

func TestVoiceoverProcessorSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverResult, error) {
			return &voiceover.VoiceoverResult{
				DriveLink: "http://vo.mp3",
				Path:      "/tmp/" + filename,
			}, nil
		},
	}
	proc := adapterspkg.NewVoiceoverProcessor(gen, zap.NewNop())
	model := nScenesModel(1)
	result, err := proc.Process(context.Background(), planWithLanguage("en"), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, "http://vo.mp3", result.Voiceovers[0].Link)
	assert.Equal(t, "/tmp/unnamed_scene-0_en.mp3", result.Voiceovers[0].LocalPath)
}

func TestVoiceoverProcessorPartialFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{
		fn: func(text, lang, filename string) (*voiceover.VoiceoverResult, error) {
			if text == "Second scene narration." {
				return nil, errors.New("synthesis timeout")
			}
			return &voiceover.VoiceoverResult{
				DriveLink: "http://vo1.mp3",
				Path:      "/tmp/" + filename,
			}, nil
		},
	}
	proc := adapterspkg.NewVoiceoverProcessor(gen, zap.NewNop())
	model := nScenesModel(2)
	result, err := proc.Process(context.Background(), planWithLanguage("en"), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error (partial failures should not abort): %v", err)
	}
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 2)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, "http://vo1.mp3", result.Voiceovers[0].Link)
	assert.Equal(t, "failed", result.Voiceovers[1].Status)
	assert.Equal(t, "", result.Voiceovers[1].Link)
}

// ── Test: processor names ─────────────────────────────────────────

func TestImageProcessorName(t *testing.T) {
	proc := adapterspkg.NewImageProcessor(&fakeImageGen{results: []*adapterspkg.ImageResult{}}, zap.NewNop())
	if proc.Name() != adapterspkg.ProcessorImages {
		t.Errorf("expected name \"images\", got %q", proc.Name())
	}
}

func TestVoiceoverProcessorName(t *testing.T) {
	proc := adapterspkg.NewVoiceoverProcessor(&fakeVoiceoverGen{}, zap.NewNop())
	if proc.Name() != adapterspkg.ProcessorVoiceover {
		t.Errorf("expected name \"voiceover\", got %q", proc.Name())
	}
}

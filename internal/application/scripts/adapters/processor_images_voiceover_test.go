// Package scripts_test — processor_images_voiceover_test.go exercises
// the ImageProcessor and VoiceoverProcessor.
//
// PR 3 (June 2026): the processors walk model.SpecScene.Scenes and
// write directly into scene.Bindings.{Image, Voiceover}. Tests
// assert on the mutated model rather than on the pre-PR-3
// returned SceneImage / SceneVoiceover slices (which don't exist
// anymore).
//
// PR 3 close-out: switched from a custom `mustT` reflect wrapper
// to testify's standard assert/require. The previous closure was
// missing NotEmpty + had unprefixed reflect.{Array,Slice,Map,String}
// constants — kept compiling inline after the PR 3 type-walk
// rewrite to avoid one-off assertion helpers.
package adapters_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Image processor fakes ─────────────────────────────────────────

type fakeImageGen struct {
	results []*asset.ImageAsset
	errs    []error
	calls   atomic.Int32
}

func (f *fakeImageGen) SearchAndDownload(_ context.Context, _, _, _, _ string, _ interface{}) (*asset.ImageAsset, error) {
	i := int(f.calls.Add(1) - 1)
	if i >= len(f.results) {
		return nil, errors.New("unexpected call index")
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.results[i], nil
}

func (f *fakeImageGen) GenerateSmartImage(_ context.Context, _, _, _ string, _, _ []string, _, _ int, _ string, _ bool) (*asset.ImageAsset, error) {
	return nil, errors.New("not implemented")
}

// ── Voiceover processor fakes ─────────────────────────────────────

type fakeVoiceoverGen struct {
	results []map[string]any
	errs    []error
	calls   atomic.Int32
}

func (f *fakeVoiceoverGen) Generate(_ context.Context, _, _, _ string) (interface{}, error) {
	i := int(f.calls.Add(1) - 1)
	if i >= len(f.results) {
		return nil, errors.New("unexpected call index")
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.results[i], nil
}

func (f *fakeVoiceoverGen) GenerateWithDestination(_ context.Context, _, _, _ string, _ *voiceover.DestinationRequest) (interface{}, error) {
	// Delegates to Generate — the fake doesn't inspect the destination.
	return f.Generate(context.Background(), "", "", "")
}

// ── Helpers ───────────────────────────────────────────────────────

// textOnlyPlan returns a ResolvedGenerationPlan without clip
// evidence — the processors will no-op on empty SpecScene.
func textOnlyPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{ID: "item-text", Language: "en"}
}

// planWithLanguage returns a ResolvedGenerationPlan with the
// requested language; no clip evidence (PR 3 typed walk derives
// scenes from SpecScene, not from ClipEvidence).
func planWithLanguage(lang string) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{ID: "item-" + lang, Language: lang}
}

// nScenesModel returns a canonical typed ModelScriptOutputV1
// with n text-only scenes of kind narration.
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

// scenePool is a tiny pool of narration strings for tests with
// multiple scenes.
var scenePool = []string{
	"First scene narration.",
	"Second scene narration.",
	"Third scene narration.",
	"Fourth scene narration.",
}

// itoaSimple is a tiny strconv-free helper for test scene indexes.
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

// emptyArtifact returns a zero PostProcessArtifact accumulator
// (PR 3 typed signature).
func emptyArtifact() *scripts.PostProcessArtifact {
	return &scripts.PostProcessArtifact{}
}

// ── Test: ImageProcessor ──────────────────────────────────────────

func TestImageProcessorNilGen(t *testing.T) {
	t.Parallel()
	proc := scripts.NewImageProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), textOnlyPlan(), nScenesModel(2), emptyArtifact())
	if err == nil {
		t.Fatal("expected error when ImageGenService is nil")
	}
}

func TestImageProcessorNoScenes(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*asset.ImageAsset{{SourceURL: "http://img1"}}}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(0)
	_, err := proc.Process(context.Background(), textOnlyPlan(), model, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, int32(0), gen.calls.Load(), "gen should not be called when SpecScene is empty")
	assert.Len(t, model.SpecScene.Scenes, 0)
}

func TestImageProcessorNilModel(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*asset.ImageAsset{{SourceURL: "http://img1"}}}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), textOnlyPlan(), nil, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should be non-nil empty artifact")
	}
}

func TestImageProcessorSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*asset.ImageAsset{{SourceURL: "http://img1.jpg"}}}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(1)
	_, err := proc.Process(context.Background(), planWithLanguage("en"), model, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Assert the typed walk wrote the binding onto the scene.
	require.Len(t, model.SpecScene.Scenes, 1)
	binding := model.SpecScene.Scenes[0].Bindings.Image
	require.NotNil(t, binding)
	assert.Equal(t, "http://img1.jpg", binding.URL)
	assert.Equal(t, "generated", binding.Status)
	assert.NotEmpty(t, binding.Prompt)
}

func TestImageProcessorPartialFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{
		results: []*asset.ImageAsset{{SourceURL: "http://img1.jpg"}, nil},
		errs:    []error{nil, errors.New("timeout")},
	}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(2)
	_, err := proc.Process(context.Background(), planWithLanguage("en"), model, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error (partial failures should not abort): %v", err)
	}
	require.Len(t, model.SpecScene.Scenes, 2)
	require.NotNil(t, model.SpecScene.Scenes[0].Bindings.Image)
	assert.Equal(t, "http://img1.jpg", model.SpecScene.Scenes[0].Bindings.Image.URL)
	assert.Equal(t, "generated", model.SpecScene.Scenes[0].Bindings.Image.Status)
	require.NotNil(t, model.SpecScene.Scenes[1].Bindings.Image)
	assert.Equal(t, "failed", model.SpecScene.Scenes[1].Bindings.Image.Status)
	assert.Equal(t, "", model.SpecScene.Scenes[1].Bindings.Image.URL)
}

// ── Test: VoiceoverProcessor ──────────────────────────────────────

func TestVoiceoverProcessorNilGen(t *testing.T) {
	t.Parallel()
	proc := scripts.NewVoiceoverProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), textOnlyPlan(), nScenesModel(2), emptyArtifact())
	if err == nil {
		t.Fatal("expected error when VoiceoverService is nil")
	}
}

func TestVoiceoverProcessorNoScenes(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{results: []map[string]any{{"drive_link": "http://vo1", "path": "/tmp/vo1.mp3"}}}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	model := nScenesModel(0)
	_, err := proc.Process(context.Background(), textOnlyPlan(), model, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, int32(0), gen.calls.Load(), "gen should not be called when SpecScene is empty")
}

func TestVoiceoverProcessorNilModel(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{results: []map[string]any{{"drive_link": "http://vo1", "path": "/tmp/vo1.mp3"}}}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), textOnlyPlan(), nil, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should be non-nil empty artifact")
	}
}

func TestVoiceoverProcessorSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{
		results: []map[string]any{{"drive_link": "http://vo.mp3", "path": "/tmp/scene-1.mp3"}},
	}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	model := nScenesModel(1)
	_, err := proc.Process(context.Background(), planWithLanguage("en"), model, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.Len(t, model.SpecScene.Scenes, 1)
	binding := model.SpecScene.Scenes[0].Bindings.Voiceover
	require.NotNil(t, binding)
	assert.Equal(t, "completed", binding.Status)
	assert.Equal(t, "http://vo.mp3", binding.Link)
	assert.Equal(t, "/tmp/scene-1.mp3", binding.LocalPath)
}

func TestVoiceoverProcessorPartialFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{
		results: []map[string]any{
			{"drive_link": "http://vo1.mp3", "path": "/tmp/s1.mp3"},
			nil,
		},
		errs: []error{nil, errors.New("synthesis timeout")},
	}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	model := nScenesModel(2)
	_, err := proc.Process(context.Background(), planWithLanguage("en"), model, emptyArtifact())
	if err != nil {
		t.Fatalf("unexpected error (partial failures should not abort): %v", err)
	}
	require.Len(t, model.SpecScene.Scenes, 2)
	require.NotNil(t, model.SpecScene.Scenes[0].Bindings.Voiceover)
	assert.Equal(t, "completed", model.SpecScene.Scenes[0].Bindings.Voiceover.Status)
	assert.Equal(t, "http://vo1.mp3", model.SpecScene.Scenes[0].Bindings.Voiceover.Link)
	require.NotNil(t, model.SpecScene.Scenes[1].Bindings.Voiceover)
	assert.Equal(t, "failed", model.SpecScene.Scenes[1].Bindings.Voiceover.Status)
	assert.Equal(t, "", model.SpecScene.Scenes[1].Bindings.Voiceover.Link)
}

// ── Test: processor names ─────────────────────────────────────────

func TestImageProcessorName(t *testing.T) {
	proc := scripts.NewImageProcessor(&fakeImageGen{}, zap.NewNop())
	if proc.Name() != "images" {
		t.Errorf("expected name \"images\", got %q", proc.Name())
	}
}

func TestVoiceoverProcessorName(t *testing.T) {
	proc := scripts.NewVoiceoverProcessor(&fakeVoiceoverGen{}, zap.NewNop())
	if proc.Name() != "voiceover" {
		t.Errorf("expected name \"voiceover\", got %q", proc.Name())
	}
}

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
// ProcessInput, NewImageProcessor, NewVoiceoverProcessor,
// ImageGenService, VoiceoverService, ImageResult all live).
// Updated Process() call signatures to match the canonical
// (ctx, plan, ProcessInput) shape. Updated fakeImageGen return
// type to *adapterspkg.ImageResult.
//
// PR-LEGACY-CLEANUP-2026-07-10 Item 2: the obsolete `PostProcessArtifact`
// type alias (the historical accumulator name, never used in
// production code, with a single test-reference at this line) was
// retired alongside `internal/application/scripts/dto/compat_types.go`.
// The canonical surface is now `adapterspkg.PostProcessResult`
// (still imported above).
package adapters_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// ── Image processor fakes ─────────────────────────────────────────

type fakeImageGen struct {
	results []*adapterspkg.ImageResult
	errs    []error
	calls   atomic.Int32
}

func (f *fakeImageGen) SearchAndDownload(_ context.Context, sceneName, _, _, _ string) (*adapterspkg.ImageResult, error) {
	i := int(f.calls.Add(1) - 1)
	// Keep the fake deterministic under parallel fan-out by mapping
	// scene-<n> back to the corresponding fixture slot when possible.
	// Falls back to call order for any other sceneName format.
	if idx, ok := sceneIndexFromName(sceneName); ok && idx >= 0 && idx < len(f.results) {
		i = idx
	}
	if i >= len(f.results) {
		return nil, errors.New("unexpected call index")
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.results[i], nil
}

func sceneIndexFromName(sceneName string) (int, bool) {
	const prefix = "scene-"
	if !strings.HasPrefix(sceneName, prefix) {
		return 0, false
	}
	i, err := strconv.Atoi(strings.TrimPrefix(sceneName, prefix))
	if err != nil {
		return 0, false
	}
	return i, true
}

type blockingImageGen struct {
	release       chan struct{}
	prewarmCalled atomic.Int32
	prewarmMisses atomic.Int32
	calls         atomic.Int32
	inFlight      atomic.Int32
	maxInFlight   atomic.Int32
}

func (f *blockingImageGen) TriggerPrewarm(_ context.Context, _ string, _ int) {
	f.prewarmCalled.Store(1)
}

func (f *blockingImageGen) SearchAndDownload(_ context.Context, sceneName, _, _, _ string) (*adapterspkg.ImageResult, error) {
	if f.prewarmCalled.Load() == 0 {
		f.prewarmMisses.Add(1)
	}
	cur := f.inFlight.Add(1)
	for {
		max := f.maxInFlight.Load()
		if cur <= max || f.maxInFlight.CompareAndSwap(max, cur) {
			break
		}
	}
	f.calls.Add(1)
	<-f.release
	f.inFlight.Add(-1)
	return &adapterspkg.ImageResult{SourceURL: "http://img/" + sceneName}, nil
}

type generatedPriorityImageGen struct {
	searchCalls atomic.Int32
	genCalls    atomic.Int32
	lastPrompts []string
}

func (f *generatedPriorityImageGen) SearchAndDownload(_ context.Context, sceneName, _, _, _ string) (*adapterspkg.ImageResult, error) {
	f.searchCalls.Add(1)
	return &adapterspkg.ImageResult{SourceURL: "http://search/" + sceneName}, nil
}

func (f *generatedPriorityImageGen) GenerateSmartImage(_ context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error) {
	f.genCalls.Add(1)
	f.lastPrompts = append([]string(nil), prompts...)
	return &asset.ImageAsset{
		SourceURL:   "google-slides/" + subject + ".png",
		DriveFileID: "drive-" + subject,
	}, nil
}

// ── Voiceover processor fakes ─────────────────────────────────────

// fakeVoiceoverGen is a voiceover.VoiceoverItemExecutor stub used by
// the image+voiceover processor tests.
//
// P0-#3 final closure (July 2026): the legacy VoiceoverService port
// (Generate + GenerateWithDestination) is RETIRED. The stub now
// implements the single canonical Execute method with a typed
// *voiceover.GenerateVoiceoverItemCommand, returning a
// *voiceover.VoiceoverItemResult.
type fakeVoiceoverGen struct {
	fn    func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error)
	calls atomic.Int32
}

func (f *fakeVoiceoverGen) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	f.calls.Add(1)
	if item == nil {
		return &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "nil GenerateVoiceoverItemCommand",
		}, nil
	}
	if f.fn != nil {
		return f.fn(item.Text, string(item.Language), item.Filename)
	}
	return &voiceover.VoiceoverItemResult{
		Status:    voiceover.StatusCompleted,
		Language:  item.Language,
		Filename:  item.Filename,
		DriveLink: "http://default.example/" + item.Filename,
		LocalPath: "/tmp/" + item.Filename,
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): fakeVoiceoverGen must
// structurally satisfy voiceover.VoiceoverItemExecutor.
var _ voiceover.VoiceoverItemExecutor = (*fakeVoiceoverGen)(nil)

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

func TestImageProcessorPrefersGeneratedImagePath(t *testing.T) {
	t.Parallel()
	gen := &generatedPriorityImageGen{}
	proc := adapterspkg.NewImageProcessor(gen, zap.NewNop())
	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Generated script.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID:    "scene-0",
					Index: 0,
					Title: "Ancient Rome at dawn",
					Text:  "Long narrative about the early Republic and the Roman hills.",
					Kind:  scriptpkg.SceneImage,
				},
			},
		},
	}
	result, err := proc.Process(context.Background(), planWithLanguage("en"), processInputFromModel(model))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, result)
	require.Len(t, result.SceneImages, 1)
	assert.Equal(t, int32(1), gen.genCalls.Load(), "GenerateSmartImage should be preferred when available")
	assert.Equal(t, int32(0), gen.searchCalls.Load(), "SearchAndDownload should not be used when generation is available")
	assert.Equal(t, "https://drive.google.com/file/d/drive-Create a cinematic documentary image depicting: Ancient Rome at dawn/view", result.SceneImages[0].URL)
	require.Len(t, gen.lastPrompts, 3)
	assert.Equal(t, "Create a cinematic documentary image depicting: Ancient Rome at dawn", gen.lastPrompts[0], "the primary prompt should be an explicit visual instruction")
	assert.Equal(t, "Ancient Rome at dawn", gen.lastPrompts[1], "scene title should remain available as context")
	assert.Equal(t, "Long narrative about the early Republic and the Roman hills.", gen.lastPrompts[2], "full scene text should remain available as secondary context")
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

func TestImageProcessorWarmupAndParallelFanout(t *testing.T) {
	t.Parallel()

	gen := &blockingImageGen{
		release: make(chan struct{}),
	}

	proc := adapterspkg.NewImageProcessor(gen, zap.NewNop())
	model := nScenesModel(4)

	done := make(chan struct{})
	var result *adapterspkg.PostProcessResult
	var err error
	go func() {
		result, err = proc.Process(context.Background(), planWithLanguage("en"), processInputFromModel(model))
		close(done)
	}()

	require.Eventually(t, func() bool {
		return gen.calls.Load() >= 2
	}, 2*time.Second, 10*time.Millisecond, "expected at least two concurrent image generations")

	close(gen.release)
	<-done
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.SceneImages, 4)
	assert.Equal(t, int32(1), gen.prewarmCalled.Load(), "expected prewarm to run before fan-out")
	assert.Equal(t, int32(0), gen.prewarmMisses.Load(), "search should never start before warmup completes")
	assert.GreaterOrEqual(t, gen.maxInFlight.Load(), int32(2), "expected parallel fan-out to keep >1 generation in flight")
	for i, img := range result.SceneImages {
		assert.Equal(t, i, img.Index)
		assert.Equal(t, "http://img/scene-"+itoaSimple(i), img.URL)
	}
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
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			return &voiceover.VoiceoverItemResult{
				Status:    voiceover.StatusCompleted,
				Language:  voiceover.Language(lang),
				Filename:  filename,
				DriveLink: "http://vo.mp3",
				LocalPath: "/tmp/" + filename,
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
		fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
			if text == "Second scene narration." {
				return nil, errors.New("synthesis timeout")
			}
			return &voiceover.VoiceoverItemResult{
				Status:    voiceover.StatusCompleted,
				Language:  voiceover.Language(lang),
				Filename:  filename,
				DriveLink: "http://vo1.mp3",
				LocalPath: "/tmp/" + filename,
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

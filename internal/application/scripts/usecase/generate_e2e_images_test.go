// Package scripts — generate_e2e_images_test.go is the E2E acceptance
// pin for the canonical SpecScene + image binding contract.
//
// The test runs the FULL pipeline (GenerateOneUseCase.Execute) end-to-end
// with:
//   - a fakeOllamaGen whose Script emits a canonical V1 JSON envelope
//     carrying N model-defined scenes
//   - a PostProcessorRegistry with the **images** processor wired to a
//     fake ImageGenService that returns a canned SourceURL for every
//     scene
//   - SaveToDB=false so the persistence processor is NOT in
//     plan.Postprocessors (saves us from having to fake ScriptRepository)
//   - ExtractEntities=false / GenerateMetadata=false so neither
//     Required-class processor lands in the plan (avoids the
//     preflight ValidateRequested miss)
//   - clip_bindings + stock_association are unconditional BestEffort
//     processors; not registering them produces warnings only, not
//     a hard fail (per postprocessor_registry.go::Run semantics)
//
// The assertion surface is the **canonical** SpecScene JSON wire shape
// that the user-facing API emits:
//
//	{
//	  "output": {
//	    "specscene": {
//	      "version": 1,
//	      "scenes": [
//	        { "id": "scene-0", "index": 0, "bindings": {
//	          "image": { "url": "<...>", "status": "generated" }
//	        }},
//	        ...
//	      ]
//	    }
//	  }
//	}
//
// Why this matters: PR 6 (June 2026) introduced the dedicated
// `specscene TEXT` column on the scripts table and the typed
// ports.ScriptRecord.SpecScene field on the canonical row. The two
// surfaces (model-emitted SpecScene + per-scene image binding) MUST
// land on the same envelope so callers reading `result.output.specscene`
// at the JSON envelope see populated image bindings without a
// second duck-type fetch against `artifacts.document` or a manifest
// lookup.
//
// Per godlike/06 single-owner-per-fact, the image binding is written
// by exactly one postprocessor (ImageProcessor) into
// `result.Output.SpecScene.Scenes[i].Bindings.Image.{URL, Status}`;
// per godlike/07 no-fake-availability, the binding is a typed
// `*scriptpkg.ImageBinding` (NOT a stringified JSON column) and the
// status is the canonical literal "generated".
package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// fakeImageGenSvc implements adapters.ImageGenService and returns
// a deterministic SourceURL for every scene. Captures the call
// count so the E2E test can assert the processor is invoked once
// per scene (no silent skip when the model emits multiple scenes).
type fakeImageGenSvc struct {
	calls     int
	lastName  string
	sourceURL string
}

func newFakeImageGenSvc(sourceURL string) *fakeImageGenSvc {
	return &fakeImageGenSvc{sourceURL: sourceURL}
}

func (f *fakeImageGenSvc) SearchAndDownload(_ context.Context, sceneName, _sceneText, _altText, _language string) (*adapters.ImageResult, error) {
	f.calls++
	f.lastName = sceneName
	return &adapters.ImageResult{SourceURL: f.sourceURL}, nil
}

// TestGenerateE2E_GenerateSceneImages_BindsBindingImageStatus is
// the canonical E2E pin that the SpecScene + image binding
// wire shape is populated when GenerateSceneImages=true. Asserted:
//
//  1. The engine's model-emitted scenes survive all the way to
//     result.Output.SpecScene.Scenes (1:1 mapping by Index).
//  2. The image processor is invoked once per scene (no silent
//     drop between scenes and image generation calls).
//  3. Each scene's Bindings.Image is a non-nil typed
//     scriptpkg.ImageBinding (godlike/07 typed-envelope contract).
//  4. Each scene's Bindings.Image.Status is the canonical literal
//     "generated" (godlike/06: only one processor writes this; it
//     MUST be ImageProcessor, NOT a downstream retry / mock).
//  5. Each scene's Bindings.Image.URL matches the fake image
//     service's SourceURL (binding source-of-truth verified:
//     the URL flows from the typed port through mergePostProcessResult
//     into the canonical SpecScene envelope, NOT from a freeform
//     JSON rewrite).
func TestGenerateE2E_GenerateSceneImages_BindsBindingImageStatus(t *testing.T) {
	t.Parallel()

	// ── Engine: fake ollama returning canonical V1 with 3 scenes.
	const numScenes = 3
	sceneJSON := `{"schema_version":1,"text":"Narrative covering 3 distinct scenes.","specscene":{"version":1,"scenes":[` +
		`{"id":"scene-0","index":0,"text":"Opening narration.","kind":"narration","bindings":{}},` +
		`{"id":"scene-1","index":1,"text":"Conflict narration.","kind":"narration","bindings":{}},` +
		`{"id":"scene-2","index":2,"text":"Resolution narration.","kind":"narration","bindings":{}}` +
		`]}}`
	gen := &fakeOllamaGen{
		result: &ollamatypes.GenerationResult{
			Script:      sceneJSON,
			WordCount:   24,
			EstDuration: 6,
			Model:       "llama3:8b",
			Prompt:      "ignored",
		},
	}
	e := buildTestEngine(gen, nil) // nil memory gate → fresh generation path

	// ── Image processor + fake ImageGenService.
	const cannedURL = "https://drive.google.com/file/d/fake-image-001/view"
	imgSvc := newFakeImageGenSvc(cannedURL)
	imgProc := adapters.NewImageProcessor(imgSvc, zap.NewNop())

	// Registry: only images registered. clip_bindings + stock_association
	// are unconditional BestEffort; not registering them produces
	// warnings only (verified in postprocessor_registry_test.go
	// TestRegistry_RunProcessorNotRegistered + the empty-registry
	// regression tests).
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(imgProc), "images proc must register")
	ppReg.Freeze()

	// ── Use case: text-only plan path (Source.Type=SourceText)
	// means source resolution is a no-op even when registry=nil
	// (generate_one_usecase.go guards `if uc.registry != nil`).
	// SaveToDB=false keeps the persistence postprocessor OFF the
	// plan → no fake ScriptRepository needed.
	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, // registry
		e,
		ppReg,
		zap.NewNop(),
	)

	item := scriptpkg.GenerationItemV2{
		ID:       "e2e-images-item",
		Title:    "SpecScene Images E2E",
		Language: "en",
		Tone:     "neutral",
		Style:    "cinematic",
		Model:    "llama3:8b",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "Scene image binding canonical shape",
			SourceText: "Verify that an image postprocessor populates scene bindings.image.status on the SpecScene envelope.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 240,
		},
		Output: scriptpkg.OutputSpec{
			// GenerateSceneImages=true → plan.Postprocessors
			// becomes [clip_bindings, stock_association, images].
			GenerateSceneImages: true,
			// Opt-out of every other postprocessor.
			// SaveToDB=false: persistence NOT in plan.Postprocessors
			// (Required class without a fake repo would be a hard
			// failure at preflight).
			SaveToDB: false,
			// entities / metadata both Required; opt-out keeps
			// them off plan.Postprocessors so the ValidateRequested
			// preflight does not demand their registration.
			ExtractEntities:   false,
			GenerateMetadata:  false,
			GenerateVoiceover: false,
			GenerateDocument:  false,
		},
	}

	// ── Execute.
	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err, "Execute must succeed for a well-formed text-only item with generate_scene_images=true")
	require.NotNil(t, result, "Execute must return a non-nil result")

	// ── Assertion (1): SpecScene envelope is canonical V1.
	assert.Equal(t, 1, result.Output.SpecScene.Version,
		"SpecScene.Version must be 1 (canonical V1)")

	// ── Assertion (2): all 3 model-emitted scenes survive to result.
	require.GreaterOrEqual(t, imgSvc.calls, numScenes,
		"image processor must be called once per scene (got %d, want ≥%d)", imgSvc.calls, numScenes)
	require.Len(t, result.Output.SpecScene.Scenes, numScenes,
		"result must carry all %d model-emitted scenes", numScenes)

	// ── Assertion (3, 4, 5): per-scene image binding state.
	for i, sc := range result.Output.SpecScene.Scenes {
		assert.Equal(t, i, sc.Index,
			"scene[%d].Index must match the model-emitted index", i)
		require.NotNil(t, sc.Bindings.Image,
			"scene[%d].Bindings.Image must be populated when generate_scene_images=true (got nil)", i)
		// godlike/06 single-owner-per-fact: the canonical literal
		// is "generated" — ImageProcessor is the SOLE writer.
		assert.Equal(t, "generated", sc.Bindings.Image.Status,
			"scene[%d].Bindings.Image.Status must be 'generated' when image proc succeeds (got %q)", i, sc.Bindings.Image.Status)
		// godlike/06 binding source-of-truth: URL flows from the
		// fake ImageGenService.SourceURL through mergePostProcessResult
		// into the canonical envelope.
		assert.Equal(t, cannedURL, sc.Bindings.Image.URL,
			"scene[%d].Bindings.Image.URL must equal the fake ImageGenService.SourceURL (got %q)", i, sc.Bindings.Image.URL)
	}
}

// TestGenerateE2E_GenerateSceneImages_SingleScene_PinsStatus pins
// the same contract at the single-scene edge: even when the model
// produces exactly one scene (the smallest legal SpecScene envelope
// for clip-driven narration), the image binding shape must hold.
// Catches regressions where a "zero or one scene" guard short-circuits
// the image loop without surfacing the typed binding.
func TestGenerateE2E_GenerateSceneImages_SingleScene_PinsStatus(t *testing.T) {
	t.Parallel()

	const cannedURL = "https://drive.google.com/file/d/single-scene-img/view"
	sceneJSON := `{"schema_version":1,"text":"Solo narration.","specscene":{"version":1,"scenes":[{"id":"scene-0","index":0,"text":"Solo narration.","kind":"narration","bindings":{}}]}}`

	gen := &fakeOllamaGen{
		result: &ollamatypes.GenerationResult{
			Script:      sceneJSON,
			WordCount:   8,
			EstDuration: 2,
			Model:       "llama3:8b",
			Prompt:      "ignored",
		},
	}
	e := buildTestEngine(gen, nil)

	imgSvc := newFakeImageGenSvc(cannedURL)
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(adapters.NewImageProcessor(imgSvc, zap.NewNop())))
	ppReg.Freeze()

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil,
		e,
		ppReg,
		zap.NewNop(),
	)

	item := scriptpkg.GenerationItemV2{
		ID:       "e2e-single-scene",
		Title:    "Single Scene E2E",
		Language: "en",
		Model:    "llama3:8b",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "Single scene image binding",
			SourceText: "One scene → one image binding.",
		},
		ScriptParams: scriptpkg.ScriptSpec{TargetWords: 60},
		Output: scriptpkg.OutputSpec{
			GenerateSceneImages: true,
			SaveToDB:            false,
		},
	}

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Output.SpecScene.Scenes, 1,
		"single-scene envelope must survive the postprocessor walk")

	sc := result.Output.SpecScene.Scenes[0]
	require.NotNil(t, sc.Bindings.Image,
		"single-scene case must still produce a typed *scriptpkg.ImageBinding on Bindings.Image (no-nil godlike/07)")

	assert.Equal(t, "generated", sc.Bindings.Image.Status,
		"single-scene Status must be 'generated' (no silent no-op)")
	assert.Equal(t, cannedURL, sc.Bindings.Image.URL,
		"single-scene URL must equal the fake ImageGenService.SourceURL")
	assert.Equal(t, 1, imgSvc.calls,
		"single-scene case must invoke image proc exactly once")
}

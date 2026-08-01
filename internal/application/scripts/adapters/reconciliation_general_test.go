// Package adapters — reconciliation_general_test.go
// covers the general asset-location reconciliation processor behaviors
// that span multiple binding types: nil/missing verifier composition
// failures, empty/no-link no-ops, multi-language envelopes and
// full-structure document refresh preservation.
package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// 13. Nil verifier → hard error
func TestAssetLocationReconciliation_NilVerifierError(t *testing.T) {
	p := &AssetLocationReconciliationProcessor{verifier: nil}
	_, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
			sceneWithClip("c", "https://drive.google.com/file/d/x/view"),
		}},
	})
	if err == nil {
		t.Fatal("nil verifier must produce an error")
	}
}

// A missing verifier is a required composition failure: the registry must
// propagate the fail-closed scene and stop before document publication.
func TestAssetLocationReconciliation_MissingVerifierStopsDownstreamPublication(t *testing.T) {
	reconciliation := NewAssetLocationReconciliationProcessor(nil)
	downstream := &reconciliationDownstreamRecorder{}
	registry := NewPostProcessorRegistry(nil)
	if !registry.Register(reconciliation) || !registry.Register(downstream) {
		t.Fatal("expected reconciliation and downstream processors to register")
	}

	link := "https://drive.google.com/file/d/stale-before-gate/view"
	result, err := registry.Run(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		Postprocessors: []string{
			string(ProcessorAssetLocationReconciliation),
			string(ProcessorDocument),
		},
	}, ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{sceneWithClip("clip-1", link)},
	}})
	if err == nil {
		t.Fatal("missing verifier must fail the pipeline")
	}
	if downstream.calls != 0 {
		t.Fatalf("downstream document processor called %d times after gate failure", downstream.calls)
	}
	if result == nil || len(result.FinalSpecScene.Scenes) != 1 {
		t.Fatalf("expected fail-closed final scene, got %#v", result)
	}
	if got := result.FinalSpecScene.Scenes[0].Bindings.Clip.DriveLink; got != "" {
		t.Fatalf("stale link reached final pipeline surface: %q", got)
	}
}

// 14. Empty scenes → no-op
func TestAssetLocationReconciliation_EmptyScenes(t *testing.T) {
	r := newStubVerifier()
	p := NewAssetLocationReconciliationProcessor(r)
	got, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed {
		t.Fatal("empty scenes should not report changed")
	}
}

// 15. No links to verify → no-op
func TestAssetLocationReconciliation_NoLinks(t *testing.T) {
	r := newStubVerifier()
	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "text", Kind: scriptpkg.SceneNarration, Bindings: scriptpkg.SceneBindings{}},
		}},
	}
	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed {
		t.Fatal("no links should not report changed")
	}
}

// 16. 10 lingue — multi-item envelope with all links valid, zero changes.
func TestAssetLocationReconciliation_TenLanguagesNoBrokenLinks(t *testing.T) {
	languages := []string{"en", "it", "fr", "de", "es", "pt", "ja", "ko", "zh", "ar"}
	if len(languages) != 10 {
		t.Fatal("test contract: exactly 10 languages")
	}

	link := "https://drive.google.com/file/d/shared1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-shared",
		DriveFileID: "shared1",
		DriveLink:   link,
		State:       scriptpkg.LocationStateVerified,
	})

	p := NewAssetLocationReconciliationProcessor(r)

	for _, lang := range languages {
		input := ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
				sceneWithClip("clip-shared", link),
			}},
			EffectiveLanguage: lang,
		}

		got, err := p.Process(context.Background(), nil, input)
		if err != nil {
			t.Fatalf("language %s: unexpected error: %v", lang, err)
		}
		if got.Changed {
			t.Fatalf("language %s: Changed should be false for verified link", lang)
		}
		if len(got.Warnings) > 0 {
			t.Fatalf("language %s: unexpected warnings: %v", lang, got.Warnings)
		}
		if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != link {
			t.Fatalf("language %s: link should be preserved", lang)
		}
	}
}

// 19. Document refresh: SpecScene structure preserved after reconciliation
// ensures the UpdatedSpecScene is suitable for downstream document
// rendering with the same doc_id — all binding types survive round-trip.
func TestAssetLocationReconciliation_DocumentRefreshStructurePreserved(t *testing.T) {
	clipLink := "https://drive.google.com/file/d/docClip/view"
	stockLink := "https://drive.google.com/file/d/docStock/view"
	voLink := "https://drive.google.com/file/d/docVO/view"
	mediaLink := "https://drive.google.com/file/d/docMedia/view"

	r := newStubVerifier()
	r.stubResult(clipLink, &scriptpkg.VerifiedLocation{
		AssetID: "clip-doc", DriveFileID: "docClip",
		DriveLink: clipLink, State: scriptpkg.LocationStateVerified,
	})
	r.stubResult(stockLink, &scriptpkg.VerifiedLocation{
		AssetID: "stock-doc", DriveFileID: "docStock",
		DriveLink: stockLink, State: scriptpkg.LocationStateVerified,
	})
	r.stubResult(voLink, &scriptpkg.VerifiedLocation{
		AssetID: "voiceover:scene-doc", DriveFileID: "docVO",
		DriveLink: voLink, State: scriptpkg.LocationStateVerified,
	})
	r.stubResult(mediaLink, &scriptpkg.VerifiedLocation{
		AssetID: "media-doc", DriveFileID: "docMedia",
		DriveLink: mediaLink, State: scriptpkg.LocationStateVerified,
	})

	// Build a scene with all 4 binding types populated.
	scene := scriptpkg.SpecScene{
		ID: "scene-doc", Index: 0, Text: "test scene for doc refresh",
		Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip:      &scriptpkg.ClipBinding{ClipID: "clip-doc", ClipTitle: "Doc Clip", DriveLink: clipLink},
			Stock:     &scriptpkg.StockBinding{AssetID: "stock-doc", Name: "Doc Stock", DriveLink: stockLink},
			Voiceover: &scriptpkg.VoiceoverBinding{Link: voLink, Status: "completed"},
			Media: []scriptpkg.ResolvedMediaBinding{
				{Slot: "bg", AssetID: "media-doc", DriveLink: mediaLink},
			},
		},
	}

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{scene}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}

	out := got.UpdatedSpecScene.Scenes[0]

	// All links preserved for valid bindings.
	if out.Bindings.Clip == nil || out.Bindings.Clip.DriveLink != clipLink {
		t.Fatal("clip binding must be preserved for document refresh")
	}
	if out.Bindings.Stock == nil || out.Bindings.Stock.DriveLink != stockLink {
		t.Fatal("stock binding must be preserved for document refresh")
	}
	if out.Bindings.Voiceover == nil || out.Bindings.Voiceover.Link != voLink {
		t.Fatal("voiceover binding must be preserved for document refresh")
	}
	if len(out.Bindings.Media) != 1 || out.Bindings.Media[0].DriveLink != mediaLink {
		t.Fatal("media binding must be preserved for document refresh")
	}

	// Structure integrity: same scene ID, text, kind.
	if out.ID != scene.ID {
		t.Fatalf("scene ID changed: %q → %q", scene.ID, out.ID)
	}
	if out.Text != scene.Text {
		t.Fatalf("scene text changed")
	}
	if out.Kind != scene.Kind {
		t.Fatalf("scene kind changed")
	}

	if got.Changed {
		t.Fatal("Changed should be false when all links are verified — safe for document refresh")
	}
	if len(got.Warnings) > 0 {
		t.Fatalf("no warnings expected for fully valid bindings: %v", got.Warnings)
	}
}

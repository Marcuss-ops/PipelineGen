// Package scripts_test — specscene_validator_test.go covers the
// canonical ValidateAndEnrichSpecScene acceptance scenarios for
// PR 6. Each test corresponds to one bullet in the user spec's
// "Test" list:
//
//   - clip valida
//   - clip inesistente
//   - clip binding mancante
//   - range temporale invalido
//   - drive link arricchito
//   - scene narration senza clip
//   - scene mixed con clip e image
//   - status invalido
package usecase_test

import (
	"context"
	"strings"
	"testing"

	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// helperEvidence returns a canonical ClipEvidence for tests.
func helperEvidence(ids ...string) *scriptpkg.ClipEvidence {
	driveLinks := make(map[string]string, len(ids))
	for _, id := range ids {
		driveLinks[id] = "https://drive.google.com/file/d/" + id
	}
	return &scriptpkg.ClipEvidence{
		AcceptedClipIDs: ids,
		ClipCount:       len(ids),
		DriveLinks:      driveLinks,
	}
}

func TestValidateSpecScene_ValidClip(t *testing.T) {
	ev := helperEvidence("A", "B")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:  "A",
						StartMs: 0,
						EndMs:   1000,
					},
				},
			}},
		},
	}
	enriched, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err != nil {
		t.Fatal(err)
	}
	if enriched == nil || len(enriched.Scenes) != 1 {
		t.Fatalf("expected 1 scene, got %v", enriched)
	}
	clip := enriched.Scenes[0].Bindings.Clip
	if clip == nil || clip.ClipID != "A" {
		t.Fatalf("expected clip binding for A, got %v", clip)
	}
}

func TestValidateSpecScene_NonexistentClipRejected(t *testing.T) {
	ev := helperEvidence("A") // evidence has only A
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:  "GHOST", // not in evidence
						StartMs: 0,
						EndMs:   1000,
					},
				},
			}},
		},
	}
	_, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err == nil {
		t.Fatal("expected rejection for nonexistent clip")
	}
	if !strings.Contains(err.Error(), "not in resolved ClipEvidence") {
		t.Errorf("error should mention ClipEvidence mismatch: %v", err)
	}
}

func TestValidateSpecScene_MissingClipBindingRejected(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{}, // no clip binding
			}},
		},
	}
	_, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err == nil {
		t.Fatal("expected rejection for missing clip binding on kind=clip")
	}
	if !strings.Contains(err.Error(), "clip_id is empty") {
		t.Errorf("error should mention missing clip_id: %v", err)
	}
}

func TestValidateSpecScene_InvalidTemporalRangeRejected(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID: "A",
						// end_ms <= start_ms: invalid
						StartMs: 1000,
						EndMs:   500,
					},
				},
			}},
		},
	}
	_, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err == nil {
		t.Fatal("expected rejection for invalid temporal range")
	}
	if !strings.Contains(err.Error(), "invalid temporal range") {
		t.Errorf("error should mention invalid temporal range: %v", err)
	}
}

func TestValidateSpecScene_DriveLinkEnriched(t *testing.T) {
	ev := helperEvidence("A", "B")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:  "A",
						StartMs: 0,
						EndMs:   1000,
						// DriveLink intentionally omitted so the
						// validator can auto-enrich from evidence.
					},
				},
			}},
		},
	}
	enriched, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err != nil {
		t.Fatal(err)
	}
	clip := enriched.Scenes[0].Bindings.Clip
	if clip.DriveLink == "" {
		t.Fatal("expected DriveLink auto-enriched from evidence")
	}
	if !strings.Contains(clip.DriveLink, "A") {
		t.Errorf("DriveLink should reference clip A, got %q", clip.DriveLink)
	}
	if clip.ClipTitle == "" {
		t.Error("expected ClipTitle auto-derived as placeholder")
	}
}

func TestValidateSpecScene_OmittedTemporalRangeAccepted(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID: "A",
					},
				},
			}},
		},
	}
	enriched, warnings, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err != nil {
		t.Fatalf("expected omitted timestamps to be accepted, got: %v", err)
	}
	if enriched == nil {
		t.Fatal("expected enriched output")
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for omitted timestamps, got %v", warnings)
	}
}

func TestValidateSpecScene_NarrationWithoutClip(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "narration scene",
				Kind:     scriptpkg.SceneNarration, // narrative, no binding required
				Bindings: scriptpkg.SceneBindings{},
			}},
		},
	}
	enriched, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	_ = enriched
	if err != nil {
		t.Fatalf("narration scene should be accepted without binding, got: %v", err)
	}
}

func TestValidateSpecScene_MixedClipAndImage(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "mixed scene",
				Kind: scriptpkg.SceneMixed,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID: "A", StartMs: 0, EndMs: 1000,
					},
					Image: &scriptpkg.ImageBinding{
						Status: string(scriptpkg.ImageStatusPending),
					},
				},
			}},
		},
	}
	enriched, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err != nil {
		t.Fatalf("mixed scene should be accepted, got: %v", err)
	}
	clip := enriched.Scenes[0].Bindings.Clip
	image := enriched.Scenes[0].Bindings.Image
	if clip == nil || clip.ClipID != "A" {
		t.Errorf("expected clip binding for A in mixed scene, got %v", clip)
	}
	if image == nil || image.Status != string(scriptpkg.ImageStatusPending) {
		t.Errorf("expected pending image binding in mixed scene, got %v", image)
	}
}

func TestValidateSpecScene_InvalidStatusRejected(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Text",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "s1", Index: 0, Text: "scene",
				Kind: scriptpkg.SceneMixed,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID: "A", StartMs: 0, EndMs: 1000,
					},
					Image: &scriptpkg.ImageBinding{
						Status: "bogus-status", // not in enum
					},
				},
			}},
		},
	}
	_, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err == nil {
		t.Fatal("expected rejection for invalid image binding status")
	}
	if !strings.Contains(err.Error(), "unknown image binding status") {
		t.Errorf("error should mention unknown status: %v", err)
	}
}

func TestValidateSpecScene_MultiClipContractPreservesOrderLinksTimingAndReuse(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"A", "B"},
		DriveLinks:      map[string]string{"A": "https://drive/A", "B": "https://drive/B"},
		ClipNames:       map[string]string{"A": "Clip A", "B": "Clip B"},
	}
	output := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
			{ID: "s1", Kind: scriptpkg.SceneClip, Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{
				{ClipID: "A", StartMs: 100, EndMs: 1100, SubtitleLink: "https://drive/A.ass", SubtitleFileID: "sub-A"},
				{ClipID: "B", StartMs: 200, EndMs: 2200, SubtitleLink: "https://drive/B.ass", SubtitleFileID: "sub-B"},
			}}},
			{ID: "s2", Kind: scriptpkg.SceneMixed, Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{
				{ClipID: "B", StartMs: 300, EndMs: 1300},
			}}},
		}},
	}
	enriched, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err != nil {
		t.Fatalf("multi-clip validation failed: %v", err)
	}
	if enriched == nil || len(enriched.Scenes) != 2 {
		t.Fatalf("enriched scenes = %#v, want 2 scenes", enriched)
	}
	first := enriched.Scenes[0].Bindings.Clips
	if len(first) != 2 || first[0].ClipID != "A" || first[1].ClipID != "B" {
		t.Fatalf("multi-clip order = %#v, want [A B]", first)
	}
	if first[0].DriveLink != "https://drive/A" || first[0].SubtitleLink != "https://drive/A.ass" || first[0].SubtitleFileID != "sub-A" || first[0].DurationMs != 1000 {
		t.Fatalf("clip A enrichment/preservation = %+v", first[0])
	}
	if first[0].EndMs-first[0].StartMs != 1000 || first[1].EndMs-first[1].StartMs != 2000 {
		t.Fatalf("multi-clip timing changed: %+v", first)
	}
	if enriched.Scenes[0].Bindings.Clip != &enriched.Scenes[0].Bindings.Clips[0] {
		t.Fatal("legacy alias must point at the canonical first multi-clip entry")
	}
	if got := enriched.Scenes[1].Bindings.Clips[0].ClipID; got != "B" {
		t.Fatalf("cross-scene reuse lost: got %q, want B", got)
	}
	if output.SpecScene.Scenes[0].Bindings.Clips[0].DriveLink != "" {
		t.Fatal("validator must not mutate the model output")
	}
}

func TestValidateSpecScene_MultiClipUnknownIDRejected(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID: "s1", Kind: scriptpkg.SceneMixed,
		Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{
			{ClipID: "A", StartMs: 0, EndMs: 1000},
			{ClipID: "GHOST", StartMs: 0, EndMs: 1000},
		}},
	}}}}
	_, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err == nil || !strings.Contains(err.Error(), "not in resolved ClipEvidence") {
		t.Fatalf("expected unknown multi-clip ID rejection, got %v", err)
	}
}

func TestValidateSpecScene_MultiClipDuplicateWithinSceneRejected(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID: "s1", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{
			{ClipID: "A", StartMs: 0, EndMs: 1000},
			{ClipID: "A", StartMs: 1000, EndMs: 2000},
		}},
	}}}}
	_, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err == nil || !strings.Contains(err.Error(), "duplicate clip_id") {
		t.Fatalf("expected duplicate multi-clip ID rejection, got %v", err)
	}
}

func TestValidateSpecScene_EmptySpecscene(t *testing.T) {
	ev := helperEvidence("A")
	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Pure prose",
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: nil},
	}
	enriched, _, err := scripts.ValidateAndEnrichSpecScene(context.Background(), output, ev)
	if err != nil {
		t.Fatal(err)
	}
	if enriched == nil || len(enriched.Scenes) != 0 {
		t.Errorf("expected empty scenes to pass through, got %v", enriched)
	}
}

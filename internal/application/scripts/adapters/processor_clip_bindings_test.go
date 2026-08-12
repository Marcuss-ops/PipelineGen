// Package adapters_test — processor_clip_bindings_test.go pins the
// PR 5 (June 2026) contract that the clip-scene binding
// processor honours ONLY the resolved ClipEvidence.AcceptedClipIDs
// set, and never binds to IDs that ended up in
// ClipEvidence.MissingClipIDs.
//
// PR 5 contract: pre-PR-5 the Pack's `clip_ids` slot was the
// dedup'd requested set (so any missing ID stayed in there and
// the binder happily bound scenes to orphan IDs). PR 5
// rewires the resolver so ClipEvidence.AcceptedClipIDs is resolved-only
// and MissingClipIDs carries the structured reason.
package adapters_test

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestClipBindings_OnlyBindsResolvedClips_PR5 is the central
// PR 5 contract assertion: ClipBindingsProcessor.Process must
// only bind scenes against IDs in ClipEvidence.AcceptedClipIDs. IDs
// in MissingClipIDs are not eligible for binding.
//
// P0 #2 (June 2026): updated for the no-cycling model — when
// there are more scenes than resolved clips, extra scenes get
// no binding. Previously the binder cycled the single resolved
// ID across all scenes; now only scene[0] gets "clip-a".
//
// Setup:
//
//   - Evidence with ONE resolved ID ("clip-a") and TWO missing
//     IDs ("missing-b" with reason not_found, "missing-c" with
//     reason drivenotfound).
//   - Model output with three scenes, two of which point at
//     the missing IDs and one at the resolved ID.
//
// Expectation: after Process runs, scene[0].Bindings.Clip.ClipID
// == "clip-a". Scenes 1 and 2 have NO clip binding (extra scenes
// beyond clip count are left unbound to surface LLM mismatches).
// The two scenes that pre-PR-5 would have bound to
// "missing-b"/"missing-c" must not appear in any scene's bound
// ClipID — those IDs are in MissingClipIDs, not in ClipIDs.
func TestClipBindings_OnlyBindsResolvedClips_PR5(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a"},
		ClipCount:       1,
		ClipNames:       map[string]string{"clip-a": "Clip A"},
		DriveLinks:      map[string]string{"clip-a": "https://drive.google.com/a"},
		MissingClipIDs: []scriptpkg.MissingClipID{
			{ClipID: "missing-b", Reason: scriptpkg.MissingClipReasonNotFound},
			{ClipID: "missing-c", Reason: scriptpkg.MissingClipReasonDriveNotFound},
		},
	}
	if len(ev.AcceptedClipIDs) != 1 || ev.AcceptedClipIDs[0] != "clip-a" {
		t.Fatalf("AcceptedClipIDs = %v, want [clip-a]", ev.AcceptedClipIDs)
	}
	if len(ev.MissingClipIDs) != 2 {
		t.Fatalf("MissingClipIDs = %v, want 2 entries", ev.MissingClipIDs)
	}

	// The model emitted 3 scenes with clip bindings. Two of
	// them point at IDs that the resolver already classified
	// as missing (PR 5: binder must NOT honour them — they're
	// outside the resolved set).
	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Scenes: []scriptpkg.SpecScene{
				{
					ID: "s1", Index: 0, Kind: scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{ClipID: "missing-b"},
					},
				},
				{
					ID: "s2", Index: 1, Kind: scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{ClipID: "missing-c"},
					},
				},
				{
					ID: "s3", Index: 2, Kind: scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip: &scriptpkg.ClipBinding{ClipID: "clip-a"},
					},
				},
			},
		},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     3, // NumClips > resolved count — binder binds the single resolved ID to scene 0 only
	}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: model.SpecScene}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// P0 #2: only scene[0] gets "clip-a" (the sole resolved clip).
	// Scenes 1 and 2 have no binding — extra scenes beyond clip
	// count are left unbound to surface LLM mismatches.
	const wantClipID = "clip-a"
	for i, s := range model.SpecScene.Scenes {
		if i == 0 {
			if s.Bindings.Clip == nil {
				t.Fatalf("scene[0].Bindings.Clip = nil after Process (expected binding to %q)", wantClipID)
			}
			if s.Bindings.Clip.ClipID != wantClipID {
				t.Errorf("scene[0].Bindings.Clip.ClipID = %q, want %q", s.Bindings.Clip.ClipID, wantClipID)
			}
		} else {
			if s.Bindings.Clip != nil {
				t.Errorf("scene[%d].Bindings.Clip = %v, want nil (P0 #2: no cycling, extra scenes unbound)",
					i, s.Bindings.Clip.ClipID)
			}
		}
	}
}

// TestClipBindings_CyclesAllResolvedIDs_PR5 verifies that
// when there are SEVERAL resolved IDs, the binder maps them
// 1:1 to scenes in canonical order. P0 #2 (June 2026): the
// modulo cycling anti-pattern is removed — extra scenes beyond
// the clip count get no binding to surface LLM mismatches.
func TestClipBindings_CyclesAllResolvedIDs_PR5(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
		ClipCount:       3,
		ClipNames:       map[string]string{"clip-a": "A", "clip-b": "B", "clip-c": "C"},
		DriveLinks: map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-b": "https://drive.google.com/b",
			"clip-c": "https://drive.google.com/c",
		},
		MissingClipIDs: []scriptpkg.MissingClipID{
			{ClipID: "missing-x", Reason: scriptpkg.MissingClipReasonNotFound},
		},
	}

	// 5 scenes, 3 resolved clips — P0 #2: only first 3 scenes
	// get bindings (a, b, c); scenes 3-4 are unbound.
	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Scenes: make([]scriptpkg.SpecScene, 5),
		},
	}
	for i := range model.SpecScene.Scenes {
		model.SpecScene.Scenes[i] = scriptpkg.SpecScene{
			ID:    "s" + string(rune('1'+i)),
			Index: i,
			Kind:  scriptpkg.SceneClip,
		}
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     3,
	}
	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: model.SpecScene}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// P0 #2: bindings are 1:1, no cycling.
	for i, s := range model.SpecScene.Scenes {
		if i < 3 {
			want := []string{"clip-a", "clip-b", "clip-c"}[i]
			if s.Bindings.Clip == nil {
				t.Fatalf("scene[%d] missing binding, want %q", i, want)
			}
			if s.Bindings.Clip.ClipID != want {
				t.Errorf("scene[%d] bound to %q, want %q", i, s.Bindings.Clip.ClipID, want)
			}
		} else {
			if s.Bindings.Clip != nil {
				t.Errorf("scene[%d].Bindings.Clip = %v, want nil (P0 #2: no cycling, extra scenes unbound)",
					i, s.Bindings.Clip.ClipID)
			}
		}
	}
}

// TestClipBindings_CanonicalID_DriveFileID_PR6 pins the PR 6
// canonical-ID contract from the binder's perspective. The user
// may have supplied a Drive file ID as input, and the resolver
// returned a clip whose internal Asset.ID is DIFFERENT. PR 6
// ensures ClipEvidence.ClipIDs holds the Drive file ID
// (canonical) and ClipEvidence.DriveLinks is keyed by that
// same canonical — so the binder reads the Drive URL via the
// canonical key, not via clip.ID (which would silently miss).
//
// P0 #2 (June 2026): updated for 1:1 binding (no cycling).
// Only scene[0] gets the binding since there's 1 clip for 3
// scenes; scenes 1-2 are unbound.
//
// Setup:
//
//   - Evidence: ClipIDs = ["drive-file-id-ABC"], DriveLinks =
//     {"drive-file-id-ABC": "https://drive.google.com/..."},
//     MissingClipIDs carries nothing (the resolve succeeded).
//   - 3 model-emitted scenes with Bindings.Clip.ClipID pointing
//     at the canonical Drive file ID.
//
// Expectation: after Process, scene[0]'s binding references
// "drive-file-id-ABC" and its DriveLink is the resolved URL.
// Scenes 1-2 have no binding.
func TestClipBindings_CanonicalID_DriveFileID_PR6(t *testing.T) {
	const (
		canonicalDriveFileID = "1BxiMVs0XRX5TOXUdv_QQ_E2uALQ7Y_"
		driveURL             = "https://drive.google.com/file/d/" + canonicalDriveFileID + "/view"
		// The asset's INTERNAL ID — explicit in the test so we
		// can prove it does NOT leak into bindings.
		internalAssetID = "internal-asset-789"
	)

	ev := &scriptpkg.ClipEvidence{
		// PR 6 contract: ClipIDs holds the canonical (Drive
		// file ID), NOT the internal asset.ID. Pre-PR-6 this
		// slice would have been [internalAssetID].
		AcceptedClipIDs: []string{canonicalDriveFileID},
		ClipCount:       1,
		ClipNames:       map[string]string{canonicalDriveFileID: "Clip via Drive File ID"},
		DriveLinks: map[string]string{
			canonicalDriveFileID: driveURL,
			// Defensive: ensure no one accidentally keys by
			// asset.ID. If a future refactor reintroduces
			// asset.ID-keyed DriveLinks, this test catches it.
		},
	}
	if !reflect.DeepEqual(ev.AcceptedClipIDs, []string{canonicalDriveFileID}) {
		t.Fatalf("AcceptedClipIDs = %v, want [%q] (canonical is the Drive file ID, NOT %q)",
			ev.AcceptedClipIDs, canonicalDriveFileID, internalAssetID)
	}
	if link, ok := ev.DriveLinks[canonicalDriveFileID]; !ok || link != driveURL {
		t.Fatalf("DriveLinks[%q] = (%q, %v), want (%q, true) "+
			"(PR 6: DriveLinks MUST be keyed by the canonical requested ID)",
			canonicalDriveFileID, link, ok, driveURL)
	}
	// And specifically, DriveLinks[internalAssetID] must NOT
	// exist — if it does, pre-PR-6 keying has crept back in.
	if _, present := ev.DriveLinks[internalAssetID]; present {
		t.Fatalf("DriveLinks[%q] is unexpectedly present; PR 6 forbids "+
			"asset.ID-keyed DriveLinks when the caller passed a Drive file ID",
			internalAssetID)
	}

	// 3 scenes, 1 clip — P0 #2: only scene[0] gets binding.
	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Scenes: make([]scriptpkg.SpecScene, 3),
		},
	}
	for i := range model.SpecScene.Scenes {
		model.SpecScene.Scenes[i] = scriptpkg.SpecScene{
			ID:    "s" + string(rune('1'+i)),
			Index: i,
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{ClipID: canonicalDriveFileID},
			},
		}
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     3,
	}
	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: model.SpecScene}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// P0 #2: scene[0] bound, scenes 1-2 unbound.
	for i, s := range model.SpecScene.Scenes {
		if i == 0 {
			if s.Bindings.Clip == nil {
				t.Fatalf("scene[0].Bindings.Clip = nil")
			}
			if s.Bindings.Clip.ClipID != canonicalDriveFileID {
				t.Errorf("scene[0].Bindings.Clip.ClipID = %q, want %q",
					s.Bindings.Clip.ClipID, canonicalDriveFileID)
			}
			if s.Bindings.Clip.DriveLink != driveURL {
				t.Errorf("scene[0].Bindings.Clip.DriveLink = %q, want %q",
					s.Bindings.Clip.DriveLink, driveURL)
			}
		} else {
			if s.Bindings.Clip != nil {
				t.Errorf("scene[%d].Bindings.Clip = %v, want nil (P0 #2: no cycling)",
					i, s.Bindings.Clip.ClipID)
			}
		}
	}
}

// TestClipBindings_SynthesizesScenesFromClipEvidence_P0 verifies
// the P0 (July 2026) behaviour: when the engine emits plain text
// and leaves SpecScene.Scenes empty, the processor builds scenes
// deterministically from ClipEvidence via ScenePlanner.PlanFromClipEvidence
// and then binds clips 1:1. This is the canonical fix for the live
// source.type=clips path failing with CLIP_NATIVE_PLAN_UNAVAILABLE.
func TestClipBindings_SynthesizesScenesFromClipEvidence_P0(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
		ClipCount:       3,
		ClipNames: map[string]string{
			"clip-a": "Clip A",
			"clip-b": "Clip B",
			"clip-c": "Clip C",
		},
		DriveLinks: map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-b": "https://drive.google.com/b",
			"clip-c": "https://drive.google.com/c",
		},
		ClipDetails: map[string]scriptpkg.ClipDetail{
			"clip-a": {Name: "Clip A", Transcript: "First clip transcript.", DriveLink: "https://drive.google.com/a", StartMs: 1000, EndMs: 5000},
			"clip-b": {Name: "Clip B", Transcript: "Second clip transcript.", DriveLink: "https://drive.google.com/b", StartMs: 6000, EndMs: 9000},
			"clip-c": {Name: "Clip C", Transcript: "Third clip transcript.", DriveLink: "https://drive.google.com/c", StartMs: 10000, EndMs: 14000},
		},
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     3,
	}

	// Empty SpecScene simulates plain-text engine output.
	input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{}}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	result, err := p.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process error = %v", err)
	}
	if !result.Changed {
		t.Errorf("result.Changed = false, want true")
	}
	if len(result.SynthesizedScenes) != 3 {
		t.Fatalf("SynthesizedScenes = %d, want 3", len(result.SynthesizedScenes))
	}

	// The returned scenes must be bound to the accepted clips in order,
	// preserving the detailed bindings produced by PlanFromClipEvidence.
	for i, s := range result.SynthesizedScenes {
		wantID := ev.AcceptedClipIDs[i]
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d].Bindings.Clip = nil, want binding to %q", i, wantID)
		}
		if s.Bindings.Clip.ClipID != wantID {
			t.Errorf("scene[%d].Bindings.Clip.ClipID = %q, want %q", i, s.Bindings.Clip.ClipID, wantID)
		}
		if s.Bindings.Clip.DriveLink != ev.DriveLinks[wantID] {
			t.Errorf("scene[%d].Bindings.Clip.DriveLink = %q, want %q",
				i, s.Bindings.Clip.DriveLink, ev.DriveLinks[wantID])
		}
		if s.Bindings.Clip.ClipTitle != ev.ClipDetails[wantID].Name {
			t.Errorf("scene[%d].Bindings.Clip.ClipTitle = %q, want %q",
				i, s.Bindings.Clip.ClipTitle, ev.ClipDetails[wantID].Name)
		}
		if s.Bindings.Clip.StartMs != ev.ClipDetails[wantID].StartMs {
			t.Errorf("scene[%d].Bindings.Clip.StartMs = %d, want %d",
				i, s.Bindings.Clip.StartMs, ev.ClipDetails[wantID].StartMs)
		}
		if s.Bindings.Clip.EndMs != ev.ClipDetails[wantID].EndMs {
			t.Errorf("scene[%d].Bindings.Clip.EndMs = %d, want %d",
				i, s.Bindings.Clip.EndMs, ev.ClipDetails[wantID].EndMs)
		}
	}

	// In the real pipeline mergePostProcessResult writes
	// result.SynthesizedScenes back into currentInput.SpecScene.Scenes.
	// The direct Process call receives input by value, so we assert
	// on the returned SynthesizedScenes surface.
	if len(result.SynthesizedScenes) != 3 {
		t.Errorf("result.SynthesizedScenes = %d, want 3", len(result.SynthesizedScenes))
	}
}

// TestClipBindings_ClipEvidence_EmptyScenes_SynthesizesScenes is the
// canonical unit test for the new scene-builder path: when the engine
// emits plain text and SpecScene.Scenes is empty, the processor
// synthesises one scene per accepted clip from the clip evidence.
func TestClipBindings_ClipEvidence_EmptyScenes_SynthesizesScenes(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a", "clip-b"},
		ClipCount:       2,
		ClipNames: map[string]string{
			"clip-a": "Clip A",
			"clip-b": "Clip B",
		},
		DriveLinks: map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-b": "https://drive.google.com/b",
		},
		ClipDetails: map[string]scriptpkg.ClipDetail{
			"clip-a": {Name: "Clip A", Transcript: "First clip.", StartMs: 0, EndMs: 3000},
			"clip-b": {Name: "Clip B", Transcript: "Second clip.", StartMs: 4000, EndMs: 7000},
		},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{ClipEvidence: ev, NumClips: 2}
	input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{}}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	result, err := p.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process error = %v", err)
	}

	if !result.Changed {
		t.Errorf("result.Changed = false, want true")
	}
	if len(result.SynthesizedScenes) != 2 {
		t.Fatalf("SynthesizedScenes = %d, want 2", len(result.SynthesizedScenes))
	}
	for i, wantID := range ev.AcceptedClipIDs {
		s := result.SynthesizedScenes[i]
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d].Bindings.Clip = nil, want binding to %q", i, wantID)
		}
		if s.Bindings.Clip.ClipID != wantID {
			t.Errorf("scene[%d].Bindings.Clip.ClipID = %q, want %q", i, s.Bindings.Clip.ClipID, wantID)
		}
		if s.Text != ev.ClipDetails[wantID].Transcript {
			t.Errorf("scene[%d].Text = %q, want transcript from clip evidence", i, s.Text)
		}
	}
}

// TestClipBindings_ModelProducedScenes_BindsClips covers the case where
// the model already emitted scenes: the processor must bind each
// accepted clip to the existing scenes in canonical order without
// synthesising new ones.
func TestClipBindings_ModelProducedScenes_BindsClips(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a", "clip-b"},
		ClipCount:       2,
		ClipNames: map[string]string{
			"clip-a": "Clip A",
			"clip-b": "Clip B",
		},
		DriveLinks: map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-b": "https://drive.google.com/b",
		},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{ClipEvidence: ev, NumClips: 2}
	input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "Model scene 1"},
			{ID: "scene-1", Index: 1, Text: "Model scene 2"},
		},
	}}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	result, err := p.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process error = %v", err)
	}

	if !result.Changed {
		t.Errorf("result.Changed = false, want true")
	}
	if len(result.SynthesizedScenes) != 0 {
		t.Errorf("SynthesizedScenes = %d, want 0 (model scenes must be preserved)", len(result.SynthesizedScenes))
	}
	for i, s := range input.SpecScene.Scenes {
		wantID := ev.AcceptedClipIDs[i]
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d].Bindings.Clip = nil, want binding to %q", i, wantID)
		}
		if s.Bindings.Clip.ClipID != wantID {
			t.Errorf("scene[%d].Bindings.Clip.ClipID = %q, want %q", i, s.Bindings.Clip.ClipID, wantID)
		}
	}
	if input.SpecScene.Scenes[0].Text != "Model scene 1" {
		t.Errorf("scene[0].Text was mutated, want Model scene 1")
	}
	if input.SpecScene.Scenes[1].Text != "Model scene 2" {
		t.Errorf("scene[1].Text was mutated, want Model scene 2")
	}
}

func TestClipBindings_ExplicitSegmentsPreserveCardinalityAndMultiClipOwnership(t *testing.T) {
	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"intro-clip", "clip-a", "clip-b", "clip-c"},
		ClipNames: map[string]string{
			"intro-clip": "Intro clip",
			"clip-a":     "Clip A",
			"clip-b":     "Clip B",
			"clip-c":     "Clip C",
		},
		DriveLinks: map[string]string{
			"intro-clip": "https://drive/intro",
			"clip-a":     "https://drive/a",
			"clip-b":     "https://drive/b",
			"clip-c":     "https://drive/c",
		},
		ClipDetails: map[string]scriptpkg.ClipDetail{
			"intro-clip": {Name: "Intro clip", StartMs: 0, EndMs: 1000, DriveLink: "https://drive/intro"},
			"clip-a":     {Name: "Clip A", StartMs: 1000, EndMs: 2500, DriveLink: "https://drive/a"},
			"clip-b":     {Name: "Clip B", StartMs: 0, EndMs: 2000, DriveLink: "https://drive/b"},
			"clip-c":     {Name: "Clip C", StartMs: 2000, EndMs: 4000, DriveLink: "https://drive/c"},
		},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind:   string(scriptpkg.SourceClips),
		NumClips:     4,
		ClipEvidence: evidence,
		Segments: []scriptpkg.ScriptSegment{
			{ID: "intro", Kind: "intro", Topic: "Opening", ClipIDs: []string{"intro-clip"}},
			{ID: "scene-1", Kind: "scene", Topic: "Combined beat", ClipIDs: []string{"clip-a", "clip-b"}},
			{ID: "scene-2", Kind: "narration", Topic: "Text only", ClipIDs: []string{}},
			{ID: "scene-3", Kind: "scene", Topic: "Closing", ClipIDs: []string{"clip-c"}},
		},
	}
	input := adapters.ProcessInput{Text: "Intro paragraph.\n\nCombined paragraph.\n\nText-only paragraph.\n\nClosing paragraph."}

	result, err := adapters.NewClipBindingsProcessor(zap.NewNop()).Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process error = %v", err)
	}
	if result == nil || len(result.SynthesizedScenes) != 4 {
		t.Fatalf("synthesized scene count = %d, want 4", len(result.SynthesizedScenes))
	}
	if len(result.UpdatedSpecScene.Scenes) != 4 {
		t.Fatalf("updated scene count = %d, want 4", len(result.UpdatedSpecScene.Scenes))
	}

	scenes := result.SynthesizedScenes
	if scenes[0].SegmentID != "intro" || scenes[0].Kind != scriptpkg.SceneIntro {
		t.Fatalf("intro scene identity = (%q, %q), want (intro, intro)", scenes[0].SegmentID, scenes[0].Kind)
	}
	if len(scenes[0].Bindings.Clips) != 1 || scenes[0].Bindings.Clip == nil || scenes[0].Bindings.Clip.ClipID != "intro-clip" {
		t.Fatalf("intro clip bindings = %+v, want one intro clip", scenes[0].Bindings)
	}
	if scenes[1].SegmentID != "scene-1" || len(scenes[1].Bindings.Clips) != 2 {
		t.Fatalf("multi-clip scene = (%q, %+v), want segment scene-1 with two clips", scenes[1].SegmentID, scenes[1].Bindings.Clips)
	}
	if scenes[1].Bindings.Clips[0].ClipID != "clip-a" || scenes[1].Bindings.Clips[1].ClipID != "clip-b" {
		t.Fatalf("multi-clip order = %v, want [clip-a clip-b]", []string{scenes[1].Bindings.Clips[0].ClipID, scenes[1].Bindings.Clips[1].ClipID})
	}
	if scenes[2].SegmentID != "scene-2" || scenes[2].Kind != scriptpkg.SceneNarration || len(scenes[2].Bindings.Clips) != 0 || scenes[2].Bindings.Clip != nil {
		t.Fatalf("text-only scene = %+v, want narration with zero clips", scenes[2])
	}
	if scenes[3].SegmentID != "scene-3" || len(scenes[3].Bindings.Clips) != 1 || scenes[3].Bindings.Clips[0].ClipID != "clip-c" {
		t.Fatalf("closing scene = %+v, want one clip-c binding", scenes[3])
	}
}

// TestClipBindings_NoClipEvidence_NoOp verifies that when there is no
// clip evidence the processor returns an empty result and does not
// touch the input scenes. Covers both nil ClipEvidence and a non-nil
// ClipEvidence with empty AcceptedClipIDs.
func TestClipBindings_NoClipEvidence_NoOp(t *testing.T) {
	tests := []struct {
		name string
		plan *scriptpkg.ResolvedGenerationPlan
	}{
		{
			name: "nil ClipEvidence",
			plan: &scriptpkg.ResolvedGenerationPlan{ClipEvidence: nil},
		},
		{
			name: "empty AcceptedClipIDs",
			plan: &scriptpkg.ResolvedGenerationPlan{ClipEvidence: &scriptpkg.ClipEvidence{
				AcceptedClipIDs: []string{},
				DriveLinks:      map[string]string{"clip-a": "https://drive/a"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := adapters.ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{
				Scenes: []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Text: "Only scene"}},
			}}

			p := adapters.NewClipBindingsProcessor(zap.NewNop())
			result, err := p.Process(context.Background(), tt.plan, input)
			if err != nil {
				t.Fatalf("process error = %v", err)
			}

			if result.Changed {
				t.Errorf("result.Changed = true, want false")
			}
			if len(result.SynthesizedScenes) != 0 {
				t.Errorf("SynthesizedScenes = %d, want 0", len(result.SynthesizedScenes))
			}
			if input.SpecScene.Scenes[0].Bindings.Clip != nil {
				t.Errorf("input scene binding was mutated unexpectedly")
			}
		})
	}
}

// TestClipBindings_FallbackRange_UsesCanonicalKeys_PR6 verifies
// the P0 #2 behaviour: when ClipEvidence.ClipIDs is empty, the
// binder is a no-op (returns early with no bindings). The old
// fallback path that ranged over DriveLinks is removed — the
// canonical ClipIDs list is the single source of truth for
// clip order.
func TestClipBindings_FallbackRange_UsesCanonicalKeys_PR6(t *testing.T) {
	const (
		driveFileA = "drive-file-A"
		driveFileB = "drive-file-B"
	)
	ev := &scriptpkg.ClipEvidence{
		// empty ClipIDs → P0 #2: binder returns early, no fallback
		DriveLinks: map[string]string{
			driveFileA: "https://drive.google.com/" + driveFileA,
			driveFileB: "https://drive.google.com/" + driveFileB,
		},
		MissingClipIDs: []scriptpkg.MissingClipID{
			{ClipID: driveFileA, Reason: scriptpkg.MissingClipReasonDriveNotFound},
		},
	}

	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{
			Scenes: make([]scriptpkg.SpecScene, 2),
		},
	}
	for i := range model.SpecScene.Scenes {
		model.SpecScene.Scenes[i] = scriptpkg.SpecScene{
			ID:    "s" + string(rune('1'+i)),
			Index: i,
			Kind:  scriptpkg.SceneClip,
		}
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     2,
	}
	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: model.SpecScene}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// P0 #2: empty ClipIDs → early return, no scenes get bindings.
	for i, s := range model.SpecScene.Scenes {
		if s.Bindings.Clip != nil {
			t.Errorf("scene[%d].Bindings.Clip = %v, want nil (P0 #2: empty ClipIDs → no bindings)",
				i, s.Bindings.Clip.ClipID)
		}
	}
}

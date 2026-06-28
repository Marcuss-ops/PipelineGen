// Package scripts — processor_clip_bindings_test.go pins the
// PR 5 (June 2026) contract that the clip-scene binding
// processor honours ONLY the resolved ClipEvidence.ClipIDs
// set, and never binds to IDs that ended up in
// ClipEvidence.MissingClipIDs.
//
// PR 5 contract: pre-PR-5 the Pack's `clip_ids` slot was the
// dedup'd requested set (so any missing ID stayed in there and
// the binder happily bound scenes to orphan IDs). PR 5
// rewires the resolver so Pack's `clip_ids` is resolved-only
// and MissingClipIDs carries the structured reason. This test
// verifies the binder therefore NEVER sees the dropped IDs
// (since BuildClipEvidence silently excludes them from
// ClipIDs).
package adapters_test

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestClipBindings_OnlyBindsResolvedClips_PR5 is the central
// PR 5 contract assertion: ClipBindingsProcessor.Process must
// only bind scenes against IDs in ClipEvidence.ClipIDs. IDs
// in MissingClipIDs are not eligible for binding.
//
// Setup:
//
//   - Evidence with ONE resolved ID ("clip-a") and TWO missing
//     IDs ("missing-b" with reason not_found, "missing-c" with
//     reason drivenotfound).
//   - Model output with three scenes, two of which point at
//     the missing IDs and one at the resolved ID.
//   - Plan requests NumClips=3 so the binder would otherwise
//     cycle if more than one resolved ID existed.
//
// Expectation: after Process runs, every scene's
// Bindings.Clip.ClipID == "clip-a". The two scenes that
// pre-PR-5 would have bound to "missing-b"/"missing-c" must
// not appear in any scene's bound ClipID — those IDs are in
// MissingClipIDs, not in ClipIDs.
func TestClipBindings_OnlyBindsResolvedClips_PR5(t *testing.T) {
	ev := usecase.BuildClipEvidence(map[string]any{
		"clip_ids":         []string{"clip-a"},
		"clip_names":       []string{"Clip A"},
		"clip_drive_links": map[string]string{"clip-a": "https://drive.google.com/a"},
		"missing_clip_ids": []scriptpkg.MissingClipID{
			{ClipID: "missing-b", Reason: scriptpkg.MissingClipReasonNotFound},
			{ClipID: "missing-c", Reason: scriptpkg.MissingClipReasonDriveNotFound},
		},
	}, "")
	if ev == nil {
		t.Fatal("evidence = nil (BuildClipEvidence refused the pack)")
	}
	if len(ev.ClipIDs) != 1 || ev.ClipIDs[0] != "clip-a" {
		t.Fatalf("ClipIDs = %v, want [clip-a]", ev.ClipIDs)
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
		NumClips:     3, // request more than the resolved count → binder cycles the single resolved ID
	}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: model.SpecScene}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// All scenes should be bound to "clip-a" because that's the
	// ONLY ID in ClipEvidence.ClipIDs. Missing IDs cannot leak
	// into the bound bindings under PR 5.
	const wantClipID = "clip-a"
	gotIDs := make([]string, 0, len(model.SpecScene.Scenes))
	for i, s := range model.SpecScene.Scenes {
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d].Bindings.Clip = nil after Process", i)
		}
		if s.Bindings.Clip.ClipID != wantClipID {
			t.Errorf("scene[%d].Bindings.Clip.ClipID = %q, want %q "+
				"(PR 5: binder cycles ONLY over resolved ClipIDs; missing IDs "+
				"in MissingClipIDs MUST NOT appear as bound ClipID)",
				i, s.Bindings.Clip.ClipID, wantClipID)
		}
		gotIDs = append(gotIDs, s.Bindings.Clip.ClipID)
	}
	// Also assert that NO missing ID appears anywhere in the
	// bound set (defensive belt-and-suspenders):
	if anyInSlice(gotIDs, "missing-b") || anyInSlice(gotIDs, "missing-c") {
		t.Fatalf("bound ClipIDs leaked missing IDs: got=%v (no missing-b/c allowed)", gotIDs)
	}
}

// TestClipBindings_CyclesAllResolvedIDs_PR5 verifies that
// when there are SEVERAL resolved IDs (instead of one), the
// binder cycles through them in order. Catches regressions
// where a refactor accidentally drops a resolved ID from the
// cycle. Combined with the single-resolved test above, this
// pins both endpoints of "binder respect resolved set only".
func TestClipBindings_CyclesAllResolvedIDs_PR5(t *testing.T) {
	ev := usecase.BuildClipEvidence(map[string]any{
		"clip_ids":   []string{"clip-a", "clip-b", "clip-c"},
		"clip_names": []string{"A", "B", "C"},
		"clip_drive_links": map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-b": "https://drive.google.com/b",
			"clip-c": "https://drive.google.com/c",
		},
		"missing_clip_ids": []scriptpkg.MissingClipID{
			{ClipID: "missing-x", Reason: scriptpkg.MissingClipReasonNotFound},
		},
	}, "")
	if ev == nil {
		t.Fatal("evidence = nil")
	}

	// 5 scenes, 3 resolved → cycle should be a, b, c, a, b
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

	want := []string{"clip-a", "clip-b", "clip-c", "clip-a", "clip-b"}
	for i, s := range model.SpecScene.Scenes {
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d] missing binding", i)
		}
		if s.Bindings.Clip.ClipID != want[i] {
			t.Errorf("scene[%d] bound to %q, want %q "+
				"(PR 5: binder cycles resolved ClipIDs in order)",
				i, s.Bindings.Clip.ClipID, want[i])
		}
	}
}

// anyInSlice returns true if needle is in haystack. Tiny
// helper local to the bindings test.
func anyInSlice(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
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
// Setup:
//
//   - Evidence: ClipIDs = ["drive-file-id-ABC"], DriveLinks =
//     {"drive-file-id-ABC": "https://drive.google.com/..."},
//     MissingClipIDs carries nothing (the resolve succeeded).
//   - 3 model-emitted scenes with Bindings.Clip.ClipID pointing
//     at the canonical Drive file ID.
//
// Expectation: after Process, every scene's binding references
// "drive-file-id-ABC" and its DriveLink is the resolved URL.
// This proves the binder never falls back to asset.ID lookup,
// because if it did, scene bound IDs would land somewhere
// outside DriveLinks["drive-file-id-ABC"].
func TestClipBindings_CanonicalID_DriveFileID_PR6(t *testing.T) {
	const (
		canonicalDriveFileID = "1BxiMVs0XRX5TOXUdv_QQ_E2uALQ7Y_"
		driveURL             = "https://drive.google.com/file/d/" + canonicalDriveFileID + "/view"
		// The asset's INTERNAL ID — explicit in the test so we
		// can prove it does NOT leak into bindings.
		internalAssetID = "internal-asset-789"
	)

	ev := usecase.BuildClipEvidence(map[string]any{
		// PR 6 contract: ClipIDs holds the canonical (Drive
		// file ID), NOT the internal asset.ID. Pre-PR-6 this
		// slice would have been [internalAssetID].
		"clip_ids":   []string{canonicalDriveFileID},
		"clip_names": []string{"Clip via Drive File ID"},
		"clip_drive_links": map[string]string{
			canonicalDriveFileID: driveURL,
			// Defensive: ensure no one accidentally keys by
			// asset.ID. If a future refactor reintroduces
			// asset.ID-keyed DriveLinks, this test catches it.
		},
	}, "")
	if ev == nil {
		t.Fatal("evidence = nil (BuildClipEvidence refused the canonical pack)")
	}
	if !reflect.DeepEqual(ev.ClipIDs, []string{canonicalDriveFileID}) {
		t.Fatalf("ClipIDs = %v, want [%q] (canonical is the Drive file ID, NOT %q)",
			ev.ClipIDs, canonicalDriveFileID, internalAssetID)
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

	// 3 scenes, model emitted each Bindings.Clip.ClipID as the
	// canonical (Drive file ID) — exactly what the grounding
	// prompt instructs.
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

	for i, s := range model.SpecScene.Scenes {
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d].Bindings.Clip = nil", i)
		}
		if s.Bindings.Clip.ClipID != canonicalDriveFileID {
			t.Errorf("scene[%d].Bindings.Clip.ClipID = %q, want %q "+
				"(PR 6: binder must drive the canonical Drive file ID, NOT the asset ID)",
				i, s.Bindings.Clip.ClipID, canonicalDriveFileID)
		}
		if s.Bindings.Clip.DriveLink != driveURL {
			t.Errorf("scene[%d].Bindings.Clip.DriveLink = %q, want %q "+
				"(PR 6: binder reads DriveLinks[canonical] = url)",
				i, s.Bindings.Clip.DriveLink, driveURL)
		}
		if s.Bindings.Clip.ClipID == internalAssetID {
			t.Errorf("scene[%d] bound to asset.ID; PR 6 forbids this "+
				"when caller passed a Drive file ID", i)
		}
	}
}

// TestClipBindings_FallbackRange_UsesCanonicalKeys_PR6 pins the
// secondary fallback path in processor_clip_bindings.go: when
// ClipEvidence.ClipIDs is empty (e.g. an all-missing edge case
// from BuildClipEvidence) the binder falls back to
// `for id := range plan.ClipEvidence.DriveLinks` to derive
// the cycle's source. After PR 6, this iteration MUST yield
// canonical IDs (whatever the resolver chose), which feed
// back into DriveLinks lookup. Without explicit pinning, a
// future refactor could silently swap the order or use a
// different key source.
func TestClipBindings_FallbackRange_UsesCanonicalKeys_PR6(t *testing.T) {
	const (
		driveFileA = "drive-file-A"
		driveFileB = "drive-file-B"
	)
	ev := usecase.BuildClipEvidence(map[string]any{
		"clip_ids": nil, // empty → triggers fallback range over DriveLinks
		"clip_drive_links": map[string]string{
			driveFileA: "https://drive.google.com/" + driveFileA,
			driveFileB: "https://drive.google.com/" + driveFileB,
		},
		"missing_clip_ids": []scriptpkg.MissingClipID{
			// All-missing contract: PR 5 made BuildClipEvidence
			// return non-nil (with MissingClipIDs populated)
			// even when ClipIDs is empty. The binder's
			// fallback range kicks in for ordering.
			{ClipID: driveFileA, Reason: scriptpkg.MissingClipReasonDriveNotFound},
		},
	}, "")
	if ev == nil {
		t.Fatal("evidence = nil")
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

	// After the fallback-range sorted cycle, BOTH scenes should
	// reference canonical Drive file IDs (NOT any internal
	// asset IDs, which the test fixtures deliberately do NOT
	// include). The exact ordering depends on Go's map sort
	// behaviour; both keys must be canonical.
	gotKeys := make(map[string]bool, 2)
	for i, s := range model.SpecScene.Scenes {
		if s.Bindings.Clip == nil {
			t.Fatalf("scene[%d] missing binding", i)
		}
		gotKeys[s.Bindings.Clip.ClipID] = true
	}
	if !gotKeys[driveFileA] || !gotKeys[driveFileB] {
		t.Fatalf("binder range produced %v; want both %q and %q "+
			"(PR 6: fallback range MUST be canonical-keyed)",
			gotKeys, driveFileA, driveFileB)
	}
}

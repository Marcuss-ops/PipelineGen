// Package adapters — postprocessor_composite_merge_test.go: Phase 1 regression guards.
//
// Two regression guards in this file:
//
//   - TestMergePostProcessResult_PropagatesTranslatedToCurrentInput (Phase 1, B2):
//     pins the canonical in-place propagation of `src.TranslatedText` +
//     `src.TranslatedSpecScene` into the next-stage `currentInput.Text` +
//     `currentInput.SpecScene.Scenes[i].Text` so document/persistence read
//     the translated surface, not the pre-translation English surface.
//
//   - TestMergePostProcessResult_ImageBinding_FailClosed (Commit 7, July 2026):
//     pins the fail-closed bind rule that ONLY an outcome with a populated
//     SceneImageDriveLink promotes to "generated" + URL="<link>". Every
//     other case (FAILED / SKIPPED / SUCCEEDED-with-empty-DriveLink) terminates
//     with Status="failed" and URL="" per godlike/07 NO-FAKE-AVAILABILITY.
//
// Pre-fix bug (commit 7):
//   - postprocessor_composite_merge.go bound `sc.Bindings.Image.Status = "generated"`
//     UNCONDITIONALLY whenever the SceneImages buffer was non-empty, even
//     when the underlying image URL / DriveFileID were empty (e.g. when
//     the per-scene image call returned no asset). This produced a stream
//     of FALSE successes — empty images declared generated.
//
// Post-fix expectation (commit 7):
//   - A SceneImage with a non-empty SceneImageDriveLink (URL or Drive-link
//     fallback) promotes to "generated" + URL populated.
//   - A SceneImage with NO link (e.g. failed/skipped/deferred) terminates
//     with Status="failed" + URL="" (the honest answer).
package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestMergePostProcessResult_SynthesizedScenesPreserveNewClipBindings(t *testing.T) {
	currentInput := &ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID: "scene-0", SegmentID: "segment-0", Index: 0,
		Bindings: scriptpkg.SceneBindings{Stock: &scriptpkg.StockBinding{AssetID: "visual-0"}},
	}}}}
	src := &PostProcessResult{SynthesizedScenes: []scriptpkg.SpecScene{{
		ID: "scene-0", SegmentID: "segment-0", Index: 0,
		Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "clip-0"}},
	}}}

	mergePostProcessResult(&PipelineResult{}, src, currentInput)
	scene := currentInput.SpecScene.Scenes[0]
	if scene.Bindings.Clip == nil || scene.Bindings.Clip.ClipID != "clip-0" {
		t.Fatalf("synthesized clip binding was lost: %+v", scene.Bindings.Clip)
	}
	if scene.Bindings.Stock == nil || scene.Bindings.Stock.AssetID != "visual-0" {
		t.Fatalf("previous stock binding was lost: %+v", scene.Bindings.Stock)
	}
}

func TestMergeVidRushSegments_PreservesCandidatesAcrossProviderDeltas(t *testing.T) {
	dst := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "main",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "artlist-clip-1", Provider: "artlist", SourceURL: "https://artlist.test/clip.m3u8",
		}}},
	}}
	src := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "main",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "image-1", Provider: "internet_images", SourceURL: "https://images.test/maya.jpg",
		}}},
	}}

	merged := mergeVidRushSegments(dst, src)
	if len(merged) != 1 {
		t.Fatalf("merged segments = %d, want 1", len(merged))
	}
	if len(merged[0].Assets.Candidates) != 2 {
		t.Fatalf("merged candidates = %d, want 2", len(merged[0].Assets.Candidates))
	}
	if merged[0].Assets.Candidates[0].Provider != "artlist" {
		t.Fatalf("first candidate provider = %q, want artlist", merged[0].Assets.Candidates[0].Provider)
	}
	if merged[0].Assets.Candidates[1].Provider != "internet_images" {
		t.Fatalf("second candidate provider = %q, want internet_images", merged[0].Assets.Candidates[1].Provider)
	}
}

// TestMergePostProcessResult_PropagatesTranslatedToCurrentInput is Phase 1 / Bug B2 regression guard.
//
// Canonical scenario:
//   - currentInput.Text starts as "I will defeat you." (English, canonical pre-translation state)
//   - currentInput.SpecScene has 1 scene with English text
//   - src.PostProcessResult carries TranslatedText="Sconfiggerò." + TranslatedSpecScene with 1 Italian scene
//   - After mergePostProcessResult call:
//   - currentInput.Text MUST equal "Sconfiggerò." (the translated surface
//     propagates IN-PLACE for the next postprocessor's downstream use).
//   - currentInput.SpecScene.Scenes[0].Text MUST equal "Sconfiggerò." (per-scene).
//
// Pre-fix: src.TranslatedText is copied only to dst.TranslatedText (the
// pipeline-aggregate); currentInput.Text is untouched → currentInput.Text
// stays at "I will defeat you." → first assertion FAILS. Second assertion
// also FAILS because src.TranslatedSpecScene is NOT written into
// currentInput.SpecScene (currentInput.SpecScene.Scenes[0].Text stays English).
func TestMergePostProcessResult_PropagatesTranslatedToCurrentInput(t *testing.T) {
	// ── Arrange ────────────────────────────────────────────────────────────
	// Canonical pre-translation English surface (currentInput starts here).
	preTranslationText := "I will defeat you."

	currentInput := &ProcessInput{
		Text: preTranslationText,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					Index: 0,
					Kind:  scriptpkg.SceneClip,
					Text:  preTranslationText,
				},
			},
		},
	}

	// Canonical translated Italian surface (src carries this).
	translatedText := "Sconfiggerò."

	src := &PostProcessResult{
		TranslatedText: translatedText,
		TranslatedSpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					Index: 0,
					Kind:  scriptpkg.SceneClip,
					Text:  translatedText,
				},
			},
		},
	}

	// dst: a fresh PipelineResult (the function writes into it; we don't
	// care about dst.TranslatedText for this B2 regression — the bug
	// is about currentInput, not dst).
	dst := &PipelineResult{}

	// ── Pre-flight sanity ───────────────────────────────────────────────────
	// If a future refactor zeroes-out currentInput.Text before the merge
	// (we want to be sure the assertion catches the in-place propagation
	// specifically, not some other path).
	if currentInput.Text != preTranslationText {
		t.Fatalf("pre-flight failed: currentInput.Text should start as %q, got %q",
			preTranslationText, currentInput.Text)
	}
	if len(currentInput.SpecScene.Scenes) != 1 {
		t.Fatalf("pre-flight failed: currentInput.SpecScene.Scenes should have 1 element, got %d",
			len(currentInput.SpecScene.Scenes))
	}

	// ── Act ────────────────────────────────────────────────────────────────
	mergePostProcessResult(dst, src, currentInput)

	// ── Assert ─────────────────────────────────────────────────────────────
	// Primary regression guard: in-place TEXT propagation.
	if currentInput.Text != translatedText {
		t.Errorf("currentInput.Text = %q, want %q "+
			"(mergePostProcessResult must propagate src.TranslatedText into "+
			"currentInput.Text so the NEXT-stage postprocessor reads the "+
			"translated surface, not the pre-translation English surface)",
			currentInput.Text, translatedText)
	}

	// Secondary regression guard: per-scene SpecScene TEXT propagation.
	if len(currentInput.SpecScene.Scenes) == 0 {
		t.Errorf("currentInput.SpecScene.Scenes is unexpectedly empty post-merge " +
			"(mergePostProcessResult must preserve SpecScene structure when " +
			"propagating src.TranslatedSpecScene)")
	} else {
		got := currentInput.SpecScene.Scenes[0].Text
		if got != translatedText {
			t.Errorf("currentInput.SpecScene.Scenes[0].Text = %q, want %q "+
				"(mergePostProcessResult must propagate src.TranslatedSpecScene "+
				"into currentInput.SpecScene.Scenes[0].Text so document/persistence "+
				"output the translated per-scene text)",
				got, translatedText)
		}
	}

	// Sanity: dst.TranslatedText gets populated by the EXISTING write-back
	// path (pre-fix); we assert it to lock that fixing B2 doesn't regress B5
	// (related: dst.TranslatedText propagation MUST still happen post-fix).
	if dst.TranslatedText != translatedText {
		t.Errorf("dst.TranslatedText = %q, want %q "+
			"(regression guard: existing dst-level propagation must remain intact)",
			dst.TranslatedText, translatedText)
	}
}

func TestCloneSceneBindings_PreservesMultiClipBindings(t *testing.T) {
	original := scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{
		{ClipID: "clip-a", DriveLink: "https://drive/a", StartMs: 100, EndMs: 1100},
		{ClipID: "clip-b", DriveLink: "https://drive/b", StartMs: 200, EndMs: 2200},
	}}
	cloned := cloneSceneBindings(original)
	if len(cloned.Clips) != 2 || cloned.Clips[0] != original.Clips[0] || cloned.Clips[1] != original.Clips[1] {
		t.Fatalf("cloneSceneBindings lost multi-clip bindings: got %#v, want %#v", cloned.Clips, original.Clips)
	}
	if cloned.Clip == nil || cloned.Clip.ClipID != "clip-a" {
		t.Fatalf("legacy alias = %#v, want first canonical clip", cloned.Clip)
	}
	cloned.Clips[0].DriveLink = ""
	if original.Clips[0].DriveLink == "" {
		t.Fatal("cloneSceneBindings must isolate the multi-clip slice from the source")
	}
}

func TestCloneSceneBindings_PreservesMediaBindings(t *testing.T) {
	original := scriptpkg.SceneBindings{
		Media: []scriptpkg.ResolvedMediaBinding{{Slot: "background", AssetID: "asset-1", DriveLink: "https://drive.google.com/file/d/media-1/view"}},
	}

	cloned := cloneSceneBindings(original)
	if len(cloned.Media) != 1 || cloned.Media[0] != original.Media[0] {
		t.Fatalf("cloneSceneBindings lost Media binding: got %#v, want %#v", cloned.Media, original.Media)
	}
	cloned.Media[0].DriveLink = ""
	if original.Media[0].DriveLink == "" {
		t.Fatal("cloneSceneBindings must isolate the Media slice from the source")
	}
}

// TestMergePostProcessResult_ImageBinding_FailClosed (Commit 7, July 2026) is the
// canonical regression guard for the fail-closed image-bind rule.
//
// Scenario: the CLI / API surfaces scene-image bindings to operator dashboards
// and to the document / persistence postprocessors downstream of the image
// postprocessor. Pre-fix, EVERY scene bound to the merge's `src.SceneImages`
// got `Bindings.Image.Status = "generated"` regardless of whether the
// underlying image was populated. Today this is the source of "false
// successes" — empty images declared generated.
//
// Post-fix:
//   - A SceneImage whose SceneImageDriveLink helper returns a non-empty
//     URL (either via .URL or via the DriveFileID fallback) → bound with
//     Status="generated" and URL populated.
//   - A SceneImage whose SceneImageDriveLink helper returns "" (no URL,
//     no DriveFileID, or both empty) → bound with Status="failed" and
//     URL="" — the honest answer per godlike/07 NO-FAKE-AVAILABILITY.
//
// Note on test-fixture architecture: src.SceneImages in production today
// carries []SceneImage (NOT the typed []SceneImageOutcome from Commit 6).
// The fail-closed rule is enforced via the proxy "non-empty DriveLink"
// (which is the only canonical signal available on SceneImage today).
// A future commit that wires []SceneImageOutcome through the merge can
// replace this proxy with the explicit Status comparison
// (outcome.Status == SceneImageSucceeded ...) at the same site.
//
// This test is table-driven across the 3 spec case surfaces:
//
//  1. FAILED outcome (URL empty + DriveFileID empty)                 → Status="failed", URL=""
//  2. SUCCEEDED-with-empty-DriveLink outcome (functionally same     → Status="failed", URL=""
//     as case 1 in current implementation)
//  3. SUCCEEDED-with-non-empty-DriveLink outcome (URL populated      → Status="generated", URL=<link>
//     or DriveFileID populated)
func TestMergePostProcessResult_ImageBinding_FailClosed(t *testing.T) {
	const sceneText = "the canonical pre-fix empty image (URL & DriveFileID both empty)"

	tests := []struct {
		name              string
		sceneImage        SceneImage
		wantStatus        string
		wantURL           string
		wantStatusIsValid bool
	}{
		{
			// Case 1 (spec): outcome failed → Status="failed" + URL empty.
			name:              "case1_failed_outcome: empty URL + empty DriveFileID -> failed",
			sceneImage:        SceneImage{Index: 0, Text: sceneText, URL: "", DriveFileID: ""},
			wantStatus:        string(scriptpkg.ImageStatusFailed),
			wantURL:           "",
			wantStatusIsValid: true,
		},
		{
			// Case 2 (spec): outcome succeeded con DriveLink="" → Status="failed" + URL empty.
			// In current implementation (SceneImage has no Status field),
			// case 2 is functionally identical to case 1 — the proxy
			// "non-empty DriveLink" cannot distinguish "failed" from
			// "succeeded-without-link". Both terminate with "failed".
			// Naming the two cases distinctly documents the spec'd intent
			// and guards the rule under future refactors that may split
			// the two cases (e.g. if SceneImage acquires a .Status field).
			name:              "case2_succeeded_with_empty_link: empty URL + empty DriveFileID -> failed (proxy indistinguishable from case1 today)",
			sceneImage:        SceneImage{Index: 0, Text: sceneText, URL: "", DriveFileID: ""},
			wantStatus:        string(scriptpkg.ImageStatusFailed),
			wantURL:           "",
			wantStatusIsValid: true,
		},
		{
			// Case 3 (spec): outcome succeeded con DriveLink valorizzata
			// → Status="generated" + URL uguale al link.
			// Use a real-looking .URL (HTTPS SourceURL) so SceneImageDriveLink
			// returns the URL directly (no Drive-link fallback needed).
			name:              "case3_succeeded_with_link: HTTPS URL populated -> generated + URL preserved",
			sceneImage:        SceneImage{Index: 0, Text: sceneText, URL: "https://drive.google.com/file/d/abc123/view", DriveFileID: "abc123"},
			wantStatus:        string(scriptpkg.ImageStatusGenerated),
			wantURL:           "https://drive.google.com/file/d/abc123/view",
			wantStatusIsValid: true,
		},
		{
			// DriveFileID-only fallback: case 3 covers "URL populated"
			// but a separate scenario is "URL empty + DriveFileID set"
			// (workshop of the Drive-link fallback path inside
			// SceneImageDriveLink). This is also SUCCEEDED-with-link
			// and must promote to "generated" + URL via Drive-link
			// fallback.
			name:              "case3_variant_drive_fallback: empty URL + DriveFileID set -> generated + URL via Drive-link fallback",
			sceneImage:        SceneImage{Index: 0, Text: sceneText, URL: "", DriveFileID: "abc123"},
			wantStatus:        string(scriptpkg.ImageStatusGenerated),
			wantURL:           "https://drive.google.com/file/d/abc123/view",
			wantStatusIsValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ── Arrange ────────────────────────────────────────────────────
			currentInput := &ProcessInput{
				Text: "",
				SpecScene: scriptpkg.SpecSceneOutput{
					Version: 1,
					Scenes: []scriptpkg.SpecScene{
						{
							Index: tt.sceneImage.Index,
							Kind:  scriptpkg.SceneImage,
							Text:  tt.sceneImage.Text,
							Bindings: scriptpkg.SceneBindings{
								// Pre-fix pre-existing Image binding on the
								// dst-side currentInput.SpecScene.Scenes[i]
								// (this is what mergePostProcessResult writes
								// back into). Start it nil so the merge must
								// initialise it via `if sc.Bindings.Image == nil`.
								Image: nil,
							},
						},
					},
				},
			}

			src := &PostProcessResult{
				SceneImages: []SceneImage{tt.sceneImage},
			}

			dst := &PipelineResult{}

			// ── Act ───────────────────────────────────────────────────────
			mergePostProcessResult(dst, src, currentInput)

			// ── Assert ────────────────────────────────────────────────────
			sc := &currentInput.SpecScene.Scenes[0]

			// The binding MUST be initialised (the merge code path
			// initialises via `if sc.Bindings.Image == nil`).
			if sc.Bindings.Image == nil {
				t.Fatalf("sc.Bindings.Image unexpectedly nil post-merge (mergePostProcessResult must initialise it)")
			}

			// Status: must match the spec’d case.
			if sc.Bindings.Image.Status != tt.wantStatus {
				t.Errorf("sc.Bindings.Image.Status = %q, want %q "+
					"(commit 7 fail-closed rule: only DriveLink-populated "+
					"outcomes promote to %q)",
					sc.Bindings.Image.Status, tt.wantStatus, scriptpkg.ImageStatusGenerated)
			}

			// URL: must equal the canonical DriveLink result.
			if sc.Bindings.Image.URL != tt.wantURL {
				t.Errorf("sc.Bindings.Image.URL = %q, want %q "+
					"(commit 7 fail-closed rule: URL must equal SceneImageDriveLink "+
					"when promoted to 'generated', and must be empty when failed)",
					sc.Bindings.Image.URL, tt.wantURL)
			}

			// Status validity (defense-in-depth): the emitted Status
			// MUST be a valid ImageBindingStatus per binding_status.go.
			if tt.wantStatusIsValid {
				status := scriptpkg.ImageBindingStatus(sc.Bindings.Image.Status)
				if !status.Valid() {
					t.Errorf("sc.Bindings.Image.Status = %q is NOT a valid "+
						"ImageBindingStatus per binding_status.go::Valid",
						sc.Bindings.Image.Status)
				}
			}

			// dst.Scenes must contain the SceneImage (existing surface).
			if len(dst.Scenes) != 1 {
				t.Fatalf("dst.Scenes length = %d, want 1 (existing dst-level "+
					"SceneImages propagation must remain intact post-fix)",
					len(dst.Scenes))
			}
			if dst.Scenes[0].Index != tt.sceneImage.Index {
				t.Errorf("dst.Scenes[0].Index = %d, want %d",
					dst.Scenes[0].Index, tt.sceneImage.Index)
			}
		})
	}
}

package scripts

import (
	"strings"
	"testing"
)

// ── ParseScenes ───────────────────────────────────────────────────────────

func TestParseScenes_SingleClip(t *testing.T) {
	script := "Some intro text.\n\n[Clip: clip-a]\nFirst scene narration.\n\n[Clip: clip-b]\nSecond scene narration."
	scenes := ParseScenes(script)
	if len(scenes) != 3 {
		// 1 preamble (index 0) + 2 clip scenes
		t.Fatalf("got %d scenes, want 3 (1 preamble + 2 clip)", len(scenes))
	}
	if scenes[0].Kind != "preamble" {
		t.Errorf("scenes[0].Kind = %q, want preamble", scenes[0].Kind)
	}
	if scenes[1].Kind != "clip" || scenes[1].ClipID != "clip-a" {
		t.Errorf("scenes[1] = %+v, want clip/clip-a", scenes[1])
	}
	if scenes[2].Kind != "clip" || scenes[2].ClipID != "clip-b" {
		t.Errorf("scenes[2] = %+v, want clip/clip-b", scenes[2])
	}
}

func TestParseScenes_NarrationMarker(t *testing.T) {
	script := "[Narration: opening]\nThis is the intro.\n\n[Clip: clip-a]\nFirst clip.\n\n[Narration: closing]\nFinal wrap-up."
	scenes := ParseScenes(script)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if scenes[0].Kind != "narration" || scenes[0].NarrationRole != "opening" {
		t.Errorf("scenes[0] = %+v, want narration/opening", scenes[0])
	}
	if scenes[1].Kind != "clip" {
		t.Errorf("scenes[1].Kind = %q, want clip", scenes[1].Kind)
	}
	if scenes[2].Kind != "narration" || scenes[2].NarrationRole != "closing" {
		t.Errorf("scenes[2] = %+v, want narration/closing", scenes[2])
	}
}

func TestParseScenes_NoMarkers(t *testing.T) {
	script := "Just plain text with no markers at all."
	scenes := ParseScenes(script)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (preamble)", len(scenes))
	}
	if scenes[0].Kind != "preamble" {
		t.Errorf("scenes[0].Kind = %q, want preamble", scenes[0].Kind)
	}
}

func TestParseScenes_WhitespaceOnlyClipID(t *testing.T) {
	// The new regex matches whitespace-only IDs so the structural
	// check can flag them. [Clip: ] (with a single space) IS matched
	// as a clip scene with empty ClipID.
	script := "[Clip: ]\nEmpty marker body.\n\n[Clip: valid-id]\nValid marker body."
	scenes := ParseScenes(script)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (whitespace marker + valid marker, no preamble)", len(scenes))
	}
	if scenes[0].Kind != "clip" {
		t.Errorf("scenes[0].Kind = %q, want clip", scenes[0].Kind)
	}
	if scenes[0].ClipID != "" {
		t.Errorf("scenes[0].ClipID = %q, want empty (whitespace stripped)", scenes[0].ClipID)
	}
	if scenes[1].Kind != "clip" || scenes[1].ClipID != "valid-id" {
		t.Errorf("scenes[1] = %+v, want clip/valid-id", scenes[1])
	}
}

// ── ValidateScriptWithPack ────────────────────────────────────────────────

func makeValidationPack() *ClipSourcePack {
	return &ClipSourcePack{
		Clips: []ClipEvidence{
			{ClipID: "clip-a", Title: "Title A"},
			{ClipID: "clip-b", Title: "Title B"},
		},
	}
}

func makeValidationPlan() *ScriptGenerationPlan {
	return &ScriptGenerationPlan{TargetWords: 200}
}

func TestValidateScriptWithPack_AllPass(t *testing.T) {
	script := "[Clip: clip-a]\nFirst scene narration here.\n\n[Clip: clip-b]\nSecond scene narration here."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if !res.Valid {
		t.Errorf("expected Valid=true, got false; missing=%v unknown=%v dup=%v empty=%v",
			res.MissingClipIDs, res.UnknownClipIDs, res.DuplicateClipIDs, res.EmptyClipBlocks)
	}
	if res.SceneCount != 2 {
		t.Errorf("SceneCount = %d, want 2", res.SceneCount)
	}
	if res.ExpectedSceneCount != 2 {
		t.Errorf("ExpectedSceneCount = %d, want 2", res.ExpectedSceneCount)
	}
	if len(res.MissingAcceptedClips) != 0 {
		t.Errorf("MissingAcceptedClips = %v, want empty", res.MissingAcceptedClips)
	}
}

func TestValidateScriptWithPack_EmptyClipID(t *testing.T) {
	// The regex now matches whitespace-only IDs so the structural check
	// can flag them. [Clip: ] (with a space) IS matched and the
	// CapturedClipID is "" → flagged as a missing clip ID.
	script := "[Clip: clip-a]\nFirst scene.\n\n[Clip:   ]\nEmpty ID body."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if res.Valid {
		t.Errorf("expected Valid=false (whitespace-only clip ID is a hard failure)")
	}
	if len(res.MissingClipIDs) == 0 {
		t.Errorf("MissingClipIDs should be non-empty, got %v", res.MissingClipIDs)
	}
}

func TestValidateScriptWithPack_UnknownClipID(t *testing.T) {
	script := "[Clip: clip-a]\nFirst scene.\n\n[Clip: clip-unknown]\nBody with unknown clip."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if res.Valid {
		t.Errorf("expected Valid=false (unknown clip ID)")
	}
	if len(res.UnknownClipIDs) != 1 || res.UnknownClipIDs[0] != "clip-unknown" {
		t.Errorf("UnknownClipIDs = %v, want [clip-unknown]", res.UnknownClipIDs)
	}
}

func TestValidateScriptWithPack_DuplicateClipID(t *testing.T) {
	script := "[Clip: clip-a]\nFirst use.\n\n[Clip: clip-a]\nSecond use."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if res.Valid {
		t.Errorf("expected Valid=false (duplicate clip ID)")
	}
	if len(res.DuplicateClipIDs) == 0 {
		t.Errorf("DuplicateClipIDs should be non-empty, got %v", res.DuplicateClipIDs)
	}
}

func TestValidateScriptWithPack_MissingAcceptedClips(t *testing.T) {
	script := "[Clip: clip-a]\nOnly one clip used."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if !res.Valid {
		// Missing accepted clips is a soft warning, not a hard failure
		t.Errorf("expected Valid=true (missing accepted is soft), got false")
	}
	if len(res.MissingAcceptedClips) != 1 || res.MissingAcceptedClips[0] != "clip-b" {
		t.Errorf("MissingAcceptedClips = %v, want [clip-b]", res.MissingAcceptedClips)
	}
}

func TestValidateScriptWithPack_OverlongScene(t *testing.T) {
	longBody := strings.Repeat("a", 1000)
	script := "[Clip: clip-a]\n" + longBody + "\n\n[Clip: clip-b]\nShort body."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 500)
	if !res.Valid {
		t.Errorf("expected Valid=true (overlong is soft), got false")
	}
	if len(res.OverlongScenes) != 1 || res.OverlongScenes[0] != 1 {
		t.Errorf("OverlongScenes = %v, want [1]", res.OverlongScenes)
	}
}

func TestValidateScriptWithPack_NarrationForbidden(t *testing.T) {
	script := "[Narration: opening]\nIntro.\n\n[Clip: clip-a]\nBody."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if res.Valid {
		t.Errorf("expected Valid=false (narration forbidden for compilation)")
	}
	if len(res.NarrationScenesForbidden) == 0 {
		t.Errorf("NarrationScenesForbidden should be non-empty, got %v", res.NarrationScenesForbidden)
	}
}

func TestValidateScriptWithPack_NarrationAllowed(t *testing.T) {
	script := "[Narration: opening]\nIntro narration.\n\n[Clip: clip-a]\nBody.\n\n[Clip: clip-b]\nBody 2.\n\n[Narration: closing]\nOutro narration."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), true, 0)
	if !res.Valid {
		t.Errorf("expected Valid=true (narration allowed for story), got false; errs=%v", res.InvalidMarkers)
	}
}

func TestValidateScriptWithPack_InvalidNarrationRole(t *testing.T) {
	script := "[Narration: bogus_role]\nBody.\n\n[Clip: clip-a]\nBody.\n\n[Clip: clip-b]\nBody 2."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), true, 0)
	if res.Valid {
		t.Errorf("expected Valid=false (invalid narration role)")
	}
	if len(res.InvalidMarkers) == 0 {
		t.Errorf("InvalidMarkers should be non-empty, got %v", res.InvalidMarkers)
	}
}

func TestValidateScriptWithPack_EmptyScript(t *testing.T) {
	res := ValidateScriptWithPack("", makeValidationPlan(), makeValidationPack(), false, 0)
	if res.Valid {
		t.Errorf("expected Valid=false for empty script")
	}
	if len(res.StructuralWarnings) == 0 {
		t.Errorf("StructuralWarnings should mention empty script")
	}
}

func TestValidateScriptWithPack_NilPackSkipsStructural(t *testing.T) {
	// When pack is nil (regular text flow), no structural checks run.
	script := "Some script with no markers.\n[Clip: anything]\nMore text."
	res := ValidateScriptWithPack(script, makeValidationPlan(), nil, false, 0)
	if !res.Valid {
		t.Errorf("expected Valid=true with nil pack, got false")
	}
	if res.SceneCount != 0 {
		t.Errorf("SceneCount = %d, want 0 with nil pack", res.SceneCount)
	}
}

func TestValidateScriptWithPack_EmptyClipBlock(t *testing.T) {
	script := "[Clip: clip-a]\n\n\n[Clip: clip-b]\nBody."
	res := ValidateScriptWithPack(script, makeValidationPlan(), makeValidationPack(), false, 0)
	if res.Valid {
		t.Errorf("expected Valid=false (empty clip block)")
	}
	if len(res.EmptyClipBlocks) == 0 {
		t.Errorf("EmptyClipBlocks should be non-empty, got %v", res.EmptyClipBlocks)
	}
}

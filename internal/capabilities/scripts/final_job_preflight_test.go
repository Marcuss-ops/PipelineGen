package scriptgeneration

import (
	"strings"
	"testing"
)

// locatorScene builds a remote scene whose media slot carries the given
// locator fields, mirroring compositeVideoScene's wire shape.
func locatorScene(id string, slot map[string]any) map[string]any {
	return map[string]any{
		"scene_id":         id,
		"index":            0,
		"kind":             "stock",
		"duration_seconds": 5.0,
		"stock":            slot,
	}
}

// TestFinalJobPreflight_MissingMediaLocatorFailsClosed pins the I1 gate on
// the `images selected = 0, expected 1` retry class from the Milton
// certification: a scene whose media slot carries no resolvable Drive
// locator is rejected BEFORE the remote job exists, so the failure costs a
// local validation instead of a 184s-average worker attempt.
func TestFinalJobPreflight_MissingMediaLocatorFailsClosed(t *testing.T) {
	scenes := []map[string]any{locatorScene("scene-1", map[string]any{"drive_file_id": nil, "url": ""})}
	err := enforceFinalJobPreflight(scenes, 5000)
	if err == nil {
		t.Fatal("expected preflight failure for a scene without a media locator")
	}
	if !strings.Contains(err.Error(), "preflight") || !strings.Contains(err.Error(), "media locator") {
		t.Fatalf("error must name the preflight gate and the missing locator, got: %v", err)
	}
	if !strings.Contains(err.Error(), "scene-1") {
		t.Fatalf("error must name the offending scene, got: %v", err)
	}
}

// TestFinalJobPreflight_DriveFileIDLocatorIsAccepted pins the locator
// resolution: a scene whose media slot carries only the canonical
// deferred-Drive locator (drive_file_id, no url) is admitted.
func TestFinalJobPreflight_DriveFileIDLocatorIsAccepted(t *testing.T) {
	scenes := []map[string]any{locatorScene("scene-1", map[string]any{"drive_file_id": "drive-1"})}
	if err := enforceFinalJobPreflight(scenes, 5000); err != nil {
		t.Fatalf("preflight rejected a drive_file_id locator: %v", err)
	}
}

// TestFinalJobPreflight_InvalidDurationFailsClosed pins the audio-budget
// half of the gate: zero/NaN scene durations fail closed instead of rounding
// into zero-length remote scenes.
func TestFinalJobPreflight_InvalidDurationFailsClosed(t *testing.T) {
	cases := map[string]map[string]any{
		"zero":     {"scene_id": "s", "duration_seconds": 0.0, "stock": map[string]any{"drive_file_id": "d"}},
		"negative": {"scene_id": "s", "duration_seconds": -1.0, "stock": map[string]any{"drive_file_id": "d"}},
		"missing":  {"scene_id": "s", "stock": map[string]any{"drive_file_id": "d"}},
	}
	for name, scene := range cases {
		if err := enforceFinalJobPreflight([]map[string]any{scene}, 5000); err == nil {
			t.Fatalf("%s: expected preflight failure for an invalid scene duration", name)
		} else if !strings.Contains(err.Error(), "invalid duration") {
			t.Fatalf("%s: error must name the invalid duration, got: %v", name, err)
		}
	}
}

// TestFinalJobPreflight_CertifiedAudioMustBePositive pins the fail-closed
// contract on the run projection itself: a run without certified audio never
// reaches the remote handoff.
func TestFinalJobPreflight_CertifiedAudioMustBePositive(t *testing.T) {
	scenes := []map[string]any{locatorScene("scene-1", map[string]any{"drive_file_id": "d"})}
	if err := enforceFinalJobPreflight(scenes, 0); err == nil {
		t.Fatal("expected preflight failure for non-positive certified audio")
	}
}

// TestFinalJobPreflight_ValidPayloadAdmitted pins the no-false-positive
// contract: a well-formed scene set with the canonical wire fields passes.
func TestFinalJobPreflight_ValidPayloadAdmitted(t *testing.T) {
	scenes := []map[string]any{
		locatorScene("scene-1", map[string]any{"drive_file_id": "d1", "url": "velox-drive://d1"}),
		locatorScene("scene-2", map[string]any{"url": "velox-drive://d2"}),
	}
	if err := enforceFinalJobPreflight(scenes, 10000); err != nil {
		t.Fatalf("preflight rejected a valid payload: %v", err)
	}
}

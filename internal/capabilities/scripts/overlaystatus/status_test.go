package overlaystatus

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestUpdateMergesStagesAndKeepsEarlierFacts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VELOX_OVERLAY_STATUS_DIR", dir)

	Update("run_test", func(status *Status) {
		status.Stage = "background_resolved"
		status.JobID = "job_1"
		status.Requested = &Request{BackgroundAssetID: "drive-background-01", EntityImages: true, MaxImageOverlays: 20}
		status.Background = &Background{AssetID: "drive-background-01", Resolved: true, SHA256: strings.Repeat("a", 64)}
		status.Audit = map[string]any{"entity_images": map[string]any{"selected_entities": 18}}
	})
	Update("run_test", func(status *Status) {
		status.Stage = "preflight_failed"
		status.Error = "entity images were requested but the audit selected zero"
		status.Preflight = &Preflight{Passed: false, Error: status.Error}
	})

	data, err := os.ReadFile(Path("run_test"))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var got Status
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %q, want %q", got.SchemaVersion, SchemaVersion)
	}
	if got.Stage != "preflight_failed" {
		t.Fatalf("stage = %q, want preflight_failed", got.Stage)
	}
	if got.JobID != "job_1" {
		t.Fatalf("job id was lost: %q", got.JobID)
	}
	if got.Requested == nil || got.Requested.BackgroundAssetID != "drive-background-01" || !got.Requested.EntityImages {
		t.Fatalf("requested contract was lost: %+v", got.Requested)
	}
	if got.Background == nil || !got.Background.Resolved || got.Background.AssetID != "drive-background-01" {
		t.Fatalf("background was lost: %+v", got.Background)
	}
	if got.Audit == nil {
		t.Fatal("audit was lost on the second update")
	}
	if got.Preflight == nil || got.Preflight.Passed {
		t.Fatalf("preflight = %+v, want failed", got.Preflight)
	}
	if !strings.Contains(got.Error, "zero") {
		t.Fatalf("error = %q, want the preflight failure", got.Error)
	}
}

func TestUpdateEmptyRunIsNoOp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VELOX_OVERLAY_STATUS_DIR", dir)
	Update("", func(status *Status) { status.Stage = "should-not-exist" })
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty run wrote %d files", len(entries))
	}
}

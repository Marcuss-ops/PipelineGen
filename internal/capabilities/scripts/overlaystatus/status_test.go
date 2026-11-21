package overlaystatus

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
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

// TestUpdateConcurrentRunsStayIsolated pins the per-run lock: independent runs
// write concurrently without serialising on one global mutex, and each document
// remains a complete, latest-state JSON (the atomic replace is not lost under
// contention).
func TestUpdateConcurrentRunsStayIsolated(t *testing.T) {
	t.Setenv("VELOX_OVERLAY_STATUS_DIR", t.TempDir())
	const runs = 8
	const steps = 20
	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			runID := fmt.Sprintf("run_%d", i)
			for step := 0; step < steps; step++ {
				Update(runID, func(status *Status) {
					status.Stage = fmt.Sprintf("stage_%d", step)
				})
			}
		}()
	}
	wg.Wait()

	for i := 0; i < runs; i++ {
		runID := fmt.Sprintf("run_%d", i)
		data, err := os.ReadFile(Path(runID))
		if err != nil {
			t.Fatalf("%s: %v", runID, err)
		}
		var status Status
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatalf("%s: decode: %v", runID, err)
		}
		if status.Stage != fmt.Sprintf("stage_%d", steps-1) {
			t.Fatalf("%s: stage = %q, want the last update", runID, status.Stage)
		}
	}
	// The lock table must not retain entries after every document is released.
	runLocks.Lock()
	left := len(runLocks.entries)
	runLocks.Unlock()
	if left != 0 {
		t.Fatalf("run lock table retained %d entries", left)
	}
}

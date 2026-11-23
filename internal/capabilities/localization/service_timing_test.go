package localization

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// delayedRenderExecutor is a render boundary whose Execute takes a known time,
// so a saturated gate leaves a measurable admission wait on the next unit.
type delayedRenderExecutor struct {
	facts RenderFacts
	delay time.Duration
}

func (e *delayedRenderExecutor) Execute(context.Context, render.RenderPlan, *SubtitleAsset) (RenderFacts, error) {
	time.Sleep(e.delay)
	return e.facts, nil
}

// TestLocalizeReportsAdmissionWaitOutsideTheUnitWorkSpan pins the 2026-11-22
// Augias r39 regression: per-render wall_ms was measured from the Localize call
// (submission) to its return, so the last clip of a 25-render lane carried the
// whole lane's admission wait as its own work, and render_metrics.work_ms summed
// that wait 25 times (1 965 s for cache hits). The unit's work span is now
// admission → publication, and the wait before admission is QueueWaitMS.
func TestLocalizeReportsAdmissionWaitOutsideTheUnitWorkSpan(t *testing.T) {
	const delay = 80 * time.Millisecond
	renderer := newTestRenderer(t,
		&fakeRendererCompiler{},
		newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()}),
		&delayedRenderExecutor{facts: validRenderFacts(), delay: delay},
	)
	service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, &fakeDriveUploader{
		result: &DriveUploadResult{FileID: "drive-1", Link: "https://drive/1"},
	}), 1, 1)
	if err != nil {
		t.Fatalf("NewServiceWithConcurrency: %v", err)
	}

	first := validPlan()
	second := validPlan()
	second.SceneID = "scene-2"
	second.ClipID = "clip-2"
	second.Fingerprint = Fingerprint(second)

	result, err := service.Localize(context.Background(), LocalizeInput{
		Concurrency: 1, FolderID: "folder-run", Plans: []LocalizedClipPlan{first, second},
	})
	if err != nil {
		t.Fatalf("Localize: %v", err)
	}
	if result == nil || len(result.Artifacts) != 2 || len(result.Failures) != 0 {
		t.Fatalf("Localize result = %+v, want 2 artifacts and no failures", result)
	}

	var waited, immediate *LocalizedClipArtifact
	for i := range result.Artifacts {
		artifact := &result.Artifacts[i]
		if artifact.AdmittedAt.IsZero() || artifact.FinishedAt.IsZero() {
			t.Fatalf("%s: timing boundaries missing: %+v", artifact.ClipID, artifact)
		}
		if !artifact.FinishedAt.After(artifact.AdmittedAt) {
			t.Fatalf("%s: finished_at %v must be after admitted_at %v", artifact.ClipID, artifact.FinishedAt, artifact.AdmittedAt)
		}
		if artifact.QueueWaitMS >= 40 {
			waited = artifact
			continue
		}
		immediate = artifact
	}
	if waited == nil {
		t.Fatalf("no unit reported an admission wait; artifacts = %+v", result.Artifacts)
	}
	if immediate == nil {
		t.Fatalf("both units reported an admission wait; artifacts = %+v", result.Artifacts)
	}
	// The waiting unit did the same work as the first one: its own span is the
	// render+publication window, not the ~80 ms it spent queued behind the gate.
	waitedWorkMS := waited.FinishedAt.Sub(waited.AdmittedAt).Milliseconds()
	if waitedWorkMS < 60 {
		t.Fatalf("%s: work span = %d ms, want the unit's own render+publication window", waited.ClipID, waitedWorkMS)
	}
	if waitedWorkMS > 4*delay.Milliseconds() {
		t.Fatalf("%s: work span = %d ms absorbed the admission wait (queue_wait_ms = %d)", waited.ClipID, waitedWorkMS, waited.QueueWaitMS)
	}
	if immediate.QueueWaitMS > 40 {
		t.Fatalf("%s: queue_wait_ms = %d, want the first unit to be admitted immediately", immediate.ClipID, immediate.QueueWaitMS)
	}
}

// The compile check keeps the test honest if the executor interface changes.
var _ RenderPlanExecutor = (*delayedRenderExecutor)(nil)

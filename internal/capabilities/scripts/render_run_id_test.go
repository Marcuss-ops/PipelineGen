package scriptgeneration

import (
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestRenderRunIDDerivation pins the run-identity derivation shared with the
// migration-287 backfill: the write path (this builder) and the historical
// backfill (287_render_attempt_run_id.sql) must produce identical run_id
// values for the same job id shapes.
func TestRenderRunIDDerivation(t *testing.T) {
	cases := []struct {
		jobID string
		want  string
	}{
		{"run_9405664a-c61f-4c75-9e06-bc43ff1272f5:item:005:phrase", "run_9405664a-c61f-4c75-9e06-bc43ff1272f5"},
		{"run_ea669c55-f519-4930-be1b-9687e688eadf:item:000:batch-5813a9be", "run_ea669c55-f519-4930-be1b-9687e688eadf"},
		{"run_1788527686642826949_3182094a7b5e", "run_1788527686642826949_3182094a7b5e"},
		{"trump-final-render", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := RenderRunID(tc.jobID); got != tc.want {
			t.Errorf("RenderRunID(%q) = %q, want %q", tc.jobID, got, tc.want)
		}
	}
}

// TestBuildRenderAttemptAnalyticsProjectsRunID pins that the builder projects
// the run identity from the job id (plan id) without inventing one.
func TestBuildRenderAttemptAnalyticsProjectsRunID(t *testing.T) {
	perItem := capoverlay.OverlayPlan{PlanID: "run_x:item:001:phrase"}
	if got := BuildRenderAttemptAnalytics("attempt-1", perItem, nil).RunID; got != "run_x" {
		t.Errorf("per-item plan: RunID = %q, want %q", got, "run_x")
	}
	legacy := capoverlay.OverlayPlan{PlanID: "run_1788527686642826949_3182094a7b5e"}
	if got := BuildRenderAttemptAnalytics("attempt-2", legacy, nil).RunID; got != "run_1788527686642826949_3182094a7b5e" {
		t.Errorf("legacy run_ plan: RunID = %q, want the plan id itself", got)
	}
	other := capoverlay.OverlayPlan{PlanID: "trump-final-render"}
	if got := BuildRenderAttemptAnalytics("attempt-3", other, nil).RunID; got != "" {
		t.Errorf("unattributable plan: RunID = %q, want empty", got)
	}
}

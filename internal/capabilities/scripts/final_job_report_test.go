package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestFinalJobReceiptDeclaresTheSingleReportOwner pins the one-owner rule: a
// reader must not infer timing from a log line or a second sidecar. The durable
// run result carries the owner explicitly.
func TestFinalJobReceiptDeclaresTheSingleReportOwner(t *testing.T) {
	raw, err := json.Marshal(RemoteFinalJobResult{
		JobID:       "job-1",
		Status:      "SUCCEEDED",
		ReportOwner: FinalJobReportOwner,
		Timing:      &RemoteFinalJobTiming{ExecutionMS: 11_771},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"report_owner":"run_observability.report_json"`) {
		t.Fatalf("receipt %s is missing the report owner", raw)
	}
}

// TestFinalJobReceiptIsPersistedInTheCanonicalRunReport proves the owner is not
// just a string on the receipt: the runner projects the receipt into
// RunReport.FinalJob, which is what run_observability.report_json stores.
func TestFinalJobReceiptIsPersistedInTheCanonicalRunReport(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "job-1", JobType: "script.generate", AttemptID: "attempt-1",
	})
	ctx := kernobs.WithRun(context.Background(), run)
	recordFinalJobReport(ctx, &RemoteFinalJobResult{
		JobID:       "master-1",
		Status:      "SUCCEEDED",
		ReportOwner: FinalJobReportOwner,
		Timing:      &RemoteFinalJobTiming{ExecutionMS: 11_771},
	})
	report := run.Finish()
	if report == nil || report.FinalJob == nil {
		t.Fatalf("run report has no final_job projection: %+v", report)
	}
	if report.FinalJob.ReportOwner != FinalJobReportOwner {
		t.Fatalf("projection owner = %q, want %q", report.FinalJob.ReportOwner, FinalJobReportOwner)
	}
	var receipt RemoteFinalJobResult
	if err := json.Unmarshal(report.FinalJob.Receipt, &receipt); err != nil {
		t.Fatalf("decode projected receipt: %v", err)
	}
	if receipt.JobID != "master-1" || receipt.Status != "SUCCEEDED" {
		t.Fatalf("projected receipt = %+v, want the Master handle", receipt)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"final_job":{`) || !strings.Contains(string(raw), `"report_owner":"run_observability.report_json"`) {
		t.Fatalf("run report JSON does not expose the canonical final-job projection: %s", raw)
	}
}

// TestFinalJobTimingMergeKeepsTheLocallyMeasuredHandoff: the attach result
// reports Master-clock phases, while the handoff was measured here. Merging
// must keep both, so a resumed wait cannot erase what submission spent.
func TestFinalJobTimingMergeKeepsTheLocallyMeasuredHandoff(t *testing.T) {
	base := &RemoteFinalJobTiming{HandoffMS: 335_000}
	merged := base.Merge(&RemoteFinalJobTiming{ExecutionMS: 11_771})
	if merged.HandoffMS != 335_000 || merged.ExecutionMS != 11_771 {
		t.Fatalf("merge lost a phase: %+v", merged)
	}
}

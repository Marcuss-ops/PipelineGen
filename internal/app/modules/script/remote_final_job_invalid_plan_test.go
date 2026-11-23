package script

// remote_final_job_invalid_plan_test.go pins the classification that separates
// a rejected PLAN from a stale remote IDENTITY. A stale identity is worth
// rotating; a plan the Master refuses is not, because the next attempt would
// submit the same plan and reproduce the rejection. The two classes must
// therefore never collapse into ErrFinalJobStaleReceipt.

import (
	"errors"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

func TestFinalJobWaitErrorKeepsPlanRejectionSeparateFromStaleIdentity(t *testing.T) {
	cause := errors.New("window 2 names no source range")
	err := finalJobWaitError(&remotejob.InvalidPlanError{
		JobID:  "job-kaufmann",
		Status: "RUNNING",
		Code:   "WINDOW_EXTRADATA_IDENTITY_MISMATCH",
		Cause:  cause,
	})
	if !errors.Is(err, scriptgen.ErrFinalJobInvalidPlan) {
		t.Fatalf("mapped error = %v, want ErrFinalJobInvalidPlan", err)
	}
	if errors.Is(err, scriptgen.ErrFinalJobStaleReceipt) {
		t.Fatalf("mapped error = %v, a rejected plan must not be reported as a stale receipt", err)
	}
	if !errors.Is(err, remotejob.ErrRemoteInvalidPlan) {
		t.Fatalf("mapped error = %v, want the transport sentinel preserved for logs", err)
	}
	if errors.Is(err, remotejob.ErrRemoteStaleJob) {
		t.Fatalf("mapped error = %v, must not claim a stale identity", err)
	}

	// A genuine stale identity still maps to its own class, and only that one.
	stale := finalJobWaitError(&remotejob.StaleJobError{JobID: "job-old", Status: "CANCELED"})
	if !errors.Is(stale, scriptgen.ErrFinalJobStaleReceipt) {
		t.Fatalf("stale mapping = %v, want ErrFinalJobStaleReceipt", stale)
	}
	if errors.Is(stale, scriptgen.ErrFinalJobInvalidPlan) {
		t.Fatalf("stale mapping = %v, must not claim a rejected plan", stale)
	}
}

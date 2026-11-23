package finalize

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	capfinalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// TestStockFinalizeAdapter_SkipModeSurvivesRoundTrip pins the adapter
// round-trip contract that the production FAILED run exposed: the step
// threads MetadataState into finalize.Request and the adapter rebuilds a
// MetadataState from it before re-running the VerifyMetadata gate. If
// DriveUploadSkipped is not part of the neutral finalize.Metadata contract,
// the flag is lost and the fail-closed gate rejects the run.
func TestStockFinalizeAdapter_SkipModeSurvivesRoundTrip(t *testing.T) {
	sha := "2036e99215c101f82679172d685b46cbe42d8f0abe153b46806bce11c12371ff"
	req := newStockFinalizeRequest(
		"job-1",
		capfinalization.Lease{JobID: "job-1", WorkerID: "w-1", LeaseID: "l-1"},
		[]byte("{}"),
		nil,
		core.MetadataState{LocalPath: "/tmp/metadata.json", SHA256: sha, DriveUploadSkipped: true},
		"fp-1",
	)

	back := toLegacyMetadata(req.Metadata)
	if !back.DriveUploadSkipped {
		t.Fatal("toLegacyMetadata lost DriveUploadSkipped across the adapter round-trip")
	}
	if err := core.VerifyMetadata(back); err != nil {
		t.Fatalf("VerifyMetadata(after round-trip) = %v, want nil", err)
	}
}

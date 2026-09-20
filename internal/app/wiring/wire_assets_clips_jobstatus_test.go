package wiring

import (
	"context"
	"testing"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
)

// ledgerWithStatus embeds the write-only Registry contract and adds the narrow
// read method the adapter looks for (the embedded nil is never called: the
// adapter only ever invokes LatestAssetJobStatus).
type ledgerWithStatus struct {
	capjobregistry.Registry
}

func (ledgerWithStatus) LatestAssetJobStatus(context.Context, string) (string, int, bool) {
	return "RUNNING", 4, true
}

// writeOnlyLedger has no read method: the adapter must leave the port nil so the
// handler falls back to its generic 409 rather than fabricating a status.
type writeOnlyLedger struct {
	capjobregistry.Registry
}

func TestClipsAssetJobStatusResolvesReadableLedger(t *testing.T) {
	got := clipsAssetJobStatus(&JobsBundle{JobLedger: ledgerWithStatus{}})
	if got == nil {
		t.Fatal("adapter is nil for a ledger that implements LatestAssetJobStatus")
	}
	status, retry, found := got.LatestJobStatus(context.Background(), "yt_1")
	if !found || status != "RUNNING" || retry != 4 {
		t.Fatalf("got %s/%d found=%v, want RUNNING/4 found=true", status, retry, found)
	}
}

func TestClipsAssetJobStatusNilWhenLedgerUnreadable(t *testing.T) {
	if got := clipsAssetJobStatus(nil); got != nil {
		t.Fatal("adapter must be nil for a nil JobsBundle")
	}
	if got := clipsAssetJobStatus(&JobsBundle{}); got != nil {
		t.Fatal("adapter must be nil when JobLedger is nil")
	}
	if got := clipsAssetJobStatus(&JobsBundle{JobLedger: writeOnlyLedger{}}); got != nil {
		t.Fatal("adapter must be nil for a ledger without the read method (no fake status)")
	}
}

package stockpipeline

import (
	"context"
	"testing"
	"time"

	stockfinalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/finalize"
	capfinalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

type recordingFinalizeSpine struct {
	request capfinalization.FinalizationRequest
}

func (s *recordingFinalizeSpine) CompleteWithArtifacts(_ context.Context, request capfinalization.FinalizationRequest) (*capfinalization.FinalizationResult, error) {
	s.request = request
	return &capfinalization.FinalizationResult{JobID: request.Lease.JobID, Status: "SUCCEEDED"}, nil
}

func TestStockFinalizeAdapterDelegatesNeutralRequest(t *testing.T) {
	spine := &recordingFinalizeSpine{}
	adapter := &stockFinalizeAdapter{finalizer: spine}
	lease := stockfinalize.Lease{
		JobID:     "job-1",
		WorkerID:  "worker-1",
		LeaseID:   "lease-1",
		Attempt:   2,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	// BuildFinalizationRequest owns strict chunk/metadata validation, so this
	// test provides a complete neutral request with valid digest-shaped values.
	request := stockfinalize.Request{
		JobID:       "job-1",
		Lease:       lease,
		ResultData:  []byte(`{"ok":true}`),
		Fingerprint: "fingerprint-1",
		Artifacts: []stockfinalize.Artifact{{
			Index:        0,
			ArtifactID:   "chunk-1",
			Filename:     "chunk.mp4",
			LocalPath:    "/tmp/chunk.mp4",
			SHA256:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RemoteFileID: "remote-chunk-1",
		}},
		Metadata: stockfinalize.Metadata{
			LocalPath:    "/tmp/metadata.json",
			SHA256:       "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			RemoteFileID: "remote-metadata-1",
		},
	}

	result, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if result.JobID != "job-1" || spine.request.Lease.LeaseID != "lease-1" {
		t.Fatalf("unexpected delegation result=%#v request=%#v", result, spine.request)
	}
	if len(spine.request.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want metadata plus chunk", len(spine.request.Artifacts))
	}
}

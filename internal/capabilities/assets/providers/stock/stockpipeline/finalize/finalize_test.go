package finalize

import (
	"context"
	"testing"
)

type recordingPort struct {
	request Request
}

func (p *recordingPort) Complete(_ context.Context, request Request) (Result, error) {
	p.request = request
	return Result{JobID: request.JobID, Status: "SUCCEEDED"}, nil
}

func TestPortCarriesNeutralRequest(t *testing.T) {
	port := &recordingPort{}
	request := Request{
		JobID:       "job-1",
		Fingerprint: "fingerprint-1",
		Lease:       Lease{JobID: "job-1", LeaseID: "lease-1"},
		Artifacts:   []Artifact{{ArtifactID: "artifact-1", SHA256: "hash"}},
	}

	result, err := port.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if result.JobID != request.JobID || port.request.Fingerprint != request.Fingerprint {
		t.Fatalf("request/result mismatch: request=%#v result=%#v", port.request, result)
	}
}

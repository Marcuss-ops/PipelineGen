package stockpipeline

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
)

type recordingIngestStager struct {
	request acquisition.PrepareRequest
}

func (s *recordingIngestStager) Prepare(_ context.Context, request acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	s.request = request
	return &acquisition.PrepareContext{
		ID:           "stage-id",
		LocalPath:    "/tmp/source.mp4",
		SizeBytes:    42,
		CleanupToken: "cleanup-token",
	}, nil
}

func (s *recordingIngestStager) Release(context.Context, string) error { return nil }

func TestStockIngestPreparerTranslatesSourceAndPreservesPolicyKey(t *testing.T) {
	stager := &recordingIngestStager{}
	preparer := &stockIngestPreparer{
		stager:        stager,
		policyVersion: "policy-v2",
	}

	got, err := preparer.Prepare(context.Background(), ingest.Source{
		ID:  "source-id",
		URL: "https://example.com/video.mp4",
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got == nil {
		t.Fatal("Prepare returned nil source")
	}
	if got.SourceID != "source-id" || got.LocalPath != "/tmp/source.mp4" || got.Bytes != 42 {
		t.Fatalf("unexpected prepared source: %#v", got)
	}
	if stager.request.Source.URL != "https://example.com/video.mp4" {
		t.Fatalf("unexpected source URL: %q", stager.request.Source.URL)
	}
	if stager.request.Source.PolicyVersion != "" {
		t.Fatalf("source policy leaked into request source: %q", stager.request.Source.PolicyVersion)
	}
	if stager.request.IdempotencyKey == "" {
		t.Fatal("expected deterministic idempotency key")
	}
}

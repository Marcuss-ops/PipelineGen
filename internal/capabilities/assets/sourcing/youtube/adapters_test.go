// Package youtube — TDD tests for adapter-layer NoAudio propagation.
//
// Regression guard for PR-YT-NO-AUDIO-THREAD (July 2026): pre-fix,
// the fetcherAdapter.Fetch did not forward the NoAudio field from
// usecase.FetchRequest to sourcing.FetchRequest, silently dropping
// the flag at this adapter boundary.
//
// godlike/06 SSOT: these tests are the canonical SOLE regression guard
// for the fetcherAdapter NoAudio forwarding invariant.
package assets

import (
	"context"
	"testing"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube/usecase"
)

// stubSourcingFetcher implements sourcing.FetchProviderPort for adapter
// tests. It captures the sourcing.FetchRequest so assertions can verify
// field forwarding without spinning up a real yt-dlp pipeline.
type stubSourcingFetcher struct {
	lastReq sourcing.FetchRequest
}

func (s *stubSourcingFetcher) Fetch(_ context.Context, req sourcing.FetchRequest) (*sourcing.FetchedAsset, error) {
	s.lastReq = req
	return &sourcing.FetchedAsset{LocalPath: "/tmp/stub.mp4"}, nil
}

// TestFetcherAdapter_ForwardsNoAudio locks the adapter-layer contract:
// fetcherAdapter.Fetch MUST forward NoAudio from usecase.FetchRequest
// to sourcing.FetchRequest. This is the bridge between the YouTube
// use-case subpackage and the sourcing.Service layer.
//
// Regression guard for PR-YT-NO-AUDIO-THREAD (July 2026).
func TestFetcherAdapter_ForwardsNoAudio(t *testing.T) {
	stub := &stubSourcingFetcher{}
	adapter := &fetcherAdapter{inner: stub}

	_, err := adapter.Fetch(context.Background(), usecase.FetchRequest{
		AssetID:   "test-123",
		SourceRef: "https://www.youtube.com/watch?v=test",
		NoAudio:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.lastReq.NoAudio {
		t.Errorf("expected sourcing.FetchRequest.NoAudio=true when usecase.FetchRequest.NoAudio=true, got false")
	}
}

// TestFetcherAdapter_NoAudio_False_ForwardsFalse locks the backward-
// compatible default: when usecase.FetchRequest.NoAudio is false
// (zero-value), sourcing.FetchRequest.NoAudio MUST also be false.
func TestFetcherAdapter_NoAudio_False_ForwardsFalse(t *testing.T) {
	stub := &stubSourcingFetcher{}
	adapter := &fetcherAdapter{inner: stub}

	_, err := adapter.Fetch(context.Background(), usecase.FetchRequest{
		AssetID:   "test-456",
		SourceRef: "https://www.youtube.com/watch?v=test",
		NoAudio:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastReq.NoAudio {
		t.Errorf("expected sourcing.FetchRequest.NoAudio=false when usecase.FetchRequest.NoAudio=false (default), got true")
	}
}

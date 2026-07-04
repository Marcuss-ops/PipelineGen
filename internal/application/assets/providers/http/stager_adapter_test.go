// Package httpstager — stager_adapter_test.go (ART-002 P4.2, July 2026).
//
// 1 TDD test pinning the HTTPStager skeleton behavior:
//
//   - TestHTTPStager_StageSource_NotImplemented: every StageSource
//     call returns ErrHTTPStagerNotImplemented reachable via
//     errors.Is, with the failing URL in the message. Cleanup is a
//     no-op (returns nil) for any input.
//
// The skeleton contract is intentionally narrow: godlike/07
// no-fake-availability means the typed error is the single source of
// truth that the HTTP provider has not landed. When the real adapter
// ships, this test will be replaced with a real-happy-path test
// alongside the new SKELETON retirement commit.
package httpstager

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

func TestHTTPStager_StageSource_NotImplemented(t *testing.T) {
	s := NewHTTPStager()
	ctx := context.Background()
	ref := assets.SourceRef{URL: "https://example.com/asset.mp4"}

	staged, err := s.StageSource(ctx, ref)
	if err == nil {
		t.Fatalf("expected error from HTTPStager.StageSource, got staged=%+v", staged)
	}
	if staged != nil {
		t.Errorf("expected nil staged on error, got %+v", staged)
	}
	if !errors.Is(err, ErrHTTPStagerNotImplemented) {
		t.Errorf("expected errors.Is(err, ErrHTTPStagerNotImplemented) to be true, got err=%v", err)
	}
	// Cleanup is a no-op for any input.
	if err := s.Cleanup(ctx, nil); err != nil {
		t.Errorf("expected nil error from Cleanup(nil), got %v", err)
	}
	if err := s.Cleanup(ctx, &assets.StagedAsset{LocalPath: "/tmp/never-existed"}); err != nil {
		t.Errorf("expected nil error from Cleanup(any), got %v", err)
	}
}

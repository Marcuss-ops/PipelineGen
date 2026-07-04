// Package catalog — stager_adapter_test.go (ART-002 P4.2, July 2026).
//
// 1 TDD test pinning the CatalogStager skeleton behavior:
//
//   - TestCatalogStager_StageSource_NotImplemented: every StageSource
//     call returns ErrCatalogStagerNotImplemented reachable via
//     errors.Is, with the failing URL in the message. Cleanup is a
//     no-op (returns nil) for any input.
//
// The skeleton contract is intentionally narrow: godlike/07
// no-fake-availability means the typed error is the single source of
// truth that the catalog provider has not landed. When the real
// adapter ships, this test will be replaced with a real-happy-path
// test alongside the new SKELETON retirement commit.
package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

func TestCatalogStager_StageSource_NotImplemented(t *testing.T) {
	s := NewCatalogStager()
	ctx := context.Background()
	ref := assets.SourceRef{URL: "asset://catalog/clip-xyz-42"}

	staged, err := s.StageSource(ctx, ref)
	if err == nil {
		t.Fatalf("expected error from CatalogStager.StageSource, got staged=%+v", staged)
	}
	if staged != nil {
		t.Errorf("expected nil staged on error, got %+v", staged)
	}
	if !errors.Is(err, ErrCatalogStagerNotImplemented) {
		t.Errorf("expected errors.Is(err, ErrCatalogStagerNotImplemented) to be true, got err=%v", err)
	}
	// Cleanup is a no-op for any input.
	if err := s.Cleanup(ctx, nil); err != nil {
		t.Errorf("expected nil error from Cleanup(nil), got %v", err)
	}
	if err := s.Cleanup(ctx, &assets.StagedAsset{LocalPath: "/tmp/never-existed"}); err != nil {
		t.Errorf("expected nil error from Cleanup(any), got %v", err)
	}
}

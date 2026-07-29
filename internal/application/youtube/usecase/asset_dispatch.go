// asset_dispatch.go — persistence dispatch (PR1.6 single canonical writer).
//
// Split out of orchestrator.go in Step 4 so each usecase/ file owns exactly
// one responsibility. dispatchOrIndex is unexported because it is consumed
// only by same-package files (e.g. adapters/assetrepo_integration_test.go).
package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// dispatchOrIndex writes a freshly-cut clip to the canonical asset store.
//
// The previous triple fallback (assetRepo → disp.EnqueueAndIndex →
// clipsRepo.Upsert) has been removed in PR1.6. AssetRepo is the SOLE
// writer and emits the asset.upserted outbox event atomically (PR12b
// semantics). If AssetRepo is not wired the call returns an explicit
// error so callers see the missing dependency rather than experiencing
// a silent no-op.
func (s *Service) dispatchOrIndex(ctx context.Context, clip *asset.Asset, _ string) error {
	if clip == nil {
		return fmt.Errorf("youtube.dispatchOrIndex: nil clip")
	}
	// typed-nil guard: portutil.IsNilPort catches (*Concrete)(nil) casts
	// to interface that pass == nil. Composition audit (June 2026)
	// confirmed all adapter constructors return bare nil, so this is
	// defensive.
	if s.assetRepo == nil || portutil.IsNilPort(s.assetRepo) {
		return fmt.Errorf("youtube: canonical assetRepo not wired — composition root must include AssetRepo in ServiceDeps")
	}
	return s.assetRepo.Upsert(ctx, clip)
}

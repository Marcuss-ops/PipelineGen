package cleanup

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

// NewStagerReleaser adapts an acquisition.SourceStager to the neutral
// Releaser boundary owned by this package.
func NewStagerReleaser(stager acquisition.SourceStager) Releaser {
	return &stagerReleaser{stager: stager}
}

// stagerReleaser is the only bridge from the ownership-neutral cleanup
// package to the existing acquisition lifecycle. Cleanup owns orchestration;
// acquisition remains the owner of release semantics and lease bookkeeping.
type stagerReleaser struct {
	stager acquisition.SourceStager
}

var _ Releaser = (*stagerReleaser)(nil)

func (r *stagerReleaser) Release(ctx context.Context, resource Resource) error {
	if r == nil || r.stager == nil {
		return fmt.Errorf("stock cleanup: source stager is not wired")
	}
	if resource.LocalPath == "" {
		return fmt.Errorf("stock cleanup: empty local path for source %q", resource.SourceID)
	}
	return r.stager.Release(ctx, resource.LocalPath)
}

// ─── ingest bridge ───────────────────────────────────────────────────────────

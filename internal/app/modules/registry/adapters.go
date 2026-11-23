package registry

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// mutationsDispatcherAdapter bridges the canonical outbox dispatcher to the
// capability-level AssetMutationDispatcher port. The adapter remains pure
// composition glue: no business policy or event synthesis lives here.
type mutationsDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

var _ mutations.AssetMutationDispatcher = (*mutationsDispatcherAdapter)(nil)

// NewMutationsDispatcherAdapter constructs the canonical mutation dispatcher
// adapter and fails closed when the outbox dispatcher is missing.
func NewMutationsDispatcherAdapter(disp *outbox.Dispatcher) (mutations.AssetMutationDispatcher, error) {
	if disp == nil {
		return nil, fmt.Errorf("registry.NewMutationsDispatcherAdapter: outbox.Dispatcher is required")
	}
	return &mutationsDispatcherAdapter{disp: disp}, nil
}

func (a *mutationsDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

func (a *mutationsDispatcherAdapter) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndRestore(ctx, assetID)
}

func (a *mutationsDispatcherAdapter) EnqueueAndDelete(ctx context.Context, assetID string) error {
	if a == nil || a.disp == nil {
		return mutations.ErrDispatcherUnavailable
	}
	return a.disp.EnqueueAndDelete(ctx, assetID)
}

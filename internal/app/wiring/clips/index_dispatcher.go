// Package clipswiring adapts the SQLite outbox dispatcher to the Clips
// capability's indexing port.
package clipswiring

import (
	"context"
	"fmt"

	clipscap "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// clipsDispatcherAdapter adapts the concrete *outbox.Dispatcher to
// application/clips.ClipIndexDispatcherPort.
//
// outbox.Dispatcher.EnqueueAndIndex accepts *asset.Asset, so this
// adapter is a pure type-tag shim — no field rewriting required
// (unlike the sourcing adapter which converts sourcing.ExistingClip
// → asset.Asset).
//
// Construction happens once in the clips composition, gated
// on `dispatcher != nil`. Nil-port callers are the handler's
// concern: it checks `h.dispatcher != nil` before delegating and
// falls back to raw repo.UpsertClip on the no-port path.
// Adapter construction with a nil dispatcher is rejected —
// the compile assertion on the var-decl line below catches drift
// of the port signature, and the constructor's nil-check catches runtime
// configuration mistakes.
type clipsDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// Compile-time assertion: clipsDispatcherAdapter satisfies
// clips.ClipIndexDispatcherPort. Latent signature drift in either
// direction will fail `go build` rather than wait for the first
// runtime panic.
var _ clipscap.ClipIndexDispatcherPort = (*clipsDispatcherAdapter)(nil)

func NewIndexDispatcherAdapter(dispatcher *outbox.Dispatcher) clipscap.ClipIndexDispatcherPort {
	if dispatcher == nil {
		return nil
	}
	return &clipsDispatcherAdapter{disp: dispatcher}
}

func (a *clipsDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if clip == nil {
		return fmt.Errorf("clipsDispatcherAdapter: clip is nil")
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

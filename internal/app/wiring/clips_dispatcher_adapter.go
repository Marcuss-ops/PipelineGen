// Package app: composition-time adapters that bridge concrete
// infrastructure to application-layer ports. The composition root is
// the ONLY place where concrete infra types meet application ports
// (per AGENTS.md Pattern 8 / "internal/api/** non deve contenere
// business orchestration").
package wiring

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
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
// Construction happens ONCE in WireAssets (module_media.go), gated
// on `dispatcher != nil`. Nil-port callers are the handler's
// concern: it checks `h.dispatcher != nil` before delegating and
// falls back to raw repo.UpsertClip on the no-port path.
// Adapter construction with a nil disp is a programmer error —
// the compile assertion on the var-decl line below catches drift
// of the port signature, and the construction-time nil-check in
// WireAssets catches runtime configuration mistakes.
type clipsDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// Compile-time assertion: clipsDispatcherAdapter satisfies
// clips.ClipIndexDispatcherPort. Latent signature drift in either
// direction will fail `go build` rather than wait for the first
// runtime panic.
var _ clips.ClipIndexDispatcherPort = (*clipsDispatcherAdapter)(nil)

func (a *clipsDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if clip == nil {
		return fmt.Errorf("clipsDispatcherAdapter: clip is nil")
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

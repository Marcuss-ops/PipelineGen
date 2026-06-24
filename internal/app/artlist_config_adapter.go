// Package app — Artlist config adapter.
//
// Wraps the canonical *config.Config into the typed
// `artlistPkg.ArtlistConfigPort` so the api handler stays free of
// infrastructure-layer imports. Only the single
// `ArtlistRootFolderID()` value the handler actually consumes is
// exposed via the port (Pattern 0: define only the methods the
// handler uses).
//
// The adapter delegates to the existing `artlist.ResolveRootFolderID`
// helper (defined in internal/application/assets/providers/artlist/run_helpers.go)
// so the operator-facing precedence rules (MediaRootFolder >
// ArtlistRootFolder > "") stay in a single source of truth.
package app

import (
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// artlistConfigAdapter wraps *config.Config to satisfy
// artlistPkg.ArtlistConfigPort. The composition-root keeps the
// config concrete; this adapter exposes only the narrow port
// surface to the api/ layer.
type artlistConfigAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: artlistConfigAdapter satisfies artlistPkg.ArtlistConfigPort.
var _ artlistPkg.ArtlistConfigPort = (*artlistConfigAdapter)(nil)

// ArtlistRootFolderID resolves the canonical artlist root folder.
// Nil-tolerant: a nil underlying cfg returns "" matching the
// pre-refactor behaviour of artlist.ResolveRootFolderID(nil).
func (a *artlistConfigAdapter) ArtlistRootFolderID() string {
	return artlistPkg.ResolveRootFolderID(a.cfg)
}

// newArtlistConfigAdapter is the canonical composition-root constructor.
// Returns a nil interface when cfg is nil so the wiring site preserves
// the `cfgPort != nil` discipline callers can rely on (production
// wiring always passes a non-nil cfg).
func newArtlistConfigAdapter(cfg *config.Config) artlistPkg.ArtlistConfigPort {
	if cfg == nil {
		return nil
	}
	return &artlistConfigAdapter{cfg: cfg}
}

package wiring

import (
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// artlistConfigAdapter exposes only the narrow Artlist configuration port from
// the concrete platform config owned by the composition root.
type artlistConfigAdapter struct {
	cfg *config.Config
}

var _ artlistPkg.ArtlistConfigPort = (*artlistConfigAdapter)(nil)

func (a *artlistConfigAdapter) ArtlistRootFolderID() string {
	return artlistPkg.ResolveRootFolderID(a.cfg)
}

func newArtlistConfigAdapter(cfg *config.Config) artlistPkg.ArtlistConfigPort {
	if cfg == nil {
		return nil
	}
	return &artlistConfigAdapter{cfg: cfg}
}

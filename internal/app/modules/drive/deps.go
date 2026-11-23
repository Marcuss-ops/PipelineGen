package drive

// deps.go owns the typed input contracts of the Drive module.
//
// Before this file the composition root handed the module positional argument
// lists (four for the bundle, seven for catalog sync, four for the admin
// adapter). A positional list cannot be validated in one place, cannot be
// extended without touching every call site, and silently accepts a
// re-ordered argument when the two types are assignable. Each entry point now
// takes a named struct, validates it where the module owns the requirement,
// and reports the failure with the module's own vocabulary (BON-03, step A:
// one module, one `Deps`, one call site).
//
// `Provide` is deliberately the only producer of the Bundle: the composition
// root calls it once, for the first phase of the graph
// (`BuildRepoBundle -> BuildSearchBundle -> drive.Provide -> BuildJobsBundle`).

import (
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	legacyassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"go.uber.org/zap"
)

// Deps is the input contract of Provide. Config and Log are mandatory: the
// module reads the Drive folder layout from the config and reports degraded
// paths through the logger. Database stays optional because the bundle builds
// a working in-memory configuration when the operational writer is absent.
type Deps struct {
	Config   *config.Config
	Database *sql.DB
	Log      *zap.Logger
}

func (d Deps) validate() error {
	if d.Config == nil {
		return fmt.Errorf("drive: config is required")
	}
	if d.Log == nil {
		return fmt.Errorf("drive: logger is required")
	}
	return nil
}

// CatalogSyncDeps is the input contract of BuildCatalogSyncService. Every
// field is an already-composed port: the caller resolves them, the module does
// not reach into the composition root.
type CatalogSyncDeps struct {
	Config     *config.Config
	MediaDB    *sql.DB
	Legacy     *legacyassets.ClipsRepository
	Uploader   *platformdrive.Uploader
	AssetTree  *assettree.Service
	Dispatcher *outbox.Dispatcher
	Log        *zap.Logger
}

// SystemAdminAdapterDeps is the input contract of NewSystemAdminAdapter. Admin
// is the only mandatory port: without it the adapter is not created at all and
// the caller keeps its own degraded mode.
type SystemAdminAdapterDeps struct {
	Admin     platformdrive.Admin
	Reader    platformdrive.Reader
	Lifecycle platformdrive.FileLifecycle
	Log       *zap.Logger
}

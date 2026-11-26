// Package app — helper functions extracted from registry.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// standalone helpers used by WireRegistry: capability/provider aliases, provider
// registry bootstrap, module registration and the artifact cache entry point.
//
// Wave A Item 15 (June 2026): ensureStyleDriveFolders + style-folder
// pre-creation REMOVED — the canonical StyleRegistry already serves
// style metadata at the composition boundary, and the legacy
// drive-side pre-creation step was a hard-coded single point of
// failure that masked style discovery drift. The driveup + generation
// imports are no longer needed here (this file's remaining helpers
// never reach the Drive SDK directly or the StyleRegistry).
package wiring

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"

	assetsmod "github.com/Marcuss-ops/PipelineGen/internal/app/modules/assets"
	registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/registry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	platformcache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifactcache"

	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

// ── Style Drive folder pre-creation ─────────────────────────────────────────
//
// Wave A Item 15 (June 2026): REMOVED. The legacy ensureStyleDriveFolders
// helper (which called uploader.GetOrCreateFolder in a loop for every
// registered style) has been deleted from this file. The corresponding
// concurrent.SafeGo("drive-style-folders", ...) call site in
// build_bundles_drive.go::startDriveBackgroundFolders is also removed.
// Composition's role is to wire deps; per-style Drive folder creation
// is the operator's responsibility post-deploy via the canonical
// `reset-video-ai` admin command (which uses the drive.Admin port).

type PreparedCapabilities = registrywiring.PreparedCapabilities
type CapabilityDeps = registrywiring.CapabilityDeps
type TrackedHTTPModule = registrywiring.TrackedHTTPModule
type TrackedProviderEntry = registrywiring.TrackedProviderEntry
type ProviderKind = registrywiring.ProviderKind

const (
	ProviderKindSearch = registrywiring.ProviderKindSearch
	ProviderKindFetch  = registrywiring.ProviderKindFetch
)

type strictOption = registrywiring.StrictOption

func bootstrapProviderRegistry(provReg *providers.Registry, entries []TrackedProviderEntry, descriptors []module.DescriptorProviders) error {
	return registrywiring.BootstrapProviderRegistry(provReg, entries, descriptors)
}

func registerCapabilities(reg *module.Registry, provReg *providers.Registry, deps CapabilityDeps) error {
	return registrywiring.RegisterCapabilities(registrywiring.CapabilityRegistrationDeps{
		Registry:     reg,
		Providers:    provReg,
		Capabilities: deps,
		Validate:     registrywiring.ValidateRuntimeGraph,
	})
}

func WithRegistrationPoint(point string) strictOption {
	return registrywiring.WithRegistrationPoint(point)
}

func tryRegisterModuleStrict(reg *module.Registry, log *zap.Logger, mod module.Module, opts ...strictOption) error {
	return registrywiring.TryRegisterModuleStrict(registrywiring.RegisterModuleDeps{Registry: reg, Log: log, Module: mod}, opts...)
}

func tryRegisterModule(reg *module.Registry, log *zap.Logger, mod module.Module) error {
	return registrywiring.TryRegisterModule(reg, log, mod)
}

// newArtifactCache is the single composition-site entry into the assets
// module's artifact cache (BON-03 step B): the six wiring sites that need a
// cache pass their two handles here, and this function is the only code in the
// root that knows the module's Deps shape. It lives in this helper file rather
// than its own because internal/app/wiring is a registered package-count
// hotspot at its baseline and must not grow.
func newArtifactCache(cfg *config.Config, cacheDB, contentDB *sql.DB, log *zap.Logger) (*platformcache.Cache, error) {
	return assetsmod.NewArtifactCache(assetsmod.ArtifactCacheDeps{
		Config:    cfg,
		CacheDB:   cacheDB,
		ContentDB: contentDB,
		Log:       log,
	})
}

// Package asset — Service factory-method type-switch bridge
// (Wave C / Phase 3 slim).
//
// Phase 3 (Wave C / Blocco 1 Asset SSOT, June 2026): the Get/Save/
// Delete/List receivers on `*AssetStoreSQLite` + the
// assetRepositoryAdapter struct + 8 methods + FindByExternalRef +
// listAssetsByFilter helper + getAssetByID top-level helper +
// buildSummaryQuery/scanSummary SQL builders were relocated to
// Local infra:
//
//   - Get/Save/Delete/List methods        → internal/infrastructure/database/sqlite/assets/asset_store.go
//   - assetRepositoryAdapter + 8 methods  → internal/infrastructure/database/sqlite/assets/repo_queries.go
//   - listAssetsByFilter + FindByExternalRef → internal/infrastructure/database/sqlite/assets/repo_queries.go
//   - buildSummaryQuery + scanSummary + getAssetByID
//     → internal/infrastructure/database/sqlite/assets/clip_list_queries.go + asset_store.go
//
// This file now hosts ONLY:
//
//   - The unexported `assetStoreAdapter` interface — the type-switch
//     bridge that decouples `Service.{Repository,LocationRepository,
//     ProcessingRepository,VersionRepository}` from the concrete
//     `*AssetStoreSQLite` type so callers in this domain layer never
//     reach into Local infra's adapter structs.
//
// NO `database/sql` import. NO `fmt`/`strings`/`time`/`timeutil`/
// `encoding/json` imports. The new embed-friendly shape is:
//
//   - Legacy `*asset.AssetStoreSQLite` declares 4 nil-returning
//     factory method stubs (in store_helpers.go) so the type satisfies
//     `assetStoreAdapter` even on its own.
//   - Local `*sqassets.AssetStoreSQLite` (HYBRID embed) declares its
//     own concrete factory methods, which shadow the legacy stubs.
//   - `Service.{Repository,...}()` type-switches against
//     `assetStoreAdapter`; only Local satisfies it with real adapters,
//     legacy alone returns nil. Production paths always go through
//     Local so this asymmetry is invisible at runtime.
package asset

// assetStoreAdapter exposes the 4 concrete factory methods needed by
// `Service.{Repository,LocationRepository,ProcessingRepository,
// VersionRepository}` without forcing the domain layer to import
// Local infra.
//
// The interface lives in domain (NOT in Local infra) because the
// canonical caller is `Service` — a domain type. Putting the bridge
// in domain keeps the direction-of-import graph acyclic:
//
//	internal/kernel/asset/Service → assetStoreAdapter (here)
//	internal/infrastructure/database/sqlite/assets/AssetStoreSQLite
//	                              → satisfies assetStoreAdapter
//	                                (declarations live next to it)
//
// Caller-side contract: the value passed to `NewService(Store, log)`
// MUST additionally implement assetStoreAdapter for the Repository
// accessors to be useful. Production callers satisfy this through
// `sqassets.NewAssetStoreSQLite(*sql.DB, *zap.Logger)`; legacy
// `*asset.AssetStoreSQLite` alone satisfies it but returns nil from
// every factory method (the adapter structs moved to Local infra).
type assetStoreAdapter interface {
	Repository() Repository
	LocationRepository() LocationRepository
	ProcessingRepository() ProcessingRepository
	VersionRepository() VersionRepository
}

// ── Service factory method type-switches ───────────────────────────
//
// Each method below type-switches against `assetStoreAdapter`. When
// the underlying Store implements the bridge interface (i.e. is a
// Local `*sqassets.AssetStoreSQLite` via embed promotion, OR a
// legacy `*asset.AssetStoreSQLite` directly), the corresponding real
// adapter is returned. Otherwise nil.
//
// The nil return is deliberate: passing a non-asset-store Store
// (eg a mock test double) does NOT defeat the build, it just means
// the Repository accessors are unavailable. Consumers handle nil
// explicitly when they care (Repository factories are usually
// optional dependencies in the asset pipeline).

// Repository returns the canonical Asset Repository adapter for the
// underlying Store, or nil when the Store does not implement the
// assetStoreAdapter bridge.
func (s *Service) Repository() Repository {
	if a, ok := s.store.(assetStoreAdapter); ok {
		return a.Repository()
	}
	return nil
}

// LocationRepository returns the canonical LocationRepository adapter
// for the underlying Store, or nil when the bridge is not implemented.
func (s *Service) LocationRepository() LocationRepository {
	if a, ok := s.store.(assetStoreAdapter); ok {
		return a.LocationRepository()
	}
	return nil
}

// ProcessingRepository returns the canonical ProcessingRepository
// adapter for the underlying Store, or nil when the bridge is not
// implemented.
func (s *Service) ProcessingRepository() ProcessingRepository {
	if a, ok := s.store.(assetStoreAdapter); ok {
		return a.ProcessingRepository()
	}
	return nil
}

// VersionRepository returns the canonical VersionRepository adapter
// for the underlying Store, or nil when the bridge is not implemented.
func (s *Service) VersionRepository() VersionRepository {
	if a, ok := s.store.(assetStoreAdapter); ok {
		return a.VersionRepository()
	}
	return nil
}

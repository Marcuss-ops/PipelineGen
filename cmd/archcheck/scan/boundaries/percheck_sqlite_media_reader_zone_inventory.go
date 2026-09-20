// Package scan — percheck_sqlite_media_reader_zone_inventory.go holds the DATA
// of the media-reader gate: the exact-file inventory of the legacy read-plane
// zones that were converted from a path prefix. The scanner that consumes it
// (and the other two registers) lives in percheck_sqlite_media_reader_ban.go;
// the lists live here because they are a WORKLIST that is edited every time a
// legacy consumer is retired, and keeping them out of the scanner keeps both
// files under the per-file line cap.
package boundaries

// sqliteMediaReaderInventoriedZoneFiles is the exact-file inventory of a
// legacy read-plane zone that has been CONVERTED from a path prefix.
//
// WHY A CONVERTED ZONE NEEDS A REGISTER OF ITS OWN. A prefix exemption is
// invisible forward prevention: it pardons the whole package, so a new SQLite
// reader dropped into that package inherits the pardon and the promoted
// SQLITE_MEDIA_READERS=0 counter stops meaning anything. Converting a zone
// means enumerating the files that actually read media_assets today and
// dropping the prefix in the same change; from then on the package is exact
// and a new sibling is a violation.
//
// This register is the SAME KIND of thing as the two debt registers in
// percheck_sqlite_media_reader_ban.go (an exact-file pardon that must shrink),
// and the same three pins cover it: every entry must still exist, every entry
// must still have a SQLite-dialect media read (never a permanent allowlist),
// and the entry must be deleted in the same change as its consumer is migrated
// or removed.
//
// CONVERTED ZONES — 2026-09-20. Each prefix was removed from
// sqliteMediaReaderGrandfatheredZones in the same change that enumerated its
// readers, so this inventory is the only thing standing between those packages
// and a clean gate:
//
//   - internal/platform/qdrant/indexing/ — the Qdrant media compatibility seam
//     and its local-catalog payload readers, retired wholesale with the Qdrant
//     media projection. PROGRESS 2026-09-20: the six `clipindexer/` legacy
//     implementation files left the inventory (and the tree) when clipindexer
//     was reduced to a pure PostgreSQL-outbox delegator, and
//     `asset_store_reconcile.go` followed it once its `ListAssetsForReconcile`
//     scan was CERTIFIED CALLERLESS — the `reconciliation` capability its
//     header named has no non-test importer at all, and reads through its own
//     `SQLiteReconcileReader.ListForReconcile` port instead, which nothing
//     implements.
//
//     The two readers that remain are NOT deletable yet, and the blocker is
//     per-file rather than generic debt:
//
//   - asset_store_fetch.go is LIVE. `FetchAsset` is called by the Qdrant search
//     hydration (search_adapter.go, semantic_asset_search_adapter.go),
//     `ListAllAssetIDs` by verification/verifier_counts.go and
//     indexing/payload_mapper.go, and `ErrAssetNotFound` is matched with
//     errors.Is by those same two search adapters — so deleting the file breaks
//     the build. Step 2 of the MEDIA LEGACY READ-PLANE DEMOLITION plan
//     ("only after their production responsibilities have moved to PostgreSQL
//     or have no callers") is therefore NOT satisfied for it.
//
//   - asset_store.go owns the `canonicalQuery` + `assetRowScanner` +
//     `maxTranscriptsPerAsset` triple shared with asset_store_fetch.go and
//     asset_store_batch.go, so it can only leave after both do.
//
//   - cmd/admin/ — operator tooling that deliberately runs against the
//     operational database. It is inventoried rather than migrated because
//     these commands are the documented operational read plane, not a
//     production split-brain: naming them file by file is what stops a NEW
//     admin command from inheriting the exemption.
//
//   - internal/platform/sqlite/ — the operational SQLite state store, and the
//     largest converted zone (32 files). It is the legacy read plane itself
//     (the ClipsRepository/AssetStoreSQLite facade, the operator read port, the
//     mediaregistry ledger and the one-shot backfills), retired file by file as
//     its consumers move to the PostgreSQL media SSOT. Enumerating it converts
//     "the whole package is exempt" into a WORKLIST: every future deletion must
//     delete its entry in the same change, and the list can only shrink.
var sqliteMediaReaderInventoriedZoneFiles = map[string]bool{
	"cmd/admin/internal/backfill/backfill_missing.go":                            true,
	"cmd/admin/internal/cleanup/cleanup_drive_orphans.go":                        true,
	"cmd/admin/internal/drive/drive_reconcile.go":                                true,
	"internal/platform/qdrant/indexing/asset_store.go":                           true,
	"internal/platform/qdrant/indexing/asset_store_fetch.go":                     true,
	"internal/platform/sqlite/assets/imagesregistry/asset_store.go":              true,
	"internal/platform/sqlite/assets/imagesregistry/asset_store_batch.go":        true,
	"internal/platform/sqlite/assets/imagesregistry/clip_list_queries.go":        true,
	"internal/platform/sqlite/assets/imagesregistry/clips_index_state.go":        true,
	"internal/platform/sqlite/assets/imagesregistry/clips_queries.go":            true,
	"internal/platform/sqlite/assets/imagesregistry/clips_repository_queries.go": true,
	"internal/platform/sqlite/assets/imagesregistry/clips_statistics.go":         true,
	"internal/platform/sqlite/assets/imagesregistry/folder_queries.go":           true,
	"internal/platform/sqlite/assets/imagesregistry/maintenance_repository.go":   true,
	"internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go":    true,
	"internal/platform/sqlite/assets/imagesregistry/primitives.go":               true,
	"internal/platform/sqlite/assets/imagesregistry/repo_queries.go":             true,
	"internal/platform/sqlite/assets/imagesregistry/search_queries.go":           true,
	"internal/platform/sqlite/assets/imagesregistry/source_version.go":           true,
	"internal/platform/sqlite/assets/imagesregistry/store_helpers.go":            true,
	"internal/platform/sqlite/assets/imagesrepo/images_aggregate.go":             true,
	"internal/platform/sqlite/assets/imagesrepo/images_generated.go":             true,
	"internal/platform/sqlite/assets/imagesrepo/images_insert_update.go":         true,
	"internal/platform/sqlite/assets/imagesrepo/images_search.go":                true,
	"internal/platform/sqlite/controlplane/verifier.go":                          true,
	"internal/platform/sqlite/deletion/stuck_row_scanner.go":                     true,
	"internal/platform/sqlite/mediaregistry/canonical_identity.go":               true,
	"internal/platform/sqlite/mediaregistry/content_link_backfill.go":            true,
	"internal/platform/sqlite/mediaregistry/ledger.go":                           true,
	"internal/platform/sqlite/metadataexport/asset_resolver.go":                  true,
}

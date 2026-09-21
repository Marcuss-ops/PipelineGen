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
//     `maxTranscriptsPerAsset` triple shared with asset_store_fetch.go, so it
//     can only leave after that one does. (The sibling asset_store_batch.go
//     this note used to name no longer exists in the package.)
//
//   - cmd/admin/ — operator tooling that deliberately runs against the
//     operational database. It is inventoried rather than migrated because
//     these commands are the documented operational read plane, not a
//     production split-brain: naming them file by file is what stops a NEW
//     admin command from inheriting the exemption.
//
//   - internal/platform/sqlite/ — the operational SQLite state store, and the
//     largest converted zone (29 files). It is the legacy read plane itself
//     (the ClipsRepository/AssetStoreSQLite facade, the operator read port, the
//     mediaregistry ledger and the one-shot backfills), retired file by file as
//     its consumers move to the PostgreSQL media SSOT. Enumerating it converts
//     "the whole package is exempt" into a WORKLIST: every future deletion must
//     delete its entry in the same change, and the list can only shrink.
//
//     PROGRESS 2026-09-21 (MEDIA LEGACY READ-PLANE DEMOLITION):
//     `asset_store_batch.go` left the inventory and the tree. `BatchGetByIDs`
//     (the SQLite search-result hydrator + its 60 s LRU) and `SetBatchCache`
//     had zero production and zero test call sites, the Qdrant hydration they
//     were written for is gone, and the file had no non-media half — so it is a
//     DELETE row, complete in one change. The `batchCache` field it required was
//     removed from `asset_store.go` in the same change, which can therefore stay
//     in the inventory (it still reads media_assets for Get/Save/List).
//
//     `clips_statistics.go` left the inventory AND the tree: its live
//     `CountBySource` (the /api/artlist/diagnostics per-source total) now
//     resolves on the media SSOT through pgmedia.MediaStatisticsReader, exposed
//     as a dedicated optional artlist port (artlist.SourceCounter then; widened
//     to artlist.MediaStats in sub-wave B' below, when its two siblings moved
//     too), and its dead CountPersistedSince had already gone. The count was
//     moved OFF artlist.AssetStore rather than stubbed on the repository, so
//     this package never reads media_assets again: the operational store keeps
//     implementing only its operational methods (seven at that point, five after
//     sub-wave B'). With no media handle the port stays nil and the diagnostics
//     field is reported as unavailable instead of counting a mirror the
//     canonical committer does not write to.
//
//     PROGRESS 2026-09-21, sub-wave B: two more SQLite rows left the inventory
//     and the tree.
//
//     `source_version.go` was a terminal DELETE (not the migration the earlier
//     recon expected): the supersede gate that consumed
//     `detail.SourceVersionQuerier` was retired with the IndexingHandler family
//     on 2026-09-12, so the port had no consumer left besides its own
//     compile-time pin, and neither the producer nor the consumer path called
//     the free function any more (the cmd/admin producer computes the same
//     projection inline in backfill_missing.go:67). The media-SSOT twin already
//     exists in `pgmedia/index_event.go` (COALESCE over source_version /
//     legacy_file_md5), so nothing had to be written: the file, its test, the
//     wrapper method, the port and the pin were removed together.
//
//     `clips_index_state.go` left the inventory (but NOT the tree) once its
//     media READ was gone: `GetIndexState` had zero production call sites, the
//     production read already resolves on the media SSOT through the
//     `postgresCatalogRepository` router, and the one remaining consumer shape —
//     the catalogsync degrade branch — now answers the index-state slot with
//     `noMediaPlaneIndexState` (fail closed) instead of reading
//     `media_assets.index_state` off the operational mirror. What remains is the
//     `SoftDelete` / `SetIndexState` terminal WRITE half, and the register
//     governs reads — the same shape as `folder_queries.go` above, which the
//     staleness pin found rather than the author.
//
//     PROGRESS 2026-09-21, sub-wave B': `clip_list_queries.go` left the
//     inventory AND the tree. Its two remaining reads (CountClips and
//     LastUpdatedAtForTerm, the media half of a SPLIT row) were the only reason
//     the operational store still owned a media_assets aggregate: both now
//     resolve on the media SSOT through `pgmedia.MediaStatisticsReader`,
//     exposed as the widened optional `artlist.MediaStats` port (the same port
//     that already carried the per-source count). Moving them OFF the port is
//     what let the file go: `artlist.AssetStore` keeps only its five operational
//     methods, and the chained-dead `youtube/ports.ClipStorePort.CountClips`
//     (zero call sites) was deleted with it. Two semantics were deliberately
//     preserved and pinned on the live engine — CountClips keeps the
//     soft-delete discount that CountBySource does not apply, and
//     LastUpdatedAtForTerm keeps the absence of a lifecycle filter — while one
//     divergence was forced: SQLite's case-insensitive LIKE became ILIKE.
//
//     `folder_queries.go` also left the inventory (but NOT the tree): its only
//     media_assets read was `CountByFolderID` (zero call sites, deleted in the
//     same change), and everything that remains — UpsertFolder, DeleteFolder,
//     GetFolder*, ListFolders and the manifest readers — queries `clip_folders`,
//     which is not a media row. The staleness pin caught this rather than the
//     author: with the count read gone the entry had no `?`-bound media_assets
//     read left, so keeping it would have been a permanent allowlist for a file
//     that is no longer on the read plane. This is the SPLIT row reaching its
//     terminal state (the media half is gone; the folder half survives).
var sqliteMediaReaderInventoriedZoneFiles = map[string]bool{
	"cmd/admin/internal/backfill/backfill_missing.go":                            true,
	"cmd/admin/internal/cleanup/cleanup_drive_orphans.go":                        true,
	"cmd/admin/internal/drive/drive_reconcile.go":                                true,
	"internal/platform/qdrant/indexing/asset_store.go":                           true,
	"internal/platform/qdrant/indexing/asset_store_fetch.go":                     true,
	"internal/platform/sqlite/assets/imagesregistry/asset_store.go":              true,
	"internal/platform/sqlite/assets/imagesregistry/clips_queries.go":            true,
	"internal/platform/sqlite/assets/imagesregistry/clips_repository_queries.go": true,
	"internal/platform/sqlite/assets/imagesregistry/maintenance_repository.go":   true,
	"internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go":    true,
	"internal/platform/sqlite/assets/imagesregistry/primitives.go":               true,
	"internal/platform/sqlite/assets/imagesregistry/repo_queries.go":             true,
	"internal/platform/sqlite/assets/imagesregistry/search_queries.go":           true,
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

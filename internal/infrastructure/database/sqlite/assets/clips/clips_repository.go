package clips

import (
	"database/sql"
	assets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_repository.go is the canonical home of the *ClipsRepository
// receiver: struct, constructors, MediaAssetColumns constant, the
// SourceVersionQuerier (PR 11) compile-time assertion, and a
// handful of utility accessors (Canonical, SoftDeleteFilter, Log, DB).
//
// Method-bearing files (PR1 strict-6 split):
//   - clips_crud.go         Upsert, Get, GetClip, SourceVersionFor,
//                           UpsertClip, DeleteClip, Mutate
//   - clips_queries.go      Count (caller-filtered)
//   - clips_resolution.go   ResolveByMediaAssetID, ResolveByYouTubeVideoID,
//                           ResolveByDriveFileID, ResolveByExternalProviderID,
//                           GetByDriveFileID, GetClipFolderByVideoID
//   - clips_index_state.go  SoftDelete, SetIndexState,
//                           DeleteClipByDriveLink
//   - clips_transactions.go BeginTx, UpsertClipTx, SetIndexStateTx
//   - clips_statistics.go   (intentionally empty — metric home reserved
//                           for future unconditional aggregation methods)
//
// Plus the Wave 15 split already on disk:
//   - clips_repository_queries.go   CountAll / CountIndexed / CountIndexable /
//                                    CountPendingOutbox / CountDeadLetter /
//                                    ListIndexedIDs / List / StreamAssetIDs /
//                                    inClause / AdvancedSearch* aliases
//   - clips_repository_folders.go   UpsertFolder / GetFolder /
//                                    GetFolderByVideoID / GetFolderByPath /
//                                    ListFolders / DriveFolderAttrs /
//                                    LookupDriveFolderIDBySourcePath /
//                                    UpsertDriveFolder
//   - txmutation/ subpackage        RestoreTx / HardDeleteTx (the
//                                    raw-mutation replacements — see
//                                    PR-CLIP-RAW-MUTATIONS, Wave 22)

// Compile-time assertion (Wave 16, June 2026; PR 11 followup
// June 2026): *ClipsRepository statically implements
// jobsoutbox.SourceVersionQuerier (pre-flight idempotency surface
// for the source_version supersede gate in IndexingHandler). Per
// AGENTS.md Pattern 0, the assertion lives at the adapter
// (infrastructure) home so port-drift bugs surface at compile
// time, not at the first index.requested replay.
//
// PR 11 follow-up: AssetSourceChecker interface (which exposed
// GetClip(ctx, id) (*asset.Asset, error)) was replaced with the
// narrower SourceVersionQuerier (SourceVersionFor(ctx, id)
// (string, error)). The replacement eliminates the producer-side
// ↔ consumer-side priority-chain drift that pre-PR-11-followup
// code carried (producer scanned via inline COALESCE; consumer
// walked the Asset struct — different chains, same name). Both
// sides now route through SourceVersionFor from
// source_version.go above, which is the single source of truth.
//
// Two-method-or-more port guidance (now vacated): the legacy note
// referenced the GetClip-only interface, but SourceVersionQuerier
// also has a single method. Adding a second method to the
// upstream port will trip this assertion and force the concrete
// to implement the new method — same compile-time behaviour under
// the new name. The same pattern as
// qdrant/search_adapter.go::var _ appsearch.VectorStorePort = ...
var _ jobsoutbox.SourceVersionQuerier = (*ClipsRepository)(nil)

// MediaAssetColumns is the canonical SELECT projection used by every
// Get / List / Search / Resolve path in this package. The projection
// is locked to ScanMediaAsset's scan signature in scan_helpers.go;
// every AS alias here MUST appear at the same positional index in
// ScanMediaAsset's `s.Scan(&dest...)` argument list.
//
// FASE 4 (June 2026): re-aligned from a drifted 38-column version to
// 39 columns matching ScanMediaAsset. The previous version was missing
// six columns (media_type, drive_folder_id, drive_link,
// download_link, group_name, status) and contained three ghost columns no
// longer in the canonical schema (web_view_link, is_folder, depth —
// removed by migration 059's json_remove). `status` was later removed
// (July 2026) because migration 101 dropped the DB column.
//
// If you change this constant, update scans in lockstep:
//   - scan_helpers.go::ScanMediaAsset  (consumes the AS aliases)
//   - clips_crud_test.go::canonicalMediaAssetColumns  (pins the order)
//
// `status` column was REMOVED (PR-search-handler-provider-errors, July 2026):
// migration 101 removed the DB column but the SELECT still referenced it,
// causing "no such column: status" on every SearchClipsAdvanced query.
// The paired ScanMediaAsset edit removed the corresponding scan target.
const MediaAssetColumns = `
	id,
	COALESCE(source, '') AS source,
	COALESCE(name, '') AS name,
	COALESCE(tags, '[]') AS tags,
	COALESCE(tags_norm, '') AS tags_norm,
	COALESCE(embedding_json, '[]') AS embedding_json,
	COALESCE(duration_ms, 0) AS duration_ms,
	COALESCE(url, '') AS url,
	COALESCE(media_type, '') AS media_type,
	COALESCE(local_path, '') AS local_path,
	COALESCE(relative_path, '') AS relative_path,
	COALESCE(drive_file_id, '') AS drive_file_id,
	COALESCE(drive_folder_id, '') AS drive_folder_id,
	COALESCE(drive_link, '') AS drive_link,
	COALESCE(download_link, '') AS download_link,
	COALESCE(file_hash, '') AS file_hash,
	COALESCE(metadata_json, '{}') AS metadata_json,
	COALESCE(visual_embedding, '[]') AS visual_embedding,
	COALESCE(transcript_embedding, '[]') AS transcript_embedding,
	created_at,
	COALESCE(updated_at, '') AS updated_at,
	COALESCE(width, 0) AS width,
	COALESCE(height, 0) AS height,
	COALESCE(lifecycle_state, 'ACTIVE') AS lifecycle_state,
	COALESCE(deleted_at, '') AS deleted_at,
	COALESCE(folder_id, '') AS folder_id,
	COALESCE(parent_folder_id, '') AS parent_folder_id,
	COALESCE(folder_path, '') AS folder_path,
	COALESCE(category, '') AS category,
	COALESCE(group_name, '') AS group_name,
	COALESCE(filename, '') AS filename,
	COALESCE(error, '') AS error,
	COALESCE(thumb_url, '') AS thumb_url,
	COALESCE(phash, '') AS phash,
	COALESCE(search_text, '') AS search_text,
	COALESCE(scene_type, '') AS scene_type,
	COALESCE(quality_score, 0.0) AS quality_score,
	COALESCE(reuse_count, 0) AS reuse_count,
	COALESCE(last_used_at, '') AS last_used_at`

type ClipsRepository struct {
	*assets.AssetStoreSQLite // Wave C / Phase 3: embed LOCAL *assets.AssetStoreSQLite (the canonical infra struct) instead of legacy *asset.AssetStoreSQLite. LOCAL has the canonical Save/Get/Delete/List methods AND transitively exposes legacy receivers via its own HYBRID embed of legacy. Existing call sites like `r.AssetStoreSQLite.Save(...)` auto-resolve because the embedded-field name is unchanged.
	db                       *sql.DB
	log                      *zap.Logger
}

func NewClipsRepository(db *sql.DB, log *zap.Logger) *ClipsRepository {
	return &ClipsRepository{
		AssetStoreSQLite: assets.NewAssetStoreSQLite(db, log),
		db:               db,
		log:              log,
	}
}

func NewClipsRepositoryCanonical(db *sql.DB, log *zap.Logger, canonical any) *ClipsRepository {
	return NewClipsRepository(db, log)
}

// ── Dangerous-mutation removal — Wave 22 task 5 / PR-CLIP-RAW-MUTATIONS ───
//
// Restore, HardDelete, and HardDeleteClip are REMOVED from this concrete
// repository as of PR-CLIP-RAW-MUTATIONS (June 2026). Their replacements live
// in the restricted tx-scoped package
// `internal/infrastructure/database/sqlite/assets/txmutation/`:
//
//   - txmutation.RestoreTx(ctx, tx, id)   — flips lifecycle_state back to
//                                          'ready' inside a caller-owned tx.
//   - txmutation.HardDeleteTx(ctx, tx, id) — physically removes the row
//                                          + dependent rows inside a
//                                          caller-owned tx.
//
// Rationale:
//   1. Production-reachability ban. The presence of public methods on
//      *assets.ClipsRepository made it trivial for non-canonical callers
//      to skip the outbox + the InternalAdminPurge safety validation. Removing
//      them from this receiver breaks all direct-producer paths. The CI
//      lint (`scripts/ci-architectural-checks.sh::Check 5`) bans any
//      direct caller regardless.
//   2. Tx-scoped only. The replacements REQUIRE the caller to hold an
//      open *sql.Tx. The caller — `admin.PurgeService` in
//      `internal/infrastructure/database/sqlite/admin/purge.go::HardDeleteClip`
//      and `RestoreClip` — opens the tx, calls the tx-scoped primitive,
//      and commits. A future caller that skips the tx boundary won't
//      compile because *sql.Tx is a non-optional parameter.
//
// The internal/application/assets/mutations/AssetMutationPrimitives
// interface ALSO drops Restore/HardDelete (UpsertClip stays — fixtures
// rely on it). The outbox dispatcher, which already implements
// AssetMutationDispatcher (3-method, not Primitives), is unaffected.
//
// Reference: architecture/deprecations.yaml → PR-CLIP-RAW-MUTATIONS.

func (r *ClipsRepository) Canonical() *ClipsRepository {
	return r
}

func (r *ClipsRepository) SoftDeleteFilter() string {
	// Phase 4 unification (June 2026): thin-wrapper that delegates
	// to the canonical asset.SoftDeleteFilter — the single SSOT for
	// the soft-delete SQL fragment. PR 1 (Lifecycle state SSOT)
	// semantics live on the canonical function in domain/asset.
	return asset.SoftDeleteFilter()
}

func (r *ClipsRepository) Log() *zap.Logger { return r.log }
func (r *ClipsRepository) DB() *sql.DB      { return r.db }

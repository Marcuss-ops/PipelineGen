// Package scan — percheck_sqlite_media_reader_ban promotes the historical
// certificate counter `SQLITE_MEDIA_READERS=0` into an enforced hard gate.
//
// BACKGROUND. The September 2026 media cutover moved the media domain
// authority to PostgreSQL + pgvector. The WRITE side is already enforced by
// percheck_media_assets_writer_canonical (no direct SQL write to media_assets
// outside the canonical AssetCommitter) and percheck_media_txn_boundary (no
// SQLite *sql.Tx can reach the PostgreSQL media writer). The READ side was
// never enforced: the only assertion that no production code read media_assets
// from SQLite lived in scripts/ci/certify-media-cutover.sh, which was deleted
// by commit 7e6965aab ("purge 94% shell"). architecture/catalog.yaml recorded
// the consequence honestly — the three certificate counters became
// UNVERIFIED, with the standing instruction "restore the driver or promote the
// three counters into cmd/archcheck".
//
// This scanner is that promotion for the READ counter.
//
// WHAT IT BANS. A NEW non-test Go file under internal/ or cmd/ whose SQL reads
// the `media_assets` table, outside the grandfathered read surfaces below.
// New media reads MUST go through the PostgreSQL media read authority
// (internal/platform/postgres/media.MediaSearcher) rather than a SQLite
// mirror.
//
// WHY A GRANDFATHER LIST INSTEAD OF ZERO. The legacy operational SQLite read
// plane still has live production consumers (the exact inventory is the
// "REMAINING BLOCKER FOR P2-9 PHASE 2" list in architecture/catalog.yaml).
// Failing the build on the existing set would be a red gate that cannot pass —
// a target that cannot pass is not a gate. The list is therefore an explicit,
// reviewable DEBT REGISTER that ratchets to zero as those consumers migrate;
// what it buys today is forward prevention: no NEW SQLite media reader can
// land unnoticed.
//
// godlike/06 SSOT: the exemption vocabulary (skip dirs, scanner-source prefix,
// SQL-migration prefixes) comes from cmd/archcheck/policy/exempt.go — this
// file declares no second copy.
//
// matched rule_id: `percheck_sqlite_media_reader_ban`.
package boundaries

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// sqliteMediaReaderRule is the rule-family id the scanner emits.
const sqliteMediaReaderRule = "percheck_sqlite_media_reader_ban"

// sqliteMediaReaderScanRoots are the roots the gate walks. cmd/ is included
// for the same reason the write gate widened to it: the documented claim is
// repo-wide, and the admin CLI is the historical exception surface.
var sqliteMediaReaderScanRoots = []string{
	"internal",
	"cmd",
}

// sqliteMediaReaderReadRe matches a SQL read of the media_assets table.
//
// Only read clauses (FROM / JOIN) are matched. Writes are owned by
// percheck_media_assets_writer_canonical, so a write here would be a
// duplicate report of the same fact.
var sqliteMediaReaderReadRe = regexp.MustCompile(
	`(?i)\b(?:FROM|JOIN)\s+media_assets\b`,
)

// sqliteMediaReaderDeleteRe matches the pure-write `DELETE FROM media_assets`
// form, which the read regex would otherwise misclassify as a read (it
// literally contains "FROM media_assets"). Those spans are blanked before the
// read match so the DELETE stays owned solely by the writer gate.
//
// `INSERT INTO ... SELECT ... FROM media_assets` is deliberately NOT excluded:
// that statement genuinely READS the table, so both gates reporting it is
// correct rather than duplicated.
var sqliteMediaReaderDeleteRe = regexp.MustCompile(
	`(?i)\bDELETE\s+FROM\s+media_assets\b`,
)

// sqliteMediaReaderPGPlaceholderRe identifies a PostgreSQL positional bind
// placeholder ($1, $2, …). It is the dialect discriminator: every PostgreSQL
// SQL statement that binds a parameter uses it, while SQLite uses `?`.
//
// DIALECT PRECISION. The historical certificate counter was
// SQLITE_MEDIA_READERS=0 — SQLite readers, not "any reads". A PostgreSQL read
// of media_assets (internal/platform/postgres/media, or any composition-root
// adapter that legitimately queries the media SSOT) is CORRECT by
// construction and must never be reported as debt. Without this
// discrimination the gate would both misstate the P2-9 Phase 2 inventory and
// make a correct new PostgreSQL reader fail the build — a false positive that
// would push contributors to add exemptions instead of reading the SSOT.
var sqliteMediaReaderPGPlaceholderRe = regexp.MustCompile(`\$\d+`)

type sqlMediaSpan struct {
	start    int
	end      int
	postgres bool
}

// sqlMediaSpan is one SQL-bearing Go expression with the dialect its
// placeholders imply.
//
// It is deliberately expression-shaped rather than literal-shaped: a long SQL
// statement is routinely written as adjacent string literals
// (`… FROM media_assets WHERE ` + filter + ` AND …`), so the `$N` may live in
// a different piece than the FROM clause. Spans therefore cover whole
// concatenation expressions, and the widest span containing a match wins.
//
// Fail-closed: a file that does not parse yields no spans, so every read in it
// is reported. A SQL expression with no placeholder at all is classified
// SQLite, which is the conservative direction (a parameterless full scan of
// media_assets is a defect either way).
func sqlMediaSpans(source []byte, positionFile *token.File, parsed *ast.File) []sqlMediaSpan {
	var spans []sqlMediaSpan
	add := func(node ast.Node) {
		start := positionFile.Offset(node.Pos())
		end := positionFile.Offset(node.End())
		if start < 0 || end > len(source) || end <= start {
			return
		}
		spans = append(spans, sqlMediaSpan{
			start:    start,
			end:      end,
			postgres: sqliteMediaReaderPGPlaceholderRe.Match(source[start:end]),
		})
	}
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			// A concatenation expression: covers `a + b + c` as one span so a
			// placeholder in any piece marks the whole statement PostgreSQL.
			if node.Op == token.ADD {
				add(node)
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				add(node)
			}
		}
		return true
	})
	// Widest first: an outer concatenation must win over one of its pieces.
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].end-spans[i].start > spans[j].end-spans[j].start })
	return spans
}

// sqlMediaSpanContaining returns the widest SQL span containing offset.
func sqlMediaSpanContaining(spans []sqlMediaSpan, offset int) (sqlMediaSpan, bool) {
	for _, span := range spans {
		if offset >= span.start && offset < span.end {
			return span, true
		}
	}
	return sqlMediaSpan{}, false
}

// blankSpans replaces the given byte spans with spaces, preserving newlines so
// reported line numbers stay accurate.
func blankSpans(s string, spans [][]int) string {
	if len(spans) == 0 {
		return s
	}
	b := []byte(s)
	for _, span := range spans {
		for i := span[0]; i < span[1] && i < len(b); i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

// sqliteMediaReaderGrandfatheredZones are the path prefixes that ARE the
// legacy SQLite media read plane being retired. Every file beneath them is
// grandfathered by construction:
//
//   - internal/platform/sqlite/ — the operational SQLite state store (the
//     non-media mutation primitives plus the legacy media read facade).
//
// The zone is a prefix and NOT an exact-file list on purpose here (unlike the
// writer gate's exemptions): this package is the legacy read plane itself, and
// its internal file layout is expected to shrink, not to be pinned. A NEW
// package reading media_assets is a violation because it lives outside the
// zone.
//
// ZONE CONVERSION RATCHET. A prefix is the STARTING state of a zone, not its
// terminal architecture: it exempts files nobody has read, so a NEW reader
// dropped into the package inherits the exemption. Each zone is therefore
// converted, one at a time, into an exact-file inventory
// (sqliteMediaReaderInventoriedZoneFiles) as its readers are enumerated; the
// prefix is dropped in the SAME change, so the converted zone stops
// auto-exempting anything.
//
// CONVERTED SO FAR (2026-09-20): internal/platform/qdrant/indexing/ (the Qdrant
// media compatibility seam) and cmd/admin/ (operator tooling that deliberately
// runs against the operational database). Both are deliberately absent from
// this list: a new file under either is now a violation until someone
// enumerates it, which is the whole point of the conversion.
var sqliteMediaReaderGrandfatheredZones = []string{
	"internal/platform/sqlite/",
}

// sqliteMediaReaderGrandfatheredFiles is the explicit DEBT REGISTER: the
// non-zone production files that still read media_assets from SQLite today.
// Each entry is a read split-brain site named in the P2-9 Phase 2 blocker
// list (architecture/catalog.yaml); removing a consumer means deleting its
// entry here in the same change.
//
// This is an exact-file list, not a directory prefix, so a new file dropped
// into one of these packages is NOT auto-exempted.
//
// The register is SQLITE-only. Two entries that once appeared here were
// removed on 2026-09-13 because they are PostgreSQL readers (they bind with
// $N), so listing them misstated the debt and inflated the counter:
// `internal/app/wiring/build_bundles_domain_media.go` (source_version read
// inside the media-commit pg transaction) and
// `internal/app/wiring/vidrush/vidrush_materialization.go` (index_state read
// on the media SSOT). Their absence is load-bearing: if the dialect
// discrimination in sqlMediaSpans regresses, they reappear as violations and
// TestScanSQLiteMediaReaderBan_RepoTreeIsClean fails.
//
// Mixed files (a PostgreSQL path plus a SQLite degrade branch) live in
// sqliteMediaReaderDegradeOnlyFiles instead, so this map stays the list of
// things that are simply WRONG and must be migrated.
//
// ENTRY RETIRED 2026-09-16: internal/capabilities/assets/ingest/adapter_clip.go
// left this register in the same change that replaced its
// `SELECT id FROM media_assets WHERE drive_file_id ...` statement with the
// engine-named ingest.MediaDriveFileIDLister port (resolved from the canonical
// media committer by wiring.mediaDriveFileListerFromCommitter). The SQLite read
// was not merely misplaced — the operational mirror holds no committed media
// rows, so the listing could only ever answer "empty" while PostgreSQL held the
// assets. A nil port now fails the listing closed instead of degrading onto a
// second engine, which is why this file must NOT reappear here: if it does, the
// port was bypassed rather than the dialect discriminating.
//
//   - internal/capabilities/assets/providers/stock/enrichment/handler_repository.go
//     was retired the same way on the same day (SQLiteAssetRepository →
//     PostgresAssetRepository over the media SSOT handle).
//
//   - internal/app/wiring/assets/folders.go was DELETED (not migrated) on
//     2026-09-16: its parent-video folder read moved to
//     pgmedia.MediaFolderResolver over root.MediaPostgres, and its
//     `COALESCE(folder_id, drive_folder_id)` fallback was removed rather than
//     ported (the SSOT has no drive_folder_id column and 0 rows depended on it).
//     A deleted file cannot reappear here — the gate would report an unmapped
//     file if a new SQLite media reader were added to this package.
//
//   - BOTH voiceover entries left on 2026-09-16, and they left for different
//     reasons. internal/app/wiring/voiceover/adapters_voiceover_repo.go was a
//     genuine live migration: its two media_assets reads
//     (findVoiceoverMediaAsset, the cross-run cache location lookup, and
//     CountByDriveFileIDTx, the PR-VO-B3 dedupe gate) now resolve through the
//     capability-owned VoiceoverMediaReader port over the media SSOT
//     (pgmedia.MediaVoiceoverMediaReader).
//     internal/app/wiring/voiceover/adapters_voiceover_projection.go is the more
//     instructive removal: its media_assets read was in
//     VoiceoverPostCommitVerifierAdapter.Verify, which has NO production
//     construction site at all (PostCommitVerifier is never assigned), and the
//     file's other half feeds the finalizer's explicit "Legacy pre-Cutover path"
//     whose projection upsert is a fail-closed stub. Rather than delete a
//     documented optional capability unilaterally, the verifier was made
//     engine-correct-if-wired: it now takes the operational handle for the
//     voiceovers check AND a narrow VoiceoverProjectionChecker
//     (pgmedia.MediaVoiceoverProjectionChecker) for the media half, so the
//     two-engine check can no longer be satisfied by one handle. Both halves of
//     a two-engine verification must be named; that is the reusable lesson.
//
//   - internal/capabilities/ai/autotag/process_by_enrich_candidates.go was
//     retired on 2026-09-16, and it is the entry that proves a read-plane
//     migration can be blocked by a WRITE-plane split: the sweeper selected
//     PENDING rows from SQLite and then claimed them through the enrichment
//     state machine, which was ALSO wired to SQLite (repos.ClipsRepo) while
//     pgmedia's patch path wrote enrich_state on the media SSOT. Migrating the
//     read alone would have left the sweep reading rows it could never claim.
//     Both halves moved together: pgmedia.MediaEnrichStateStore (resolved by
//     wiring.enrichStateStoreFromCommitter) implements the transition port and
//     completes the migration-004 dual-write of enrich_state_updated_at_ts,
//     while pgmedia.MediaEnrichmentCandidateReader answers the scan. The
//     selector's SQLite-only fence encoding (datetime('now', ?) with a relative
//     modifier) was NOT translated literally — see the reader's package note on
//     why an empty stamp must keep comparing as OLDER.
//
//   - internal/capabilities/scripts/usecase/clip_sampler_gates.go was retired
//     on 2026-09-16: its subtitle_ready gate read media_assets.source inside a
//     package-global *sql.DB (usecase.SetSamplerDB, wired from root.DB.DB)
//     while ALSO joining asset_subtitle_artifacts, which exists only on SQLite.
//     The two facts now have separate engine-named ports —
//     SamplerGateDeps.AssetSource (pgmedia.MediaAssetSourceReader over the media
//     SSOT) and SamplerGateDeps.ReadyASSArtifacts (operational, because that
//     table has no PostgreSQL home) — so this file can no longer read
//     media_assets from either engine. The package-global was itself the reason
//     the engine was invisible, which is why it was removed rather than
//     re-typed.
//
//   - internal/capabilities/mediaregistry/index_eligibility_resolver.go was
//     retired the same day with the narrow AssetEligibilityReader port. Its
//     `SELECT ... FROM media_assets WHERE id = ?` ran on whatever handle the
//     caller held, and the single production caller
//     (clipindexer.Service.Eligibility) held the operational SQLite handle while
//     PostgreSQL owned media_assets — so the taxonomy gate graded a database
//     that holds no committed rows. The read now resolves from the media SSOT
//     via wiring composition (SetMediaEligibilityReader ← same mediaPG handle as
//     the canonical reindex requester), and a nil reader fails closed instead
//     of degrading onto a second engine.
//
//   - internal/app/wiring/lifecycle_sweepers.go — THE LAST ENTRY — left on
//     2026-09-16, and it is the entry that explains why this register had a
//     tail at all. runDedupSweep scanned media_assets through the OPERATIONAL
//     handle AND retired the duplicates it found through that same handle
//     (ClipsRepository.DeleteClip → SoftDelete). So it was never a read-only
//     debt: a duplicate pair committed by the canonical writer was invisible to
//     the scan, and the pair the scan DID find was retired on the mirror, so
//     the SSOT copy stayed live and the sweeper counted it again on every
//     30-minute tick. Migrating only the read would have kept that loop and made
//     it quieter. Both halves now resolve from ONE canonical committer
//     (wiring.mediaDuplicateGroupReaderFromCommitter +
//     persistence.CanonicalAssetSoftDeleter), the file no longer mentions
//     media_assets, and runDedupSweep fails closed on a nil reader OR a nil
//     retirer rather than enumerating one engine while mutating another.
//
// THE REGISTER IS NOW EMPTY, AND THAT IS THE POINT. An empty map is the
// terminal state of this ratchet: the reachable P2-9 Phase 2 read debt is zero.
// It must be reached by migrating a site, never by widening
// sqliteMediaReaderDegradeOnlyFiles or sqliteMediaReaderGrandfatheredZones — a
// new SQLite reader of media_assets is a violation until someone either
// migrates it or writes down the degrade-path selector that selects it. Deleting
// a legitimately-degrade-only path's entry back into THIS map would be a
// category error, which is what TestSQLiteMediaReaderRegistersAreDisjoint
// guards.
//
// The map is declared-but-empty rather than removed because the ratchet pins
// (exact-path exemption, staleness, union iteration) address it by name; deleting
// it would delete the pins that keep it empty.
var sqliteMediaReaderGrandfatheredFiles = map[string]bool{}

// sqliteMediaReaderDegradeOnlyFiles is the second, deliberately separate
// register: files whose SQLite media read is only ever selected when the
// PostgreSQL media plane is CLOSED. Each entry names its selector, and the
// selector must short-circuit on the media SSOT handle — a legitimately
// degrade-only path is not the same kind of thing as the production
// split-brain debt above, and conflating them would make the debt list read as
// larger (and less actionable) than it is.
//
// Adding an entry here is how a reviewable degrade path is acknowledged; a NEW
// file reading media_assets still fails the gate until someone does so.
//
// ENTRY RETIRED 2026-09-13: internal/app/wiring/canonical_media_committer.go
// left this register together with the sqliteMediaAssetStore type it was
// pardoning. ComposeRoot.MediaAssetStore() now fails closed when the media SSOT
// is closed, so there is no SQLite reader of media_assets left in that file to
// exempt — and deleting the entry in the same change is exactly what the
// staleness pin (TestSQLiteMediaReaderRegisterHasNoStaleEntries) enforces.
//
// THE REGISTER IS NOW EMPTY TOO. Both entries left on 2026-09-20, each by
// removing the SQLite read rather than reclassifying it:
//
//   - internal/capabilities/assets/artifacts/clips_adapter.go — the Sqlite
//     branch of ClipsRegistry (and the *sql.DB handle it needed) was DELETED;
//     GetAllWithDriveFileID / FindByPHash / FindByContentHash now resolve from
//     the canonical committer's engine and fail closed when it is absent.
//   - internal/capabilities/youtube/adapters/youtube_adapters_store.go — the
//     ListYouTubeClipIDsForSearchText read left the ClipStorePort entirely and
//     is now answered by pgmedia.MediaYouTubeClipLister over the media SSOT,
//     resolved by wiring.mediaYouTubeClipListerFromCommitter.
//
// As with the production register above, emptiness is the terminal state of
// this ratchet: reach it by migrating a site, never by widening the map.
var sqliteMediaReaderDegradeOnlyFiles = map[string]bool{}

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
// This register is the SAME KIND of thing as the two above it (an exact-file
// pardon that must shrink), and the same three pins cover it: every entry must
// still exist, every entry must still have a SQLite-dialect media read (never
// a permanent allowlist), and the entry must be deleted in the same change as
// its consumer is migrated or removed.
//
// CONVERTED ZONES — 2026-09-20. Each prefix was removed from
// sqliteMediaReaderGrandfatheredZones in the same change that enumerated its
// readers, so this inventory is the only thing standing between those packages
// and a clean gate:
//
//   - internal/platform/qdrant/indexing/ — the Qdrant media compatibility seam
//     and its local-catalog payload readers, retired wholesale with the Qdrant
//     media projection; the files below still hold a media read while that
//     demolition lands.
//   - cmd/admin/ — operator tooling that deliberately runs against the
//     operational database. It is inventoried rather than migrated because
//     these commands are the documented operational read plane, not a
//     production split-brain: naming them file by file is what stops a NEW
//     admin command from inheriting the exemption.
var sqliteMediaReaderInventoriedZoneFiles = map[string]bool{
	"cmd/admin/internal/audit/broken_references.go":               true,
	"cmd/admin/internal/audit/clip_drive_audit.go":                true,
	"cmd/admin/internal/audit/matt_damon_assets.go":               true,
	"cmd/admin/internal/audit/repair_stock_metadata.go":           true,
	"cmd/admin/internal/backfill/backfill_asset_embeddings_db.go": true,
	"cmd/admin/internal/backfill/backfill_clip_folder_path.go":    true,
	"cmd/admin/internal/backfill/backfill_embedding_contract.go":  true,
	"cmd/admin/internal/backfill/backfill_media_durations.go":     true,
	"cmd/admin/internal/backfill/backfill_missing.go":             true,
	"cmd/admin/internal/backfill/backfill_provider_timestamps.go": true,
	"cmd/admin/internal/backfill/backfill_source_url_metadata.go": true, "cmd/admin/internal/cleanup/cleanup_drive_orphans.go": true,
	"cmd/admin/internal/drive/drive_reconcile.go": true,

	"cmd/admin/internal/soundeffects/classify_sound_effects.go":       true,
	"cmd/admin/internal/soundeffects/download_sound_effects.go":       true,
	"cmd/admin/internal/soundeffects/organize_sound_effects_drive.go": true,
	"cmd/admin/internal/soundeffects/trim_sound_effects.go":           true,
	"internal/platform/qdrant/indexing/asset_store.go":                true,
	"internal/platform/qdrant/indexing/asset_store_fetch.go":          true, "internal/platform/qdrant/indexing/asset_store_reconcile.go": true,

	"internal/platform/qdrant/indexing/clipindexer/indexing.go":                 true,
	"internal/platform/qdrant/indexing/clipindexer/indexing_api.go":             true,
	"internal/platform/qdrant/indexing/clipindexer/indexing_hash.go":            true,
	"internal/platform/qdrant/indexing/clipindexer/indexing_skip.go":            true,
	"internal/platform/qdrant/indexing/clipindexer/indexing_state.go":           true,
	"internal/platform/qdrant/indexing/clipindexer/indexing_api_persistence.go": true,
}

// sqliteMediaReaderNote is the violation Note string.
const sqliteMediaReaderNote = "forbidden NEW SQLite reader of media_assets (MEDIA-SSOT read-side gate, September 2026): PostgreSQL + pgvector is the sole durable authority for the media domain, so media reads MUST go through internal/platform/postgres/media.MediaSearcher. Reading media_assets from the operational SQLite store reintroduces the Postgres-writer/SQLite-reader split-brain. Route this read through the PostgreSQL media read authority, or add an explicit, justified entry to sqliteMediaReaderGrandfatheredFiles in the same reviewed change. This gate promotes the historical certify-media-cutover counter SQLITE_MEDIA_READERS=0 to enforcement."

// sqliteMediaReaderIsGrandfathered reports whether a repo-relative path is
// exempt: inside a legacy read-plane zone, or an explicitly listed debt
// register entry.
func sqliteMediaReaderIsGrandfathered(relPath string) bool {
	if sqliteMediaReaderGrandfatheredFiles[relPath] || sqliteMediaReaderDegradeOnlyFiles[relPath] || sqliteMediaReaderInventoriedZoneFiles[relPath] {
		return true
	}
	for _, zone := range sqliteMediaReaderGrandfatheredZones {
		if strings.HasPrefix(relPath, zone) {
			return true
		}
	}
	return false
}

// sqliteMediaReaderRegisterEntryIsLive reports whether a debt-register entry
// still has a SQLite-dialect read of media_assets to pardon.
//
// WHY THIS EXISTS. The register is the ratchet that drives the historical
// SQLITE_MEDIA_READERS=0 counter to zero, and a ratchet only works in one
// direction if a stale entry FAILS. An entry that survives after its consumer
// migrated is the failure mode that makes such registers useless: it hides
// real debt behind a permanently-growing allowlist people stop reading. An
// entry is therefore live only when the file still contains at least one read
// of media_assets that is not PostgreSQL-dialect — the same predicate
// inspectSQLiteMediaReaderFile uses to report a violation. Anything else (read
// gone, or every remaining read migrated to $N placeholders) must be deleted
// from the register in the same change.
func sqliteMediaReaderRegisterEntryIsLive(root, relPath string) (bool, error) {
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	source, err := os.ReadFile(absPath)
	if err != nil {
		return false, err
	}
	masked := source
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	var spans []sqlMediaSpan
	if parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
		if positionFile := fileSet.File(parsed.Pos()); positionFile != nil {
			spans = sqlMediaSpans(source, positionFile, parsed)
		}
	}
	maskedStr := string(masked)
	maskedStr = blankSpans(maskedStr, sqliteMediaReaderDeleteRe.FindAllStringIndex(maskedStr, -1))
	for _, match := range sqliteMediaReaderReadRe.FindAllStringIndex(maskedStr, -1) {
		if span, ok := sqlMediaSpanContaining(spans, match[0]); ok && span.postgres {
			continue
		}
		return true, nil
	}
	return false, nil
}

// ScanSQLiteMediaReaderBan walks internal/ and cmd/ and reports every
// non-test Go file that reads media_assets without being a grandfathered
// legacy reader, a canonical PostgreSQL reader, or the scanner itself.
func ScanSQLiteMediaReaderBan(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range sqliteMediaReaderScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if policy.StandardSkipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectSQLiteMediaReaderFile(root, path, r)
			return nil
		})
	}
}

// inspectSQLiteMediaReaderFile scans one production Go file for a
// comment-masked SQL read of media_assets and reports it unless exempt.
func inspectSQLiteMediaReaderFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)

	// The PostgreSQL media SSOT readers are correct by construction.
	if strings.HasPrefix(relPath, "internal/platform/postgres/") {
		return
	}
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || policy.IsTestOnlySupportFile(relPath) {
		return
	}
	if sqliteMediaReaderIsGrandfathered(relPath) {
		return
	}

	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        sqliteMediaReaderRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        sqliteMediaReaderNote + " | cannot open file: " + err.Error(),
		})
		return
	}

	// Comments are masked so a doc comment that merely NAMES the table (this
	// scanner's own prose, architecture notes, pipeline diagrams) is never
	// classified as a read. A file that cannot be parsed is scanned verbatim
	// — fail-closed: a syntax error must not become an exemption.
	masked := source
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	var spans []sqlMediaSpan
	if parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
		if positionFile := fileSet.File(parsed.Pos()); positionFile != nil {
			spans = sqlMediaSpans(source, positionFile, parsed)
		}
	}
	maskedStr := string(masked)
	// Blank the pure-write DELETE form first: it contains "FROM media_assets"
	// but is a write, owned by percheck_media_assets_writer_canonical.
	maskedStr = blankSpans(maskedStr, sqliteMediaReaderDeleteRe.FindAllStringIndex(maskedStr, -1))

	for _, match := range sqliteMediaReaderReadRe.FindAllStringIndex(maskedStr, -1) {
		// Dialect discrimination: a PostgreSQL read of media_assets is the
		// correct read path, not debt. Only SQLite-dialect reads (no $N
		// placeholder) count toward the SQLITE_MEDIA_READERS counter.
		if span, ok := sqlMediaSpanContaining(spans, match[0]); ok && span.postgres {
			continue
		}
		lineNo := 1 + strings.Count(maskedStr[:match[0]], "\n")
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        sqliteMediaReaderRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "forbidden_sql_read_media_assets",
			Note: sqliteMediaReaderNote +
				" | file: " + relPath +
				" | matched: " + strings.TrimSpace(maskedStr[match[0]:match[1]]),
		})
	}
}

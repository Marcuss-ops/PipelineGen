package sqlite

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// legacyRunResourceReports238Checksum is the checksum of the historically
// deployed 238_run_resource_reports.sql.  The migration was renamed without
// changing its SQL; keep the old identity explicit so a fabricated 238 row
// can never be promoted merely because its schema happens to look right.
const legacyRunResourceReports238Checksum = "66be1ded3070b3a2dfe09039e848a70981396cbe0407cdb90914003670b7a8f4"

// reconcileHistoricalMigrationIdentities repairs renumberings that happened
// after a migration was deployed.  A migration is identified by its durable
// schema effect as well as its filename; once an old identity is explicitly
// mapped here, the ledger can be upgraded atomically without re-running SQL.
//
// This is intentionally narrow.  It does not accept arbitrary filenames or
// checksums: the expected old identity, target DB, canonical file and live
// schema must all match.  This keeps the normal checksum fail-closed rule in
// place for every other migration.
func reconcileHistoricalMigrationIdentities(db queryable, migrations []migrationFile, targetDB string, log *zap.Logger) error {
	if targetDB != "observability" {
		return nil
	}
	var canonical migrationFile
	for _, m := range migrations {
		if m.version == 239 && m.filename == "239_run_resource_reports.sql" {
			canonical = m
			break
		}
	}
	if canonical.filename == "" || !migrationAppliesToTargetDB(canonical.scope, targetDB) {
		return nil
	}

	rows, err := db.Query(`SELECT version, filename, checksum FROM schema_migrations WHERE version = 238`)
	if err != nil {
		return fmt.Errorf("read legacy migration 238: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read legacy migration 238 rows: %w", err)
		}
		return nil
	}
	var oldFilename, oldChecksum string
	var oldVersion int
	if err := rows.Scan(&oldVersion, &oldFilename, &oldChecksum); err != nil {
		return fmt.Errorf("scan legacy migration 238: %w", err)
	}
	// RunMigrationsOnDB limits the pool to one connection. Release the ledger
	// cursor before issuing the following queries or the pool waits forever.
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy migration 238 rows: %w", err)
	}
	if oldVersion != 238 || oldFilename != "238_run_resource_reports.sql" {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(oldChecksum), legacyRunResourceReports238Checksum) {
		return fmt.Errorf("legacy migration 238 checksum is not authorized: got %q", oldChecksum)
	}

	canonicalRows, err := db.Query(`SELECT COUNT(*) FROM schema_migrations WHERE version = 239`)
	if err != nil {
		return fmt.Errorf("check canonical migration 239: %w", err)
	}
	defer func() { _ = canonicalRows.Close() }()
	var canonicalExists int
	if !canonicalRows.Next() || canonicalRows.Scan(&canonicalExists) != nil {
		return fmt.Errorf("scan canonical migration 239 presence")
	}
	if err := canonicalRows.Close(); err != nil {
		return fmt.Errorf("close canonical migration 239 rows: %w", err)
	}
	if canonicalExists != 0 {
		return fmt.Errorf("legacy migration 238 is present while canonical migration 239 is also present")
	}
	tableRows, err := db.Query(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='run_resource_reports'`)
	if err != nil {
		return fmt.Errorf("check run_resource_reports schema: %w", err)
	}
	defer func() { _ = tableRows.Close() }()
	var tableExists int
	if !tableRows.Next() || tableRows.Scan(&tableExists) != nil {
		return fmt.Errorf("scan run_resource_reports schema presence")
	}
	if err := tableRows.Close(); err != nil {
		return fmt.Errorf("close run_resource_reports schema rows: %w", err)
	}
	if tableExists != 1 {
		return fmt.Errorf("legacy migration 238 is recorded but run_resource_reports is absent")
	}

	content, err := os.ReadFile(canonical.path)
	if err != nil {
		return fmt.Errorf("read canonical migration 239: %w", err)
	}
	checksum := sha256Hex(content)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration identity reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE schema_migrations SET version = 239, migration_id = 239, filename = ?, checksum = ?, checksum_sha256 = ? WHERE version = 238 AND filename = ?`, canonical.filename, checksum, checksum, oldFilename); err != nil {
		return fmt.Errorf("move migration ledger 238→239: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration identity reconciliation: %w", err)
	}
	if log != nil {
		log.Warn("reconciled historical migration identity 238→239",
			zap.String("old_filename", oldFilename),
			zap.String("new_filename", canonical.filename),
			zap.String("legacy_checksum", oldChecksum),
			zap.String("new_checksum", checksum),
		)
	}
	return nil
}

// legacyCachePlaneRepair299Checksum is the sha256 of 299_cache_plane_repair.sql
// as it was first applied (2026-11-18 17:18Z). Commit 86836ac6d later edited
// that file in place, so live cache databases kept the pre-edit hash and the
// fail-closed checksum guard refused to boot.
const legacyCachePlaneRepair299Checksum = "5cb2d317b080b5b12594c4f1ad930d79bddace5682bb1ae965c0d678b933db3d"

// canonicalCachePlaneRepair299Checksum is the sha256 of the current
// 299_cache_plane_repair.sql. Pinned here so a third in-place edit cannot be
// absorbed silently: it needs a new authorized constant and a new decision.
const canonicalCachePlaneRepair299Checksum = "f001a96369db1e435f4aa711e3a39d3efede22f7a3de2297857149cc8dea7c0a"

// reconcileCachePlaneRepair299Checksum repairs the cache-plane ledger row 299
// after its file was edited in place. A forward SQL migration cannot do this:
// the checksum guard runs BEFORE any pending migration is applied, so a
// database carrying the legacy hash never reaches the repair. The repair is
// therefore an explicit, narrow identity reconciliation executed before the
// guard, mirroring reconcileHistoricalMigrationIdentities for 238→239.
//
// Narrowness rules: only the cache database, only version 299 with the exact
// canonical filename, only when the recorded hash is the authorized legacy
// one, only when the current file hashes to the authorized canonical value,
// and only when the durable schema effect (vidrush_provider_cache) is present
// — so a fabricated ledger row cannot be promoted by a schema that merely
// looks right.
func reconcileCachePlaneRepair299Checksum(db queryable, migrations []migrationFile, targetDB string, log *zap.Logger) error {
	if targetDB != "cache" {
		return nil
	}
	var canonical migrationFile
	for _, m := range migrations {
		if m.version == 299 && m.filename == "299_cache_plane_repair.sql" {
			canonical = m
			break
		}
	}
	if canonical.filename == "" || !migrationAppliesToTargetDB(canonical.scope, targetDB) {
		return nil
	}

	rows, err := db.Query(`SELECT filename, checksum FROM schema_migrations WHERE version = 299`)
	if err != nil {
		return fmt.Errorf("read legacy migration 299: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read legacy migration 299 rows: %w", err)
		}
		return nil
	}
	var oldFilename, oldChecksum string
	if err := rows.Scan(&oldFilename, &oldChecksum); err != nil {
		return fmt.Errorf("scan legacy migration 299: %w", err)
	}
	// RunMigrationsOnDB limits the pool to one connection. Release the ledger
	// cursor before issuing the following queries or the pool waits forever.
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy migration 299 rows: %w", err)
	}
	if oldFilename != canonical.filename {
		return fmt.Errorf("migration 299 identity changed: ledger has %q, repository has %q", oldFilename, canonical.filename)
	}
	if strings.EqualFold(strings.TrimSpace(oldChecksum), canonicalCachePlaneRepair299Checksum) {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(oldChecksum), legacyCachePlaneRepair299Checksum) {
		return fmt.Errorf("legacy migration 299 checksum is not authorized: got %q", oldChecksum)
	}

	content, err := os.ReadFile(canonical.path)
	if err != nil {
		return fmt.Errorf("read canonical migration 299: %w", err)
	}
	checksum := sha256Hex(content)
	if !strings.EqualFold(checksum, canonicalCachePlaneRepair299Checksum) {
		return fmt.Errorf("migration 299 drifted again: file hashes to %q, authorized %q", checksum, canonicalCachePlaneRepair299Checksum)
	}

	tableRows, err := db.Query(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='vidrush_provider_cache'`)
	if err != nil {
		return fmt.Errorf("check cache plane schema: %w", err)
	}
	defer func() { _ = tableRows.Close() }()
	var tableExists int
	if !tableRows.Next() || tableRows.Scan(&tableExists) != nil {
		return fmt.Errorf("scan cache plane schema presence")
	}
	if err := tableRows.Close(); err != nil {
		return fmt.Errorf("close cache plane schema rows: %w", err)
	}
	if tableExists != 1 {
		return fmt.Errorf("migration 299 is recorded but its cache plane (vidrush_provider_cache) is absent")
	}

	if _, err := db.Exec(`UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = 299 AND filename = ?`, checksum, checksum, canonical.filename); err != nil {
		return fmt.Errorf("realign migration 299 ledger: %w", err)
	}
	if log != nil {
		log.Warn("reconciled cache plane migration 299 checksum",
			zap.String("filename", canonical.filename),
			zap.String("legacy_checksum", oldChecksum),
			zap.String("canonical_checksum", checksum),
		)
	}
	return nil
}

// isLegacyControlPlaneMetaSchema recognizes the schema produced by the one
// deployed pre-singleton variant of migration 198. It is intentionally
// narrow: the checksum shim must never turn an arbitrary schema drift into a
// successful migration run.
func isLegacyControlPlaneMetaSchema(db queryable) bool {
	rows, err := db.Query("PRAGMA table_info(control_plane_meta)")
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()

	want := map[string]bool{
		"database_id":       false,
		"schema_family":     false,
		"instance_role":     false,
		"canonical_version": false,
		"created_at":        false,
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		if _, ok := want[name]; !ok || name == "singleton_id" {
			return false
		}
		want[name] = true
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, present := range want {
		if !present {
			return false
		}
	}
	countRows, err := db.Query("SELECT COUNT(*) FROM control_plane_meta")
	if err != nil {
		return false
	}
	defer func() { _ = countRows.Close() }()
	if !countRows.Next() {
		return false
	}
	var count int
	if err := countRows.Scan(&count); err != nil {
		return false
	}
	return count == 1
}

func hasMediaTaxonomyColumns(db queryable) bool {
	rows, err := db.Query("PRAGMA table_info(media_assets)")
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	want := map[string]bool{"namespace": false, "asset_kind": false, "source_type": false}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, present := range want {
		if !present {
			return false
		}
	}
	return true
}

// legacyCommentChecksums maps a migration version to the historical checksum of
// a deployment whose file changed only in stale package-path comments. SQL and
// schema effects are byte-for-byte identical, so the ledger is reconciled. A
// hash outside this table still fails closed.
var legacyCommentChecksums = map[int]string{
	37:  "9162ff7a29771c01fa0b3467960b94207f3f6bd3330e0a81bce1dcbfd14ec59f",
	68:  "e9eaf9a2118f658b0c47102b8d8b0bb256bf27b48ba7df8072dc3cc398e6b537",
	89:  "34a6f63829f13ba816b39e27c4642b7ba6bba28505a4145fab929dbd1c03b0a8",
	90:  "7fc081ead4bc3ef108174ae0e585f4ddb334dcf903e6c42b8f60258295acff48",
	91:  "b2ae5a7681fd614243542901d253fda875033869071fd5dcdcbcd8e6f6b04a91",
	92:  "0f91943040205c0ec74ddfea633dc54c7ecce13c1d34508398bc15df32399d01",
	94:  "81a59ffe3bf1c981e30248149c8549a358f19a231d9cec2ead01797a085ddc89",
	95:  "1886cb3e4208fe39eb6ed9d2379dac758cf5e6648be7a12014401e1eaa862359",
	96:  "addeefa96ae3ba1a67cfee75c89f519a2508b6aa28a57399b66d3492b6810433",
	97:  "2f4d9a40129d1dfcd74b039a9b79a2eb1a4b98d4545bf306d6d281e2356633a6",
	98:  "d8aafb61b2928fb6955e300020a53530f9aee11adc4aa1edac88b32c292d01a7",
	102: "41baff1a82b1ee03b6a3aeff43495957c87a6591f9d3a3c9fed045d825496751",
	105: "028713f76b05017b46d7cd0f4f520469daf1276100185145b2000ee979e57aee",
	108: "63cb5dffd6a1ab755646f7eac6c463e3f71f25b99ca733c58a89313e4b90ea23",
	120: "1682c4c47796d18921696c93df1adf561ff75eb78c5d6ffb59137440789b06f2",
	123: "14c0e244e5972568c448d852d08bb04cfd96b2fe9f42c70d441986b640420334",
	129: "01af20ab1757ffb8a704c7a488593421e18feb6144df54fdbd338b68646bd8d4",
	137: "0def3c2a2309e2bc0e8c29e9ff8ef31af3c1f51efa3b85612790b23be11c0bb5",
	148: "ae4c43979c67ca21d01cf78fe2955627add216181106015961992c74bc445343",
	151: "46ce7b091341338e85bdefd01543b4f737231b7adabeb66f23dab445d43e50e0",
	154: "75a625c49558e7f0dcd00703598f323942df1787efcbeb3a54ed73729f79392e",
	155: "088b151a5370f4b522f109472956dd6d790b4c70207ff3fc376acf4cf8f0fe81",
	158: "042ffb06cbba8324b6e07de3ab5a900e02d2d91e1da934ce21c1b2b31d9e0e47",
	159: "940568ccd17e194b16b53a7f2bb5772424ad34fb9e957c2e0f9548ff09a27a63",
	160: "2a666620d1c5c5bd54c0986f7eb3c910fcc12442a4f758c58c9f542f42b70ea9",
	162: "587b31be29358b8f72a9ebdf2775bcd14ea4f3798e6816ce22819aeefe1d017a",
	163: "5fc93683400092a2b7546d79bbb7733b2bcbca4313fd0da0d4362cc57b6d5692",
	169: "2fa6489ebcae70eff4073a2caab7f0cee78796a850f0d47a584b66d4c6d953ab",
	170: "0a4da5918aab3f68f744a19d0bdc2d419c7042e7cf4b47da188b7d59a16c178f",
	230: "0a471101a2bfc372236ca551d87f95200801da816551c9caaf2289ec3900d4ea",
	268: "6c4ee47536ce159249cfed000c8de253dcee760d071f529cdce5b89186f582bf",
}

// legacyRemovedChecksums maps a migration version to the checksum of a file that
// was removed from the repository after being applied but whose ledger row is
// still valid for a primary database.
var legacyRemovedChecksums = map[int]string{
	191: "441f6502693e3ffceb4bf85a18d22559b937624861c5db6daa6cc59233f91d5b",
	192: "90a5ffc831f380fa4e0763835b5699d0049db5192f84125c3b15bac8f77d6d66",
	193: "e5f9ea1d18ca6542346cf6abe0e07d145b992d7b95e50a05affb1b4f44e43358",
}

// reconcileChecksumDrift dispatches the historical cases where an
// already-applied migration legitimately differs from its file. Every shim is
// gated on the exact deployed identity (and, where relevant, the live schema)
// and returns handled=false for anything else, so an arbitrary edit still fails
// closed in the caller. Order matters only for readability: the cases are
// mutually exclusive by version.
func reconcileChecksumDrift(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, content []byte, targetDB string) (bool, error) {
	shims := []func(queryable, *zap.Logger, migrationFile, appliedRecord, string, []byte, string) (bool, error){
		shimMig198LegacyControlPlaneMeta,
		shimMig201MissingTaxonomyFile,
		shimMig195MediaTaxonomy,
		shimMig186OutboxPriority,
		shimMig109ScopeFlagHeader,
		shimLegacyCommentOnly,
		shimRemovedLedgerEntry,
	}
	for _, shim := range shims {
		handled, err := shim(db, log, m, prev, checksum, content, targetDB)
		if err != nil || handled {
			return handled, err
		}
	}
	return false, nil
}

// rewriteMigrationChecksum is the single write path for every shim: it rewrites
// only checksum/checksum_sha256 (never the version) and logs at WARN, because
// operators must not have to descend into INFO to discover that the audit ledger
// was rewritten.
func rewriteMigrationChecksum(db queryable, log *zap.Logger, version int, filename, checksum, message string) error {
	if _, err := db.Exec("UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = ?", checksum, checksum, version); err != nil {
		return fmt.Errorf("storage: shim update %03d checksum: %w", version, err)
	}
	if log != nil {
		log.Warn(message, zap.Int("version", version), zap.String("filename", filename), zap.String("new_checksum", checksum))
	}
	return nil
}

// shimMig198LegacyControlPlaneMeta repairs a deployment of 198 that used the
// equivalent database_id-primary-key form of control_plane_meta. The canonical
// file now carries the stronger singleton form, so only that known hash is
// accepted, and only when the live schema proves the compatible shape.
func shimMig198LegacyControlPlaneMeta(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, _ []byte, targetDB string) (bool, error) {
	if m.version != 198 || targetDB != "primary" || prev.filename != m.filename ||
		prev.checksum != "c5b770ce3c9fca18234ac8953e143535ec7da8b7740f186f7ce9edb0c518843b" ||
		!isLegacyControlPlaneMetaSchema(db) {
		return false, nil
	}
	err := rewriteMigrationChecksum(db, log, 198, m.filename, checksum,
		"reconciled migration 198 ledger for compatible deployed control-plane schema")
	return err == nil, err
}

// shimMig201MissingTaxonomyFile accepts the ledger row of a 201 deployment whose
// file was not retained in the repository — but only with the exact deployed
// hash and only once the live taxonomy columns are present.
func shimMig201MissingTaxonomyFile(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, _ []byte, targetDB string) (bool, error) {
	if m.version != 201 || targetDB != "primary" || prev.filename != m.filename ||
		prev.checksum != "1f1d9052bd130dd810215705923088e79ccd8748f1f7ec46ff47419a1f75c019" ||
		!hasMediaTaxonomyColumns(db) {
		return false, nil
	}
	err := rewriteMigrationChecksum(db, log, 201, m.filename, checksum,
		"reconciled missing migration 201 ledger for deployed media taxonomy")
	return err == nil, err
}

// shimMig195MediaTaxonomy is the 195 counterpart of the 201 shim: same schema
// proof, different deployed hash and version.
func shimMig195MediaTaxonomy(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, _ []byte, targetDB string) (bool, error) {
	if m.version != 195 || targetDB != "primary" || prev.filename != m.filename ||
		prev.checksum != "a341d66a5411f4e8db9c66b75dc57dce44275972db98684ab4602497890b6b6a" ||
		!hasMediaTaxonomyColumns(db) {
		return false, nil
	}
	err := rewriteMigrationChecksum(db, log, 195, m.filename, checksum,
		"reconciled migration 195 ledger for deployed media taxonomy")
	return err == nil, err
}

// shimMig186OutboxPriority accepts the untracked deployment copy of the local
// 186 outbox-priority migration: the SQL is unchanged in effect, only the
// checksum of the deployed copy differs.
func shimMig186OutboxPriority(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, _ []byte, targetDB string) (bool, error) {
	if m.version != 186 || targetDB != "primary" || prev.filename != m.filename ||
		prev.checksum != "06d40a3bee8655737dacdc758aeb4bf3fe36b5731da74e5f804f39a78b43e807" ||
		checksum != "aa56b176ff008dde206b6da3946859712fbf76fb24bb40d3e42a930a8f950d40" {
		return false, nil
	}
	err := rewriteMigrationChecksum(db, log, 186, m.filename, checksum,
		"applied one-time checksum reconciliation for migration 186")
	return err == nil, err
}

// shimMig109ScopeFlagHeader reconciles the legitimate edit that prepended the
// `-- database: primary` header to 109. The shim fires ONLY when the file
// carries the magic marker: without it an unexpected modify of 109 surfaces as
// a hard error, so the never-modify-an-applied-migration invariant holds for an
// attacker or a careless edit.
func shimMig109ScopeFlagHeader(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, content []byte, targetDB string) (bool, error) {
	if m.version != 109 || targetDB != "primary" || prev.filename != m.filename {
		return false, nil
	}
	const shimMarker = "-- TODO-8-SCOPE-FLAG-RECONCILE-109"
	if !strings.Contains(string(content), shimMarker) {
		return false, fmt.Errorf(
			"storage: migration %03d (%s) checksum mismatch — "+
				"missing shim marker %q in file. The shim "+
				"only fires when the marker is present, so an "+
				"unexpected modify of 109 surfaces as a hard error "+
				"instead of silently rewriting the ledger. To "+
				"honour the legitimate scope-aware modification "+
				"add the marker above the existing content and re-run",
			m.version, m.filename, shimMarker,
		)
	}
	err := rewriteMigrationChecksum(db, log, 109, m.filename, checksum,
		"applied one-time checksum shim for migration 109 (-- database: primary header added; ledger rewritten with new SHA-256)")
	return err == nil, err
}

// shimLegacyCommentOnly reconciles migrations whose file changed only in stale
// package-path comments after deployment: the SQL/schema effects are
// byte-for-byte identical, and only the exact historical hashes are accepted.
func shimLegacyCommentOnly(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, _ []byte, targetDB string) (bool, error) {
	expected, ok := legacyCommentChecksums[m.version]
	if !ok || (targetDB != "primary" && targetDB != "observability") ||
		prev.filename != m.filename || prev.checksum != expected {
		return false, nil
	}
	err := rewriteMigrationChecksum(db, log, m.version, m.filename, checksum,
		"reconciled historical comment-only migration checksum")
	return err == nil, err
}

// shimRemovedLedgerEntry reconciles a primary-DB ledger row for a migration file
// that was removed from the repository after it was applied.
func shimRemovedLedgerEntry(db queryable, log *zap.Logger, m migrationFile, prev appliedRecord, checksum string, _ []byte, targetDB string) (bool, error) {
	expected, ok := legacyRemovedChecksums[m.version]
	if !ok || targetDB != "primary" || prev.filename != m.filename || prev.checksum != expected {
		return false, nil
	}
	err := rewriteMigrationChecksum(db, log, m.version, m.filename, checksum,
		"reconciled removed historical migration ledger entry")
	return err == nil, err
}

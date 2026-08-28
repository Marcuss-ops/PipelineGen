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
	defer rows.Close()
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
	defer canonicalRows.Close()
	var canonicalExists int
	if !canonicalRows.Next() || canonicalRows.Scan(&canonicalExists) != nil {
		return fmt.Errorf("scan canonical migration 239 presence")
	}
	if canonicalExists != 0 {
		return fmt.Errorf("legacy migration 238 is present while canonical migration 239 is also present")
	}
	tableRows, err := db.Query(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='run_resource_reports'`)
	if err != nil {
		return fmt.Errorf("check run_resource_reports schema: %w", err)
	}
	defer tableRows.Close()
	var tableExists int
	if !tableRows.Next() || tableRows.Scan(&tableExists) != nil {
		return fmt.Errorf("scan run_resource_reports schema presence")
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
	defer tx.Rollback()
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

// isLegacyControlPlaneMetaSchema recognizes the schema produced by the one
// deployed pre-singleton variant of migration 198. It is intentionally
// narrow: the checksum shim must never turn an arbitrary schema drift into a
// successful migration run.
func isLegacyControlPlaneMetaSchema(db queryable) bool {
	rows, err := db.Query("PRAGMA table_info(control_plane_meta)")
	if err != nil {
		return false
	}
	defer rows.Close()

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
	defer countRows.Close()
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
	defer rows.Close()
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

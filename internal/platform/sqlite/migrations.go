package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// schemaMigrationsTable is the DDL for the migration tracking table.
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version          INTEGER PRIMARY KEY,
    migration_id     INTEGER NOT NULL UNIQUE,
    filename         TEXT    NOT NULL,
    checksum         TEXT    NOT NULL,
    checksum_sha256  TEXT    NOT NULL,
    applied_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    app_git_sha      TEXT    NOT NULL DEFAULT 'unknown'
)`

// currentAppGitSHA returns the build identity captured with each newly
// applied migration. Local tests and development binaries use an explicit
// stable fallback rather than an empty audit value.
func currentAppGitSHA() string {
	if value := strings.TrimSpace(os.Getenv("PIPELINEGEN_GIT_SHA")); value != "" {
		return value
	}
	return "unknown"
}

// migrationFile represents a single SQL migration file in the canonical
// migrations dir. Scope is the parsed target-DB list from the file's
// `-- database:` header directive (default: "all" if absent).
//
// Scope-aware migrations: the runner reads each
// file's first non-blank SQL comment line for the optional header
// `-- database: primary|observability|cache|jobs|all` (comma-separated, default
// "all"). When the runner's targetDB does not match a migration's scope,
// the migration is skipped before the checksum check — see
// parseMigrationScope + migrationAppliesToTargetDB in
// migrations_discovery.go.
type migrationFile struct {
	version  int
	filename string
	path     string
	scope    string // default "all"; populated by parseMigrationScope
	// baselineCovers is the optional `-- baseline-covers: <N>` directive
	// carried by the consolidated baseline. It records the highest numbered
	// migration whose schema effects the dump already contains, which can be
	// later than the version in the filename (e.g. 000_baseline_268.sql was
	// regenerated after 298, so it covers 1..298). Zero means "use the
	// filename threshold".
	baselineCovers int
}

// RunMigrations scans targetDir for .sql files, compares their versions
// against the schema_migrations ledger, and applies any pending migrations
// in version order. Each migration file is applied inside its own
// transaction. Applied migrations are recorded with their version, filename,
// and SHA-256 checksum.
//
// Migrations are never modified or renamed after being applied — the
// runner rejects files whose version is already in the ledger but whose
// checksum differs from the recorded one.
//
// Scope-aware migrations: targetDB names the
// canonical DB the receiver represents ("primary", "observability", or
// "cache", or "jobs" in the canonical DatabaseSet; "all" for tests / fixtures that don't
// care). Migrations whose `-- database:` header directive excludes
// targetDB are skipped before the checksum check, so a primary-only
// migration (e.g. 109) is never attempted on the observability DB.
func (s *SQLiteDB) RunMigrations(log *zap.Logger, targetDir, targetDB string) error {
	if log == nil {
		log = s.log
		if log == nil {
			log = zap.NewNop()
		}
	}
	return migrateAll(s.DB, log, targetDir, targetDB)
}

// RunMigrationsOnDB is a convenience wrapper that opens the database at
// dbPath, runs migrations, and closes the database. Useful for one-shot
// scripts (e.g. seed_fixture). See RunMigrations for the targetDB
// parameter's meaning.
//
// Scope-aware migrations (June 2026): targetDB selects the canonical DB.
func RunMigrationsOnDB(dbPath string, log *zap.Logger, targetDir, targetDB string) error {
	if log == nil {
		log = zap.NewNop()
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	// Single connection, like NewSQLiteDB: the `-- foreign_keys: off` migration
	// directive toggles a per-connection PRAGMA around one file's transaction,
	// which is only sound when Exec and Begin share the connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return migrateAll(db, log, targetDir, targetDB)
}

// migrateAll is the shared migration logic used by both RunMigrations
// and RunMigrationsOnDB. See RunMigrations for the targetDB parameter's
// meaning.
//
// Scope-aware migrations: the loop below skips
// migrations whose `-- database:` header excludes targetDB BEFORE the
// checksum check, so the ledger never gains an entry for an out-of-scope
// file. Migration 109 carries a one-time checksum shim — see below —
// so existing primary DBs that pre-date the `-- database: primary`
// header preserve their applied status.
//
// The migrateAll body is deliberately scoped to orchestration: the
// ledger bootstrap, discovery, status parsing and per-file apply logic
// each live in a sibling migrations_*.go file (migrations_ledger.go,
// migrations_discovery.go, migrations_splitter.go, migrations_apply.go,
// migrations_reconcile.go, migrations_status.go). The historical
// checksum-reconciliation shims are likewise split out.
func migrateAll(db queryable, log *zap.Logger, targetDir, targetDB string) error {
	state, migrations, err := prepareMigrationRun(db, log, targetDir, targetDB)
	if err != nil {
		return err
	}

	appliedCount := 0
	for _, m := range migrations {
		if skipMigrationForLedgerState(db, log, m, state, migrations, targetDB) {
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			return fmt.Errorf("storage: read %s: %w", m.filename, err)
		}
		checksum := sha256Hex(content)

		if prev, ok := state.applied[m.version]; ok {
			if prev.filename == m.filename && prev.checksum == checksum { // Already applied with matching checksum — skip
				continue
			}
			handled, err := reconcileChecksumDrift(db, log, m, prev, checksum, content, targetDB)
			if err != nil {
				return err
			}
			if handled {
				continue
			}
			return fmt.Errorf(
				"storage: migration %03d (%s) identity/checksum mismatch — "+
					"applied=%s/%s current=%s/%s. Migrations must never be modified or renamed after being applied",
				m.version, m.filename, prev.filename, prev.checksum, m.filename, checksum,
			)
		}

		// Apply migration inside its own transaction
		log.Info("applying migration", zap.Int("version", m.version), zap.String("filename", m.filename))

		if err := applyMigration(db, log, m.version, m.filename, checksum, content); err != nil {
			return fmt.Errorf("storage: apply %s: %w", m.filename, err)
		}
		appliedCount++
		if isBaselineMigrationFile(m) {
			state.baselineAppliedNow = true
		}
	}

	// Declared-vs-live verification: the ledger proves a file ran, not that its
	// schema survived. A table that an APPLIED migration declares but the
	// database does not contain is drift (an operator restore, a rebuilt file,
	// a dropped table) and the only correct repair is a NEW numbered migration —
	// editing the applied file can never work, because the ledger skips it.
	// Reported at error level and deliberately non-fatal: a drifted production
	// database must still boot so the forward migration can be applied.
	if err := verifyDeclaredTables(db, targetDir, targetDB, log); err != nil {
		log.Warn("migration schema verification could not run", zap.String("target_db", targetDB), zap.Error(err))
	}

	log.Info("migrations complete",
		zap.Int("total", len(migrations)),
		zap.Int("newly_applied", appliedCount),
		zap.Int("already_applied", len(state.applied)),
	)
	return nil
}

// migrationRunState is the ledger snapshot the apply loop reads and the two
// flags it mutates. Keeping it in one value makes the loop's inputs explicit:
// the loop itself no longer recomputes the baseline threshold or re-queries the
// ledger.
type migrationRunState struct {
	applied            map[int]appliedRecord
	baselineThreshold  int
	baselineAppliedNow bool
}

// prepareMigrationRun bootstraps and expands the ledger, discovers and
// validates the migration files, reconciles historical identities, and reloads
// the ledger once reconciliation may have moved a row. It returns the state the
// apply loop needs plus the ordered file list.
func prepareMigrationRun(db queryable, log *zap.Logger, targetDir, targetDB string) (*migrationRunState, []migrationFile, error) {
	// Bootstrap and expand the ledger before reading migration files. Existing
	// databases may have the legacy four-column shape; expansion backfills the
	// canonical identity/checksum fields without dropping migration history.
	if err := ensureMigrationLedger(db); err != nil {
		return nil, nil, fmt.Errorf("storage: ensure schema_migrations: %w", err)
	}

	// Discover migration files
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return nil, nil, err
	}

	// Validate: no duplicate versions
	if err := validateNoDuplicateVersions(migrations, log); err != nil {
		return nil, nil, err
	}

	// Validate: log gaps as warnings (not errors) since real migration dirs
	// may have gaps from historical renumbering / removed migrations.
	// Gaps are informational; the runner proceeds normally.
	warnOnGaps(migrations, log)

	// Load already-applied migrations for checksum validation
	if _, err := loadAppliedMigrations(db); err != nil {
		return nil, nil, fmt.Errorf("storage: load applied migrations: %w", err)
	}
	if err := reconcileHistoricalMigrationIdentities(db, migrations, targetDB, log); err != nil {
		return nil, nil, fmt.Errorf("storage: reconcile historical migration identities: %w", err)
	}
	if err := reconcileCachePlaneRepair299Checksum(db, migrations, targetDB, log); err != nil {
		return nil, nil, fmt.Errorf("storage: reconcile cache plane migration 299: %w", err)
	}
	// The reconciliation may have moved a ledger row to its canonical
	// version, so load the ledger after it before validating gaps and
	// out-of-scope entries.
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return nil, nil, fmt.Errorf("storage: reload applied migrations after reconciliation: %w", err)
	}
	if err := validateAppliedMigrationSet(db, applied, migrations, targetDB); err != nil {
		return nil, nil, err
	}

	// Baseline bookkeeping: the consolidated baseline sentinel (version 0)
	// satisfies the frozen window 1..N. Discover its threshold so the
	// runner can skip the historical incrementals on fresh databases
	// and skip the baseline itself on old databases that already carry
	// the full 1..N ledger rows. The applied snapshot is captured once;
	// a mutable flag tracks whether the baseline was already present or
	// was just applied in this run so subsequent 1..N files are skipped
	// on fresh bootstraps without needing to re-query the ledger.
	baselineThreshold := baselineThresholdForMigrations(migrations)
	// A database bootstrapped from an older consolidated baseline (e.g. the
	// shipped 000_baseline_267.sql) must keep applying every migration the
	// OLD dump did not contain. The sentinel row names the file that was
	// actually applied, so its window wins over the current file's coverage;
	// otherwise replacing 267 with a 268 dump that covers 1..298 would mark
	// 268..298 as covered on a 267 database and silently strand its schema
	// at 267 while the ledger says 298.
	if record, ok := applied[0]; ok {
		if appliedThreshold := baselineThresholdForAppliedRecord(record, migrations); appliedThreshold > 0 {
			baselineThreshold = appliedThreshold
		}
	}

	return &migrationRunState{
		applied:            applied,
		baselineThreshold:  baselineThreshold,
		baselineAppliedNow: baselineApplied(applied),
	}, migrations, nil
}

// skipMigrationForLedgerState reports whether the runner must skip m before
// reading it: out-of-scope files, baseline/sentinel bookkeeping and the
// execution-plane cutover. Skipping a baseline because an existing history
// already satisfies the frozen window flips baselineAppliedNow, exactly like
// applying the baseline does — the two are the only places that flag moves.
func skipMigrationForLedgerState(db queryable, log *zap.Logger, m migrationFile, state *migrationRunState, migrations []migrationFile, targetDB string) bool {
	if !isBaselineMigrationFile(m) {
		// Skip out-of-scope migrations BEFORE the
		// checksum check. A primary-only migration must NEVER land on
		// the observability ledger, regardless of whether its checksum
		// matches. The skip is silent at INFO level (operators
		// running with -v can grep the log for confirmation).
		if !migrationAppliesToTargetDB(m.scope, targetDB) {
			if log != nil {
				log.Debug("skipping migration (out of DB scope)",
					zap.Int("version", m.version),
					zap.String("filename", m.filename),
					zap.String("scope", m.scope),
					zap.String("target_db", targetDB),
				)
			}
			return true
		}
		// Historical incrementals satisfied by baseline sentinel (either
		// pre-existing from a prior run or just applied in this run).
		if state.baselineThreshold > 0 && m.version > 0 && m.version <= state.baselineThreshold && state.baselineAppliedNow {
			if log != nil {
				log.Debug("skipping historical migration (covered by baseline)", zap.Int("version", m.version), zap.String("filename", m.filename))
			}
			return true
		}
		if skipMigrationAfterExecutionCutover(db, state.applied, m, targetDB) {
			if log != nil {
				log.Warn("skipping historical migration after completed execution-plane cutover", zap.Int("version", m.version), zap.String("filename", m.filename))
			}
			return true
		}
		return false
	}

	// Scope gate still applies to the baseline itself.
	if !migrationAppliesToTargetDB(m.scope, targetDB) {
		if log != nil {
			log.Debug("skipping baseline (out of DB scope)",
				zap.Int("version", m.version),
				zap.String("filename", m.filename),
				zap.String("scope", m.scope),
				zap.String("target_db", targetDB),
			)
		}
		return true
	}
	if state.baselineAppliedNow {
		if log != nil {
			log.Debug("skipping baseline (already applied)", zap.String("filename", m.filename))
		}
		return true
	}
	// Old or partially-migrated DB: any non-baseline history means the
	// incremental path was already chosen — skip the consolidated baseline
	// (BASELINE_PLAN.md §2.2).
	if hasAnyHistory(state.applied) {
		if log != nil {
			log.Info("skipping baseline (incremental history already present)", zap.String("filename", m.filename))
		}
		// Do NOT set baselineAppliedNow: the baseline was skipped, so it does
		// not satisfy the frozen window. A partially migrated DB (e.g. ledger
		// 1..197 with threshold 267) MUST still apply the missing historical
		// incrementals 198..267 through the incremental path. Marking the
		// baseline as covered here would skip them, leaving both a real schema
		// gap and a ledger gap that the next run's validateAppliedMigrationSet
		// rejects as soon as a later migration (> threshold) is applied.
		// Already-applied incrementals are still skipped by the
		// checksum-matched ledger check in migrateAll.
		return true
	}
	if isHistoricalWindowCovered(state.applied, migrations, targetDB, state.baselineThreshold) {
		if log != nil {
			log.Info("skipping baseline (historical window already covered by individual ledger rows)", zap.String("filename", m.filename))
		}
		// Mark as covered so subsequent historical incrementals are not
		// re-evaluated against an empty baseline flag — the window is already
		// satisfied by the 1..N rows.
		state.baselineAppliedNow = true
		return true
	}
	// Fresh database: apply the baseline as a normal migration (version 0).
	// After it commits, 1..N will be skipped via baselineAppliedNow.
	return false
}

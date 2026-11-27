// migrate.go — the canonical PostgreSQL media migration runner
// and ledger (PLANE_INVENTORY.md divergence D2).
//
// The media plane previously applied the ordered MediaSelfBootstrapDDL without
// recording which versions ran, so checksum, duration and build provenance
// existed only for the SQLite planes. RunMediaMigrations closes that gap:
//
//  1. take a transaction-independent advisory lock so two processes cannot
//     race the ledger;
//  2. create the schema_migrations table (014);
//  3. for every ordered migration, compare the embedded SHA-256 with the
//     recorded checksum and fail closed on drift;
//  4. apply the ones the ledger does not know and record version, filename,
//     checksum, duration and build revision.
//
// 010/011 remain deployment-managed one-shots and are intentionally absent
// from the registry (BASELINE_PLAN.md §4); their convergence twins 012/013 are
// in the registry and therefore ledgered.
package mediaops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

//
// 010/011 remain deployment-managed one-shots and are intentionally absent
// from the registry (BASELINE_PLAN.md §4); their convergence twins 012/013 are
// in the registry and therefore ledgered.

// MediaMigrationReport is the machine-readable outcome of one migration run.
type MediaMigrationReport struct {
	Applied []int `json:"applied"`
	Skipped []int `json:"skipped"`
}

const mediaMigrationsLockKey = "pipelinegen_media_schema_migrations"

// RunMediaMigrations applies the canonical PostgreSQL media migrations through
// the schema_migrations ledger. It is idempotent: a second run applies
// nothing, and a mutated embedded migration is rejected instead of silently
// re-applied.
func RunMediaMigrations(ctx context.Context, db *sql.DB) (*MediaMigrationReport, error) {
	if db == nil {
		return nil, fmt.Errorf("media migrations: db is nil")
	}
	if _, err := db.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext($1)::bigint)`, mediaMigrationsLockKey); err != nil {
		return nil, fmt.Errorf("media migrations: acquire advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, mediaMigrationsLockKey)
	}()

	report := &MediaMigrationReport{Applied: []int{}, Skipped: []int{}}

	// The ledger bootstrap is itself a migration: create the table, then
	// record it so the ledger accounts for its own existence.
	if _, err := db.ExecContext(ctx, pgmigration.MediaSchemaMigrationsDDL); err != nil {
		return report, fmt.Errorf("media migrations: create schema_migrations: %w", err)
	}
	if err := recordMediaMigration(ctx, db, pgmigration.MediaSchemaMigrationsMigration, report); err != nil {
		return report, err
	}

	for _, migration := range pgmigration.MediaSelfBootstrapMigrations {
		if err := recordMediaMigration(ctx, db, migration, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// recordMediaMigration verifies the ledger identity of one migration, applies
// it when absent, and records it. A recorded checksum that differs from the
// embedded DDL is a hard error: editing an already-applied migration would
// otherwise silently change the meaning of a version.
func recordMediaMigration(ctx context.Context, db *sql.DB, migration pgmigration.MediaMigration, report *MediaMigrationReport) error {
	checksum := digest.SHA256String(migration.DDL)

	var recorded string
	err := db.QueryRowContext(ctx,
		`SELECT checksum_sha256 FROM schema_migrations WHERE version = $1`,
		migration.Version,
	).Scan(&recorded)
	switch {
	case err == nil:
		if recorded != checksum {
			return fmt.Errorf(
				"media migration %d (%s): ledger checksum mismatch: recorded %s, embedded %s — editing an applied migration requires a new version",
				migration.Version, migration.Filename, recorded, checksum,
			)
		}
		report.Skipped = append(report.Skipped, migration.Version)
		return nil
	case errors.Is(err, sql.ErrNoRows):
		// Apply below.
	default:
		return fmt.Errorf("media migration %d (%s): read ledger: %w", migration.Version, migration.Filename, err)
	}

	start := time.Now()
	if _, err := db.ExecContext(ctx, migration.DDL); err != nil {
		return fmt.Errorf("media migration %d (%s): apply: %w", migration.Version, migration.Filename, err)
	}
	duration := time.Since(start).Milliseconds()

	appGitSHA := ""
	if identity := buildinfo.Current(); identity.GitCommit != "" {
		appGitSHA = identity.GitCommit
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO schema_migrations
			(version, migration_id, filename, checksum_sha256, duration_ms, app_git_sha)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (version) DO NOTHING`,
		migration.Version,
		"postgres/"+migration.Filename,
		migration.Filename,
		checksum,
		duration,
		appGitSHA,
	); err != nil {
		return fmt.Errorf("media migration %d (%s): record ledger: %w", migration.Version, migration.Filename, err)
	}
	report.Applied = append(report.Applied, migration.Version)
	return nil
}

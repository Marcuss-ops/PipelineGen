package backfill

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// RunMediaBackfillPostgres implements the `backfill-media-postgres` admin
// command: the FASE-3 SQLite → PostgreSQL media backfill with fail-closed
// parity verification. It is a thin flag-delegator over
// internal/platform/postgres/media.RunMediaBackfill — all engine logic
// (mapping, batching, upserts, parity checks) lives there so tests can
// exercise it without the CLI.
//
// Usage:
//
//	admin backfill-media-postgres \
//	  --sqlite-dsn /path/to/media.db.sqlite?_journal_mode=WAL \
//	  --postgres-dsn postgres://.../pipelinegen_media?sslmode=disable \
//	  [--limit 0] [--batch-size 500] [--verify-only]
//
// Exit status: zero only when the backfill AND its parity verification
// succeeded. Any parity mismatch is a non-zero exit (fail-closed cutover
// evidence, godlike/07).
func RunMediaBackfillPostgres(args []string) error {
	fs := flag.NewFlagSet("backfill-media-postgres", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sqliteDSN := fs.String("sqlite-dsn", "", "SQLite media database DSN (required; e.g. /path/media.db.sqlite?_journal_mode=WAL)")
	pgDSN := fs.String("postgres-dsn", "", "PostgreSQL media SSOT DSN (required)")
	batchSize := fs.Int("batch-size", 500, "Keyset-pagination page size")
	limit := fs.Int("limit", 0, "Maximum media_assets rows to copy; zero means all")
	verifyOnly := fs.Bool("verify-only", false, "Skip the copy phase and only run the parity verifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*sqliteDSN) == "" {
		return fmt.Errorf("--sqlite-dsn is required")
	}
	if strings.TrimSpace(*pgDSN) == "" {
		return fmt.Errorf("--postgres-dsn is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	report, err := pgmedia.RunMediaBackfill(ctx, pgmedia.BackfillConfig{
		SQLiteDSN:   *sqliteDSN,
		PostgresDSN: *pgDSN,
		BatchSize:   *batchSize,
		Limit:       *limit,
		VerifyOnly:  *verifyOnly,
	})
	if report != nil {
		report.PrintJSON()
	}
	if err != nil {
		return err
	}
	fmt.Printf("media backfill OK: assets=%d locations=%d (verify_only=%v)\n",
		report.AssetsCopied, report.LocationsCopied, report.VerifyOnly)
	return nil
}

package backfill

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
)

// runBackfillPayloadHash fills the empty payload_hash column on jobs using
// the canonical job-registry hash (sha256 of the canonicalized payload JSON).
// It is the write-side companion to migration 214, which declared the column:
// rows written before the hash was computed at record time are repaired here
// so outbox/idempotency fingerprinting sees a complete payload_hash surface.
//
// The backfill is idempotent: the UPDATE is guarded by payload_hash = ”, so
// re-running converges instead of overwriting.
//
//	admin backfill-payload-hash [--limit N]
func RunBackfillPayloadHash(args []string) error {
	fs := flag.NewFlagSet("backfill-payload-hash", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 0, "Maximum number of jobs to backfill; zero means all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	jobsDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open primary database: %w", err)
	}
	defer jobsDB.Close()

	reg, err := jobregistry.New(jobsDB.DB)
	if err != nil {
		return err
	}
	updated, err := reg.BackfillPayloadHashes(context.Background(), *limit)
	if err != nil {
		return err
	}
	fmt.Printf("payload-hash backfill complete: updated=%d\n", updated)
	return nil
}

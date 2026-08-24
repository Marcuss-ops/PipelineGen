// cmd/admin/db_restore.go (June 2026 codex/db-doctor-restore):
//
// `admin db restore --verify <src> <dst>` performs Restore + Verify.
// `--verify` is REQUIRED in this version (a blind restore would
// overwrite a destination without confirming integrity, which has
// historically caused data loss). The output is structured JSON
// suitable for piping to restore-drill scripts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

func runDBRestore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db restore", flag.ExitOnError)
	src := fs.String("src", "", "source backup path (required)")
	dst := fs.String("dst", "", "destination DB path (required)")
	verify := fs.Bool("verify", false, "REQUIRED: run integrity check + smoke probe")
	fs.Parse(args)

	if *src == "" || *dst == "" {
		return fmt.Errorf("-src and -dst are required")
	}
	if !*verify {
		return fmt.Errorf("--verify is required: blind restore is not supported in this version (pass --verify to confirm integrity + smoke)")
	}

	r, err := storage.Restore(ctx, *src, *dst)
	if err != nil {
		// Emit partial struct so operators can see what passed.
		enc := json.NewEncoder(os.Stderr)
		_ = enc.Encode(r)
		return fmt.Errorf("restore failed: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(r); err != nil {
		return err
	}
	if !r.IntegrityOK || len(r.FKViolations) > 0 || !r.SmokeInsertOK {
		return fmt.Errorf("restore verify failed: integrity_ok=%v, fk_violations=%d, smoke_insert_ok=%v",
			r.IntegrityOK, len(r.FKViolations), r.SmokeInsertOK)
	}
	return nil
}

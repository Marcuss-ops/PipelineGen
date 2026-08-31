// cmd/admin/verify_projection.go — active-projection set-parity
// verifier command (plan item #8, August 2026).
//
// Compares the canonical eligible SQLite asset IDs (SearchIndexEligibilitySQL
// SSOT via SQLiteAssetStore.ListAllAssetIDs) against the asset_ids present
// in the ACTIVE Qdrant projection (runtime alias target). PASS only when
// missing_in_qdrant == 0 AND orphan_in_qdrant == 0 AND the scan completed
// cleanly.
//
// Exit code: 0 on PASS, 1 on FAIL (or fatal error) — scriptable as a gate.
//
// Usage:
//
//	go run ./cmd/admin verify-projection
//	go run ./cmd/admin verify-projection --json
//	go run ./cmd/admin verify-projection --batch-size=1000
//
// The verifier always reads the canonical runtime projection through
// `media_assets_current`; it has no collection override. Historical or
// recovery collections belong to explicit emergency/DR commands.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
)

// verifyProjectionDeps holds the parsed flags for RunVerifyProjection.
type verifyProjectionDeps struct {
	JSON      bool
	BatchSize int
}

// parseVerifyProjectionArgs parses CLI args.
// Flags:
//
//	--json                  machine-readable output
//	--batch-size=N          points per scroll page (default 500)
//
// Collection selection is intentionally not configurable on this normal
// production verification path.
func parseVerifyProjectionArgs(args []string) (verifyProjectionDeps, error) {
	deps := verifyProjectionDeps{BatchSize: 500}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--batch-size="):
			n, err := cli.ParsePositiveFlag(a, "--batch-size")
			if err != nil {
				return deps, err
			}
			deps.BatchSize = n
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	return deps, nil
}

// runVerifyProjection is the entry point registered in cmd/admin/main.go.
func RunVerifyProjection(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseVerifyProjectionArgs(args)
	if err != nil {
		return err
	}

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"verify-projection requires qdrant.enabled=true",
		)
	}

	ctx := cli.CmdContext()

	log.Info("verify-projection starting",
		zap.String("runtime_alias", qdrantschema.DefaultV3Schema().RuntimeAlias),
		zap.Int("batch_size", deps.BatchSize), zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()
	sqliteDB := dbSet.Primary

	schema := qdrantschema.DefaultV3Schema()
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	verifier := verification.NewProjectionVerifier(client, indexing.NewSQLiteAssetStore(sqliteDB.DB), schema, log)
	verifier.BatchSize = deps.BatchSize

	report, err := verifier.VerifyActiveProjection(ctx)
	if err != nil {
		return fmt.Errorf("verify active projection: %w", err)
	}

	if deps.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
	} else {
		printProjectionVerification(*report)
	}

	if !report.Passed {
		return fmt.Errorf(
			"projection verification FAILED: missing_in_qdrant=%d orphan_in_qdrant=%d points_without_asset_id=%d complete_scan=%v errors=%d",
			report.MissingCount, report.OrphanCount, report.PointsMissingAssetID, report.CompleteScan, len(report.Errors),
		)
	}
	return nil
}

func printProjectionVerification(r verification.ProjectionVerificationReport) {
	verdict := "PASS"
	if !r.Passed {
		verdict = "FAIL"
	}
	fmt.Printf("=== Projection verification: %s → %s ===\n", r.Collection, verdict)
	fmt.Printf("  eligible_sqlite        %d\n", r.EligibleSQLite)
	fmt.Printf("  qdrant_points          %d\n", r.QdrantPoints)
	if r.PointsMissingAssetID > 0 {
		fmt.Printf("  points_missing_asset_id %d\n", r.PointsMissingAssetID)
	}
	fmt.Printf("  missing_in_qdrant      %d\n", r.MissingCount)
	fmt.Printf("  orphan_in_qdrant       %d\n", r.OrphanCount)
	fmt.Printf("  complete_scan          %v\n", r.CompleteScan)
	if len(r.MissingIDs) > 0 {
		fmt.Printf("  --- missing IDs (%d):\n", len(r.MissingIDs))
		for _, id := range r.MissingIDs {
			fmt.Printf("    %s\n", id)
		}
	}
	if len(r.OrphanIDs) > 0 {
		fmt.Printf("  --- orphan IDs (%d):\n", len(r.OrphanIDs))
		for _, id := range r.OrphanIDs {
			fmt.Printf("    %s\n", id)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Printf("  errors: %d\n", len(r.Errors))
		for i, e := range r.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
}

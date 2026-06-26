// cmd/admin/clean_locators.go — QDRANT-005 payload cleaner (June 2026)
//
// One-shot scan-and-clean of legacy drive_link / local_path payload keys
// from historical Qdrant points. Uses the canonical LocatorCleaner
// (dry-run-first, idempotent, batch-delete via payload/delete API).
//
// Usage:
//
//	go run ./cmd/admin clean-qdrant-locators              # dry-run (scan only)
//	go run ./cmd/admin clean-qdrant-locators --apply       # actually delete the keys
//	go run ./cmd/admin clean-qdrant-locators --json        # machine-readable dry-run
//	go run ./cmd/admin clean-qdrant-locators --apply --json # machine-readable apply
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// cleanLocatorsDeps holds the parsed flags for runCleanLocators.
type cleanLocatorsDeps struct {
	Apply bool
	JSON  bool
}

// parseCleanLocatorsArgs parses CLI args into cleanLocatorsDeps.
// Flags:
//
//	--apply   actually delete the legacy keys (default: dry-run)
//	--json    machine-readable output
func parseCleanLocatorsArgs(args []string) (cleanLocatorsDeps, error) {
	deps := cleanLocatorsDeps{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch a {
		case "--apply":
			deps.Apply = true
		case "--json":
			deps.JSON = true
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	return deps, nil
}

// runCleanLocators is the entry point registered in cmd/admin/main.go.
//
// Pipeline:
//  1. Load config and open the media DB
//  2. Build the canonical Qdrant stack: SQLiteAssetStore → PayloadMapper → Client → LocatorCleaner
//  3. Dry-run: scroll all points, count affected, print report
//  4. Apply: scroll + batch-delete legacy keys via DeletePayloadKeys
func runCleanLocators(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseCleanLocatorsArgs(args)
	if err != nil {
		return err
	}

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"clean-qdrant-locators requires qdrant.enabled=true",
		)
	}

	ctx := cmdContext()

	log.Info("clean-qdrant-locators starting",
		zap.Bool("apply", deps.Apply),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL))

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// Build canonical Qdrant stack for the cleaner.
	schema := qdrant.DefaultV3Schema()
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)
	cleaner := qdrant.NewLocatorCleaner(client, schema, log)

	report, err := cleaner.CleanLocators(ctx, deps.Apply)
	if err != nil {
		log.Error("clean-qdrant-locators failed", zap.Error(err))
		// Still print the partial report when available.
		if report != nil && deps.JSON {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
		}
		return err
	}

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return nil
	}

	mode := "DRY-RUN"
	if deps.Apply {
		mode = "APPLY"
	}
	fmt.Printf("=== QDRANT-005 locator cleanup: %s ===\n", mode)
	fmt.Printf("  Collection:       %s\n", report.Collection)
	fmt.Printf("  Points scrolled:  %d\n", report.TotalPointsScrolled)
	fmt.Printf("  With drive_link:  %d\n", report.PointsWithDriveLink)
	fmt.Printf("  With local_path:  %d\n", report.PointsWithLocalPath)
	fmt.Printf("  Affected (total): %d\n", report.PointsAffected)
	if deps.Apply {
		fmt.Printf("  Keys removed:     %d\n", report.KeysRemoved)
		fmt.Printf("  Batch calls:      %d\n", report.BatchCount)
	}
	if len(report.Errors) > 0 {
		fmt.Printf("  Errors:           %d\n", len(report.Errors))
		for i, e := range report.Errors {
			fmt.Printf("    [%d] %s\n", i, e)
		}
	}
	if !deps.Apply {
		fmt.Println("\nRe-run with --apply to actually delete the legacy keys.")
	}
	return nil
}

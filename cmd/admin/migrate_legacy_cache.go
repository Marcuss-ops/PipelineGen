// cmd/admin/migrate_legacy_cache.go — one-shot migration of all
// gemma_script_outputs rows from legacy/pre-V1 shapes (plain text)
// into canonical ModelScriptOutputV1 JSON.
//
// DL-MODECOMPAT-REMOVAL (August 2026): ModeCompatibility and
// legacy_converter.go were removed. This tool now uses ModeFreshPlainText
// (the sole canonical decoder). Legacy JSON array rows will report
// as undecodable; plain-text rows will be wrapped into V1.
//
// Usage:
//
//	pip-admin migrate-legacy-cache
//	pip-admin migrate-legacy-cache --dry-run
//	pip-admin migrate-legacy-cache --report migrated.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jsonextract"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

func runMigrateLegacyCache(args []string) error {
	fs := flag.NewFlagSet("migrate-legacy-cache", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Scan without writing")
	reportPath := fs.String("report", "", "Write JSON report to file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cli.CmdContext()
	sdb, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("migrate-legacy-cache: open DB: %w", err)
	}
	defer sdb.Close()

	// Read all exact-cache rows.
	rows, err := sdb.DB.QueryContext(ctx,
		`SELECT id, output_text FROM gemma_script_outputs`)
	if err != nil {
		return fmt.Errorf("migrate-legacy-cache: query rows: %w", err)
	}
	defer rows.Close()

	type migrationResult struct {
		ID          string `json:"id"`
		BeforeType  string `json:"before_type"`
		AfterText   string `json:"after_text,omitempty"`
		Error       string `json:"error,omitempty"`
	}

	var results []migrationResult
	var migrated, skipped, errors int

	for rows.Next() {
		var id, outputText string
		if err := rows.Scan(&id, &outputText); err != nil {
			return fmt.Errorf("migrate-legacy-cache: scan row: %w", err)
		}

		// Decode with ModeFreshPlainText (sole canonical decoder; DL-MODECOMPAT-REMOVAL).
		// Plain text will be wrapped into V1; legacy JSON arrays will fail as undecodable.
		scanner := jsonextract.NewScanner(jsonextract.ModeFreshPlainText)
		v1out, scanErr := scanner.Scan([]byte(outputText), "migration")
		if scanErr != nil {
			errors++
			results = append(results, migrationResult{
				ID:         id,
				BeforeType: "undecodable",
				Error:      scanErr.Error(),
			})
			fmt.Printf("  ERROR  %s: %v\n", id, scanErr)
			continue
		}

		// Marshal the canonical V1 shape to JSON.
		v1JSON, marshalErr := json.Marshal(v1out)
		if marshalErr != nil {
			errors++
			results = append(results, migrationResult{
				ID:         id,
				BeforeType: "marshal_failed",
				Error:      marshalErr.Error(),
			})
			fmt.Printf("  ERROR  %s: marshal: %v\n", id, marshalErr)
			continue
		}
		v1JSONStr := string(v1JSON)

		// Determine if this row needs migration.
		if outputText == v1JSONStr {
			skipped++
			results = append(results, migrationResult{
				ID:         id,
				BeforeType: "already_v1",
			})
			continue
		}

		// Classify the before-type for reporting.
		beforeType := classifyLegacyShape([]byte(outputText))

		if !*dryRun {
			_, updateErr := sdb.DB.ExecContext(ctx,
				`UPDATE gemma_script_outputs SET output_text = ?, updated_at = datetime('now') WHERE id = ?`,
				v1JSONStr, id,
			)
			if updateErr != nil {
				errors++
				results = append(results, migrationResult{
					ID:         id,
					BeforeType: beforeType,
					AfterText:  v1JSONStr,
					Error:      updateErr.Error(),
				})
				fmt.Printf("  ERROR  %s: update: %v\n", id, updateErr)
				continue
			}
		}

		migrated++
		results = append(results, migrationResult{
			ID:         id,
			BeforeType: beforeType,
			AfterText:  v1JSONStr,
		})
		fmt.Printf("  MIGRATED  %s: %s → V1 JSON\n", id, beforeType)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate-legacy-cache: rows iteration: %w", err)
	}

	// Summary.
	fmt.Printf("\n---\n")
	fmt.Printf("Total rows:      %d\n", len(results))
	fmt.Printf("Already V1:      %d\n", skipped)
	fmt.Printf("Migrated:        %d\n", migrated)
	fmt.Printf("Errors:          %d\n", errors)
	if *dryRun {
		fmt.Println("DRY RUN — no writes performed.")
	}

	if *reportPath != "" {
		report := map[string]interface{}{
			"total":    len(results),
			"skipped":  skipped,
			"migrated": migrated,
			"errors":   errors,
			"dry_run":  *dryRun,
			"rows":     results,
		}
		payload, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("migrate-legacy-cache: write report: %w", err)
		}
		fmt.Printf("Report written to %s\n", *reportPath)
	}

	if errors > 0 {
		return fmt.Errorf("migrate-legacy-cache: %d row(s) failed", errors)
	}
	return nil
}

// classifyLegacyShape heuristically identifies the shape of raw LLM
// output for reporting purposes (before vs after type).
func classifyLegacyShape(raw []byte) string {
	trimmed := string(raw)
	if len(trimmed) == 0 {
		return "empty"
	}
	if trimmed[0] == '[' {
		return "legacy_array"
	}
	if trimmed[0] == '{' {
		return "v1_or_json"
	}
	return "plain_text"
}
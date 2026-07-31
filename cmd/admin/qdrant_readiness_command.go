package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

func runQdrantReadiness(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	jsonOut := false
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(strings.TrimSpace(a), "-") {
				return fmt.Errorf("unknown flag: %s", a)
			}
		}
	}

	ctx := cmdContext()
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// Build the production composition root (PR 15). The server,
	// dispatcher, qdrant client, clips repo, worker pool, real
	// outbox/mediasearch handler wires — every check reads from
	// root. InitComposition is the canonical producer of root
	// (mirrors what cmd/server/main.go constructs).
	root, rootCleanup, err := appInitCompositionForReadiness(ctx, cfg, log)
	if err != nil {
		// Root construction itself failed — readiness gate cannot
		// proceed because server_production_constructor will fail
		// (the test of the canonical constructor). We surface this
		// as a synthetic nil root + log; the per-check functions
		// that need Root handle nil safely and report the failure
		// in the report.
		log.Warn("production composition root failed to init; readiness checks will surface the failure per-check",
			zap.Error(err))
		root = nil
	} else {
		defer rootCleanup()
	}

	report, err := qdrantReadiness(ctx, sqliteDB.DB, cfg, log, root)
	if err != nil {
		log.Warn("readiness scan returned non-fatal error; emitting partial report",
			zap.Error(err))
		// Continue — we surface the partial report so operators can see
		// WHICH check failed. Errors here are NEVER accepted-by-default.
	}

	if jsonOut {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("READY=%t\n", report.Ready)
		for name, status := range report.Checks {
			fmt.Printf("%s=%s\n", name, status)
		}
		fmt.Printf("qdrant_reachable=%t\n", report.QdrantReachable)
		fmt.Printf("sqlite_migrations_complete=%t\n", report.SQLiteMigrationsComplete)
		fmt.Printf("active_collection=%q\n", report.ActiveCollection)
		fmt.Printf("active_collection_compatible=%t\n", report.ActiveCollectionCompatible)
		fmt.Printf("total_assets=%d\n", report.TotalAssets)
		fmt.Printf("non_media_assets=%d\n", report.NonMediaAssets)
		fmt.Printf("invalid_text_vectors=%d\n", report.InvalidTextVectors)
		fmt.Printf("invalid_transcript_vectors=%d\n", report.InvalidTranscriptVectors)
		fmt.Printf("invalid_visual_vectors=%d\n", report.InvalidVisualVectors)
		fmt.Printf("invalid_audio_vectors=%d\n", report.InvalidAudioVectors)
		fmt.Printf("missing_source_file=%d\n", report.MissingSourceFile)
		fmt.Printf("legacy_status_rows=%d\n", report.LegacyStatusRows)
		fmt.Printf("legacy_locator_rows=%d\n", report.LegacyLocatorRows)
		fmt.Printf("outbox_operational=%t\n", report.OutboxOperational)
	}

	if !report.Ready {
		// Spec: exit non-zero when ready=false so CI/operators see the
		// failed gate. The error message lists failing check names.
		var failing []string
		for name, status := range report.Checks {
			if status != "pass" {
				failing = append(failing, fmt.Sprintf("%s=%s", name, status))
			}
		}
		return fmt.Errorf("qdrant readiness gate failed: %s", strings.Join(failing, ", "))
	}
	return nil
}

func parseStrictPositiveIntFlag(arg, name string) (int, error) {
	v := strings.TrimPrefix(arg, name+"=")
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %d", name, n)
	}
	return n, nil
}

// cmd/admin/backfill_monitored_sources_to_category_channels.go
//
// Wave CONFORMANCE-001 step (architecture/current.yaml::id-24, A1.3
// followup, June 2026): one-shot admin CLI that performs BACKFILL
// `monitored_sources` (legacy monitor-config table from
// 001_velox_core.sql) into `category_channels`, the canonical SoT
// per the project's data-ownership policy.
//
// Idempotency:
//   channels.Service.UpsertBulk dedupes on (category, channel_url).
//   This CLI proves the cross-run invariant by running UpsertBulk
//   TWICE and asserting `drift = post2 - post1 = 0`. A non-zero
//   drift fail-loud is treated as a real bug in channels.Service
//   (this CLI exits 2 immediately; operators get signal within a
//   single invocation rather than waiting for a separate regression
//   test run).
//
// Snapshot convention:
//   Operator MUST `cp data/media/media.db.sqlite
//   data/snapshots/pre-TIMESTAMP.db` BEFORE invoking. The CLI does
//   not snapshot automatically.
//
// Field mapping (monitored_sources → channels.UpsertChannelCommand):
//
//   ┌─────────────────────┬───────────────────────────────────────────┐
//   │ monitored_sources   │ category_channels (via UpsertBulk cmds)   │
//   ├─────────────────────┼───────────────────────────────────────────┤
//   │ id                  │ informational only (no mapping)          │
//   │ name                │ informational only (Service extracts     │
//   │                     │   ChannelName from URL)                  │
//   │ url                 │ ChannelURL                                │
//   │ kind                │ informational only (Service default      │
//   │                     │   applies 'youtube')                       │
//   │ status              │ informational only (Service default       │
//   │                     │   applies enabled=1)                       │
//   │ metadata_json       │ SKIPPED (no schema asserted)             │
//   │ last_harvester_run  │ SKIPPED (Service does not expose)        │
//   │ (--category flag)   │ Category (default: "monitored-import")    │
//   └─────────────────────┴───────────────────────────────────────────┘
//
// Note on per-kind discrimination (deferred to Wave 22 followups):
//   monitored_sources.kind is a per-source-kind discriminator
//   (youTube today; spotify/vimeo later). The current CLI merges all
//   imported rows under one CLI-controlled category bucket
//   ("monitored-import" by default). Per-kind category derivation
//   ("monitored:<kind>") is a design-space trade-off; the current
//   flat-bucket approach keeps the operator visible and easy to
//   re-bucket via the channels HTTP bulk endpoint or direct SQL upsert.
//
// Out of scope:
//   - Interpret metadata_json (no JSON schema asserted).
//   - Set drive_folder_id.
//   - DELETE monitored_sources (Wave 16 followup CONTRACT phase).

package main

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	sqlchannels "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func runBackfillMonitoredSourcesToCategoryChannels(args []string) error {
	category := "monitored-import"
	for i, arg := range args {
		if arg == "--category" && i+1 < len(args) {
			category = args[i+1]
		}
	}

	log, _ := zap.NewDevelopment()
	defer func() { _ = log.Sync() }()

	cfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	dbPath := cfg.Storage.FullPath("media/media.db.sqlite")
	log.Info("opening database", zap.String("path", dbPath))
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqliteDB.Close()

	// pass targetDB="primary" — this CLI
	// operates on data/media/media.db.sqlite, the canonical primary DB.
	if err := sqliteDB.RunMigrations(log, "migrations/sqlite", "primary"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	svc := channels.NewService(
		channels.NewRepositoryAdapter(sqlchannels.NewChannelsRepository(sqliteDB.DB)),
		log,
	)

	ctx := context.Background()

	preCount, err := countCategoryChannels(ctx, sqliteDB.DB, category)
	if err != nil {
		return fmt.Errorf("pre count: %w", err)
	}
	log.Info("pre category_channels rowcount",
		zap.String("category", category), zap.Int("count", preCount))

	cmds, err := readMonitoredSourcesAsCommands(ctx, sqliteDB.DB, category, log)
	if err != nil {
		return fmt.Errorf("read monitored_sources: %w", err)
	}
	log.Info("read monitored_sources", zap.Int("rows", len(cmds)))

	if len(cmds) == 0 {
		fmt.Printf("ℹ️  No rows in monitored_sources. category=%q pre=%d post=%d delta=%+d\n",
			category, preCount, preCount, 0)
		fmt.Printf("✅ Done (no work needed — DB already canonical for this category).\n")
		return nil
	}

	res, err := svc.UpsertBulk(ctx, channels.BulkUpsertChannelsCommand{Channels: cmds})
	if err != nil {
		return fmt.Errorf("upsert bulk: %w", err)
	}

	fmt.Printf("📊 UpsertBulk (Run 1): created=%d updated=%d errors=%d\n",
		len(res.Created), len(res.Updated), len(res.Errors))
	for _, e := range res.Errors {
		fmt.Printf("  ❌ %s\n", e)
	}

	postCount, err := countCategoryChannels(ctx, sqliteDB.DB, category)
	if err != nil {
		return fmt.Errorf("post count: %w", err)
	}
	delta := postCount - preCount
	fmt.Printf("📈 Run 1 rowcount for category=%q: pre=%d post=%d delta=%+d\n",
		category, preCount, postCount, delta)

	// ZERO-DRIFT CROSS-RUN ASSERTION.
	res2, err := svc.UpsertBulk(ctx, channels.BulkUpsertChannelsCommand{Channels: cmds})
	if err != nil {
		return fmt.Errorf("verify upsert bulk (Run 2): %w", err)
	}
	post2Count, err := countCategoryChannels(ctx, sqliteDB.DB, category)
	if err != nil {
		return fmt.Errorf("post2 count: %w", err)
	}
	drift := post2Count - postCount
	fmt.Printf("📊 UpsertBulk (Run 2): created=%d updated=%d errors=%d\n",
		len(res2.Created), len(res2.Updated), len(res2.Errors))
	fmt.Printf("📈 Run 2 rowcount: post2=%d vs post1=%d → drift=%+d\n",
		post2Count, postCount, drift)
	if drift != 0 || len(res2.Created) != 0 {
		return fmt.Errorf("zero-drift ASSERTION FAILED: drift=%+d or Run2-created=%d (channels.Service.UpsertBulk is supposed to be idempotent)",
			drift, len(res2.Created))
	}
	fmt.Printf("✅ Done. Zero-drift ASSERTION passed (post2 == post1 == %d, no Run-2 creates).\n", postCount)
	return nil
}

// countCategoryChannels counts rows in category_channels where category
// equals the supplied value. Used for pre/post rowcount snapshots.
func countCategoryChannels(ctx context.Context, db *sql.DB, category string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM category_channels WHERE category = ?`,
		category).Scan(&n)
	return n, err
}

// readMonitoredSourcesAsCommands reads every row from monitored_sources
// and returns UpsertChannelCommand values. Other legacy columns (id,
// name, kind, status, metadata_json, last_harvester_run) are scanned
// (so the operator log shows the full row shape) but only url is
// mapped into UpsertChannelCommand.
func readMonitoredSourcesAsCommands(
	ctx context.Context,
	db *sql.DB,
	category string,
	log *zap.Logger,
) ([]channels.UpsertChannelCommand, error) {
	rows, err := db.QueryContext(ctx, `
        SELECT id, name, url, kind, status, metadata_json, last_harvester_run
          FROM monitored_sources
         ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cmds []channels.UpsertChannelCommand
	skipped := 0
	for rows.Next() {
		var (
			id, name, url, kind, status, metadataJSON, lastHarvesterRun string
		)
		if err := rows.Scan(
			&id, &name, &url, &kind, &status, &metadataJSON, &lastHarvesterRun,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		log.Info("candidate row",
			zap.String("id", id),
			zap.String("name", name),
			zap.String("url", url),
			zap.String("kind", kind),
			zap.String("status", status),
		)
		if url == "" {
			skipped++
			continue
		}
		cmds = append(cmds, channels.UpsertChannelCommand{
			Category:   category,
			ChannelURL: url,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if skipped > 0 {
		log.Info("skipped empty-URL rows", zap.Int("skipped", skipped))
	}
	return cmds, nil
}

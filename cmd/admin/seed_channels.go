package main

// runSeedChannels — Capability Standard migration (June 2026):
//
// Capability Standard routes the admin CLI through the canonical use
// case layer rather than the concrete repository. Domain object
// construction, JSON marshalling of keyword arrays, and default
// policy application all moved into channels.Service
// (internal/application/channels). The CLI sends typed
// UpsertChannelCommand values to channels.NewService + UpsertBulk.
//
// This file keeps the parser (config file → typed commands) and the
// no-build dry-run summary; the actual persistence goes through the
// same path the HTTP bulk handler uses, so admin and HTTP can no
// longer drift on defaults.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

func runSeedChannels(args []string) error {
	configPath := "config/channel_monitor_config.json"
	dryRun := false
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			configPath = args[i+1]
		}
		if arg == "--dry-run" {
			dryRun = true
		}
	}

	log, _ := zap.NewDevelopment()
	defer log.Sync()

	cfg := config.Get()

	dbPath := cfg.Storage.FullPath("media/media.db.sqlite")
	log.Info("opening database", zap.String("path", dbPath))
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.RunMigrations(log, "migrations/sqlite"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	log.Info("migrations applied")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config file %s: %w", configPath, err)
	}

	var monitorCfg struct {
		Channels []struct {
			URL              string   `json:"url"`
			Category         string   `json:"category"`
			Keywords         []string `json:"keywords"`
			MinViews         int      `json:"min_views"`
			MaxClipDuration  int      `json:"max_clip_duration"`
			DriveFolderID    string   `json:"drive_folder_id,omitempty"`
			SemanticKeywords []string `json:"semantic_keywords,omitempty"`
			MinSemanticScore int      `json:"min_semantic_score,omitempty"`
			PlaylistEnd      int      `json:"playlist_end,omitempty"`
			CheckInterval    string   `json:"check_interval,omitempty"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(data, &monitorCfg); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}

	// Build the channels capability through the canonical
	// constructor. Admin is a one-shot CLI — no registry path, but
	// still goes through Service so HTTP bulk and admin stay in sync
	// on defaults.
	svc := channels.NewService(
		channels.NewRepositoryAdapter(assets.NewChannelsRepository(sqliteDB.DB)),
		log,
	)

	cmds := make([]channels.UpsertChannelCommand, 0, len(monitorCfg.Channels))
	importedURL := 0
	skipped := 0
	for _, ch := range monitorCfg.Channels {
		if ch.URL == "" || ch.Category == "" {
			skipped++
			continue
		}
		cmds = append(cmds, channels.UpsertChannelCommand{
			Category:         ch.Category,
			ChannelURL:       ch.URL,
			Keywords:         ch.Keywords,
			MinViews:         ch.MinViews,
			MaxClipDuration:  ch.MaxClipDuration,
			DriveFolderID:    ch.DriveFolderID,
			SemanticKeywords: ch.SemanticKeywords,
			MinSemanticScore: ch.MinSemanticScore,
			PlaylistEnd:      ch.PlaylistEnd,
			CheckInterval:    ch.CheckInterval,
		})
		importedURL++
	}

	if dryRun {
		fmt.Println("[DRY RUN] the following channels WOULD be upserted via channels.UpsertBulk:")
		for _, cmd := range cmds {
			id := svc.IDFor(cmd.Category, cmd.ChannelURL)
			fmt.Printf("  - %s/%s → id=%s (max_clip_duration=%d, priority=%d, check_interval=%q)\n",
				cmd.Category, cmd.ChannelURL, id,
				channels.Default.MaxClipDuration, channels.Default.Priority, channels.Default.CheckInterval)
		}
		fmt.Printf("\n📊 Dry-run summary: %d would import, %d skipped (empty url/category)\n", importedURL, skipped)
		return nil
	}

	res, err := svc.UpsertBulk(context.Background(), channels.BulkUpsertChannelsCommand{
		Channels: cmds,
	})
	if err != nil {
		return fmt.Errorf("bulk upsert: %w", err)
	}
	created, updated, errs := len(res.Created), len(res.Updated), len(res.Errors)
	fmt.Printf("📊 Summary: %d created, %d updated, %d errors (skipped before upsert: %d)\n", created, updated, errs, skipped)
	for _, e := range res.Errors {
		fmt.Printf("  ❌ %s\n", e)
	}
	return nil
}

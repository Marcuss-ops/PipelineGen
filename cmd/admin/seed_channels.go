package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	channelsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"

	"go.uber.org/zap"
)

func runSeedChannels(args []string) error {
	// Parse optional flags
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

	// Load config
	cfg := config.Get()

	// Open the main database (media.db.sqlite)
	dbPath := cfg.Storage.FullPath("media/media.db.sqlite")

	log.Info("opening database", zap.String("path", dbPath))
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, log)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqliteDB.Close()

	// Run migrations to ensure category_channels table exists
	if err := sqliteDB.RunMigrations(log, "migrations/sqlite"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	log.Info("migrations applied")

	// Read the monitor config file
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

	repo := channelsrepo.NewRepository(sqliteDB.DB)

	imported := 0
	skipped := 0

	for _, ch := range monitorCfg.Channels {
		if ch.URL == "" || ch.Category == "" {
			skipped++
			continue
		}

		// Generate deterministic ID from category + URL
		hash := sha256.Sum256([]byte(ch.Category + ":" + ch.URL))
		id := fmt.Sprintf("%s_%x", ch.Category, hash[:8])

		// Marshal keywords to JSON
		keywordsJSON := "[]"
		if len(ch.Keywords) > 0 {
			b, _ := json.Marshal(ch.Keywords)
			keywordsJSON = string(b)
		}

		// Marshal semantic keywords to JSON
		semanticKeywordsJSON := "[]"
		if len(ch.SemanticKeywords) > 0 {
			b, _ := json.Marshal(ch.SemanticKeywords)
			semanticKeywordsJSON = string(b)
		}

		channel := &models.CategoryChannel{
			ID:               id,
			Category:         ch.Category,
			ChannelURL:       ch.URL,
			ChannelName:      extractChannelName(ch.URL),
			Keywords:         keywordsJSON,
			MinViews:         ch.MinViews,
			MaxClipDuration:  ch.MaxClipDuration,
			DriveFolderID:    ch.DriveFolderID,
			SemanticKeywords: semanticKeywordsJSON,
			MinSemanticScore: ch.MinSemanticScore,
			PlaylistEnd:      ch.PlaylistEnd,
		}


		// Action
		if dryRun {
			fmt.Printf("[DRY RUN] would %s channel: %s (category: %s, id: %s)\n",
				map[bool]string{true: "create", false: "update"}[true], ch.URL, ch.Category, id)
			fmt.Printf("          keywords: %s\n", keywordsJSON)
			if semanticKeywordsJSON != "[]" {
				fmt.Printf("          semantic_keywords: %s\n", semanticKeywordsJSON)
			}
			if ch.PlaylistEnd > 0 {
				fmt.Printf("          playlist_end: %d\n", ch.PlaylistEnd)
			}
			imported++
			continue
		}

		if err := repo.Upsert(context.Background(), channel); err != nil {
			log.Warn("failed to upsert channel", zap.String("url", ch.URL), zap.Error(err))
			skipped++
			continue
		}
		imported++
		fmt.Printf("✅ %s → %s\n", ch.Category, ch.URL)
	}

	fmt.Printf("\n📊 Summary: %d imported, %d skipped\n", imported, skipped)
	return nil
}

func extractChannelName(url string) string {
	// Extract @handle from YouTube URL
	url = strings.TrimSuffix(url, "/videos")
	url = strings.TrimSuffix(url, "/")
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		name := url[idx+1:]
		if strings.HasPrefix(name, "@") {
			return name
		}
		return name
	}
	return url
}

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"velox/go-master/internal/catalogdb"
	"velox/go-master/internal/clipdb"
)

type migrationState struct {
	TotalJSON    int
	TotalSQLite  int
	MigratedJSON int
	MigratedSQL  int
	Errors       []error
}

func main() {
	fmt.Println("Starting migration to Unified Catalog...")

	dataDir := "data"
	unifiedPath := filepath.Join(dataDir, "unified_catalog.db")
	jsonPath := filepath.Join(dataDir, "clip_index.json")
	legacySqlPath := filepath.Join(dataDir, "clips_catalog.db")

	// 1. Open Unified Catalog
	catalog, err := catalogdb.Open(unifiedPath)
	if err != nil {
		log.Fatalf("Failed to open unified catalog: %v", err)
	}
	defer catalog.Close()

	state := &migrationState{}

	// 2. Migrate from JSON (clip_index.json)
	migrateFromJSON(jsonPath, catalog, state)

	// 3. Migrate from Legacy SQLite (clips_catalog.db)
	migrateFromLegacySQL(legacySqlPath, catalog, state)

	fmt.Printf("\nMigration Complete!\n")
	fmt.Printf("JSON Clips:   %d / %d\n", state.MigratedJSON, state.TotalJSON)
	fmt.Printf("SQLite Clips: %d / %d\n", state.MigratedSQL, state.TotalSQLite)
	
	if len(state.Errors) > 0 {
		fmt.Printf("\nErrors encountered: %d\n", len(state.Errors))
		for i, e := range state.Errors {
			if i > 10 {
				fmt.Println("...")
				break
			}
			fmt.Printf("- %v\n", e)
		}
	}
}

func migrateFromJSON(path string, catalog *catalogdb.CatalogDB, state *migrationState) {
	fmt.Printf("Migrating from JSON: %s\n", path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("JSON file not found, skipping.")
			return
		}
		state.Errors = append(state.Errors, fmt.Errorf("read json: %w", err))
		return
	}

	type jsonClip struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		FolderID string `json:"folder_id"`
		Source   string `json:"source"`
		Duration int    `json:"duration"`
		DriveURL string `json:"drive_link"`
	}
	var clipIdx struct {
		Clips []jsonClip `json:"clips"`
	}
	if err := json.Unmarshal(data, &clipIdx); err != nil {
		state.Errors = append(state.Errors, fmt.Errorf("unmarshal json: %w", err))
		return
	}

	state.TotalJSON = len(clipIdx.Clips)
	batch := make([]catalogdb.Clip, 0, 100)

	for _, c := range clipIdx.Clips {
		source := c.Source
		if source == "" {
			source = catalogdb.SourceClipDrive
		}

		id := c.ID
		if id == "" {
			id = c.Filename // Fallback if ID is empty
		}

		clip := catalogdb.Clip{
			Source:      source,
			SourceID:    id,
			Title:       c.Filename,
			Filename:    c.Filename,
			FolderID:    c.FolderID,
			DriveURL:    c.DriveURL,
			DurationSec: c.Duration,
			IsActive:    true,
		}
		batch = append(batch, clip)

		if len(batch) >= 100 {
			if err := catalog.BulkUpsertClips(batch); err != nil {
				state.Errors = append(state.Errors, err)
			} else {
				state.MigratedJSON += len(batch)
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := catalog.BulkUpsertClips(batch); err != nil {
			state.Errors = append(state.Errors, err)
		} else {
			state.MigratedJSON += len(batch)
		}
	}
}

func migrateFromLegacySQL(path string, catalog *catalogdb.CatalogDB, state *migrationState) {
	fmt.Printf("Migrating from Legacy SQLite: %s\n", path)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Println("Legacy SQLite not found, skipping.")
		return
	}

	legacyDB, err := clipdb.OpenSQLite(path)
	if err != nil {
		state.Errors = append(state.Errors, fmt.Errorf("open legacy sql: %w", err))
		return
	}
	defer legacyDB.Close()

	rows, err := legacyDB.RawDB().Query("SELECT id, title, source, url, duration, tags, http_status, last_checked, created_at, updated_at FROM clips")
	if err != nil {
		state.Errors = append(state.Errors, fmt.Errorf("query legacy sql: %w", err))
		return
	}
	defer rows.Close()

	batch := make([]catalogdb.Clip, 0, 100)
	for rows.Next() {
		state.TotalSQLite++
		var id, title, source, url, tagsStr string
		var duration float64
		var httpStatus int
		var lastChecked, createdAt, updatedAt sql.NullTime
		
		err := rows.Scan(&id, &title, &source, &url, &duration, &tagsStr, &httpStatus, &lastChecked, &createdAt, &updatedAt)
		if err != nil {
			state.Errors = append(state.Errors, fmt.Errorf("scan legacy row: %w", err))
			continue
		}

		if source == "" {
			source = "legacy_sql"
		}

		clip := catalogdb.Clip{
			Source:       source,
			SourceID:     id,
			Title:        title,
			Filename:     filepath.Base(url),
			LocalPath:    url,
			Tags:         strings.Fields(tagsStr),
			DurationSec:  int(duration),
			CreatedAt:    createdAt.Time,
			ModifiedAt:   updatedAt.Time,
			LastSyncedAt: lastChecked.Time,
			IsActive:     httpStatus == 200 || httpStatus == 0,
			MetadataJSON: fmt.Sprintf("{\"http_status\": %d}", httpStatus),
		}
		batch = append(batch, clip)

		if len(batch) >= 100 {
			if err := catalog.BulkUpsertClips(batch); err != nil {
				state.Errors = append(state.Errors, err)
			} else {
				state.MigratedSQL += len(batch)
			}
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		if err := catalog.BulkUpsertClips(batch); err != nil {
			state.Errors = append(state.Errors, err)
		} else {
			state.MigratedSQL += len(batch)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

const soundEffectsMetadataDriveFolderID = "1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW"

type exportedSoundEffect struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Filename       string          `json:"filename"`
	DriveFileID    string          `json:"drive_file_id"`
	DriveLink      string          `json:"drive_link"`
	DownloadLink   string          `json:"download_link"`
	LocalPath      string          `json:"local_path"`
	DurationSecond float64         `json:"duration_seconds"`
	Family         string          `json:"family"`
	Subtype        string          `json:"subtype"`
	Tags           string          `json:"tags"`
	FolderID       string          `json:"folder_id"`
	ParentFolderID string          `json:"parent_folder_id"`
	FolderPath     string          `json:"folder_path"`
	Metadata       json.RawMessage `json:"metadata"`
}

type soundEffectsMetadataExport struct {
	GeneratedAt string                           `json:"generated_at"`
	DriveRootID string                           `json:"drive_root_folder_id"`
	Total       int                              `json:"total"`
	ByFamily    map[string][]exportedSoundEffect `json:"by_family"`
}

func runExportSoundEffectsMetadata(args []string) error {
	_ = args
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Drive == nil || root.Drive.Publisher == nil {
		return fmt.Errorf("database and Drive publisher are required")
	}
	adminUpload, err := delivery.NewAdminUploadService(root.Drive.Publisher)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	rows, err := root.DB.DB.QueryContext(ctx, `
		SELECT id, name, filename, drive_file_id, drive_link, download_link,
		       local_path, COALESCE(duration_ms, 0),
		       COALESCE(json_extract(metadata_json, '$.sfx_family'), group_name, ''),
		       COALESCE(json_extract(metadata_json, '$.sfx_subtype'), ''),
		       tags, folder_id, parent_folder_id, folder_path, COALESCE(metadata_json, '{}')
		FROM media_assets
		WHERE source = 'sound_effect' AND lifecycle_state <> 'DELETED'
		ORDER BY COALESCE(json_extract(metadata_json, '$.sfx_family'), group_name, ''), name`)
	if err != nil {
		return fmt.Errorf("query sound effects: %w", err)
	}
	defer rows.Close()

	export := soundEffectsMetadataExport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		DriveRootID: soundEffectsMetadataDriveFolderID,
		ByFamily:    make(map[string][]exportedSoundEffect),
	}
	for rows.Next() {
		var item exportedSoundEffect
		var durationMS int64
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Filename, &item.DriveFileID, &item.DriveLink, &item.DownloadLink,
			&item.LocalPath, &durationMS, &item.Family, &item.Subtype, &item.Tags, &item.FolderID,
			&item.ParentFolderID, &item.FolderPath, &metadata); err != nil {
			return fmt.Errorf("scan sound effect metadata: %w", err)
		}
		item.DurationSecond = float64(durationMS) / 1000
		item.Metadata = json.RawMessage(metadata)
		family := strings.TrimSpace(item.Family)
		if family == "" {
			family = "uncategorized"
			item.Family = family
		}
		export.ByFamily[family] = append(export.ByFamily[family], item)
		export.Total++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sound effects: %w", err)
	}

	tmp, err := os.CreateTemp("", "sound_effects_metadata_*.json")
	if err != nil {
		return fmt.Errorf("create metadata temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(export); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close metadata file: %w", err)
	}

	filename := "sound_effects_metadata.json"
	result, err := adminUpload.Publish(ctx, delivery.AdminUploadCommand{
		LocalPath: filepath.Clean(tmpPath),
		FolderID:  soundEffectsMetadataDriveFolderID,
		Filename:  filename,
	})
	if err != nil {
		return fmt.Errorf("upload metadata JSON: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return fmt.Errorf("metadata upload completed without Drive file ID")
	}
	familyCount := len(export.ByFamily)
	fmt.Printf("uploaded sound effects metadata: total=%d families=%d file_id=%s link=%s\n", export.Total, familyCount, result.FileID, result.WebViewLink)
	return nil
}

package qdrant

import (
	"context"
	"path/filepath"
	"testing"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteAssetStore_FetchAssetAfterMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "media.sqlite")
	migrationsDir, err := filepath.Abs("../../../migrations/sqlite")
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}

	// TODO #8 (June 2026): scope-aware test — targetDB="primary"
	// matches the test body (inserts into media_assets, which is a
	// primary-only table).
	if err := storage.RunMigrationsOnDB(dbPath, nil, migrationsDir, "primary"); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}

	db, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		t.Fatalf("OpenSQLiteDB: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		INSERT INTO media_assets (
			id, source, name, tags, media_type, lifecycle_state,
			search_text, embedding_json, transcript_embedding, visual_embedding, audio_embedding,
			metadata_json, created_at, updated_at,
			youtube_video_id, youtube_url, start_time, end_time,
			workspace_id, channel_id, license, source_version, style
		) VALUES (
			?,
			'artlist',
			'FetchAsset smoke',
			'["tag-a","tag-b"]',
			'video',
			'ACTIVE',
			'fetch asset smoke text',
			'[1,2,3]',
			'[4,5,6]',
			'[7,8,9]',
			'[10,11,12]',
			'{"origin":"smoke"}',
			datetime('now'),
			datetime('now'),
			'yt-123',
			'https://www.youtube.com/watch?v=yt-123',
			'10.0',
			'20.0',
			'ws-1',
			'chan-9',
			'standard',
			'src-v1',
			'cinematic'
		)
	`, "asset-smoke")
	if err != nil {
		t.Fatalf("insert smoke asset: %v", err)
	}

	store := NewSQLiteAssetStore(db.DB)
	asset, err := store.FetchAsset(context.Background(), "asset-smoke")
	if err != nil {
		t.Fatalf("FetchAsset: %v", err)
	}
	if asset == nil {
		t.Fatal("FetchAsset returned nil asset")
	}
	if asset.LifecycleState != "ACTIVE" {
		t.Fatalf("FetchAsset lifecycle_state = %q, want ACTIVE", asset.LifecycleState)
	}
	if asset.YouTubeVideoID != "yt-123" || asset.YouTubeURL != "https://www.youtube.com/watch?v=yt-123" {
		t.Fatalf("FetchAsset youtube fields not populated: %+v", asset)
	}
	if asset.WorkspaceID != "ws-1" || asset.ChannelID != "chan-9" || asset.License != "standard" {
		t.Fatalf("FetchAsset canonical fields not populated: %+v", asset)
	}
	if len(asset.VisualVector) != 3 || len(asset.AudioVector) != 3 || len(asset.TranscriptVector) != 3 {
		t.Fatalf("FetchAsset embeddings not parsed: text=%d transcript=%d visual=%d audio=%d",
			len(asset.TextVector), len(asset.TranscriptVector), len(asset.VisualVector), len(asset.AudioVector))
	}
	if asset.SourceVersion != "src-v1" {
		t.Fatalf("FetchAsset source_version = %q, want src-v1", asset.SourceVersion)
	}
}

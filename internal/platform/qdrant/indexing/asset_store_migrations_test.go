package indexing

import (
	"context"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

func TestSQLiteAssetStore_FetchAssetAfterMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "media.sqlite")
	migrationsDir, err := filepath.Abs("../../../../migrations/sqlite")
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
			drive_file_id, drive_link,
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
			'drive-file-smoke',
			'https://drive.google.com/file/d/drive-file-smoke/view',
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
	if asset.DriveFileID != "drive-file-smoke" || asset.DriveLink != "https://drive.google.com/file/d/drive-file-smoke/view" {
		t.Fatalf("FetchAsset Drive location = (%q, %q), want canonical SQLite values", asset.DriveFileID, asset.DriveLink)
	}

	doc := assetToIndexDocumentNoValidate(asset, qdrantschema.DefaultV3Schema())
	payload := BuildPayloadFromDocument(doc, qdrantschema.DefaultV3Schema())
	if payload["drive_file_id"] != "drive-file-smoke" || payload["drive_link"] != "https://drive.google.com/file/d/drive-file-smoke/view" {
		t.Fatalf("SQLite→AssetData→IndexDocument→payload Drive location = (%v, %v), want canonical values", payload["drive_file_id"], payload["drive_link"])
	}
	if payload["lifecycle_state"] != "ACTIVE" {
		t.Fatalf("SQLite→Qdrant lifecycle_state = %v, want ACTIVE", payload["lifecycle_state"])
	}
}

func TestSQLiteAssetStore_ReindexableIDsSkipEmptyEmbeddings(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "media.sqlite")
	migrationsDir, err := filepath.Abs("../../../../migrations/sqlite")
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}

	if err := storage.RunMigrationsOnDB(dbPath, nil, migrationsDir, "primary"); err != nil {
		t.Fatalf("RunMigrationsOnDB: %v", err)
	}

	db, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		t.Fatalf("OpenSQLiteDB: %v", err)
	}
	defer db.Close()

	seed := func(id, embeddingJSON string) {
		t.Helper()
		metadataJSON := `{"content_hash":"hash-` + id + `"}`
		_, err := db.Exec(`
			INSERT INTO media_assets (
				id, source, name, media_type, namespace, asset_kind, source_type, lifecycle_state,
				search_text, embedding_json, metadata_json,
				created_at, updated_at
			) VALUES (
				?, 'artlist', ?, 'video', 'stock', 'stock_video', 'artlist', 'ACTIVE',
				'search text', ?, ?,
				datetime('now'), datetime('now')
			)
		`, id, id, embeddingJSON, metadataJSON)
		if err != nil {
			t.Fatalf("seed asset %s: %v", id, err)
		}
	}

	seed("asset-indexed", "[1,2,3]")
	seed("asset-empty", "[]")

	store := NewSQLiteAssetStore(db.DB)

	ids, err := store.ListAllAssetIDs(context.Background())
	if err != nil {
		t.Fatalf("ListAllAssetIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "asset-indexed" {
		t.Fatalf("ListAllAssetIDs = %v, want only asset-indexed", ids)
	}

	batch, err := store.FetchAssetBatch(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("FetchAssetBatch: %v", err)
	}
	if len(batch) != 1 {
		t.Fatalf("FetchAssetBatch len = %d, want 1", len(batch))
	}
	if batch[0].ID != "asset-indexed" {
		t.Fatalf("FetchAssetBatch[0].ID = %q, want asset-indexed", batch[0].ID)
	}
}

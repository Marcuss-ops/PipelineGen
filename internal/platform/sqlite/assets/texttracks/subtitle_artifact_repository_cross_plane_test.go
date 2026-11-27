package texttracks

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// DB-02: subtitle artifacts live on SQLite while their parents live on
// PostgreSQL. The table must therefore accept the cross-plane ids without a
// local parent row, and the repository must reject them when a PostgreSQL
// validator proves the parent is missing.
const crossPlaneArtifactsDDL = `
CREATE TABLE asset_subtitle_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    text_track_id INTEGER NOT NULL,
    language_code TEXT NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('ass', 'srt', 'vtt')),
    local_path TEXT NOT NULL,
    drive_file_id TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    cues_hash TEXT NOT NULL,
    clip_content_hash TEXT NOT NULL,
    cue_count INTEGER NOT NULL,
    clip_duration_ms INTEGER NOT NULL,
    last_cue_end_ms INTEGER NOT NULL,
    style_version TEXT NOT NULL,
    generator_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PENDING', 'READY', 'FAILED', 'STALE')),
    is_current INTEGER NOT NULL DEFAULT 1,
    validation_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    drive_url TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_current_clip_ass
ON asset_subtitle_artifacts(asset_id, language_code, format)
WHERE is_current = 1;`

func newCrossPlaneArtifactRepo(t *testing.T) (*SubtitleArtifactRepositorySQLite, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifacts.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(crossPlaneArtifactsDDL); err != nil {
		t.Fatalf("create artifacts table: %v", err)
	}
	repo, err := NewSubtitleArtifactRepository(db, zap.NewNop())
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	return repo, db
}

func crossPlaneArtifact(trackID int64) *detail.SubtitleArtifact {
	return &detail.SubtitleArtifact{
		AssetID:          "asset-from-postgres",
		TextTrackID:      trackID,
		LanguageCode:     "it",
		Format:           detail.SubtitleFormatASS,
		LocalPath:        "/tmp/artifact.ass",
		ContentSHA256:    "content",
		TextHash:         "text",
		CuesHash:         "cues",
		ClipContentHash:  "clip",
		CueCount:         2,
		ClipDurationMs:   14000,
		LastCueEndMs:     13700,
		StyleVersion:     "v1",
		GeneratorVersion: "g1",
		Status:           detail.SubtitleStatusReady,
		IsCurrent:        true,
	}
}

func TestSubtitleArtifactAcceptsCrossPlaneParentWithoutLocalRow(t *testing.T) {
	repo, db := newCrossPlaneArtifactRepo(t)
	ctx := context.Background()

	// No media_assets / asset_text_tracks table exists at all: the ids are
	// opaque references into the PostgreSQL media plane.
	if err := repo.Upsert(ctx, crossPlaneArtifact(3809)); err != nil {
		t.Fatalf("opaque cross-plane artifact must be accepted, got: %v", err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_subtitle_artifacts`).Scan(&rows); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if rows != 1 {
		t.Fatalf("artifact rows = %d, want 1", rows)
	}
}

func TestSubtitleArtifactValidatorRejectsMissingParent(t *testing.T) {
	repo, db := newCrossPlaneArtifactRepo(t)
	ctx := context.Background()
	repo.SetParentValidator(func(_ context.Context, assetID string, textTrackID int64) error {
		if assetID != "asset-from-postgres" {
			return errors.New("unexpected asset")
		}
		if textTrackID != 3809 {
			return errors.New("text track not found on PostgreSQL")
		}
		return nil
	})

	if err := repo.Upsert(ctx, crossPlaneArtifact(3809)); err != nil {
		t.Fatalf("valid parent must be accepted, got: %v", err)
	}
	if err := repo.Upsert(ctx, crossPlaneArtifact(999999)); err == nil {
		t.Fatal("missing PostgreSQL parent must be rejected by the validator")
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_subtitle_artifacts`).Scan(&rows); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rejected artifact must not be persisted; rows = %d", rows)
	}
}

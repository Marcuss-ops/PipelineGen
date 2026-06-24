// cmd/admin/backfill_artlist_media_type.go — AGENT-1 recovery (June 2026)
//
// Two-step backfill for Artlist clips whose `metadata_json.media_type`
// field is missing or stale (= "artlist" rather than "video"):
//
//  1. UPDATE media_assets.metadata_json to pin `media_type: "video"`,
//     guarded by a `--apply` flag (default: dry-run).
//  2. Upsert the patched clips into Qdrant via the canonical
//     `*qdrant.ClipIndexerAdapter`, guarded by `--qdrant`.
//
// Post-fix wiring (AGENT-1):
//
//   - `internal/config`         → `internal/infrastructure/config`
//   - `internal/storage`        → `internal/infrastructure/database`
//   - `internal/media/vectorstore` → `internal/infrastructure/qdrant`
//     The canonical `qdrant.NewService` is the single-arg stub that
//     pairs with `qdrant.NewClipIndexerAdapter(db, vectorSvc, cfg, log)`.
//     The adapter ultimately calls `vectorSvc.UpsertAsset(s)` which is
//     a stub returning "qdrant service not available" until the real
//     backend is re-introduced (commit d61068b3 stub-restore note).
//     Operators running with `--qdrant` against an offline service will
//     see the stub error in the report and the DB step will still have
//     succeeded — the pipeline remains usable as an audit/ops tool.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

func runBackfillArtlistMediaType(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	apply := false
	toQdrant := false
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "--apply":
			apply = true
		case "--qdrant":
			toQdrant = true
		}
	}

	ctx := cmdContext()
	dataDir := cfg.Storage.DataDir
	log.Info("opening media database", zap.String("data_dir", dataDir))

	// AGENT-1: the canonical SQLite opener in `internal/infrastructure/database`
	// (was internal/storage — retired). The path resolves to a Go package
	// named `storage` (Go-package-name vs directory-name drift retained
	// on purpose during consolidation); the import alias `storage` is
	// used here to match the convention from sibling cmd/admin/* files.
	// `OpenSQLiteDB` accepts a fully-qualified path; we resolve it from
	// cfg.Storage.PrimaryDBFullPath() so we don't carry the legacy
	// <DataDir>/media/media.db.sqlite concatenated string.
	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("failed to open media DB: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	// Step 1: Find all Artlist clips with media_type != 'video' in metadata_json
	rows, err := db.QueryContext(ctx, `
		SELECT id, metadata_json
		FROM media_assets
		WHERE source = 'artlist'
		  AND (json_extract(metadata_json, '$.media_type') IS NULL
		       OR json_extract(metadata_json, '$.media_type') = ''
		       OR json_extract(metadata_json, '$.media_type') = '"artlist"'
		       OR json_extract(metadata_json, '$.media_type') IS NOT NULL
		          AND json_extract(metadata_json, '$.media_type') != 'video')
	`)
	if err != nil {
		return fmt.Errorf("failed to query artlist clips: %w", err)
	}
	defer rows.Close()

	type clipRecord struct {
		id           string
		metadataJSON string
	}

	var clips []clipRecord
	for rows.Next() {
		var id, metaJSON string
		if err := rows.Scan(&id, &metaJSON); err != nil {
			log.Warn("failed to scan row", zap.Error(err))
			continue
		}
		clips = append(clips, clipRecord{id: id, metadataJSON: metaJSON})
	}

	if len(clips) == 0 {
		log.Info("no Artlist clips need media_type backfill — all already have media_type='video'")
		return nil
	}

	log.Info("found Artlist clips that need media_type backfill",
		zap.Int("count", len(clips)))

	if !apply {
		log.Info("DRY-RUN: pass --apply to update, --apply --qdrant to also upsert to Qdrant")
		return nil
	}

	// Step 2: Update metadata_json to include media_type: "video"
	updated := 0
	for _, clip := range clips {
		var meta map[string]any
		if err := json.Unmarshal([]byte(clip.metadataJSON), &meta); err != nil {
			meta = make(map[string]any)
		}
		meta["media_type"] = "video"
		updatedJSON, err := json.Marshal(meta)
		if err != nil {
			log.Warn("failed to marshal metadata for clip", zap.String("id", clip.id), zap.Error(err))
			continue
		}

		if _, err := db.ExecContext(ctx, `UPDATE media_assets SET metadata_json = ?, updated_at = datetime('now') WHERE id = ?`,
			string(updatedJSON), clip.id); err != nil {
			log.Warn("failed to update clip", zap.String("id", clip.id), zap.Error(err))
			continue
		}
		updated++
	}

	log.Info("updated clips in DB",
		zap.Int("updated", updated),
		zap.Int("total_found", len(clips)))

	if !toQdrant {
		log.Info("pass --qdrant to also upsert to Qdrant")
		return nil
	}

	// Step 3: Upsert to Qdrant via canonical ClipIndexerAdapter
	log.Info("starting Qdrant upsert for updated clips")
	clipIDs := make([]string, 0, len(clips))
	for _, clip := range clips {
		clipIDs = append(clipIDs, clip.id)
	}

	if err := upsertArtlistClipsToQdrant(ctx, db, cfg, log, clipIDs); err != nil {
		return fmt.Errorf("Qdrant upsert failed: %w", err)
	}

	log.Info("backfill complete",
		zap.Int("total_updated", updated),
		zap.Int("qdrant_upserted", len(clipIDs)))

	return nil
}

// upsertArtlistClipsToQdrant reads the given clip IDs from DB and upserts
// them to Qdrant via the canonical `qdrant.ClipIndexerAdapter`. Mirrors
// the wiring in `internal/app/composition.go::BuildProcessBundle`:
// qdrant.NewService(cfg) + qdrant.NewClipIndexerAdapter(db, svc, cfg, log).
//
// AGENT-1: the legacy code reconstructed the full Qdrant HTTP client and
// retry policy plumbing here. Post-fix we delegate to the canonical
// adapter; the stub form of `qdrant.NewService` (1-arg Config) is what
// `internal/infrastructure/qdrant/service.go` currently exposes.
func upsertArtlistClipsToQdrant(ctx context.Context, db *sql.DB, cfg *config.Config, log *zap.Logger, clipIDs []string) error {
	qdrantCfg := qdrant.Config{
		Enabled: cfg.VectorSearch.Enabled,
	}

	vectorSvc := qdrant.NewService(qdrantCfg)
	adapter := qdrant.NewClipIndexerAdapter(db, vectorSvc, qdrantCfg, log)

	if err := adapter.UpsertFromClips(ctx, clipIDs); err != nil {
		return fmt.Errorf("batch upsert to Qdrant: %w", err)
	}

	return nil
}

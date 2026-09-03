// migrate-media-text-tracks-once copies the legacy transcript catalog into
// the PostgreSQL media SSOT. It is intentionally one-way and idempotent:
// asset IDs and transcript identity are preserved, while PostgreSQL owns the
// surrogate track/segment IDs.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	src := flag.String("sqlite-dsn", "data/media/media.db.sqlite?_journal_mode=WAL", "legacy SQLite media DB")
	dst := flag.String("postgres-dsn", "", "PostgreSQL media SSOT DSN")
	flag.Parse()
	if *dst == "" {
		log.Fatal("--postgres-dsn is required")
	}
	ctx := context.Background()
	sdb, err := sql.Open("sqlite3", *src)
	if err != nil {
		log.Fatal(err)
	}
	defer sdb.Close()
	pdb, err := sql.Open("pgx", *dst)
	if err != nil {
		log.Fatal(err)
	}
	defer pdb.Close()
	if err := sdb.PingContext(ctx); err != nil {
		log.Fatal(err)
	}
	if err := pdb.PingContext(ctx); err != nil {
		log.Fatal(err)
	}

	rows, err := sdb.QueryContext(ctx, `SELECT id, asset_id, language_code, text_kind,
text_content, source_type, source_language_code, is_original, provider,
model_name, model_version, prompt_version, text_hash, source_version,
translation_key, is_current, source_track_id, source_text_hash, confidence,
status, created_at, updated_at FROM asset_text_tracks ORDER BY id`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	trackIDs := map[int64]int64{}
	count := 0
	for rows.Next() {
		var id, isOriginal, isCurrent int64
		var sourceTrackID sql.NullInt64
		var assetID, lang, kind, content, sourceType, sourceLang, provider, model, modelVersion, prompt, hash, sourceVersion, translationKey, sourceTextHash, status, createdAt, updatedAt string
		var confidence sql.NullFloat64
		if err := rows.Scan(&id, &assetID, &lang, &kind, &content, &sourceType, &sourceLang, &isOriginal, &provider, &model, &modelVersion, &prompt, &hash, &sourceVersion, &translationKey, &isCurrent, &sourceTrackID, &sourceTextHash, &confidence, &status, &createdAt, &updatedAt); err != nil {
			log.Fatal(err)
		}
		var pgID int64
		err := pdb.QueryRowContext(ctx, `INSERT INTO asset_text_tracks
(asset_id, language_code, text_kind, text_content, source_type, source_language_code,
 is_original, provider, model_name, model_version, prompt_version, text_hash,
 source_version, translation_key, is_current, source_track_id, source_text_hash,
 confidence, status, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULL,$16,$17,$18,$19,$20)
ON CONFLICT (asset_id, language_code, text_kind) WHERE is_current=1 DO UPDATE SET
 text_content=EXCLUDED.text_content, source_type=EXCLUDED.source_type,
 source_language_code=EXCLUDED.source_language_code, is_original=EXCLUDED.is_original,
 provider=EXCLUDED.provider, model_name=EXCLUDED.model_name,
 model_version=EXCLUDED.model_version, prompt_version=EXCLUDED.prompt_version,
 text_hash=EXCLUDED.text_hash, source_version=EXCLUDED.source_version,
 translation_key=EXCLUDED.translation_key, source_text_hash=EXCLUDED.source_text_hash,
 confidence=EXCLUDED.confidence, status=EXCLUDED.status, updated_at=EXCLUDED.updated_at
RETURNING id`, assetID, lang, kind, content, sourceType, sourceLang, isOriginal, provider, model, modelVersion, prompt, hash, sourceVersion, translationKey, isCurrent, sourceTextHash, confidenceValue(confidence), status, createdAt, updatedAt).Scan(&pgID)
		if err != nil {
			log.Fatal(err)
		}
		trackIDs[id] = pgID
		count++
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	segRows, err := sdb.QueryContext(ctx, `SELECT track_id, sequence_no, start_ms, end_ms, text, text_hash FROM asset_text_track_segments ORDER BY track_id, sequence_no`)
	if err != nil {
		log.Fatal(err)
	}
	segments := 0
	for segRows.Next() {
		var sourceTrack, seq, start, end int64
		var text, textHash string
		if err := segRows.Scan(&sourceTrack, &seq, &start, &end, &text, &textHash); err != nil {
			log.Fatal(err)
		}
		pgTrack, ok := trackIDs[sourceTrack]
		if !ok {
			continue
		}
		if _, err := pdb.ExecContext(ctx, `INSERT INTO asset_text_track_segments(track_id, sequence_no, start_ms, end_ms, text, text_hash)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (track_id, sequence_no) DO UPDATE SET start_ms=EXCLUDED.start_ms, end_ms=EXCLUDED.end_ms, text=EXCLUDED.text, text_hash=EXCLUDED.text_hash`, pgTrack, seq, start, end, text, textHash); err != nil {
			log.Fatal(err)
		}
		segments++
	}
	if err := segRows.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("media transcript migration OK: tracks=%d segments=%d\n", count, segments)
}

func confidenceValue(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

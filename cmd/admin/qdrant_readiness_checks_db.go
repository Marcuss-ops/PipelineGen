// cmd/admin/qdrant_readiness_checks_db.go — SQL-related readiness checks
// extracted from qdrant_readiness_checks.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #1).
//
// Owns: checkDeadLetter, checkSQLiteReader, checkLegacyAudit.
package main

import (
	"context"
	"fmt"
	"strings"
)

// ── SQL-related checks ────────────────────────────────────────────────

func checkDeadLetter(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.DB == nil {
		return checkStatus{Err: "db is nil (legacy: dead_letter check needs a real *sql.DB)"}
	}
	if !tableExists(ctx, deps.DB, "outbox_events") {
		return checkStatus{Err: "outbox_events table missing"}
	}
	var dead int
	if err := deps.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE status = 'DEAD'`).Scan(&dead); err != nil {
		return checkStatus{Err: "outbox_events DEAD count query failed: " + err.Error()}
	}
	if dead > 0 {
		return checkStatus{Err: fmt.Sprintf("outbox_events has %d DEAD entries (expected 0)", dead)}
	}
	return checkStatus{Pass: true}
}

// checkSQLiteReader: production-shaped.
func checkSQLiteReader(_ context.Context, deps readinessDeps) checkStatus {
	if deps.DB == nil {
		return checkStatus{Err: "raw *sql.DB is nil — production SQLite reader missing"}
	}
	if deps.Root == nil || deps.Root.ClipsRepo == nil {
		return checkStatus{Err: "production ClipsRepo (root.Repos.ClipsRepository) is nil"}
	}
	if !tableExists(context.Background(), deps.DB, "media_assets") {
		return checkStatus{Err: "media_assets table missing"}
	}
	return checkStatus{Pass: true}
}

// checkLegacyAudit: preserved from pre-PR-15 with channel-matrix-aware
// SQL aggregate semantics.
func checkLegacyAudit(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.DB == nil {
		return checkStatus{Err: "db is nil"}
	}
	var nonMedia int
	if err := deps.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_assets
		WHERE COALESCE(media_type, '') NOT IN ('video', 'image', 'audio', 'document', 'folder', 'text')`).Scan(&nonMedia); err != nil {
		return checkStatus{Err: "non-media count query failed: " + err.Error()}
	}
	if nonMedia > 0 {
		return checkStatus{Err: fmt.Sprintf("non_media_assets=%d (expected 0)", nonMedia)}
	}
	channelSQL := map[string]struct {
		col  string
		dim  int
		mime string
	}{
		"text":       {"embedding_json", 768, "video, image, audio"},
		"transcript": {"transcript_embedding", 768, "video, audio"},
		"visual":     {"visual_embedding", 768, "video, image"},
		"audio":      {"audio_embedding", 512, "audio"},
	}
	for ch, spec := range channelSQL {
		mediaList := strings.Split(spec.mime, ", ")
		quoted := make([]string, len(mediaList))
		for i, m := range mediaList {
			quoted[i] = fmt.Sprintf("'%s'", strings.TrimSpace(m))
		}
		mediaFilter := strings.Join(quoted, ", ")
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM media_assets
			WHERE COALESCE(media_type, '') IN (%s)
			AND COALESCE(%s, '') NOT IN ('', '[]', '{}')
			AND json_array_length(%s) != %d`, mediaFilter, spec.col, spec.col, spec.dim)
		var n int
		if err := deps.DB.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return checkStatus{Err: fmt.Sprintf("%s invalid-vector count query failed: %s", ch, err.Error())}
		}
		if n > 0 {
			return checkStatus{Err: fmt.Sprintf("invalid_%s_vectors=%d (expected 0; channel requires %d-dim)", ch, n, spec.dim)}
		}
	}
	return checkStatus{Pass: true}
}

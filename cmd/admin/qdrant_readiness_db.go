// cmd/admin/qdrant_readiness_db.go — SQL query helpers for the qdrant
// production-shaped readiness gate (PR 15, June 2026).
//
// Split rationale (Commit E, July 2026): the canonical readiness
// orchestrator (qdrant_readiness.go) owns the production-shaped
// composition-root bridge (appInitCompositionForReadiness), the 9-key
// readiness check registry, the JSON/KEY=VALUE report serialization,
// the channel-matrix helpers (isChannelRequiredForMediaType,
// parseVectorLen, hasLegacyLocatorKey), and the qdrant probe+schema
// helpers. This sibling owns the 3 SQL-pure inspection helpers used
// during the readiness scan:
//
//   - inspectRequiredColumns: PRAGMA table_info(media_assets) +
//     case-insensitive lookup against the REQUIRED-COLUMNS list
//     (audio_embedding, youtube_video_id, ...). Returns present +
//     missing columns. Used by the sqlite_required_columns check.
//   - collectReadinessCounters: SELECT media_assets scan that
//     populates the legacy flat-fields on the readiness report
//     (TotalAssets, NonMediaAssets, InvalidXxxVectors, MissingSourceFile,
//     LegacyStatusRows, LegacyLocatorRows). Used by the
//     legacy_cleanup_clean check.
//   - tableExists: SELECT COUNT(*) FROM sqlite_master probe
//     restricted to type='table' AND name=? — used by the outbox
//     gate before the dead_letter check runs.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The REQUIRED-COLUMNS list used by inspectRequiredColumns is
//     the canonical readiness contract; it must mirror the schema
//     declared in internal/infrastructure/database/sqlite/.../migrations.
//     If a required column is dropped/renamed there, this list MUST be
//     updated in lockstep — this query reads production shape by SSOT
//     contract, no inline schema is re-derived here.
//   - The 768-dim vector shape required by parseVectorLen (called
//     from collectReadinessCounters) is the canonical pipeline embedding
//     dimensionality for text/transcript/visual channels and 512 for
//     audio. If the embedding model is swapped, the dim reference
//     here is updated alongside any model-bump migration.
//
// godlike/07 honest lock:
//   - The required-columns mismatch simply adds to the counter and
//     surfaces in the schema_errors field. It does NOT crash the
//     gate — operators want to see ALL failing checks in one run.
//   - collectReadinessCounters' SELECT ignores the row id (the
//     discard via `_ = id` is intentional — the id is scanned but
//     only metadata is read). Not swallowed, just unused here.
//
// Sibling-file constraint (Commit E user spec): this file lives in
// cmd/admin (package main), NOT in internal/infrastructure. The
// helpers are one-shot-CLI-only SQL queries. Promoting them to
// internal/infrastructure would force a typed-port interface that
// no other consumer uses — a "dead interface" anti-pattern per
// the PipelineGen architecture rules. Keep them here, period.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func inspectRequiredColumns(ctx context.Context, db *sql.DB, required []string) ([]string, []string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(media_assets)`)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect media_assets columns: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &defaultValue, &pk); err != nil {
			return nil, nil, fmt.Errorf("scan pragma table_info: %w", err)
		}
		seen[strings.ToLower(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	present := make([]string, 0, len(required))
	missing := make([]string, 0)
	for _, col := range required {
		if _, ok := seen[strings.ToLower(col)]; ok {
			present = append(present, col)
		} else {
			missing = append(missing, col)
		}
	}
	return present, missing, nil
}

func collectReadinessCounters(ctx context.Context, db *sql.DB, report *qdrantReadinessReport) error {
	rows, err := db.QueryContext(ctx, `
		SELECT
			id,
			COALESCE(media_type, ''),
			COALESCE(local_path, ''),
			COALESCE(embedding_json, ''),
			COALESCE(transcript_embedding, ''),
			COALESCE(visual_embedding, ''),
			COALESCE(audio_embedding, ''),
			COALESCE(status, ''),
			COALESCE(lifecycle_state, ''),
			COALESCE(metadata_json, '{}')
		FROM media_assets
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("readiness scan: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, mediaType, localPath, textJSON, transcriptJSON, visualJSON, audioJSON, status, lifecycleState, metaJSON string
		if err := rows.Scan(&id, &mediaType, &localPath, &textJSON, &transcriptJSON, &visualJSON, &audioJSON, &status, &lifecycleState, &metaJSON); err != nil {
			return fmt.Errorf("scan readiness row: %w", err)
		}
		_ = id
		report.TotalAssets++

		switch strings.ToLower(strings.TrimSpace(mediaType)) {
		case "video", "audio", "image":
		default:
			report.NonMediaAssets++
		}

		if isChannelRequiredForMediaType("text", mediaType) {
			if _, dim, err := parseVectorLen(textJSON); err != nil || dim != 768 {
				report.InvalidTextVectors++
			}
		}
		if isChannelRequiredForMediaType("transcript", mediaType) {
			if _, dim, err := parseVectorLen(transcriptJSON); err != nil || dim != 768 {
				report.InvalidTranscriptVectors++
			}
		}
		if isChannelRequiredForMediaType("visual", mediaType) {
			if _, dim, err := parseVectorLen(visualJSON); err != nil || dim != 768 {
				report.InvalidVisualVectors++
			}
		}
		if isChannelRequiredForMediaType("audio", mediaType) {
			if _, dim, err := parseVectorLen(audioJSON); err != nil || dim != 512 {
				report.InvalidAudioVectors++
			}
		}

		if strings.TrimSpace(localPath) == "" {
			report.MissingSourceFile++
		}
		if status != "" && !strings.EqualFold(status, lifecycleState) && lifecycleState != "" {
			report.LegacyStatusRows++
		}
		if hasLegacyLocatorKey(metaJSON) {
			report.LegacyLocatorRows++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate readiness rows: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	if db == nil {
		return false
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0
}

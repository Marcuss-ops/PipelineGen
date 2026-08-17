// Package database provides SQLite adapters for cmd/admin ports.
//
// Adapters here are intentionally CLI-local: they implement typed ports
// defined in cmd/admin/internal/ports without moving one-shot admin
// SQL into internal/infrastructure.
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/ports"
)

// SQLiteReadinessInspector is the SQLite implementation of
// ports.ReadinessInspector.
type SQLiteReadinessInspector struct {
	db *sql.DB
}

// NewReadinessInspector constructs a SQLite-backed readiness inspector.
func NewReadinessInspector(db *sql.DB) *SQLiteReadinessInspector {
	return &SQLiteReadinessInspector{db: db}
}

// Compile-time conformance check.
var _ ports.ReadinessInspector = (*SQLiteReadinessInspector)(nil)

// InspectRequiredColumns returns the columns from the supplied required
// list that are present in media_assets, and those that are missing.
func (r *SQLiteReadinessInspector) InspectRequiredColumns(ctx context.Context, required []string) (present, missing []string, err error) {
	rows, err := r.db.QueryContext(ctx, `PRAGMA table_info(media_assets)`)
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
		_ = cid
		_ = ctype
		_ = notNull
		_ = defaultValue
		_ = pk
		seen[strings.ToLower(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	present = make([]string, 0, len(required))
	missing = make([]string, 0)
	for _, col := range required {
		if _, ok := seen[strings.ToLower(col)]; ok {
			present = append(present, col)
		} else {
			missing = append(missing, col)
		}
	}
	return present, missing, nil
}

// CollectReadinessCounters scans media_assets and returns the canonical
// counters used by the readiness report.
func (r *SQLiteReadinessInspector) CollectReadinessCounters(ctx context.Context) (ports.ReadinessCounters, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			COALESCE(media_type, ''),
			COALESCE(local_path, ''),
			COALESCE(embedding_json, ''),
			COALESCE(transcript_embedding, ''),
			COALESCE(visual_embedding, ''),
			COALESCE(audio_embedding, ''),
			COALESCE(lifecycle_state, ''),
			COALESCE(metadata_json, '{}')
		FROM media_assets
		ORDER BY id`)
	if err != nil {
		return ports.ReadinessCounters{}, fmt.Errorf("readiness scan: %w", err)
	}
	defer rows.Close()

	var counters ports.ReadinessCounters
	for rows.Next() {
		var id, mediaType, localPath, textJSON, transcriptJSON, visualJSON, audioJSON, lifecycleState, metaJSON string
		if err := rows.Scan(&id, &mediaType, &localPath, &textJSON, &transcriptJSON, &visualJSON, &audioJSON, &lifecycleState, &metaJSON); err != nil {
			return ports.ReadinessCounters{}, fmt.Errorf("scan readiness row: %w", err)
		}
		_ = id

		counters.TotalAssets++

		switch strings.ToLower(strings.TrimSpace(mediaType)) {
		case "video", "audio", "image", "document", "folder", "text":
		default:
			counters.NonMediaAssets++
		}

		if isChannelRequiredForMediaType("text", mediaType) {
			if _, dim, err := parseVectorLen(textJSON); err != nil || dim != 768 {
				counters.InvalidTextVectors++
			}
		}
		if isChannelRequiredForMediaType("transcript", mediaType) {
			if _, dim, err := parseVectorLen(transcriptJSON); err != nil || dim != 768 {
				counters.InvalidTranscriptVectors++
			}
		}
		if isChannelRequiredForMediaType("visual", mediaType) {
			if _, dim, err := parseVectorLen(visualJSON); err != nil || dim != 768 {
				counters.InvalidVisualVectors++
			}
		}
		if isChannelRequiredForMediaType("audio", mediaType) {
			if _, dim, err := parseVectorLen(audioJSON); err != nil || dim != 512 {
				counters.InvalidAudioVectors++
			}
		}

		if strings.TrimSpace(localPath) == "" {
			counters.MissingSourceFile++
		}
		// The retired `status` column is intentionally absent from the
		// canonical media_assets schema. Legacy status rows are therefore
		// zero by construction; probing the removed column would make the
		// readiness gate fail on every current database.
		if hasLegacyLocatorKey(metaJSON) {
			counters.LegacyLocatorRows++
		}
	}
	if err := rows.Err(); err != nil {
		return ports.ReadinessCounters{}, fmt.Errorf("iterate readiness rows: %w", err)
	}
	return counters, nil
}

// TableExists reports whether the named table exists in the current
// database.
func (r *SQLiteReadinessInspector) TableExists(ctx context.Context, name string) bool {
	if r.db == nil {
		return false
	}
	var count int
	_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0
}

func isChannelRequiredForMediaType(channel, mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "video":
		return channel == "text" || channel == "transcript" || channel == "visual"
	case "image":
		return channel == "text" || channel == "visual"
	case "audio":
		return channel == "text" || channel == "transcript" || channel == "audio"
	}
	return false
}

func parseVectorLen(raw string) ([]float32, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil, 0, fmt.Errorf("empty vector")
	}
	var vec []float32
	if err := json.Unmarshal([]byte(raw), &vec); err != nil {
		return nil, 0, err
	}
	return vec, len(vec), nil
}

func hasLegacyLocatorKey(metaJSON string) bool {
	metaJSON = strings.TrimSpace(metaJSON)
	if metaJSON == "" || metaJSON == "{}" {
		return false
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return false
	}
	for _, key := range []string{"drive_link", "download_link", "drive_file_id", "local_path"} {
		if v, ok := meta[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}

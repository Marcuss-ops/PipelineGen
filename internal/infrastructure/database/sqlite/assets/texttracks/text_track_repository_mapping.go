// Package assets — text_track_repository_mapping.go
//
// TextTrackRepositorySQLite scan helpers. The reader-side projection
// from `asset_text_tracks` rows into domain `asset.TextTrack` values.
//
// PR-CATALOG-MULTILINGUA step 2 (July 2026): the SELECT/INSERT
// projections now carry the new source_track_id (FK back to the
// parent source-language track for audit-trail navigation) +
// source_text_hash (persisted source-text SHA-256) columns added
// by migration 156. scanTextTrack reads them into the domain
// TextTrack struct's SourceTrackID (nullable *int64) and
// SourceTextHash (string, ” when unset) fields.
package assets

import (
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// textTrackScanner abstracts sql.Row vs sql.Rows for scan.
type textTrackScanner interface {
	Scan(dest ...any) error
}

// scanTextTrack reads a single row from the asset_text_tracks
// SELECT projection into a domain asset.TextTrack value.
//
// The caller passes either an *sql.Row (single-row QueryRowContext)
// or an *sql.Rows (iteration with QueryContext). The behaviour
// is identical: column order, scan target list, and result
// mapping are bound together so a future maintainer who adds a
// column MUST update this function AND every place that constructs
// the SELECT projection (see text_track_repository_lookup.go).
func scanTextTrack(s textTrackScanner) (*asset.TextTrack, error) {
	var t asset.TextTrack
	var (
		id             int64
		assetID        string
		languageCode   string
		textKind       string
		textContent    string
		sourceType     string
		sourceLangCode string
		isOriginal     int
		provider       string
		modelName      string
		modelVersion   string
		promptVersion  string
		textHash       string
		sourceVersion  string
		translationKey string
		isCurrent      int
		// PR-CATALOG-MULTILINGUA step 2: nullable FK + persisted
		// source hash — read via sql.NullInt64 + string.
		sourceTrackID  sql.NullInt64
		sourceTextHash string
		confidence     sql.NullFloat64
		status         string
		createdAtStr   string
		updatedAtStr   string
	)

	err := s.Scan(
		&id, &assetID, &languageCode, &textKind,
		&textContent,
		&sourceType, &sourceLangCode, &isOriginal,
		&provider, &modelName, &modelVersion, &promptVersion,
		&textHash, &sourceVersion, &translationKey, &isCurrent,
		&sourceTrackID, &sourceTextHash,
		&confidence, &status,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}

	t.ID = id
	t.AssetID = assetID
	t.LanguageCode = languageCode
	t.TextKind = asset.TextTrackKind(textKind)
	t.TextContent = textContent
	t.SourceType = asset.TextTrackSource(sourceType)
	t.SourceLanguageCode = sourceLangCode
	t.IsOriginal = isOriginal == 1
	t.Provider = provider
	t.ModelName = modelName
	t.ModelVersion = modelVersion
	t.PromptVersion = promptVersion
	t.TextHash = textHash
	t.SourceVersion = sourceVersion
	t.TranslationKey = translationKey
	t.IsCurrent = isCurrent == 1
	// PR-CATALOG-MULTILINGUA step 2: source_track_id is a
	// nullable FK; only set the domain pointer when Valid
	// (NULL is semantically meaningful — it means "this row
	// IS the source" — see TextTrack.SourceTrackID doc).
	if sourceTrackID.Valid {
		v := sourceTrackID.Int64
		t.SourceTrackID = &v
	}
	t.SourceTextHash = sourceTextHash
	if confidence.Valid {
		v := confidence.Float64
		t.Confidence = &v
	}
	t.Status = asset.TextTrackStatus(status)

	if createdAtStr != "" {
		if parsed := timeutil.ParseRFC3339(createdAtStr); !parsed.IsZero() {
			t.CreatedAt = parsed
		}
	}
	if updatedAtStr != "" {
		if parsed := timeutil.ParseRFC3339(updatedAtStr); !parsed.IsZero() {
			t.UpdatedAt = parsed
		}
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}

	return &t, nil
}

// scanTextTrackRows is the *sql.Rows-flavored wrapper around
// scanTextTrack. It exists so callers (ListByAsset) can pass the
// rows-loop iterator directly without the overhead of an
// intermediate textTrackScanner interface assertion.
func scanTextTrackRows(rows *sql.Rows) (*asset.TextTrack, error) {
	return scanTextTrack(rows)
}

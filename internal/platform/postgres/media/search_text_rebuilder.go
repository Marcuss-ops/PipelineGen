// Package media — search_text_rebuilder.go: the multilingual
// search_text rebuild seam for the PostgreSQL media SSOT.
//
// Why this file exists (POSTGRES-MEDIA-CUTOVER follow-up, September 2026):
//
//	asset.text.materialize
//	  → Argos/Ollama translations
//	  → asset_text_tracks (READY, one row per configured language)
//	  → ???  ← the missing edge
//	  → media_assets.search_text
//	  → E5 multilingual embedding
//	  → media_embeddings
//
// The canonical clip commit writes search_text from the YouTube Step-9
// metadata envelope only (title / summary / hook / topics / source_url /
// speakers / mentioned_people). Transcript and channel are deliberately
// deferred. When the materializer later adds nine translated transcripts,
// nothing recomposed search_text, so the PostgreSQL index worker kept
// embedding the pre-translation text: the translations existed in the
// database but were invisible to semantic search — the exact
// "translated but not indexed" gap this file closes.
//
// SearchTextRebuilder recomposes the column from the asset's canonical
// metadata PLUS every READY transcript track (original + translations),
// through the SAME canonical strategy registry the indexing path uses
// (internal/platform/searchtext). One owner per fact: the YouTube
// search_text format is defined by youtubeStrategy and nowhere else.
//
// Idempotence: the composited text is a pure function of the asset row and
// its READY tracks, so a re-run with unchanged inputs is a no-op
// (changed=false) and never rewrites the row.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	platsearchtext "github.com/Marcuss-ops/PipelineGen/internal/platform/searchtext"
	"go.uber.org/zap"
)

// SearchTextRebuilder recomposes media_assets.search_text for one asset
// from its canonical metadata plus its READY transcript tracks. It is the
// production concrete of texttracks.SearchTextRebuilder.
type SearchTextRebuilder struct {
	db             *sql.DB
	builder        appsearchtext.SearchTextBuilder
	indexLanguages string
	log            *zap.Logger
}

// NewSearchTextRebuilder constructs the rebuilder against the PostgreSQL
// media SSOT handle. indexLanguages is the canonical comma-separated
// BCP-47 set the YouTube strategy filters transcript tracks by (empty =
// no filter). The strategy registry is the canonical one — never a local
// re-implementation.
func NewSearchTextRebuilder(db *sql.DB, indexLanguages string) *SearchTextRebuilder {
	if db == nil {
		panic("media.NewSearchTextRebuilder: db is required")
	}
	return &SearchTextRebuilder{
		db:             db,
		builder:        platsearchtext.NewRegistry(),
		indexLanguages: strings.TrimSpace(indexLanguages),
		log:            zap.NewNop(),
	}
}

// WithLogger attaches a logger (nil keeps the no-op default).
func (r *SearchTextRebuilder) WithLogger(log *zap.Logger) *SearchTextRebuilder {
	if r != nil && log != nil {
		r.log = log
	}
	return r
}

// rebuildAssetRow is the minimal read model the rebuilder needs. Keeping
// it local (rather than reusing MediaAssetRecord) keeps the read to one
// SELECT instead of hydrating the full record.
type rebuildAssetRow struct {
	id          string
	source      string
	mediaType   string
	name        string
	title       string
	category    string
	tags        string
	sourceURL   string
	metadataRaw string
	searchText  string
}

// Rebuild recomposes media_assets.search_text for assetID.
//
// Return contract:
//   - (false, nil) when the asset has no indexable composition, when the
//     composition equals the stored value, or when the stored value must
//     be preserved (never blanked).
//   - (true, nil) when the column was rewritten.
//   - (false, err) when the asset is unknown or the database read/write
//     failed.
func (r *SearchTextRebuilder) Rebuild(ctx context.Context, assetID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("search text rebuilder: not wired")
	}
	id := strings.TrimSpace(assetID)
	if id == "" {
		return false, fmt.Errorf("search text rebuilder: asset id is required")
	}

	asset, err := r.loadAssetRow(ctx, id)
	if err != nil {
		return false, err
	}
	tracks, err := r.loadReadyTranscripts(ctx, id)
	if err != nil {
		return false, err
	}

	input := r.buildInput(asset, tracks)
	text, err := r.builder.Build(ctx, input)
	if err != nil {
		return false, fmt.Errorf("search text rebuilder: compose asset %q: %w", id, err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		// Nothing indexable yet. Preserve whatever is stored — blanking the
		// column would silently destroy the commit-time envelope.
		return false, nil
	}
	if text == strings.TrimSpace(asset.searchText) {
		return false, nil
	}

	now := nowRFC3339()
	if _, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET search_text = $1, updated_at = $2, updated_at_ts = NULLIF($2, '')::timestamptz
		WHERE id = $3
	`, text, now, id); err != nil {
		return false, fmt.Errorf("search text rebuilder: update asset %q: %w", id, err)
	}
	r.log.Info("media.search_text_rebuilt",
		zap.String("asset_id", id),
		zap.Int("transcript_tracks", len(tracks)),
		zap.Int("chars", len(text)),
	)
	return true, nil
}

func (r *SearchTextRebuilder) loadAssetRow(ctx context.Context, assetID string) (*rebuildAssetRow, error) {
	var row rebuildAssetRow
	err := r.db.QueryRowContext(ctx, `
		SELECT id, source, COALESCE(media_type, ''), COALESCE(name, ''), COALESCE(title, ''),
		       COALESCE(category, ''), COALESCE(tags, ''), COALESCE(source_url, ''),
		       COALESCE(metadata_json, ''), COALESCE(search_text, '')
		FROM media_assets
		WHERE id = $1
	`, assetID).Scan(
		&row.id, &row.source, &row.mediaType, &row.name, &row.title,
		&row.category, &row.tags, &row.sourceURL,
		&row.metadataRaw, &row.searchText,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrMediaAssetNotFound, assetID)
	}
	if err != nil {
		return nil, fmt.Errorf("search text rebuilder: load asset %q: %w", assetID, err)
	}
	return &row, nil
}

// readyTranscript is one READY transcript track participating in the
// composition.
type readyTranscript struct {
	languageCode string
	text         string
	isOriginal   bool
}

func (r *SearchTextRebuilder) loadReadyTranscripts(ctx context.Context, assetID string) ([]readyTranscript, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT language_code, text_content, is_original
		FROM asset_text_tracks
		WHERE asset_id = $1
		  AND text_kind = 'transcript'
		  AND status = 'READY'
		  AND is_current = 1
		ORDER BY is_original DESC, language_code
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("search text rebuilder: load tracks for %q: %w", assetID, err)
	}
	defer rows.Close()
	var out []readyTranscript
	for rows.Next() {
		var t readyTranscript
		if err := rows.Scan(&t.languageCode, &t.text, &t.isOriginal); err != nil {
			return nil, fmt.Errorf("search text rebuilder: scan track row: %w", err)
		}
		if strings.TrimSpace(t.text) == "" {
			continue
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search text rebuilder: iterate tracks for %q: %w", assetID, err)
	}
	return out, nil
}

// buildInput projects the media row + READY transcripts onto the canonical
// search-text input. The Additional keys MUST match the ones youtubeStrategy
// reads (godlike/06: one key vocabulary, two producers).
func (r *SearchTextRebuilder) buildInput(asset *rebuildAssetRow, tracks []readyTranscript) appsearchtext.SearchTextInput {
	meta := decodeMetadataObject(asset.metadataRaw)

	entries := make([]appsearchtext.TextTrackEntry, 0, len(tracks))
	original := ""
	for _, t := range tracks {
		entries = append(entries, appsearchtext.TextTrackEntry{
			LanguageCode: t.languageCode,
			Text:         t.text,
			TextKind:     "transcript",
		})
		if t.isOriginal && original == "" {
			original = t.text
		}
	}

	additional := map[string]string{
		"hook":             metaString(meta, "hook"),
		"summary":          metaString(meta, "summary"),
		"topics":           metaJoined(meta, "topics"),
		"speakers":         metaJoined(meta, "speakers"),
		"mentioned_people": metaJoined(meta, "mentioned_people"),
		"source_url":       firstNonEmpty(asset.sourceURL, metaString(meta, "source_url")),
	}
	if r.indexLanguages != "" {
		additional["index_languages"] = r.indexLanguages
	}

	return appsearchtext.SearchTextInput{
		AssetID:          asset.id,
		Source:           asset.source,
		MediaType:        asset.mediaType,
		Title:            firstNonEmpty(asset.title, asset.name),
		Description:      metaString(meta, "description"),
		Transcript:       original,
		Tags:             decodeTagsJSON(asset.tags),
		Category:         asset.category,
		Channel:          metaString(meta, "source_channel"),
		DetectedEntities: metaStringSlice(meta, "detected_entities"),
		TextTracks:       entries,
		Additional:       additional,
	}
}

// decodeMetadataObject best-effort decodes metadata_json. Malformed input
// degrades to an empty map — a single legacy row must not fail the rebuild
// (the composition then falls back to the typed columns).
func decodeMetadataObject(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return map[string]any{}
	}
	return out
}

// metaString returns a scalar metadata_json value (empty when absent).
// Numeric values are formatted so a numeric metadata key never silently
// disappears from the composed text.
func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	switch value := meta[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	case bool:
		if value {
			return "true"
		}
		return ""
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

// metaJoined returns a metadata_json slice value space-joined. Both the
// native array form and a plain string are accepted.
func metaJoined(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	switch value := meta[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case []string:
		return strings.Join(value, " ")
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

// metaStringSlice returns a metadata_json string array (nil when absent).
func metaStringSlice(meta map[string]any, key string) []string {
	if meta == nil {
		return nil
	}
	switch value := meta[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// compile-time assertion: the concrete satisfies the texttracks port. The
// assertion lives in the texttracks package (which owns the contract); this
// local mirror documents the intent at the implementation site.
var _ interface {
	Rebuild(ctx context.Context, assetID string) (bool, error)
} = (*SearchTextRebuilder)(nil)

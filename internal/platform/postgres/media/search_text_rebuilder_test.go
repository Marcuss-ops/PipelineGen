// Package media — search_text_rebuilder_test.go: live PostgreSQL suite for
// the multilingual search_text rebuild seam.
//
// These tests are DSN-gated (see testmain_test.go). They pin the fact that
// turned the media-index gap into a real defect: translated transcripts were
// durably present in asset_text_tracks yet absent from media_assets.search_text,
// which is the ONLY text the PostgreSQL index worker embeds.
package media_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

const rebuildMetaJSON = `{"summary":"Broner interrupts","hook":"Stop worrying about Floyd",` +
	`"speakers":["Adrien Broner","Manny Pacquiao"],"mentioned_people":["Floyd Mayweather"],` +
	`"topics":["boxing","press conference"],"source_channel":"SHOWTIME Sports",` +
	`"description":"a boxing press conference"}`

func TestSearchTextRebuilder_ComposesMultilingualTranscripts(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const assetID = "rebuild-asset-1"
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, title, media_type, tags, source_url, metadata_json, search_text, created_at, updated_at)
		VALUES ($1, 'youtube', 'Sfuriata di Broner', 'Sfuriata di Broner', 'video', '["boxing"]', 'https://youtu.be/x', $2, 'step9 envelope', 'now', 'now')
	`, assetID, rebuildMetaJSON)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, is_original, status, is_current)
		VALUES
			($1, 'en', 'transcript', 'english transcript about boxing', 1, 'READY', 1),
			($1, 'it', 'transcript', 'frase italiana unica sul pugilato', 0, 'READY', 1),
			($1, 'fr', 'transcript', 'french pending text', 0, 'PENDING', 1),
			($1, 'en', 'description', 'a description row', 0, 'READY', 1)
	`, assetID)
	require.NoError(t, err)

	rebuilder := pgmedia.NewSearchTextRebuilder(db, "en,it,es")
	changed, err := rebuilder.Rebuild(ctx, assetID)
	require.NoError(t, err)
	require.True(t, changed, "the first rebuild must rewrite search_text")

	var text string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT search_text FROM media_assets WHERE id = $1`, assetID).Scan(&text))

	// The original transcript AND the translation must both be embedded.
	require.Contains(t, text, "english transcript about boxing")
	require.Contains(t, text, "frase italiana unica sul pugilato")

	// Superset contract: the historical Step-9 envelope fields survive the
	// rebuild (the index worker embeds this column verbatim).
	require.Contains(t, text, "Broner interrupts", "summary must survive the rebuild")
	require.Contains(t, text, "Stop worrying about Floyd", "hook must survive the rebuild")
	require.Contains(t, text, "Floyd Mayweather", "mentioned people must survive the rebuild")
	require.Contains(t, text, "SHOWTIME Sports", "channel must survive the rebuild")
	require.Contains(t, text, "https://youtu.be/x", "source_url must survive the rebuild")

	// Non-READY and non-transcript rows must not leak into the index text.
	require.NotContains(t, text, "french pending text", "a PENDING track must not be indexed")
	require.NotContains(t, text, "a description row", "only transcript tracks feed the multilingual index text")

	// Idempotence: an unchanged input set rewrites nothing.
	changedAgain, err := rebuilder.Rebuild(ctx, assetID)
	require.NoError(t, err)
	require.False(t, changedAgain, "a second rebuild with unchanged input must be a no-op")

	var textAfter string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT search_text FROM media_assets WHERE id = $1`, assetID).Scan(&textAfter))
	require.Equal(t, text, textAfter)
}

// TestSearchTextRebuilder_RespectsIndexLanguages pins the configured-language
// filter: a translation in a language the operator did not configure must not
// pollute the index text (the YouTube strategy filters on index_languages).
func TestSearchTextRebuilder_RespectsIndexLanguages(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const assetID = "rebuild-asset-2"
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, media_type, tags, metadata_json, search_text, created_at, updated_at)
		VALUES ($1, 'youtube', 'Solo Inglese', 'video', '[]', '{}', '', 'now', 'now')
	`, assetID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, is_original, status, is_current)
		VALUES
			($1, 'en', 'transcript', 'english only transcript', 1, 'READY', 1),
			($1, 'ru', 'transcript', 'russian transcript outside the configured set', 0, 'READY', 1)
	`, assetID)
	require.NoError(t, err)

	rebuilder := pgmedia.NewSearchTextRebuilder(db, "en,it")
	changed, err := rebuilder.Rebuild(ctx, assetID)
	require.NoError(t, err)
	require.True(t, changed)

	var text string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT search_text FROM media_assets WHERE id = $1`, assetID).Scan(&text))
	require.Contains(t, text, "english only transcript")
	require.NotContains(t, text, "russian transcript outside the configured set")
}

// TestSearchTextRebuilder_UnknownAssetFailsClosed pins godlike/07: a missing
// asset is a typed error, never a silent no-op.
func TestSearchTextRebuilder_UnknownAssetFailsClosed(t *testing.T) {
	db := newMediaTestDB(t)
	_, err := pgmedia.NewSearchTextRebuilder(db, "en").Rebuild(context.Background(), "does-not-exist")
	require.Error(t, err)
	require.ErrorIs(t, err, pgmedia.ErrMediaAssetNotFound)
}

// TestSearchTextRebuilder_PreservesExistingTextWhenNoTranscripts pins the
// "never blank a populated column" contract: an asset with a commit-time
// envelope but no transcript-bearing metadata still gets a canonical
// composition, and an asset with nothing indexable keeps its stored value.
func TestSearchTextRebuilder_PreservesExistingTextWhenNothingIndexable(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const assetID = "rebuild-asset-3"
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, media_type, metadata_json, search_text, created_at, updated_at)
		VALUES ($1, 'youtube', '', 'video', '{}', 'pre-existing envelope', 'now', 'now')
	`, assetID)
	require.NoError(t, err)

	changed, err := pgmedia.NewSearchTextRebuilder(db, "en").Rebuild(ctx, assetID)
	require.NoError(t, err)
	require.False(t, changed, "nothing indexable → nothing to rewrite")

	var text string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT search_text FROM media_assets WHERE id = $1`, assetID).Scan(&text))
	require.Equal(t, "pre-existing envelope", text, "the stored value must never be blanked")
}

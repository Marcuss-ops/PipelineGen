package clipindexer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// TestFetchClipSearchInputs_ColumnSearchTextPreferred proves that
// fetchClipSearchInputs reads search_text from the COLUMN first,
// falling back to metadata_json.search_text only when the column
// is empty.
//
// PR-QDRANT-SEARCH-TEXT-SOURCE-FIX (2026-07-09): the pre-fix query
// read ONLY from metadata_json.search_text, which is empty for YouTube
// clips (process_segment_step6to9 writes search_text to the column via
// clip_atomic_writer, not to metadata_json). This caused indexViaAPI
// to generate embeddings from `name` alone (e.g. "Round_7_Broner_barcolla")
// instead of the rich search text (title + summary + hook + topics +
// source_url + speakers + mentioned_people). The mismatch between
// computeContentHash (reads column) and fetchClipSearchInputs (read
// metadata_json) was the root cause of outbox-completed but empty
// Qdrant search results for YouTube clips.
func TestFetchClipSearchInputs_ColumnSearchTextPreferred(t *testing.T) {
	db := drive.NewMigratedTestDB(t)
	defer db.Close()

	ctx := context.Background()
	svc := NewService(&Config{Enabled: true}, &drive.SQLiteDB{DB: db}, ":memory:", zap.NewNop())

	// Sub-test 1: search_text in column only (YouTube clip pattern).
	// This is the exact scenario that was broken before the fix.
	t.Run("ColumnSearchText_Present_MetadataJsonEmpty", func(t *testing.T) {
		richText := "Pacquiao vs Broner press conference confrontation boxing training highlights"
		_, err := db.Exec(`INSERT INTO media_assets (id, name, source, tags, search_text, metadata_json, lifecycle_state, index_state)
			VALUES ('yt_col_only', 'Round_7_Broner_barcolla', 'youtube', '[]', ?, '{}', 'ACTIVE', 'DISCOVERED')`,
			richText)
		require.NoError(t, err)

		searchText, name, err := svc.fetchClipSearchInputs(ctx, "yt_col_only")
		require.NoError(t, err)
		assert.Equal(t, richText, searchText, "must read search_text from the column, not metadata_json")
		assert.Equal(t, "Round_7_Broner_barcolla", name)
	})

	// Sub-test 2: search_text in metadata_json only (Artlist pattern).
	// This is the existing working pattern — must continue to work.
	t.Run("MetadataJsonSearchText_Present_ColumnEmpty", func(t *testing.T) {
		richText := "Artlist clip ambient music background score"
		_, err := db.Exec(`INSERT INTO media_assets (id, name, source, tags, metadata_json, lifecycle_state, index_state)
			VALUES ('art_meta_only', 'Ambient Score', 'artlist', '[]', ?, 'ACTIVE', 'DISCOVERED')`,
			`{"search_text":"`+richText+`"}`)
		require.NoError(t, err)

		searchText, name, err := svc.fetchClipSearchInputs(ctx, "art_meta_only")
		require.NoError(t, err)
		assert.Equal(t, richText, searchText, "must fall back to metadata_json.search_text when column is empty")
		assert.Equal(t, "Ambient Score", name)
	})

	// Sub-test 3: both present — column wins.
	t.Run("BothPresent_ColumnWins", func(t *testing.T) {
		columnText := "column_text_wins"
		metaText := "meta_text_loses"
		_, err := db.Exec(`INSERT INTO media_assets (id, name, source, tags, search_text, metadata_json, lifecycle_state, index_state)
			VALUES ('yt_both', 'Test', 'youtube', '[]', ?, '{"search_text":"`+metaText+`"}', 'ACTIVE', 'DISCOVERED')`,
			columnText)
		require.NoError(t, err)

		searchText, _, err := svc.fetchClipSearchInputs(ctx, "yt_both")
		require.NoError(t, err)
		assert.Equal(t, columnText, searchText, "column search_text must take precedence over metadata_json")
	})

	// Sub-test 4: neither present — returns empty (no panic, no dangling fragment).
	t.Run("NeitherPresent_ReturnsEmpty", func(t *testing.T) {
		_, err := db.Exec(`INSERT INTO media_assets (id, name, source, tags, metadata_json, lifecycle_state, index_state)
			VALUES ('empty_clip', 'Empty Clip', 'youtube', '[]', '{}', 'ACTIVE', 'DISCOVERED')`)
		require.NoError(t, err)

		searchText, name, err := svc.fetchClipSearchInputs(ctx, "empty_clip")
		require.NoError(t, err)
		assert.Equal(t, "", searchText, "must return empty when neither source has search_text")
		assert.Equal(t, "Empty Clip", name)
	})
}

// Package assets — asset_committer_tags_test.go
//
// Pins the SQLite half of the METADATA THREADING BUG regression: the
// canonical commit must persist the request-provided Tags into the
// media_assets.tags column (JSON array) AND the derived tags_norm search
// string, and the typed Summary/Topics/Speakers/MentionedPeople/Tags must
// land in metadata_json — so the downstream indexer can reconstruct the
// Qdrant `tags`/`topics`/`mentioned_people`/`summary` payload keys.
package txmutation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// TestSQLiteAssetCommitter_PersistsRequestProvidedTagsAndSemanticFields
// pins the canonical commit boundary for the request-provided semantic
// surface: tags → media_assets.tags + tags_norm, and summary / topics /
// speakers / mentioned_people / tags → metadata_json.
func TestSQLiteAssetCommitter_PersistsRequestProvidedTagsAndSemanticFields(t *testing.T) {
	t.Parallel()

	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := persistence.CommitRequest{
		AssetID:        "yt_abc_0_60_v1",
		Source:         "youtube",
		Name:           "Clip Name",
		Filename:       "clip.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:content",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
		SearchText:     "input summary input-topic",
		Metadata: persistence.TypedMetadata{
			Summary:         "input summary",
			Tags:            []string{"tag-a", "tag-b"},
			Topics:          []string{"input-topic"},
			Speakers:        []string{"input-speaker"},
			MentionedPeople: []string{"input-person"},
		},
	}

	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex: %v", err)
	}

	// ── Tags column (canonical JSON array + normalized search string) ──
	var tags, tagsNorm string
	err := db.QueryRow(`SELECT tags, tags_norm FROM media_assets WHERE id = ?`, req.AssetID).
		Scan(&tags, &tagsNorm)
	require.NoError(t, err, "SELECT tags/tags_norm must succeed")
	require.JSONEq(t, `["tag-a","tag-b"]`, tags,
		"media_assets.tags must carry the request-provided tags verbatim as a JSON array")
	require.Equal(t, "tag-a tag-b", tagsNorm,
		"media_assets.tags_norm must be the lowercased space-joined tag list")

	// ── metadata_json semantic fields ──
	md := readMetadataJSON(t, db, req.AssetID)
	require.Contains(t, md, `"summary":"input summary"`,
		"metadata_json must persist the request-provided summary")
	require.Contains(t, md, `"topics":["input-topic"]`,
		"metadata_json must persist the request-provided topics")
	require.Contains(t, md, `"speakers":["input-speaker"]`,
		"metadata_json must persist the request-provided speakers")
	require.Contains(t, md, `"mentioned_people":["input-person"]`,
		"metadata_json must persist the request-provided mentioned_people")
	require.Contains(t, md, `"tags":["tag-a","tag-b"]`,
		"metadata_json must persist the request-provided tags")

	// ── search_text column ──
	var searchText string
	err = db.QueryRow(`SELECT search_text FROM media_assets WHERE id = ?`, req.AssetID).Scan(&searchText)
	require.NoError(t, err, "SELECT search_text must succeed")
	require.Equal(t, "input summary input-topic", searchText,
		"media_assets.search_text must carry the committed search text")
}

// TestSQLiteAssetCommitter_EmptyTagsWriteEmptyColumns pins the zero-value
// contract: a commit without tags must not crash the new tags/tags_norm
// columns nor emit a non-empty tags JSON.
func TestSQLiteAssetCommitter_EmptyTagsWriteEmptyColumns(t *testing.T) {
	t.Parallel()

	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := persistence.CommitRequest{
		AssetID:        "yt_abc_0_60_v1",
		Source:         "youtube",
		Filename:       "clip.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:content",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
	}

	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex without tags: %v", err)
	}

	var tags, tagsNorm string
	err := db.QueryRow(`SELECT tags, tags_norm FROM media_assets WHERE id = ?`, req.AssetID).
		Scan(&tags, &tagsNorm)
	require.NoError(t, err)
	require.Empty(t, tags, "empty tags must write an empty tags column (no fake availability)")
	require.Empty(t, tagsNorm, "empty tags must write an empty tags_norm column")
}

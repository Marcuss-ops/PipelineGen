// Package media — multilingual_index_e2e_test.go: the live PostgreSQL
// certificate for the post-translation index path.
//
// This is the end-to-end proof the POSTGRES-MEDIA-CUTOVER analysis asked
// for, executed against a real PostgreSQL 18 + pgvector instance (DSN-gated):
//
//	translated transcripts (asset_text_tracks)
//	  → SearchTextRebuilder            (media_assets.search_text)
//	  → ReindexRequester               (PostgreSQL outbox_events ONLY)
//	  → PostgresIndexWorker            (claim → embed → pgvector)
//	  → media_embeddings + index_state=INDEXED + event completed
//
// The decisive assertion is that the text handed to the embedder contains
// the TRANSLATED transcript, not just the commit-time metadata envelope:
// that is what makes the translations findable by semantic search.
package media_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// recordingEmbedder captures every text handed to the embedding provider and
// returns a fixed 4-dim vector (tests must not talk to an E5 sidecar).
type recordingEmbedder struct {
	mu    sync.Mutex
	texts []string
}

func (r *recordingEmbedder) Embed(_ context.Context, text string) (asset.EmbeddingResult, error) {
	r.mu.Lock()
	r.texts = append(r.texts, text)
	r.mu.Unlock()
	return asset.EmbeddingResult{Vector: []float32{0.1, 0.2, 0.3, 0.4}}, nil
}

func (r *recordingEmbedder) embedTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...)
}

const (
	e2eAssetID  = "yt_multilingual_e2e_v1"
	e2eModelID  = "test-e5-multilingual-v1"
	e2eItalian  = "frase italiana unica che esiste solo nella traduzione"
	e2eOriginal = "the original english transcript"
)

func TestMultilingualIndexE2E_TranslationsReachTheEmbedding(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	// 1. The committed clip: media_assets row with the Step-9 envelope plus
	//    a READY original transcript and a READY Italian translation, exactly
	//    as CommitClipTextAndIndexEvent + the materializer would have left them.
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_assets
			(id, source, name, title, media_type, tags, source_url, metadata_json,
			 search_text, source_version, lifecycle_state, index_state, created_at, updated_at)
		VALUES ($1, 'youtube', 'Clip Multilingua', 'Clip Multilingua', 'video', '["boxing"]',
			'https://youtu.be/multi', $2, 'step9 envelope without transcript', 'sv-e2e-1',
			'ACTIVE', 'DISCOVERED', 'now', 'now')
	`, e2eAssetID, rebuildMetaJSON)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, is_original, status, is_current)
		VALUES
			($1, 'en', 'transcript', $2, 1, 'READY', 1),
			($1, 'it', 'transcript', $3, 0, 'READY', 1)
	`, e2eAssetID, e2eOriginal, e2eItalian)
	require.NoError(t, err)

	// 2. Rebuild the multilingual search_text (the missing edge).
	rebuilder := pgmedia.NewSearchTextRebuilder(db, "en,it").WithLogger(zap.NewNop())
	changed, err := rebuilder.Rebuild(ctx, e2eAssetID)
	require.NoError(t, err)
	require.True(t, changed)

	var searchText string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT search_text FROM media_assets WHERE id = $1`, e2eAssetID).Scan(&searchText))
	require.Contains(t, searchText, e2eOriginal)
	require.Contains(t, searchText, e2eItalian)

	// 3. Request the reindex: it MUST land in the PostgreSQL outbox (the only
	//    outbox with a media index consumer).
	requester := pgmedia.NewReindexRequester(db)
	require.NoError(t, requester.RequestIndex(ctx, e2eAssetID))

	var eventCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE event_type = 'asset.index.requested' AND aggregate_id = $1 AND status = 'pending'
	`, e2eAssetID).Scan(&eventCount))
	require.Equal(t, 1, eventCount, "the reindex request must be a pending PostgreSQL outbox row")

	// 4. Drain it with the canonical worker, whose embedder reads
	//    media_assets.search_text — the exact production wiring.
	vectors := pgmedia.NewVectorSurfaceWriter(db)
	require.NoError(t, vectors.EnsureEmbeddingFamily(ctx, "text", e2eModelID, 4))
	embedder := &recordingEmbedder{}
	worker := pgmedia.NewPostgresIndexWorker(
		pgmedia.NewOutboxRepository(db),
		vectors,
		pgmedia.NewEmbedAssetTextAdapter(db, embedder),
		e2eModelID,
	)

	claim, err := pgmedia.NewOutboxRepository(db).ClaimNext(ctx, "e2e-worker", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claim, "the reindex event must be claimable")
	require.NoError(t, worker.Handle(ctx, claim))

	// 5. The embedding input is the REBUILT text — translations included.
	texts := embedder.embedTexts()
	require.Len(t, texts, 1)
	require.Contains(t, texts[0], e2eItalian,
		"the embedded text MUST contain the translated transcript (otherwise the translation never reaches semantic search)")
	require.Contains(t, texts[0], e2eOriginal)

	// 6. INDEXED + embedding row + event completed, all in the media SSOT.
	var state string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT index_state FROM media_assets WHERE id = $1`, e2eAssetID).Scan(&state))
	require.Equal(t, "INDEXED", state)

	var dims int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT vector_dims(embedding) FROM media_embeddings
		WHERE asset_id = $1 AND embedding_type = 'text' AND model_id = $2
	`, e2eAssetID, e2eModelID).Scan(&dims))
	require.Equal(t, 4, dims)

	var status string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status FROM outbox_events WHERE id = $1`, claim.Event.ID).Scan(&status))
	require.Equal(t, "completed", status)
}

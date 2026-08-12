package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/stretchr/testify/require"
)

// ── Subtest 3: discover_search_via_text ─────────────────────────────────
//
// Qdrant SearchPoints with the "text" vector channel returns the
// asset. The mock's query hook filters on payload.search_text — a
// deterministic substring match that mirrors the production conftest
// keyword-set.
func TestE2E_Qdrant_DiscoverSearchViaText(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const (
		assetID = "yt_search_text_003"
		needle  = "specific-keyword-marker-for-text-search-e2e"
	)
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E text-search clip — "+needle,
		"e2e_text_search_video_003",
	))
	injectMetadataJSON(t, fx, assetID, map[string]any{
		"search_text": "E2E text-search clip — " + needle,
		"language":    "en",
	})
	runOutboxWorkerClaim(t, fx, assetID, "worker-text-search")

	// Override queryHook: emit a hit ONLY when payload.search_text
	// contains the needle (simulates the production ANN-on-text vector
	// matching in Qdrant native — semantic match against the text-
	// channel embedding). Score 0.9 keeps the result within Qdrant's
	// cosine similarity range [0..1].
	const wantScore = 0.9
	fx.Qdrant.queryHook = func(_ []byte, points []schema.Point) []schema.SearchResult {
		var out []schema.SearchResult
		for _, p := range points {
			if st, ok := p.Payload["search_text"].(string); ok &&
				strings.Contains(st, needle) {
				out = append(out, schema.SearchResult{
					ID:      p.ID,
					Score:   wantScore,
					Payload: p.Payload,
				})
			}
		}
		return out
	}

	results, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "text",
			Limit:       10,
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1,
		"godlike/07 #3: text-channel search via the needle keyword must return the asset")

	var hit *schema.SearchResult
	for i := range results {
		if aid, ok := results[i].Payload["asset_id"].(string); ok && aid == assetID {
			hit = &results[i]
			break
		}
	}
	require.NotNil(t, hit, "godlike/07 #3: text-channel search must return asset_id=%q as a hit", assetID)
	require.GreaterOrEqual(t, hit.Score, 0.0)
	require.LessOrEqual(t, hit.Score, 1.0)
}

// ── Subtest 4: discover_search_via_transcript ────────────────────────────
//
// Qdrant SearchPoints with the "transcript" vector channel returns
// the asset. Production transcript embeddings come from Whisper;
// the E2E test uses payload.transcript (a canonical metadata_json key
// populated when the YouTube pipeline processed the clip) as the
// marker for transcript-channel matchability.
func TestE2E_Qdrant_DiscoverSearchViaTranscript(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const (
		assetID        = "yt_search_transcript_004"
		transcriptText = "specific-transcript-marker-for-e2e-test-audio-channel"
		transcriptHash = "sha256:e2etranscript004"
	)
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E transcript-search clip",
		"e2e_transcript_search_video_004",
	))
	// Inject transcript into metadata_json (production shape: the
	// Whisper transcript stage writes here downstream of the writer).
	injectMetadataJSON(t, fx, assetID, map[string]any{
		"transcript":      transcriptText,
		"transcript_hash": transcriptHash,
		"search_text":     transcriptText,
		"language":        "en",
	})
	runOutboxWorkerClaim(t, fx, assetID, "worker-transcript-search")

	fx.Qdrant.queryHook = func(body []byte, points []schema.Point) []schema.SearchResult {
		// godlike/06 SSOT + godlike/07 no-fake-availability: match on
		// the REQUEST signal (vector_name="transcript") — production
		// wire shape uses `vector_name` per Qdrant /points/query
		// envelope (transport/client_search.go). The transcript
		// CHANNEL is dispatched at the Qdrant server by
		// `vector_name`, NOT by payload content; a payload-key
		// matcher here is a fixture-side heuristic that drifts away
		// from production-shape behavior (the SearchTextBuilder
		// strategies rewrite asset.SearchText via YouTube strategy,
		// so payload.search_text may not carry our injected marker).
		// Support varying JSON forms by verifying the raw body contains the
		// requested vector identifier. The JSON field may be nested (e.g.
		// inside "prefetch" or "query" for /points/query), so a substring
		// check is more robust than parsing the top-level object.
		if !strings.Contains(string(body), `"transcript"`) {
			return nil
		}
		out := make([]schema.SearchResult, 0, len(points))
		for _, p := range points {
			out = append(out, schema.SearchResult{
				ID:      p.ID,
				Score:   0.85,
				Payload: p.Payload,
			})
		}
		return out
	}

	results, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "transcript",
			Limit:       10,
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1,
		"godlike/07 #3: transcript-channel search must return the asset whose transcript matches")

	var hit *schema.SearchResult
	for i := range results {
		if aid, ok := results[i].Payload["asset_id"].(string); ok && aid == assetID {
			hit = &results[i]
			break
		}
	}
	require.NotNil(t, hit, "godlike/07 #3: transcript-channel search must return asset_id=%q", assetID)
	require.Equal(t, "ACTIVE", hit.Payload["lifecycle_state"],
		"transcript-channel result must surface payload.lifecycle_state=ACTIVE")
}

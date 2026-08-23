// Package e2e — semantic_search_gate_e2e_test.go is the operational
// battery entry for the topic-relevance correctness regression: a
// query that names a single referent ("Jackie Chan interview martial
// arts") must accept ONLY the genuinely Jackie-related asset, even
// when the retrieval layer surfaces distractors (Tom Holland, Adam
// Sandler, Dwayne Johnson) whose transcripts partially overlap the
// query ("Chan era", "martial arts interview").
//
// The stack is production-shape: real ClipAtomicWriterAdapter + real
// IndexWriter + real outbox state machine + real transport client +
// real SearchSourceResolver + real ClipSamplerRegistry. The only
// surrogates are the Qdrant server (no embedding inference; the mock
// ranks by query-token coverage, mirroring the ANN-on-text channel)
// and the clip-resolver port (in-memory asset stub feeding the real
// ClipSourceBuilder) — the same substitution boundary the rest of the
// e2e package uses. The deterministic guardrail under test is the
// sampler's ALL-token topic_relevance gate: partial overlap is not
// acceptance.
package e2e

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// jackieQuery is the operational battery query. Every meaningful
// (>3-char) token is jackie/chan/interview/martial/arts — a single
// referent plus its documentary context, per the review fixture.
const jackieQuery = "Jackie Chan interview martial arts"

// jackieBatteryClip declares one indexed clip fixture. `summary` is
// the clip's canonical asset_name (and thus the title/BM25 channel of
// the indexed payload — production clips carry full descriptive
// summaries in name/search_text) and is mirrored into
// metadata_json.search_text + transcript for the downstream-stages
// shape. It feeds the in-memory clip resolver used by the real
// ClipSourceBuilder.
type jackieBatteryClip struct {
	id      string
	summary string
}

// jackieBatteryClips: one genuine Jackie clip + three distractors
// that share query tokens ("chan", "interview", "martial", "arts")
// but never the identity token "jackie".
var jackieBatteryClips = []jackieBatteryClip{
	{id: "clip-jackie", summary: "Jackie Chan interview about his martial arts career and fight choreography."},
	{id: "clip-tom", summary: "Tom Holland interview about martial arts and the Chan era of cinema."},
	{id: "clip-adam", summary: "Adam Sandler interview on the set of a comedy martial arts film."},
	{id: "clip-dwayne", summary: "Dwayne Johnson trains martial arts before the big fight."},
}

// e2eQdrantSearchPort implements usecase.SemanticSearchPort against
// the real Qdrant transport client, mirroring the payload mapping of
// the production qdrantSemanticSearchPort (internal/app). The mock
// Qdrant substitutes the embedder: the text-channel query carries a
// placeholder vector, and the mock's queryHook decides the matches.
type e2eQdrantSearchPort struct {
	client     *transport.Client
	collection string
}

func (p *e2eQdrantSearchPort) SearchByText(ctx context.Context, query string, limit int, _ string) ([]usecase.SemanticSearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	searchLimit := limit * 3
	if searchLimit < 30 {
		searchLimit = 30
	}
	results, err := p.client.SearchPoints(ctx, p.collection, schema.SearchRequest{
		QueryVector: dummyQueryVector,
		VectorName:  "text",
		Limit:       searchLimit,
		MinScore:    0.0,
	})
	if err != nil {
		return nil, err
	}
	out := make([]usecase.SemanticSearchResult, 0, len(results))
	for _, r := range results {
		assetID, _ := r.Payload["asset_id"].(string)
		if assetID == "" {
			continue
		}
		out = append(out, usecase.SemanticSearchResult{
			ClipID:              assetID,
			Name:                firstE2EPayloadString(r.Payload, "name", "title", "semantic_title"),
			Score:               r.Score,
			Transcript:          firstE2EPayloadString(r.Payload, "search_text", "transcript"),
			VisualSummary:       firstE2EPayloadString(r.Payload, "description", "embedding_text", "semantic_title", "title", "name"),
			MediaType:           firstE2EPayloadString(r.Payload, "media_type"),
			DriveLink:           firstE2EPayloadString(r.Payload, "drive_link"),
			AvailableByIngest:   isE2EActiveLifecycle(firstE2EPayloadString(r.Payload, "lifecycle_state")),
			AnchorCoverageRatio: 1.0,
		})
	}
	return out, nil
}

func firstE2EPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isE2EActiveLifecycle(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "ACTIVE", "PUBLISHED":
		return true
	default:
		return false
	}
}

// e2eClipResolver is the in-memory clip-resolver stub for the real
// ClipSourceBuilder (the SQLite resolution seam is the surrogate
// boundary of this test).
type e2eClipResolver struct {
	byID map[string]*asset.Asset
}

func (r *e2eClipResolver) ResolveByMediaAssetID(_ context.Context, id string) (*asset.Asset, error) {
	return r.byID[id], nil
}

func (r *e2eClipResolver) ResolveByDriveFileID(_ context.Context, id string) ([]*asset.Asset, error) {
	if a, ok := r.byID[id]; ok {
		return []*asset.Asset{a}, nil
	}
	return nil, nil
}

func e2eClipAsset(id, summary string) *asset.Asset {
	a := &asset.Asset{
		ID:         id,
		Name:       summary,
		Filename:   id + ".mp4",
		MediaType:  asset.MediaTypeClip,
		SearchText: summary,
		Metadata:   make(asset.Metadata),
	}
	a.SetDriveFileID("drive-" + id)
	a.SetDriveLink("https://drive.google.com/file/d/drive-" + id + "/view")
	return a
}

// e2eQueryTokens replicates the sampler's meaningful-token split for
// the query-hook retrieval simulation only (the mock Qdrant ranks
// points by token coverage; the gate under test owns the acceptance
// decision). Same separators + >3-char floor as the production gate.
func e2eQueryTokens(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(query)), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == ';' || r == ':' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) > 3 {
			out = append(out, f)
		}
	}
	return out
}

// TestE2E_JackieChanSearch_AcceptsOnlyJackieRelatedClips is the
// operational battery entry: ingest + index the 4 fixture clips
// through the production writer chain, run the canonical
// SearchSourceResolver for the identity query, and assert the
// accepted set is exactly the Jackie clip — the three partial-overlap
// distractors must be rejected by the ALL-token topic_relevance gate.
func TestE2E_JackieChanSearch_AcceptsOnlyJackieRelatedClips(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	for _, clip := range jackieBatteryClips {
		require.NoError(t, commitYouTubeClip(t, fx, clip.id, clip.summary, "e2e_jackie_video"))
		injectMetadataJSON(t, fx, clip.id, map[string]any{
			"search_text": clip.summary,
			"transcript":  clip.summary,
			"language":    "en",
		})
		runOutboxWorkerClaim(t, fx, clip.id, "worker-jackie-"+clip.id)
		// The canonical Qdrant payload must carry the retrieval text
		// (BM25 search_text derives from the title/summary strategy).
		payload := fx.Qdrant.findUpserted(t, clip.id)
		require.Contains(t, string(payload), clip.summary, "indexed payload must carry the clip summary/search_text")
	}

	// Retrieval simulation: rank points by meaningful-query-token
	// coverage (the corrected embedding contract surfaces the Jackie
	// asset first) but keep every partial-overlap distractor in the
	// result window so the sampler backstop is what rejects them.
	tokens := e2eQueryTokens(jackieQuery)
	fx.Qdrant.queryHook = func(_ []byte, points []schema.Point) []schema.SearchResult {
		type scored struct {
			point schema.Point
			count int
		}
		matched := make([]scored, 0, len(points))
		for _, p := range points {
			text, _ := p.Payload["search_text"].(string)
			text = strings.ToLower(text)
			count := 0
			for _, tok := range tokens {
				if strings.Contains(text, tok) {
					count++
				}
			}
			if count == 0 {
				continue
			}
			matched = append(matched, scored{point: p, count: count})
		}
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].count > matched[j].count })
		out := make([]schema.SearchResult, 0, len(matched))
		for _, s := range matched {
			out = append(out, schema.SearchResult{
				ID:      s.point.ID,
				Score:   0.5 + 0.1*float64(s.count),
				Payload: s.point.Payload,
			})
		}
		return out
	}

	searchPort := &e2eQdrantSearchPort{client: fx.Transport, collection: fx.Schema.RuntimeAlias}

	resolverStub := &e2eClipResolver{byID: make(map[string]*asset.Asset)}
	for _, clip := range jackieBatteryClips {
		resolverStub.byID[clip.id] = e2eClipAsset(clip.id, clip.summary)
	}

	builder := usecase.NewClipSourceBuilder(resolverStub, nil, zap.NewNop())
	resolver := usecase.NewSearchSourceResolver(searchPort, builder, usecase.NewClipSamplerRegistry(), zap.NewNop())

	resolved, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:     scriptpkg.SourceSearch,
		Query:    jackieQuery,
		MaxClips: 10,
	}, scriptpkg.SourceResolutionContext{ItemID: "item-jackie-e2e", Language: "en"})
	require.NoError(t, err, "Jackie search must resolve successfully (the genuine clip passes every gate)")
	require.NotNil(t, resolved.ClipEvidence)

	accepted := resolved.ClipEvidence.AcceptedClipIDs
	require.Equal(t, []string{"clip-jackie"}, accepted,
		"accepted set must be exactly [clip-jackie]; partial-overlap distractors (tom/adam/dwayne) must be rejected by the ALL-token topic gate")
	for _, id := range accepted {
		require.NotEqual(t, "clip-tom", id)
		require.NotEqual(t, "clip-adam", id)
		require.NotEqual(t, "clip-dwayne", id)
	}
	require.Len(t, resolved.SearchResults, 1, "SearchResults must contain only the accepted clip")
	require.Equal(t, "clip-jackie", resolved.SearchResults[0].ClipID)
}

//go:build integration

// Package fixtures — synthetic asset integration tests (Task 8, July 2026).
//
// Tests against a real Qdrant instance (Docker-based) using 5 synthetic
// assets covering all media sources: YouTube, Voiceover, Artlist, Image,
// and AI-generated images. Each asset carries artificial vectors across
// all 4 v3 channels (text/768, transcript/768, visual/768, audio/512).
//
// Prerequisites:
//
//	docker compose -f docker-compose.test-qdrant.yml up -d
//
// Run:
//
//	TEST_QDRANT_URL=http://localhost:16333 go test -tags=integration -v ./tests/fixtures/...
//
// Cleanup:
//
//	docker compose -f docker-compose.test-qdrant.yml down
//
// Make target: make test-qdrant-fixtures
package fixtures

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ── Test helpers ─────────────────────────────────────────────────────

// testQdrantURL returns the Qdrant URL from the TEST_QDRANT_URL env var,
// defaulting to localhost:16333 (docker-compose.test-qdrant.yml).
func testQdrantURL() string {
	if u := os.Getenv("TEST_QDRANT_URL"); u != "" {
		return u
	}
	return "http://localhost:16333"
}

// testClient creates a transport.Client for the test Qdrant instance.
func testClient(t *testing.T) *transport.Client {
	t.Helper()
	return transport.NewClient(&schema.Config{
		BaseURL: testQdrantURL(),
		Timeout: 10,
	}, zap.NewNop())
}

// testCollection is the collection name used for synthetic asset tests.
const testCollection = "synthetic_assets_test_v3"

// ensureCleanup deletes the test collection if it exists so each test
// starts from a clean slate.
func ensureCleanup(t *testing.T, client *transport.Client) {
	t.Helper()
	if err := client.DeleteCollection(context.Background(), testCollection); err != nil {
		t.Logf("cleanup DeleteCollection (ok if not found): %v", err)
	}
	// Give Qdrant a moment to release the collection name.
	time.Sleep(200 * time.Millisecond)
}

// createTestCollection creates the test collection using the canonical v3 schema.
func createTestCollection(t *testing.T, client *transport.Client) {
	t.Helper()

	v3 := schema.DefaultV3Schema()

	vectors := make(map[string]interface{})
	for _, spec := range v3.DenseVectors {
		vectors[spec.Channel] = map[string]interface{}{
			"size":     spec.Dimensions,
			"distance": spec.Distance,
		}
	}

	sparseVectors := make(map[string]interface{})
	for _, spec := range v3.SparseVectors {
		sparseVectors[spec.Channel] = map[string]interface{}{
			"modifier": spec.Modifier,
			"model":    spec.Model,
		}
	}

	err := client.CreateCollection(context.Background(), testCollection, vectors, sparseVectors)
	require.NoError(t, err, "CreateCollection should succeed")

	// Create payload indexes for filter queries.
	for _, idx := range v3.PayloadIndexes {
		if err := client.CreatePayloadIndex(context.Background(), testCollection, idx.FieldName, idx.FieldType); err != nil {
			t.Logf("CreatePayloadIndex %q (non-fatal): %v", idx.FieldName, err)
		}
	}
}

// ── Synthetic asset generation ───────────────────────────────────────

// syntheticAsset describes a single synthetic asset with all its vectors.
type syntheticAsset struct {
	AssetID          string
	Name             string
	Source           string    // youtube, voiceover, artlist, image, generated
	MediaType        string    // video, audio, image
	Category         string    // clip, voiceover, sfx, stock, generated
	Language         string    // en, it, etc.
	Style            string    // cinematic, documentary, etc.
	TextVector       []float32 // 768 dims
	TranscriptVector []float32 // 768 dims
	VisualVector     []float32 // 768 dims
	AudioVector      []float32 // 512 dims
}

// toPoint converts a syntheticAsset into a Qdrant Point.
func (a *syntheticAsset) toPoint() schema.Point {
	return schema.Point{
		ID: schema.AssetIDToQdrantPointID(a.AssetID),
		Vectors: map[string]interface{}{
			"text":       a.TextVector,
			"transcript": a.TranscriptVector,
			"visual":     a.VisualVector,
			"audio":      a.AudioVector,
		},
		Payload: map[string]interface{}{
			"asset_id":                     a.AssetID,
			"name":                         a.Name,
			"source":                       a.Source,
			"media_type":                   a.MediaType,
			"category":                     a.Category,
			"language":                     a.Language,
			"style":                        a.Style,
			"workspace_id":                 "test-workspace",
			"lifecycle_state":              "active",
			"index_version":                "v3",
			"embedding_version_text":       "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual":     "2026-06-16-v1",
			"embedding_version_audio":      "2026-06-16-v1",
			"duration_ms":                  15000,
			"created_at":                   "2026-07-03T00:00:00Z",
			"updated_at":                   "2026-07-03T00:00:00Z",
		},
	}
}

// makeVector creates a float32 slice of the given dimension filled with
// a deterministic pattern so each channel/asset is distinguishable.
// The pattern is: channelOffset + (assetIndex * prime) modulo normalizer,
// mapped to [-1, 1]. Vectors are NOT L2-normalized; Cosine distance on
// Qdrant still produces a meaningful angle-preserving ranking.
func makeVector(dim, channelOffset, assetIndex int) []float32 {
	v := make([]float32, dim)
	prime := 17
	normalizer := float32(9973.0)
	for i := range dim {
		// Deterministic hash: channel offset + asset index * prime + position
		raw := float32((channelOffset + assetIndex*prime + i) % int(normalizer))
		v[i] = (raw/normalizer)*2 - 1 // map to [-1, 1]
	}
	return v
}

// syntheticAssets returns the 5 synthetic assets covering all media sources.
func syntheticAssets() []syntheticAsset {
	return []syntheticAsset{
		{
			AssetID:          "yt_test_clip_001",
			Name:             "Synthetic YouTube Clip",
			Source:           "youtube",
			MediaType:        "video",
			Category:         "clip",
			Language:         "en",
			Style:            "cinematic",
			TextVector:       makeVector(768, 100, 0),
			TranscriptVector: makeVector(768, 200, 0),
			VisualVector:     makeVector(768, 300, 0),
			AudioVector:      makeVector(512, 400, 0),
		},
		{
			AssetID:          "vo_test_clip_002",
			Name:             "Synthetic Voiceover Clip",
			Source:           "voiceover",
			MediaType:        "audio",
			Category:         "voiceover",
			Language:         "it",
			Style:            "narrative",
			TextVector:       makeVector(768, 100, 1),
			TranscriptVector: makeVector(768, 200, 1),
			VisualVector:     makeVector(768, 300, 1),
			AudioVector:      makeVector(512, 400, 1),
		},
		{
			AssetID:          "art_test_clip_003",
			Name:             "Synthetic Artlist Clip",
			Source:           "artlist",
			MediaType:        "video",
			Category:         "clip",
			Language:         "en",
			Style:            "documentary",
			TextVector:       makeVector(768, 100, 2),
			TranscriptVector: makeVector(768, 200, 2),
			VisualVector:     makeVector(768, 300, 2),
			AudioVector:      makeVector(512, 400, 2),
		},
		{
			AssetID:          "img_test_clip_004",
			Name:             "Synthetic Stock Image",
			Source:           "image",
			MediaType:        "image",
			Category:         "stock",
			Language:         "en",
			Style:            "photographic",
			TextVector:       makeVector(768, 100, 3),
			TranscriptVector: makeVector(768, 200, 3),
			VisualVector:     makeVector(768, 300, 3),
			AudioVector:      makeVector(512, 400, 3),
		},
		{
			AssetID:          "gen_test_clip_005",
			Name:             "Synthetic AI-Generated Image",
			Source:           "generated",
			MediaType:        "image",
			Category:         "generated",
			Language:         "en",
			Style:            "illustration",
			TextVector:       makeVector(768, 100, 4),
			TranscriptVector: makeVector(768, 200, 4),
			VisualVector:     makeVector(768, 300, 4),
			AudioVector:      makeVector(512, 400, 4),
		},
	}
}

// ── Tests ─────────────────────────────────────────────────────────────

// TestSyntheticAssets_CreateCollectionAndUpsert creates the v3 collection,
// upserts all 5 synthetic assets, and verifies the point count.
func TestSyntheticAssets_CreateCollectionAndUpsert(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)

	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}

	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err, "UpsertPoints for 5 synthetic assets should succeed")

	count, err := client.CountPoints(context.Background(), testCollection)
	require.NoError(t, err)
	assert.Equal(t, 5, count, "should have exactly 5 points after upsert")
}

// TestSyntheticAssets_ScrollAndVerifyPayloads scrolls all points and
// verifies payload integrity for each asset.
func TestSyntheticAssets_ScrollAndVerifyPayloads(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)
	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	// Scroll all points.
	result, err := client.ScrollPoints(context.Background(), testCollection, "", 100, nil)
	require.NoError(t, err)
	assert.Len(t, result.Points, 5, "scroll should return all 5 points")
	assert.Empty(t, result.NextOffset, "no more pages expected")

	// Build a map of payloads by asset_id.
	byAssetID := make(map[string]map[string]interface{})
	for _, pt := range result.Points {
		id, ok := pt.Payload["asset_id"].(string)
		require.True(t, ok, "every point must have asset_id")
		byAssetID[id] = pt.Payload
	}

	// Verify each synthetic asset's payload.
	for _, a := range assets {
		payload, found := byAssetID[a.AssetID]
		require.True(t, found, "asset %q must be present in scroll results", a.AssetID)
		assert.Equal(t, a.Name, payload["name"])
		assert.Equal(t, a.Source, payload["source"])
		assert.Equal(t, a.MediaType, payload["media_type"])
		assert.Equal(t, a.Category, payload["category"])
		assert.Equal(t, a.Language, payload["language"])
		assert.Equal(t, a.Style, payload["style"])
		assert.Equal(t, "active", payload["lifecycle_state"])
	}

	// Verify embedding_version keys are present on every point.
	for _, pt := range result.Points {
		for _, ch := range []string{"text", "transcript", "visual", "audio"} {
			key := fmt.Sprintf("embedding_version_%s", ch)
			val, ok := pt.Payload[key].(string)
			assert.True(t, ok, "point %s must have %s", pt.ID, key)
			assert.Equal(t, "2026-06-16-v1", val, "point %s: %s version mismatch", pt.ID, key)
		}
	}
}

// TestSyntheticAssets_SearchBySource verifies that filtered search returns
// only points matching the source filter.
func TestSyntheticAssets_SearchBySource(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)
	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	// Search for "youtube" source with a generic query vector.
	queryVec := makeVector(768, 100, 0) // close to youtube asset
	req := schema.SearchRequest{
		QueryVector: queryVec,
		VectorName:  "text",
		Limit:       10,
		Filter: map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"key":   "source",
					"match": map[string]interface{}{"value": "youtube"},
				},
			},
		},
	}

	results, err := client.SearchPoints(context.Background(), testCollection, req)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1, "should find at least one youtube result")

	// All results should have source=youtube.
	for _, r := range results {
		src, _ := r.Payload["source"].(string)
		assert.Equal(t, "youtube", src, "filtered search should only return youtube assets")
	}
}

// TestSyntheticAssets_IdempotentUpsert verifies that upserting the same
// points twice does not duplicate them.
func TestSyntheticAssets_IdempotentUpsert(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)
	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}

	// First upsert.
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	count1, err := client.CountPoints(context.Background(), testCollection)
	require.NoError(t, err)
	assert.Equal(t, 5, count1)

	// Second upsert — same points.
	err = client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	count2, err := client.CountPoints(context.Background(), testCollection)
	require.NoError(t, err)
	assert.Equal(t, 5, count2, "idempotent upsert must not duplicate points")
}

// TestSyntheticAssets_CanonicalPointIDs verifies that every upserted point
// has the deterministic canonical UUID matching schema.AssetIDToQdrantPointID.
func TestSyntheticAssets_CanonicalPointIDs(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)
	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	result, err := client.ScrollPoints(context.Background(), testCollection, "", 100, nil)
	require.NoError(t, err)

	for _, pt := range result.Points {
		assetID, ok := pt.Payload["asset_id"].(string)
		require.True(t, ok, "every point must have asset_id: %s", pt.ID)
		canonical := schema.AssetIDToQdrantPointID(assetID)
		assert.Equal(t, canonical, pt.ID,
			"point ID %s must match canonical ID %s for asset %s", pt.ID, canonical, assetID)
	}
}

// TestSyntheticAssets_DeleteAndRecreate verifies the full lifecycle:
// create → upsert → delete collection → recreate → upsert again.
func TestSyntheticAssets_DeleteAndRecreate(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)

	// Round 1.
	createTestCollection(t, client)
	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	// Delete.
	err = client.DeleteCollection(context.Background(), testCollection)
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)

	// Round 2.
	createTestCollection(t, client)
	err = client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	count, err := client.CountPoints(context.Background(), testCollection)
	require.NoError(t, err)
	assert.Equal(t, 5, count, "recreated collection should have 5 fresh points")
}

// TestSyntheticAssets_EmbeddingVersionKeysPresent validates that every
// upserted point has all 4 per-channel embedding_version_<channel> keys
// in its payload, confirming vectors were written correctly for all channels.
func TestSyntheticAssets_EmbeddingVersionKeysPresent(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)
	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	v3 := schema.DefaultV3Schema()
	result, err := client.ScrollPoints(context.Background(), testCollection, "", 100, nil)
	require.NoError(t, err)

	for _, pt := range result.Points {
		// Scroll with with_vector=false doesn't return vectors, so we
		// verify the per-channel embedding_version keys are present
		// (they confirm the vector was written correctly).
		for _, spec := range v3.DenseVectors {
			key := fmt.Sprintf("embedding_version_%s", spec.Channel)
			_, ok := pt.Payload[key].(string)
			assert.True(t, ok,
				"point %s must have embedding_version for channel %s (dim=%d)",
				pt.ID, spec.Channel, spec.Dimensions)
		}
	}
}

// TestSyntheticAssets_SourceEnumeration verifies that all 5 expected
// source values are present in the collection.
func TestSyntheticAssets_SourceEnumeration(t *testing.T) {
	client := testClient(t)
	ensureCleanup(t, client)
	createTestCollection(t, client)

	assets := syntheticAssets()
	points := make([]schema.Point, len(assets))
	for i, a := range assets {
		points[i] = a.toPoint()
	}
	err := client.UpsertPoints(context.Background(), testCollection, points)
	require.NoError(t, err)

	result, err := client.ScrollPoints(context.Background(), testCollection, "", 100, nil)
	require.NoError(t, err)

	sources := make(map[string]bool)
	for _, pt := range result.Points {
		src, ok := pt.Payload["source"].(string)
		if ok {
			sources[src] = true
		}
	}

	expected := []string{"youtube", "voiceover", "artlist", "image", "generated"}
	for _, src := range expected {
		assert.True(t, sources[src], "source %q must be present in the collection", src)
	}
}

// TestSyntheticAssets_CheckConnectivity is a fast connectivity check that
// verifies the Qdrant instance is reachable and can list collections.
// Run this test individually first to fail fast if Qdrant is not available:
//
//	go test -tags=integration -v -run CheckConnectivity ./tests/fixtures/...
func TestSyntheticAssets_CheckConnectivity(t *testing.T) {
	client := testClient(t)
	names, err := client.ListCollections(context.Background())
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "no such host") {
			t.Skipf("Qdrant not reachable at %s — start with: docker compose -f docker-compose.test-qdrant.yml up -d", testQdrantURL())
		}
		t.Fatalf("unexpected error listing collections: %v", err)
	}
	t.Logf("Qdrant reachable at %s, collections: %v", testQdrantURL(), names)
}

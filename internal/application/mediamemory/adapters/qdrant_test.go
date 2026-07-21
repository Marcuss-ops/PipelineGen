// Package adapters - qdrant_test.go pins the Phase 2.1 contract
// for QdrantIndexer + QdrantSemanticLookup.
//
//  1. QdrantIndexer.IndexConcept delegates to
//     search.EmbeddingChannelRegistry.EmbedQuery per channel
//     in search.CanonicalChannelNames order, gracefully skips
//     ErrChannelNotConfigured / ErrChannelNotApplicable, and
//     writes a single Qdrant point at the canonical collection
//     name (qdrantschema.ConceptCollectionName).
//
//  2. QdrantSemanticLookup.LookupByConcept issues a
//     HybridSearchPoints call against the canonical collection,
//     filters with the language + (optionally) concept_type
//     MUST-clauses, and projects one MediaCandidate per
//     (Qdrant-hit, approved-binding) tuple via batched
//     ListApprovedByConcepts.
//
//  3. A paraphrase round-trip via httptest server recomputes
//     scores via cosine(query, hit) so the test validates real
//     ANN-style recall (not just wire-shape echo).
package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// canonicalDenseDim is the canonical conceptIndex dense-vector dim.
const canonicalDenseDim = 768

// mockEmbedder satisfies search.EmbeddingChannelRegistry with
// deterministic per-text vectors. unmapped text returns a zero
// vector so the Indexer fail-closed guard isn't tripped
// incidentally.
type mockEmbedder struct {
	mu      sync.Mutex
	perText map[string][]float32
}

func newMockEmbedder() *mockEmbedder {
	return &mockEmbedder{perText: make(map[string][]float32)}
}

func (m *mockEmbedder) set(text string, vec []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.perText[text] = vec
}

func (m *mockEmbedder) EmbedQuery(_ context.Context, channel string, text string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if channel == search.ChannelSparse {
		return nil, search.ErrChannelNotApplicable
	}
	if v, ok := m.perText[text]; ok {
		return v, nil
	}
	out := make([]float32, canonicalDenseDim)
	return out, nil
}

// mockHit pairs a canned Qdrant SearchResult with the dense
// vector the mock server uses to compute response scores via
// cosine against the live query vector.
type mockHit struct {
	Result qdrantschema.SearchResult
	Vector []float32
}

// mockQdrantServer emulates /points and /points/query. State is
// in-memory; /points/query recomputes scores via cosine.
type mockQdrantServer struct {
	t      *testing.T
	srv    *httptest.Server
	points map[string]map[string]any
	mu     sync.Mutex
	hits   []mockHit
}

func newMockQdrantServer(t *testing.T, hits []mockHit) *mockQdrantServer {
	mq := &mockQdrantServer{
		t:      t,
		points: make(map[string]map[string]any),
		hits:   hits,
	}
	mq.srv = httptest.NewServer(http.HandlerFunc(mq.handle))
	return mq
}

func (mq *mockQdrantServer) URL() string { return mq.srv.URL }

func (mq *mockQdrantServer) Close() { mq.srv.Close() }

// handle routes PUT /points, POST /points/delete, POST /points/query.
func (mq *mockQdrantServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}

	// PUT /collections/{name}/points
	if r.Method == http.MethodPut && hasSuffix(r.URL.Path, "/points") {
		var req struct {
			Points []struct {
				ID      string         `json:"id"`
				Vector  map[string]any `json:"vector"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		coll := collectionFromPath(r.URL.Path)
		mq.mu.Lock()
		defer mq.mu.Unlock()
		bucket, ok := mq.points[coll]
		if !ok {
			bucket = make(map[string]any)
			mq.points[coll] = bucket
		}
		for _, p := range req.Points {
			bucket[p.ID] = p.Payload
		}
		fmt.Fprint(w, `{"result":{"status":"ok"}}`)
		return
	}

	if r.Method == http.MethodPost && hasSuffix(r.URL.Path, "/points/delete") {
		var req struct {
			Points []string `json:"points"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		coll := collectionFromPath(r.URL.Path)
		mq.mu.Lock()
		defer mq.mu.Unlock()
		bucket, ok := mq.points[coll]
		if ok {
			for _, id := range req.Points {
				delete(bucket, id)
			}
		}
		fmt.Fprint(w, `{"result":{"status":"ok"}}`)
		return
	}

	if r.Method == http.MethodPost && hasSuffix(r.URL.Path, "/points/query") {
		var req struct {
			Prefetch []map[string]any `json:"prefetch,omitempty"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		// Go's encoding/json decodes numeric arrays under map[string]any
		// as []interface{} with float64 elements.
		var qvec []float32
		if len(req.Prefetch) > 0 {
			if raw, ok := req.Prefetch[0]["query"].([]interface{}); ok {
				qvec = make([]float32, 0, len(raw))
				for _, x := range raw {
					if f, ok := x.(float64); ok {
						qvec = append(qvec, float32(f))
					}
				}
			}
		}
		mq.mu.Lock()
		entries := make([]map[string]any, 0, len(mq.hits))
		for _, h := range mq.hits {
			score := cosine(qvec, h.Vector)
			entry := map[string]any{
				"id":      h.Result.ID,
				"score":   score,
				"payload": h.Result.Payload,
			}
			entries = append(entries, entry)
		}
		mq.mu.Unlock()
		envelope := map[string]any{
			"result": map[string]any{"points": entries},
		}
		_ = json.NewEncoder(w).Encode(envelope)
		return
	}

	http.NotFound(w, r)
}

// collectionFromPath extracts the collection name from a
// /collections/{name}/... URL path segment.
func collectionFromPath(p string) string {
	const prefix = "/collections/"
	rest := p
	if rest[:min(len(rest), len(prefix))] == prefix {
		rest = rest[len(prefix):]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func hasSuffix(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// cosine returns cosine similarity, 0.0 for zero vectors.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Test 1: end-to-end paraphrase recall.
func TestQdrantRoundTripParaphraseRecall(t *testing.T) {
	ctx := context.Background()

	citiesVec := make([]float32, canonicalDenseDim)
	for i := range citiesVec {
		citiesVec[i] = float32(i+1) / float32(canonicalDenseDim)
	}
	// Trivially-orthogonal vector: negate citiesVec. Cosine
	// is exactly -1.0 regardless of magnitude (cosine is
	// invariant under positive scaling) so the test assertion
	// `< 0.5` reliably catches "non-paraphrase" misses.
	unrelatedVec := make([]float32, canonicalDenseDim)
	for i := range unrelatedVec {
		unrelatedVec[i] = -citiesVec[i]
	}

	embedder := newMockEmbedder()
	embedder.set("I Maya costruirono grandi citta", citiesVec)
	embedder.set("Le citta Maya erano avanzate", citiesVec)
	embedder.set("Le piramidi Maya dominavano il paesaggio", unrelatedVec)

	citiesConceptID := "concept-cities-001"
	unrelatedConceptID := "concept-temples-001"

	citiesPayload := map[string]any{
		"concept_id":         citiesConceptID,
		"language":           "it",
		"phrase_fingerprint": "fingerprint-cities-aaaa",
		"concept_type":       string(mediamemory.ConceptPhrase),
		"canonical_text":     "Le citta Maya erano avanzate",
		"normalized_text":    "le citta maya erano avanzate",
		"embedding_version":  qdrantschema.ConceptEmbeddingVersion,
	}
	unrelatedPayload := map[string]any{
		"concept_id":         unrelatedConceptID,
		"language":           "it",
		"phrase_fingerprint": "fingerprint-temples-bbbb",
		"concept_type":       string(mediamemory.ConceptPhrase),
		"canonical_text":     "Le piramidi Maya dominavano il paesaggio",
		"normalized_text":    "le piramidi maya dominavano il paesaggio",
		"embedding_version":  qdrantschema.ConceptEmbeddingVersion,
	}

	hits := []mockHit{
		{
			Result: qdrantschema.SearchResult{
				ID: "concept-" + citiesConceptID, Score: 0,
				Payload: citiesPayload,
			},
			Vector: citiesVec,
		},
		{
			Result: qdrantschema.SearchResult{
				ID: "concept-" + unrelatedConceptID, Score: 0,
				Payload: unrelatedPayload,
			},
			Vector: unrelatedVec,
		},
	}

	mq := newMockQdrantServer(t, hits)
	defer mq.Close()

	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)

	cr := newFakeConceptRepoMulti()
	cr.byID[citiesConceptID] = mediamemory.MediaConcept{
		ID: citiesConceptID, CanonicalText: "Le citta Maya erano avanzate",
		Language: "it", PhraseFingerprint: "fingerprint-cities-aaaa",
		ConceptType: mediamemory.ConceptPhrase,
	}
	cr.byID[unrelatedConceptID] = mediamemory.MediaConcept{
		ID: unrelatedConceptID, CanonicalText: "Le piramidi Maya dominavano il paesaggio",
		Language: "it", PhraseFingerprint: "fingerprint-temples-bbbb",
		ConceptType: mediamemory.ConceptPhrase,
	}

	br := newFakeBindingRepoMulti()
	br.bindingsByConcept[citiesConceptID] = []mediamemory.MediaBinding{
		{ID: "binding-cities-001", ConceptID: citiesConceptID,
			AssetID:        "asset-cities-001",
			SlotKind:       mediamemory.SlotPrimaryVideo,
			ApprovalStatus: mediamemory.ApprovalApproved},
	}
	br.bindingsByConcept[unrelatedConceptID] = []mediamemory.MediaBinding{
		{ID: "binding-temples-001", ConceptID: unrelatedConceptID,
			AssetID:        "asset-temples-001",
			SlotKind:       mediamemory.SlotPrimaryVideo,
			ApprovalStatus: mediamemory.ApprovalApproved},
	}

	lookup := NewQdrantSemanticLookup(client, embedder, cr, br, zap.NewNop())

	got, err := lookup.LookupByConcept(ctx, mediamemory.ConceptPhrase,
		"I Maya costruirono grandi citta", "it", 5)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byAsset := map[string]mediamemory.MediaCandidate{}
	for _, c := range got {
		byAsset[c.AssetID] = c
	}

	require.NotNil(t, byAsset["asset-cities-001"])
	assert.InDelta(t, 1.0, byAsset["asset-cities-001"].CandidateScore, 0.001,
		"cities paraphrase vs identical-vector hit MUST score ~1.0")
	assert.Equal(t, mediamemory.MaterializationCold, byAsset["asset-cities-001"].MaterializationStatus)
	assert.Equal(t, mediamemory.DiscoveryIndexed, byAsset["asset-cities-001"].DiscoveryStatus)
	assert.Equal(t, mediamemory.ProviderSemanticIndex, byAsset["asset-cities-001"].Provider)
	assert.True(t, mediamemory.IsKnownProvider(byAsset["asset-cities-001"].Provider))

	require.NotNil(t, byAsset["asset-temples-001"])
	assert.Less(t, byAsset["asset-temples-001"].CandidateScore, 0.5,
		"non-paraphrase hit MUST score < 0.5 cosine")
}

// Test 2: IndexConcept writes canonical point.
func TestQdrantIndexerIndexConceptWritesCanonicalPoint(t *testing.T) {
	ctx := context.Background()
	mq := newMockQdrantServer(t, nil)
	defer mq.Close()

	embedder := newMockEmbedder()
	vec := make([]float32, canonicalDenseDim)
	for i := range vec {
		vec[i] = float32(i+1) / float32(canonicalDenseDim)
	}
	embedder.set("I Maya costruirono grandi citta", vec)

	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	indexer := NewQdrantIndexer(client, embedder, zap.NewNop())

	concept := mediamemory.MediaConcept{
		ID:                "concept-1",
		CanonicalText:     "I Maya costruirono grandi citta",
		NormalizedText:    "i maya costruirono grandi citta",
		Language:          "it",
		PhraseFingerprint: "fingerprint-001",
		ConceptType:       mediamemory.ConceptPhrase,
		EmbeddingVersion:  qdrantschema.ConceptEmbeddingVersion,
	}

	require.NoError(t, indexer.IndexConcept(ctx, concept))

	mq.mu.Lock()
	defer mq.mu.Unlock()
	bucket, ok := mq.points[qdrantschema.ConceptCollectionName]
	require.True(t, ok)
	point, ok := bucket["concept-"+concept.ID]
	require.True(t, ok)
	pmap, ok := point.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "it", pmap["language"])
}

// Test 3: empty text fail-closed.
func TestQdrantIndexerRejectsEmptyCanonicalTextWithTypedSentinel(t *testing.T) {
	ctx := context.Background()
	mq := newMockQdrantServer(t, nil)
	defer mq.Close()

	embedder := newMockEmbedder()
	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	indexer := NewQdrantIndexer(client, embedder, zap.NewNop())

	err := indexer.IndexConcept(ctx, mediamemory.MediaConcept{
		ID: "concept-empty", Language: "it",
		ConceptType:       mediamemory.ConceptPhrase,
		PhraseFingerprint: "ff-empty",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, mediamemory.ErrInvalidPhrase))
}

// Test 4: nil-deps fail-closed (Indexer).
func TestQdrantIndexerNilDependenciesReturnsSemanticNotConfigured(t *testing.T) {
	ctx := context.Background()
	err := NewQdrantIndexer(nil, newMockEmbedder(), zap.NewNop()).
		IndexConcept(ctx, mediamemory.MediaConcept{ID: "x", CanonicalText: "y"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, mediamemory.ErrSemanticNotConfigured))

	err = NewQdrantIndexer(
		transport.NewClient(&qdrantschema.Config{BaseURL: "http://127.0.0.1:1", Timeout: 1}, zap.NewNop()),
		nil, zap.NewNop()).
		IndexConcept(ctx, mediamemory.MediaConcept{ID: "x", CanonicalText: "y"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, mediamemory.ErrSemanticNotConfigured))
}

// Test 5: nil-deps fail-closed (Lookup).
func TestQdrantSemanticLookupNilDependenciesReturnsSemanticNotConfigured(t *testing.T) {
	ctx := context.Background()
	s := NewQdrantSemanticLookup(nil, newMockEmbedder(),
		newFakeConceptRepoMulti(), newFakeBindingRepoMulti(), zap.NewNop())
	_, err := s.LookupByConcept(ctx, mediamemory.ConceptPhrase, "x", "it", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, mediamemory.ErrSemanticNotConfigured))
}

// Test 6: empty text fail-closed (Lookup).
func TestQdrantSemanticLookupEmptyTextReturnsInvalidPhrase(t *testing.T) {
	ctx := context.Background()
	s := NewQdrantSemanticLookup(
		transport.NewClient(&qdrantschema.Config{BaseURL: "http://127.0.0.1:1", Timeout: 1}, zap.NewNop()),
		newMockEmbedder(), newFakeConceptRepoMulti(),
		newFakeBindingRepoMulti(), zap.NewNop())
	_, err := s.LookupByConcept(ctx, mediamemory.ConceptPhrase, "", "it", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, mediamemory.ErrInvalidPhrase))
}

// Test 7: DeindexConcept rejects empty conceptID.
func TestDeindexConceptRejectsEmptyConceptID(t *testing.T) {
	mq := newMockQdrantServer(t, nil)
	defer mq.Close()
	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	indexer := NewQdrantIndexer(client, newMockEmbedder(), zap.NewNop())
	err := indexer.DeindexConcept(context.Background(), "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, mediamemory.ErrInvalidBindingInput))
}

// In-memory fakes.

type fakeConceptRepoMulti struct {
	byID map[string]mediamemory.MediaConcept
}

func newFakeConceptRepoMulti() *fakeConceptRepoMulti {
	return &fakeConceptRepoMulti{byID: make(map[string]mediamemory.MediaConcept)}
}

func (f *fakeConceptRepoMulti) Upsert(_ context.Context, c mediamemory.MediaConcept) (mediamemory.MediaConcept, error) {
	return c, nil
}
func (f *fakeConceptRepoMulti) FindByID(_ context.Context, id string) (mediamemory.MediaConcept, error) {
	if c, ok := f.byID[id]; ok {
		return c, nil
	}
	return mediamemory.MediaConcept{}, mediamemory.ErrConceptNotFound
}
func (f *fakeConceptRepoMulti) FindByFingerprint(_ context.Context, _, _ string) (mediamemory.MediaConcept, error) {
	return mediamemory.MediaConcept{}, mediamemory.ErrConceptNotFound
}
func (f *fakeConceptRepoMulti) FindManyByFingerprints(_ context.Context, _ string, _ []string) ([]mediamemory.MediaConcept, error) {
	return nil, nil
}

type fakeBindingRepoMulti struct {
	bindingsByConcept map[string][]mediamemory.MediaBinding
}

func newFakeBindingRepoMulti() *fakeBindingRepoMulti {
	return &fakeBindingRepoMulti{bindingsByConcept: make(map[string][]mediamemory.MediaBinding)}
}

func (f *fakeBindingRepoMulti) Upsert(_ context.Context, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error) {
	return b, nil
}
func (f *fakeBindingRepoMulti) FindByID(_ context.Context, _ string) (mediamemory.MediaBinding, error) {
	return mediamemory.MediaBinding{}, mediamemory.ErrBindingNotFound
}
func (f *fakeBindingRepoMulti) ListApprovedByConcept(_ context.Context, conceptID string, _ []mediamemory.SlotKind, limit int) ([]mediamemory.MediaBinding, error) {
	bs := f.bindingsByConcept[conceptID]
	if limit > 0 && len(bs) > limit {
		bs = bs[:limit]
	}
	return bs, nil
}
func (f *fakeBindingRepoMulti) ListApprovedByConcepts(_ context.Context, conceptIDs []string, _ []mediamemory.SlotKind, limit int) (map[string][]mediamemory.MediaBinding, error) {
	out := make(map[string][]mediamemory.MediaBinding, len(conceptIDs))
	for _, id := range conceptIDs {
		bs := f.bindingsByConcept[id]
		if limit > 0 && len(bs) > limit {
			bs = bs[:limit]
		}
		if len(bs) > 0 {
			out[id] = bs
		}
	}
	return out, nil
}
func (f *fakeBindingRepoMulti) ListByConcept(_ context.Context, conceptID string) ([]mediamemory.MediaBinding, error) {
	return f.bindingsByConcept[conceptID], nil
}
func (f *fakeBindingRepoMulti) ListByAsset(_ context.Context, _ string) ([]mediamemory.MediaBinding, error) {
	return nil, nil
}
func (f *fakeBindingRepoMulti) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeBindingRepoMulti) UpsertBindingTx(_ context.Context, _ *sql.Tx, b mediamemory.MediaBinding) (mediamemory.MediaBinding, error) {
	return b, nil
}
func (f *fakeBindingRepoMulti) DeleteBindingTx(_ context.Context, _ *sql.Tx, _ string) error {
	return nil
}

// Test 8: ReindexConcept bumps embedding_version while leaving
// the (concept_id, language, phrase_fingerprint) tuple invariant.
// godlike/06 SSOT (Level 0 cache independence under versioning):
// the canonical Level 0 lookup is ConceptRepository.FindByFingerprint
// keyed on (lang, fp). Re-bumping the version does NOT mutate
// phrase_fingerprint, so the Level 0 cache hit SURVIVES the bump.
// This test pins that contract: IndexConcept writes v1; ReindexConcept
// with targetVersion="" bumps to v2 at the SAME point ID with the
// SAME phrase_fingerprint; explicit targetVersion="v3" writes
// without auto-bump. Each step asserts the invariant in-place so
// the test fails closed the moment a future refactor breaks it.
func TestQdrantReindexConceptBumpsVersionAndPreservesLevel0Fingerprint(t *testing.T) {
	ctx := context.Background()
	mq := newMockQdrantServer(t, nil)
	defer mq.Close()

	embedder := newMockEmbedder()
	vec := make([]float32, canonicalDenseDim)
	for i := range vec {
		vec[i] = float32(i+1) / float32(canonicalDenseDim)
	}
	embedder.set("I Maya costruirono grandi citta", vec)

	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	indexer := NewQdrantIndexer(client, embedder, zap.NewNop())

	const conceptID = "concept-reindex-001"
	const fp = "fingerprint-reinv-aaaa"
	concept := mediamemory.MediaConcept{
		ID:                conceptID,
		CanonicalText:     "I Maya costruirono grandi citta",
		NormalizedText:    "i maya costruirono grandi citta",
		Language:          "it",
		PhraseFingerprint: fp,
		ConceptType:       mediamemory.ConceptPhrase,
		EmbeddingVersion:  "v1",
	}

	// Step 1: IndexConcept writes v1 + canonical (lang, fp) tuple
	// on the canonical collection's Qdrant point.
	require.NoError(t, indexer.IndexConcept(ctx, concept))

	mq.mu.Lock()
	bucket, ok := mq.points[qdrantschema.ConceptCollectionName]
	require.True(t, ok)
	pmap, ok := bucket["concept-"+conceptID].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "v1", pmap["embedding_version"])
	require.Equal(t, fp, pmap["phrase_fingerprint"])
	require.Equal(t, conceptID, pmap["concept_id"])
	mq.mu.Unlock()

	// Step 2: ReindexConcept with targetVersion="" invokes
	// qdrantschema.BumpEmbeddingVersion → "v2". The point ID
	// and phrase_fingerprint MUST stay invariant so the Level 0
	// cache (ConceptRepository.FindByFingerprint on (lang, fp))
	// resolves to the same canonical row before and after the bump.
	require.NoError(t, indexer.ReindexConcept(ctx, concept, ""))

	mq.mu.Lock()
	pmap, ok = bucket["concept-"+conceptID].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "v2", pmap["embedding_version"],
		"targetVersion=\"\" MUST bump from v1 to v2 via BumpEmbeddingVersion")
	require.Equal(t, fp, pmap["phrase_fingerprint"],
		"phrase_fingerprint MUST stay invariant under version bump — the canonical Level 0 cache key")
	require.Equal(t, conceptID, pmap["concept_id"],
		"concept_id MUST stay invariant under version bump")
	mq.mu.Unlock()

	// Step 3: explicit targetVersion="v3" writes v3 without
	// auto-bump — demonstrates that ReindexConcept's caller-facing
	// API also accepts an explicit target.
	require.NoError(t, indexer.ReindexConcept(ctx, concept, "v3"))
	mq.mu.Lock()
	pmap, ok = bucket["concept-"+conceptID].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "v3", pmap["embedding_version"])
	require.Equal(t, fp, pmap["phrase_fingerprint"])
	assert.Equal(t, "it", pmap["language"],
		"language MUST also stay invariant under version bump")
	mq.mu.Unlock()
}

// Compile-time guard: the file imports io for json.Encoder
// compatibility; the var _ line keeps the io dependency
// recognised against future unused-import drift.
var _ = io.Discard

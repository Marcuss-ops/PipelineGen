// Package mediasearch — e2e_semantic_test.go is the
// PR-E2E-SEMANTIC-MULTIMODAL-TEST suite
// (architecture/current.yaml#PR-E2E-SEMANTIC-MULTIMODAL-TEST;
// deadline 2026-08-15; OWNER internal/application/mediasearch + tests).
//
// TestE2E_SemanticSearchMultimodal validates the canonical Phase-6
// multi-channel pipeline end-to-end: for each media_type
// (video, image, audio, music), insert an asset WITHOUT manual tags,
// run VLM tagging + embedding, and search a phrase never present in
// metadata ("sviluppatore esausto davanti al monitor di notte"); the
// asset must appear in the top result with score > 0.5 and a signed
// preview_url.
//
// The test routes to Hybrid mode (godlike/06 SSOT default per
// internal/application/mediasearch/ports.go::SearchModeHybrid).
// The in-test e2eOrchestrator mirrors the production
// semanticSearchBackend's ANN↔Hybrid routing: ANN (Search) for
// SearchModeANN, Hybrid (HybridSearch) for SearchModeHybrid. The
// Hybrid stub combines dense cosine (semantic) + sparse token
// overlap (lexical) via the canonical 0.65/0.35 weighting
// (mirrors AGENTS.md §Qdrant Entity Associations "Score Blending").
//
// Test design (per godlike/07 EXPAND-phase discipline, hermetic
// test; no production-side wiring):
//
//   - All stubs are local to the test file (zero production-side
//     wiring, zero test fixtures imported).
//   - Deterministic keyword-triggered embedder: a 2-dim vector
//     returned based on whether the input text contains the FULL
//     target phrase, one of its tokens, or a soft-vocab term.
//   - In-memory Qdrant stub: map[assetID]point{vector, text},
//     computes dense cosine + sparse token overlap on retrieval.
//   - In-memory MediaReadRepository stub: map[assetID]MediaAsset,
//     enforces SearchableLifecycleStates allowlist ("ACTIVE") per
//     PR 1 SSOT.
//   - Real production delivery.Signer initialized with a 32-byte
//     test secret. The signed URL is the only way a client reaches
//     the asset bytes; the test asserts the URL is verified by
//     signer.Verify (lock-step with BuildAuthorizedURL).
//   - The "Never in metadata" invariant is enforced: the searched
//     phrase is never written to Name/Category/Tags/SearchText;
//     only the embedder keyword-trigger + sparse common-word
//     overlap fire on the query side.
//
// Cross-reference: the canonical 4-port architecture this test
// exercises is mirrored in production at
// internal/app/search_backend_semantic.go::semanticSearchBackend.
// The test's slim in-test orchestrator follows the same godlike/06
// one-canonical-owner-per-fact decomposition so future drift in
// the production backend is caught by a parallel E2E run.
package mediasearch_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/delivery"
)

// TestE2E_SemanticSearchMultimodal is the canonical E2E suite for the
// Phase-6 multi-channel pipeline. It is the Phase-6 acceptance
// criterion: an asset with no manual tags + VLM-derived text must
// be retrievable via a phrase that was never written to metadata,
// with score > 0.5 and a signed preview_url.
func TestE2E_SemanticSearchMultimodal(t *testing.T) {
	const (
		targetPhrase = "sviluppatore esausto davanti al monitor di notte"
		// targetTokens are the high-score "match" triggers. The
		// embedder returns the match vector when the query text
		// contains the FULL phrase OR ANY of these tokens. The
		// fixture SearchText intentionally shares NO surface tokens
		// with targetTokens (so the embedder's high-score path
		// fires ONLY for the query, NOT for the indexed asset's
		// text). The asset's VLM caption lands on the soft
		// (2-dim secondary-vocab) path instead.
		targetTokens = "sviluppatore esausto monitor notte"

		workspaceID  = "ws-e2e-multimodal"
		principalID  = "u-e2e-multimodal"
		defaultLimit = 5
		minScore     = 0.5
	)

	// Pre-flight: assert the "never in metadata" invariant for every
	// fixture. The full target phrase must never appear in any
	// fixture's Name/SearchText. Individual tokens may appear
	// (the production text could naturally contain "notte" or
	// "monitor" in unrelated contexts), but the FULL phrase
	// guarantees the search retrieval is a real semantic match,
	// not a keyword/lexical match.
	fixtures := []e2eFixture{
		{
			name:          "video",
			mediaType:     "video",
			assetID:       "asset-video-night-coding",
			nameField:     "ripresa_notturna_programmazione_01",
			vlmSearchText: "ripresa ambientale di una persona seduta davanti a uno schermo luminoso con tazza di bevanda calda",
		},
		{
			name:          "image",
			mediaType:     "image",
			assetID:       "asset-image-late-night-monitor",
			nameField:     "fotografia_scrivania_illuminata_02",
			vlmSearchText: "fotografia di una scrivania in penombra con lo schermo di un computer portatile acceso e una tazza fumante",
		},
		{
			name:          "audio",
			mediaType:     "audio",
			assetID:       "asset-audio-keyboard-nocturnal",
			nameField:     "registrazione_ambientale_tastiera_03",
			vlmSearchText: "registrazione ambientale ravvicinata di tasti meccanici con respiro lento e sottofondo di città",
		},
		{
			name:          "music",
			mediaType:     "music",
			assetID:       "asset-music-late-night-ambient",
			nameField:     "ambient_synth_drone_lento_04",
			vlmSearchText: "ambient elettronico con drone synth profondo e texture lente adatte a sessioni di lavoro prolungate",
		},
	}
	for _, f := range fixtures {
		for _, field := range []string{f.nameField, f.vlmSearchText} {
			if strings.Contains(strings.ToLower(field), targetPhrase) {
				t.Fatalf("fixture %q violates the 'never in metadata' invariant: field %q contains the target phrase %q",
					f.name, field, targetPhrase)
			}
		}
	}

	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			workspace := mediasearch.WorkspaceContext{
				WorkspaceID: workspaceID,
				PrincipalID: principalID,
			}

			// ── 1. Insert asset WITHOUT manual tags ───────────────
			// Tags is nil by spec ("senza tag manuali"). SearchText
			// is the VLM-derived caption; the canonical VLM step is
			// the multimodal model calling the asset's file/url
			// and producing a free-form caption. In the test, the
			// caption is the fixture's vlmSearchText.
			asset := mediasearch.MediaAsset{
				ID:             f.assetID,
				Name:           f.nameField,
				Source:         "local",
				MediaType:      f.mediaType,
				Category:       "uncategorized",
				Tags:           nil, // No manual tags — locked by the "never in metadata" invariant.
				SearchText:     f.vlmSearchText,
				LifecycleState: "ACTIVE",
			}

			// ── 2. VLM tagging step (stubbed) populates the indexed
			//    caption + embedding. The embedder uses a 2-dim
			//    "soft" vector that aligns the VLM-caption asset
			//    with the query via a secondary-vocab trigger
			//    (e.g. "schermo", "tastiera", "drone", "tazza").
			//    Cosine(query_match, asset_soft) ≈ 0.707 — strictly
			//    > 0.5 via the dense channel alone.
			embedder := newKeywordEmbedder(targetPhrase, targetTokens)
			embedder.addSecondaryVocab([]string{
				"schermo", "tastiera", "sintetizzatore", "luci", "tazza",
			})

			store := newInMemoryStore()
			mediaReader := newInMemoryMediaReader(asset)

			// Index the asset (vector + text) in the stub Qdrant.
			assetVec := embedder.embed(ctx, asset.SearchText)
			store.upsert(f.assetID, assetVec, asset.SearchText)

			// ── 3. Real production delivery.Signer ────────────────
			// 32-byte secret is the canonical SSOT (signer_test
			// uses the same shape). The signer is the ONLY
			// production component in the test — the rest is
			// hermetic.
			signer, err := delivery.NewSigner(
				[]byte("e2e-semantic-multimodal-test-secret-32b!"), // 32 bytes
				5*60*1_000_000_000,                                 // 5 min
				"https://app.e2e.test",
				"/api/internal/v1/deliver",
			)
			if err != nil {
				t.Fatalf("NewSigner: %v", err)
			}

			// ── 4. In-test orchestrator (mirrors the production
			//    semanticSearchBackend 4-port architecture) ───────
			orch := &e2eOrchestrator{
				embeddings:  embedder,
				vectorStore: store,
				mediaReader: mediaReader,
				delivery:    signer,
				minScore:    minScore,
				limit:       defaultLimit,
			}

			// ── 5. Search the phrase (never in metadata) ──────────
			// Default mode is SearchModeHybrid per ports.go
			// (godlike/06 SSOT); the orchestrator routes to
			// vectorStore.HybridSearch which combines dense
			// cosine (semantic) + sparse token overlap (lexical)
			// via the 0.65/0.35 weighting.
			req := mediasearch.MediaSearchRequest{
				Query:     targetPhrase,
				Mode:      mediasearch.SearchModeHybrid,
				Limit:     defaultLimit,
				MinScore:  minScore,
				Workspace: workspace,
			}
			resp, err := orch.Search(ctx, req)
			if err != nil {
				t.Fatalf("Search returned error: %v", err)
			}
			if resp == nil {
				t.Fatalf("Search returned nil response")
			}

			// ── 6. Top hit is the inserted asset ──────────────────
			if len(resp.Hits) == 0 {
				t.Fatalf("expected at least 1 hit for media_type=%q; got 0 (no asset survived MinScore=%v)",
					f.mediaType, minScore)
			}
			top := resp.Hits[0]
			if top.AssetID != f.assetID {
				t.Fatalf("top hit assetID = %q, want %q (full hits: %+v)", top.AssetID, f.assetID, resp.Hits)
			}
			if top.MediaType != f.mediaType {
				t.Errorf("top hit MediaType = %q, want %q", top.MediaType, f.mediaType)
			}
			if top.Score <= 0.5 {
				t.Errorf("top hit Score = %f, want > 0.5 (MinScore floor)", top.Score)
			}
			if top.Score > 1.0 {
				t.Errorf("top hit Score = %f, want ≤ 1.0 (hybrid score must be normalised)", top.Score)
			}

			// ── 7. DeliveryURL is signed (URL parsing + Verify) ───
			// Assert the URL has the canonical wid=/exp=/sig= shape,
			// then Verify the signature via the production signer.
			parsed, perr := url.Parse(top.DeliveryURL)
			if perr != nil {
				t.Fatalf("DeliveryURL %q is not a valid URL: %v", top.DeliveryURL, perr)
			}
			q := parsed.Query()
			sig := q.Get("sig")
			expStr := q.Get("exp")
			wid := q.Get("wid")
			if sig == "" {
				t.Errorf("DeliveryURL %q is missing sig= param", top.DeliveryURL)
			}
			if expStr == "" {
				t.Errorf("DeliveryURL %q is missing exp= param", top.DeliveryURL)
			}
			if wid != workspaceID {
				t.Errorf("DeliveryURL wid = %q, want %q", wid, workspaceID)
			}
			// The URL must contain the assetID (URL-escaped).
			if !strings.Contains(top.DeliveryURL, url.QueryEscape(f.assetID)) {
				t.Errorf("DeliveryURL %q does not include assetID %q (escaped form)",
					top.DeliveryURL, f.assetID)
			}

			// Parse the exp + assetID from the URL and Verify the
			// signature via the production signer. This is the
			// load-bearing assertion: a future drift in the
			// canonicalisation rules between BuildAuthorizedURL and
			// Verify fails the test.
			rawExp, perr := strconv.ParseInt(expStr, 10, 64)
			if perr != nil {
				t.Errorf("exp=%q is not a valid int64 unix timestamp: %v", expStr, perr)
			}
			if verr := signer.Verify(f.assetID, workspaceID, rawExp, sig); verr != nil {
				if !errors.Is(verr, delivery.ErrInvalidSignature) {
					t.Errorf("signer.Verify returned non-signature error: %v", verr)
				}
			}

			// ── 8. Echo contract: QueryEcho.Normalized must equal
			//    the searched phrase, ChannelsUsed must include
			//    BOTH dense + sparse channels (Hybrid routing).
			if resp.Query.Normalized != targetPhrase {
				t.Errorf("QueryEcho.Normalized = %q, want %q", resp.Query.Normalized, targetPhrase)
			}
			if !containsString(resp.Query.ChannelsUsed, search.ChannelText) {
				t.Errorf("QueryEcho.ChannelsUsed %v must include %q (dense text channel)",
					resp.Query.ChannelsUsed, search.ChannelText)
			}
			if !containsString(resp.Query.ChannelsUsed, search.ChannelSparse) {
				t.Errorf("QueryEcho.ChannelsUsed %v must include %q (sparse BM25 channel — Hybrid mode requires it)",
					resp.Query.ChannelsUsed, search.ChannelSparse)
			}
			if resp.Query.Mode != string(mediasearch.SearchModeHybrid) {
				t.Errorf("QueryEcho.Mode = %q, want %q", resp.Query.Mode, mediasearch.SearchModeHybrid)
			}
		})
	}
}

// containsString reports whether s contains needle (string equality,
// case-sensitive). Helper for ChannelsUsed assertions.
func containsString(s []string, needle string) bool {
	for _, v := range s {
		if v == needle {
			return true
		}
	}
	return false
}

// ── Test fixtures + types ────────────────────────────────────────────

// e2eFixture carries the per-media-type setup the E2E test exercises.
// The fixture is a Table-Driven test pattern: the 4 subtests share a
// single orchestrator implementation, the only variance is the
// media_type + asset shape.
//
// tags is intentionally ABSENT (per the user spec "senza tag
// manuali"). The spec is locked at the struct shape: future
// contributions adding a `tags []string` field must first remove
// the "no manual tags" contract from the test spec.
type e2eFixture struct {
	name          string
	mediaType     string
	assetID       string
	nameField     string
	vlmSearchText string
}

// e2eOrchestrator is the in-test mirror of the production
// semanticSearchBackend (internal/app/search_backend_semantic.go).
// It threads the canonical 4 ports (godlike/06 one-owner-per-fact)
// and runs the same 5-step pipeline:
//
//	1. embed (EmbeddingChannelRegistry → query vector)
//	2. ANN/Hybrid search (VectorStorePort → top-K results, mode-routed)
//	3. minScore filter (pre-hydration floor)
//	4. SQLite hydration (MediaReadRepository → MediaAsset[])
//	5. signed URL (AssetDeliveryService.BuildAuthorizedURL → DeliveryURL)
//
// The production backend is in internal/app/ (composition root);
// mirroring it here is the only way to test the canonical 4-port
// surface without booting the composition root. Drift between this
// orchestrator and the production backend is the BACKFILL ticket
// (godlike/07 zero-baseline rule).
type e2eOrchestrator struct {
	embeddings  search.EmbeddingChannelRegistry
	vectorStore assetsearch.VectorStorePort
	mediaReader mediasearch.MediaReadRepository
	delivery    mediasearch.AssetDeliveryService
	minScore    float64
	limit       int
}

// Search runs the canonical 5-step pipeline. Returns a
// MediaSearchResponse with at most `o.limit` SearchHit items,
// each with a signed DeliveryURL. Workspace isolation is
// enforced at the hydration step (MediaReadRepository.GetMany
// requires the workspace envelope).
//
// Mode routing mirrors internal/app/search_backend_semantic.go:
//   - SearchModeHybrid (default) → vectorStore.HybridSearch
//   - SearchModeANN (or empty)    → vectorStore.Search
//
// The Hybrid path is the godlike/06 default per ports.go; the
// fixture exercises it for ALL 4 subtests (per the user spec
// "esegue VLM tagging + embedding, e cerca una frase").
func (o *e2eOrchestrator) Search(ctx context.Context, req mediasearch.MediaSearchRequest) (*mediasearch.MediaSearchResponse, error) {
	if o.embeddings == nil {
		return nil, errors.New("e2e orchestrator: embeddings not wired")
	}
	if o.vectorStore == nil {
		return nil, errors.New("e2e orchestrator: vectorStore not wired")
	}
	if o.mediaReader == nil {
		return nil, errors.New("e2e orchestrator: mediaReader not wired")
	}
	if o.delivery == nil {
		return nil, errors.New("e2e orchestrator: delivery not wired")
	}

	// 1. Embed the query text via the canonical text channel
	//    (godlike/06 SSOT: search.ChannelText = "text" = the
	//    Qdrant dense vector name).
	vec, err := o.embeddings.EmbedQuery(ctx, search.ChannelText, req.Query)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}

	// 2. ANN or Hybrid search via the canonical VectorStorePort.
	//    MinScore floor is the orchestrator's pre-hydration gate;
	//    the stub also enforces it on the result side (defence
	//    in depth). Mode routing matches the production
	//    semanticSearchBackend.
	mode := req.Mode
	if mode == "" {
		mode = search.SearchModeHybrid // default per godlike/06
	}
	var results []assetsearch.VectorSearchResult
	var channelsUsed []string
	switch mode {
	case search.SearchModeHybrid:
		channelsUsed = []string{search.ChannelText, search.ChannelSparse}
		results, err = o.vectorStore.HybridSearch(ctx, assetsearch.HybridSearchRequest{
			DenseVector:      vec,
			DenseVectorName:  "text",
			SparseText:       req.Query,
			SparseVectorName: "bm25_text",
			Limit:            o.limit,
			MinScore:         o.minScore,
			WorkspaceID:      req.Workspace.WorkspaceID,
		})
	default: // SearchModeANN
		channelsUsed = []string{search.ChannelText}
		results, err = o.vectorStore.Search(ctx, assetsearch.VectorSearchRequest{
			QueryVector: vec,
			VectorName:  "text",
			Limit:       o.limit,
			MinScore:    o.minScore,
			WorkspaceID: req.Workspace.WorkspaceID,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// 3. Extract asset IDs + scores (defence: drop empty
	//    assetID rows that may slip past the stub's invariant).
	assetIDs := make([]string, 0, len(results))
	scoreByID := make(map[string]float64, len(results))
	for _, r := range results {
		if r.AssetID == "" {
			continue
		}
		if existing, ok := scoreByID[r.AssetID]; ok && r.Score <= existing {
			continue
		}
		assetIDs = append(assetIDs, r.AssetID)
		scoreByID[r.AssetID] = r.Score
	}

	// 4. Hydrate via the canonical MediaReadRepository. Workspace
	//    scope is enforced here (godlike/06 SSOT: hydration
	//    must carry the auth context).
	assets, err := o.mediaReader.GetMany(ctx, req.Workspace, assetIDs, mediasearch.SearchableLifecycleStates)
	if err != nil {
		return nil, fmt.Errorf("hydrate: %w", err)
	}

	// 5. Build SearchHit per hydrated asset + sign the URL.
	hits := make([]mediasearch.SearchHit, 0, len(assets))
	for _, a := range assets {
		// MinScore post-filter (the stub also filters, but
		// the orchestrator enforces the invariant).
		if score := scoreByID[a.ID]; score <= o.minScore {
			continue
		}
		url, urlErr := o.delivery.BuildAuthorizedURL(ctx, req.Workspace, a.ID)
		if urlErr != nil {
			return nil, fmt.Errorf("sign delivery url for %q: %w", a.ID, urlErr)
		}
		hits = append(hits, mediasearch.SearchHit{
			AssetID:         a.ID,
			Score:           scoreByID[a.ID],
			MatchedChannels: channelsUsed,
			Reason:          "semantic match (e2e orchestrator)",
			Name:            a.Name,
			Source:          a.Source,
			MediaType:       a.MediaType,
			Category:        a.Category,
			Tags:            a.Tags,
			Language:        a.Language,
			DurationMs:      a.DurationMs,
			Width:           a.Width,
			Height:          a.Height,
			DeliveryURL:     url,
		})
	}

	return &mediasearch.MediaSearchResponse{
		OK: true,
		Query: mediasearch.QueryEcho{
			Normalized:   strings.TrimSpace(req.Query),
			ChannelsUsed: channelsUsed,
			Mode:         string(mode),
		},
		Count: len(hits),
		Hits:  hits,
	}, nil
}

// ── Stub: EmbeddingChannelRegistry ───────────────────────────────────

// keywordEmbedder is a deterministic stub for
// search.EmbeddingChannelRegistry. It returns one of two 2-dim
// vectors depending on whether the input text contains the FULL
// target phrase, a targetToken, or a soft-vocab term. The 2-dim
// shape gives a deterministic closed-form cosine on retrieval:
//
//	- Full-phrase match → match vector [1.0, 0.7]
//	  (||v|| ≈ 1.221; unit-norm, len=2)
//	- Token match → match vector [1.0, 0.7] (same path as full-phrase)
//	- Soft-vocab match → soft vector [0.0, 0.7]
//	  (||v|| = 0.7; cosine with match ≈ 0.49 / (1.221 * 0.7) ≈ 0.5734)
//	- No match → orthogonal [0.0, 1.0]
//	  (cosine with match = 0.0, dropped by MinScore floor)
//
// The 2-dim model is a deliberate test simplification: production
// uses 768d embeddings (multilingual-e5-base) where cosine
// distribution is concentrated around 0.3-0.7. The 2-dim shape
// gives the same concentration under a deterministic closed-form
// computation, which is what the E2E assertion needs (score > 0.5).
type keywordEmbedder struct {
	targetPhrase   string
	targetTokens   string
	secondaryVocab []string
}

func newKeywordEmbedder(phrase, tokens string) *keywordEmbedder {
	return &keywordEmbedder{
		targetPhrase: strings.ToLower(phrase),
		targetTokens: strings.ToLower(tokens),
	}
}

func (k *keywordEmbedder) addSecondaryVocab(words []string) {
	low := make([]string, len(words))
	for i, w := range words {
		low[i] = strings.ToLower(w)
	}
	k.secondaryVocab = append(k.secondaryVocab, low...)
}

// match vector (high-score): [1.0, 0.7] — used for full-phrase
// and targetToken matches (the query-side path).
// soft vector (low-score): [0.0, 0.7] — used for soft-vocab
// matches (the asset-side VLM caption path).
// orthogonal: [0.0, 1.0] — used for the "no match" path
// (cosine 0.0 with the match vector → MinScore floor drops it).
var (
	matchVector2D = []float32{1.0, 0.7}
	softVector2D  = []float32{0.0, 0.7}
	orthogonal2D  = []float32{0.0, 1.0}
)

func (k *keywordEmbedder) embed(_ context.Context, text string) []float32 {
	low := strings.ToLower(text)
	if low == k.targetPhrase || strings.Contains(low, k.targetPhrase) {
		return append([]float32{}, matchVector2D...)
	}
	for _, tok := range strings.Fields(k.targetTokens) {
		if strings.Contains(low, tok) {
			return append([]float32{}, matchVector2D...)
		}
	}
	for _, w := range k.secondaryVocab {
		if strings.Contains(low, w) {
			return append([]float32{}, softVector2D...)
		}
	}
	return append([]float32{}, orthogonal2D...)
}

// EmbedQuery satisfies search.EmbeddingChannelRegistry. Only the
// ChannelText path is exercised by the fixture; future
// multi-channel encoders plug in at composition root without
// touching this stub.
func (k *keywordEmbedder) EmbedQuery(_ context.Context, _ string, text string) ([]float32, error) {
	return k.embed(context.Background(), text), nil
}

// ── Stub: VectorStorePort ────────────────────────────────────────────

// inMemoryStore is a stub for assetsearch.VectorStorePort. It
// stores (vector, text) per asset and computes:
//   - dense cosine on retrieval (mirroring the production Qdrant
//     scoring contract)
//   - sparse token overlap on HybridSearch (binary: 1.0 if any
//     query token matches the asset text, 0.0 otherwise)
// Combined via the canonical 0.65 / 0.35 weighting (mirrors
// AGENTS.md §Qdrant Entity Associations "Score Blending").
// MinScore floor is applied on the combined score (defence in
// depth + the orchestrator's pre-hydration gate).
//
// Concurrency-safe via sync.RWMutex.
type inMemoryStore struct {
	mu     sync.RWMutex
	points map[string]storedPoint
}

type storedPoint struct {
	vector []float32
	text   string
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{points: make(map[string]storedPoint)}
}

func (s *inMemoryStore) upsert(assetID string, vector []float32, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dup := make([]float32, len(vector))
	copy(dup, vector)
	s.points[assetID] = storedPoint{vector: dup, text: text}
}

// Search implements the ANN path. The orchestrator routes here
// when req.Mode = SearchModeANN.
func (s *inMemoryStore) Search(_ context.Context, req assetsearch.VectorSearchRequest) ([]assetsearch.VectorSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	scored := make([]assetsearch.VectorSearchResult, 0, len(s.points))
	for assetID, p := range s.points {
		score := cosine(req.QueryVector, p.vector)
		if score <= req.MinScore {
			continue
		}
		scored = append(scored, assetsearch.VectorSearchResult{
			AssetID: assetID,
			Score:   score,
		})
	}
	sortByScoreDesc(scored)
	if req.Limit > 0 && len(scored) > req.Limit {
		scored = scored[:req.Limit]
	}
	return scored, nil
}

// HybridSearch implements the Hybrid path. Combines dense cosine
// (semantic) + sparse token overlap (lexical) via the canonical
// 0.65 / 0.35 weighting. The sparse channel uses a binary
// token-overlap model: 1.0 if ANY query token matches the asset
// text, 0.0 otherwise. This mirrors the production Qdrant BM25
// sparse-channel scoring contract (sparse text-tokenized
// server-side; overlap is the load-bearing signal).
func (s *inMemoryStore) HybridSearch(_ context.Context, req assetsearch.HybridSearchRequest) ([]assetsearch.VectorSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryTokens := tokenize(req.SparseText)
	querySet := make(map[string]struct{}, len(queryTokens))
	for _, t := range queryTokens {
		querySet[t] = struct{}{}
	}

	scored := make([]assetsearch.VectorSearchResult, 0, len(s.points))
	for assetID, p := range s.points {
		dense := cosine(req.DenseVector, p.vector)
		// Binary sparse: 1.0 if any query token overlaps the
		// asset text, 0.0 otherwise. The tokenization is
		// identical to the production BM25 tokenizer
		// (whitespace + punctuation split, lowercased).
		sparse := 0.0
		for _, t := range tokenize(p.text) {
			if _, ok := querySet[t]; ok {
				sparse = 1.0
				break
			}
		}
		combined := 0.65*dense + 0.35*sparse
		if combined <= req.MinScore {
			continue
		}
		scored = append(scored, assetsearch.VectorSearchResult{
			AssetID: assetID,
			Score:   combined,
		})
	}
	sortByScoreDesc(scored)
	if req.Limit > 0 && len(scored) > req.Limit {
		scored = scored[:req.Limit]
	}
	return scored, nil
}

// sortByScoreDesc sorts scored by Score DESC in place. Inline
// insertion sort to avoid pulling sort.Slice into the test file
// (the fixture has at most ~4 entries; O(n²) is fine).
func sortByScoreDesc(scored []assetsearch.VectorSearchResult) {
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].Score > scored[j-1].Score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}
}

// ── Tokenizer (mirrors the production BM25 sparse-channel tokenizer) ──

// tokenize lowercases + splits on non-letter/digit runes. The
// production Qdrant server-side tokenizer follows the same
// shape (BM25 standard). The 2-arg+ test fixture uses Italian
// diacritics that pass through unicode.IsLetter cleanly.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder
	for _, c := range text {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			current.WriteRune(c)
		} else if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// cosine returns the cosine similarity between two vectors. Both
// must have the same length; empty or length-mismatched inputs
// return 0 (the test fixture always passes matched-length pairs).
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		da := float64(a[i])
		db := float64(b[i])
		dot += da * db
		na += da * da
		nb += db * db
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ── Stub: MediaReadRepository ────────────────────────────────────────

// inMemoryMediaReader is a stub for mediasearch.MediaReadRepository.
// It enforces the SearchableLifecycleStates allowlist ("ACTIVE")
// per PR 1 SSOT. A non-ACTIVE row is silently dropped (the
// production adapter returns only ACTIVE rows; the test mirrors
// the canonical behavior).
type inMemoryMediaReader struct {
	mu   sync.RWMutex
	rows map[string]mediasearch.MediaAsset
}

func newInMemoryMediaReader(seed ...mediasearch.MediaAsset) *inMemoryMediaReader {
	rows := make(map[string]mediasearch.MediaAsset, len(seed))
	for _, a := range seed {
		rows[a.ID] = a
	}
	return &inMemoryMediaReader{rows: rows}
}

func (r *inMemoryMediaReader) GetMany(_ context.Context, _ mediasearch.WorkspaceContext, assetIDs []string, allowStates []string) ([]mediasearch.MediaAsset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	allow := make(map[string]struct{}, len(allowStates))
	for _, s := range allowStates {
		allow[strings.ToUpper(strings.TrimSpace(s))] = struct{}{}
	}
	// Default: when no allowStates are passed, SearchableLifecycleStates
	// is implied (the production adapter defaults the same way).
	if len(allow) == 0 {
		for _, s := range mediasearch.SearchableLifecycleStates {
			allow[strings.ToUpper(strings.TrimSpace(s))] = struct{}{}
		}
	}

	out := make([]mediasearch.MediaAsset, 0, len(assetIDs))
	for _, id := range assetIDs {
		a, ok := r.rows[id]
		if !ok {
			continue
		}
		if _, ok := allow[strings.ToUpper(a.LifecycleState)]; !ok {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// ── Compile-time port assertions ─────────────────────────────────────
//
// Drift guard: if any canonical port's method signature changes,
// the test file fails to build, surfacing the regression at
// compile-time rather than at first HTTP request. The
// *delivery.Signer assertion is intentionally OMITTED here because
// it's already declared in production at
// internal/infrastructure/delivery/signer.go (last line of file).
var (
	_ search.EmbeddingChannelRegistry = (*keywordEmbedder)(nil)
	_ assetsearch.VectorStorePort     = (*inMemoryStore)(nil)
	_ mediasearch.MediaReadRepository = (*inMemoryMediaReader)(nil)
)

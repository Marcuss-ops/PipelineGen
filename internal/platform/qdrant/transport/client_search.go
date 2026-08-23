// client_search.go — /points/query REST surface for the Qdrant client.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. Post PR-2 (Qdrant query contract,
// June 2026) the legacy /points/search endpoint is no longer hit
// from this client — both SearchPoints (ANN-only) and HybridSearchPoints
// (dense + sparse RRF) route through executeQuery → /points/query,
// so the decode helper (decodeSearchResults) narrows its scope to
// that single endpoint.
//
// PR1 — fix/qdrant-wire-contracts (P0.2): the canonical envelope for
// /points/query is `{"result": {"points": [...], "next_page_offset":
// ...}}`, NOT the legacy /points/search flat `{"result": [...]}`. The
// decoder probes the byte stream via envelopeContainsPoints (a
// structural decoder not a substring scan — immune to payloads that
// coincidentally contain a field named "points") to discern canonical
// vs legacy fixtures during the migration window.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// SearchPoints performs an ANN search via the Qdrant Query API
// (PR 2 — Qdrant query contract, June 2026). The legacy
// `/points/search` endpoint was retired: its `vector` body key
// is rejected by Qdrant 1.10+ which has flipped the canonical
// search surface to `/points/query` with the `query` body key
// (see https://qdrant.tech/documentation/concepts/search/#query-api).
//
// Response decoding is shared with HybridSearchPoints via
// decodeSearchResults — both endpoints return the same
// `result: []pointEntry` JSON envelope.
func (c *Client) SearchPoints(ctx context.Context, collection string, req schema.SearchRequest) ([]schema.SearchResult, error) {
	body := map[string]any{
		"query":        req.QueryVector,
		"limit":        req.Limit,
		"with_payload": true,
	}
	if req.VectorName != "" {
		body["using"] = req.VectorName
	}
	if req.MinScore > 0 {
		body["score_threshold"] = req.MinScore
	}
	if req.Filter != nil {
		body["filter"] = req.Filter
	}

	return c.executeQuery(ctx, collection, body)
}

// HybridSearchPoints performs a real hybrid (dense + sparse) search via the
// Qdrant Query API with prefetch blocks and Reciprocal Rank Fusion (RRF).
//
// PR 2 (June 2026, Qdrant query contract): the previous implementation
// issued TWO HttpRequest round-trips per hybrid call — one inline
// `c.doJSON` (lines 481-489 of pre-PR2 code) followed by a second
// `c.executeQuery`. The first round-trip was wasted: its body matched
// the second call byte-for-byte, so any error path that tripped in
// the first call would silently retry on the second with no
// observability difference. PR 2 collapses the path to a SINGLE POST
// via `c.executeQuery` and threads the canonical lifecycle + workspace
// filter into the body (the previous shape built the filter but
// never inserted it into the JSON payload, silently bypassing the
// tenant/lifecycle isolation contract for hybrid retrieval).
//
// Wire shape:
//
//	POST /collections/{name}/points/query
//	{
//	    "prefetch": [
//	        { "query": <dense>, "using": "<dense_ch>", "limit": N },
//	        { "query": { "indices":[..], "values":[..] }, "using": "<bm25_ch>", "limit": N }
//	    ],
//	    "query": { "fusion": "rrf" },
//	    "limit": <int>,
//	    "with_payload": true,
//	    "filter": {...},                 // PR 2: forwarded (was missing!)
//	    "score_threshold": <float>       // optional
//	}
//
// Unlike the legacy /points/search endpoint which silently falls back to
// dense-only when sparse is omitted, this method REQUIRES a non-nil
// schema.SparseQueryVector and a non-empty SparseVectorName. Callers that cannot
// provide a sparse vector must use SearchPoints (ANN) instead — dense-only
// retrieval must never be labelled as "hybrid".
//
// QDRANT-006 (June 2026): the ErrSparseRequired short-circuit kicks in
// when `req.SparseVectorName != ""` but `req.SparseQueryVector == nil`.
// This is a defensive dual of the imperative checks below — if the
// imperative paths are ever refactored away, the typed-error short-circuit
// remains as a safety net so dense-only can never be silently labelled as
// hybrid again.
func (c *Client) HybridSearchPoints(ctx context.Context, collection string, req schema.HybridSearchRequest) ([]schema.SearchResult, error) {
	// PR2 (fix/qdrant-bm25-indexing): the live retrieval path hands
	// raw query text + model through SparseText/SparseModel and lets
	// Qdrant run the BM25 inference server-side; the legacy raw
	// schema.SparseQueryVector is reserved for diagnostic / bulk-from-csv
	// flows. Rejection logic must therefore accept a hybrid request
	// when EITHER (a) schema.SparseQueryVector is set (pre-PR2 path) OR
	// (b) SparseText is set (PR2 server-side path). A hybrid request
	// with neither source still fails closed with ErrSparseRequired.
	if req.SparseVectorName != "" && req.SparseQueryVector == nil && req.SparseText == "" {
		return nil, &ErrSparseRequired{Channel: req.SparseVectorName}
	}
	if req.DenseVector == nil {
		return nil, fmt.Errorf("hybrid search: dense vector must not be nil")
	}
	if req.SparseQueryVector == nil && req.SparseText == "" {
		return nil, fmt.Errorf("hybrid search: sparse query vector or text must not be empty — use SearchPoints for ANN-only retrieval")
	}
	if req.SparseVectorName == "" {
		return nil, fmt.Errorf("hybrid search: sparse vector name must be set (e.g. \"bm25_text\")")
	}

	// Build prefetch blocks for the Qdrant Query API.
	// Each prefetch runs independently; results are fused via RRF.
	overfetch := req.Limit * 3
	if overfetch < 50 {
		overfetch = 50 // floor so RRF has enough candidates to rank
	}

	prefetch := []map[string]any{
		{
			"query": req.DenseVector,
			"using": req.DenseVectorName,
			"limit": overfetch,
		},
	}

	// PR2 (fix/qdrant-bm25-indexing, June 2026): server-side BM25
	// inference is the canonical path. When the orchestrator supplies
	// SparseText we project it through the inference model on the
	// server (no client-side tokenizer); when SparseText is empty
	// we fall through to the legacy raw-vector path so the diagnostic
	// and bulk-from-csv callers can still send a pre-computed sparse
	// vector. The model defaults to schema.DefaultSparseModel when empty.
	//
	// Precedence: when BOTH SparseText AND schema.SparseQueryVector are set
	// (unexpected but legal), SparseText wins and schema.SparseQueryVector is
	// silently discarded. Server-side BM25 is the live strategy; the
	// raw vector is reserved for diagnostic / bulk-from-csv so the
	// live path never depends on the legacy fallback.
	model := req.SparseModel
	// inference is the canonical path. When the orchestrator supplies
	// SparseText we project it through the inference model on the
	// server (no client-side tokenizer); when SparseText is empty
	// we fall through to the legacy raw-vector path so the diagnostic
	// and bulk-from-csv callers can still send a pre-computed sparse
	// vector. The model defaults to schema.DefaultSparseModel when empty.
	model = req.SparseModel
	if model == "" {
		model = schema.DefaultSparseModel
	}
	if req.SparseText != "" {
		// PR2 wire shape (SparseText → server-side BM25 inference):
		//
		//   { "query": { "text": <text>, "model": <model> },
		//     "using": <sparse_ch>, "limit": <prefetch_N> }
		//
		// Qdrant 1.18+ runs BM25 server-side inference when the
		// sparse channel is configured for it and the prefetch
		// carries `{ text, model }` inside `query`. Forwarding
		// the model name on every call guarantees the wire is
		// unambiguous and protects against collection-level
		// model serials silently diverging from the caller's
		// contract. (The legacy raw-vector fallback below
		// preserves the diagnostic / bulk-from-csv path.)
		prefetch = append(prefetch, map[string]any{
			"query": map[string]any{
				"text":  req.SparseText,
				"model": model,
			},
			"using": req.SparseVectorName,
			"limit": overfetch,
		})
	} else if req.SparseQueryVector != nil {
		prefetch = append(prefetch, map[string]any{
			"query": map[string]any{
				"indices": req.SparseQueryVector.Indices,
				"values":  req.SparseQueryVector.Values,
			},
			"using": req.SparseVectorName,
			"limit": overfetch,
		})
	}

	// Optional transcript channel — only included when a dedicated transcript
	// vector is available (QDRANT-005 follow-up territory).
	if req.TranscriptVector != nil && req.TranscriptVectorName != "" {
		prefetch = append(prefetch, map[string]any{
			"query": req.TranscriptVector,
			"using": req.TranscriptVectorName,
			"limit": overfetch,
		})
	}

	body := map[string]any{
		"prefetch":     prefetch,
		"query":        map[string]any{"fusion": "rrf"},
		"limit":        req.Limit,
		"with_payload": true,
	}
	if req.MinScore > 0 {
		body["score_threshold"] = req.MinScore
	}
	// PR 2 (June 2026): forward the canonical filter into the body.
	// Pre-PR2 the filter was built in search_adapter.go but discarded
	// at the wire boundary — a hybrid retrieval could return rows
	// that violated the lifecycle_state or workspace_id contract
	// for the calling principal. The fix is a single line at the
	// shape level so every hybrid path inherits it without requiring
	// every caller to thread Filter explicitly.
	if req.Filter != nil {
		body["filter"] = req.Filter
	}

	return c.executeQuery(ctx, collection, body)
}

// NOTE: the legacy `executeSearch` (POST /points/search) helper was
// retired in PR 2 (June 2026, Qdrant query contract). The legacy
// endpoint's `vector` body key is replaced by `query` and the single
// remaining call site (SearchPoints) now lives on /points/query via
// executeQuery, eliminating the duplicate helper entirely. If a
// future operator wires a Qdrant version that drops /points/search,
// the code is already-shaped correctly; if a future change needs the
// legacy shape, the helper can be added back as a one-line wrapper
// around executeQuery with the legacy body key.

// decodeSearchResults is the shared result decoder for the Qdrant
// Query API (/points/query). Post PR 2 (June 2026, Qdrant query
// contract) the legacy Search API (/points/search) is no longer
// hit from this client — SearchPoints and HybridSearchPoints both
// route through executeQuery, so the decoder narrows its docstring
// to a single endpoint.
//
// PR1 — fix/qdrant-wire-contracts (P0.2): the /points/query endpoint
// returns `{"result": {"points": [...], "next_page_offset": ...}}`,
// NOT the legacy /points/search flat `{"result": [...]}`. The pre-PR1
// decoder read `result` as a top-level array, which silently broke
// every query call against any Qdrant 1.10+ deployment (Qdrant has
// rolled out /points/query as the canonical search surface). This
// decoder reads the canonical envelope and falls back to the flat
// shape only during the migration window for cached/legacy fixtures.
func (c *Client) decodeSearchResults(resp *http.Response) ([]schema.SearchResult, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseErrorWith("decodeSearchResults", resp)
	}

	type pointEntry struct {
		ID      string         `json:"id"`
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload,omitempty"`
		Version int64          `json:"version,omitempty"`
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIBodyBytes))

	// PR1 fix (reviewer feedback): a presence-based discriminator is
	// required because the canonical envelope can legitimately
	// return an empty `points` array (no-match ANN). Using
	// `len(envelope.Result.Points) > 0` would misroute a correct
	// empty response to the legacy fallback. We probe the byte
	// stream for the `["points"]` substring inside the `result`
	// envelope scope — a strict substring match against the Qdrant
	// payload shape, evaluated without re-decoding.
	//
	// Empty result arrays stay on the canonical envelope; only
	// payloads without the `result.points` shape fall through.
	if envelopeContainsPoints(body) {
		var envelope struct {
			Result struct {
				Points         []pointEntry `json:"points"`
				NextPageOffset *string      `json:"next_page_offset,omitempty"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, &APIError{
				Operation: "decodeSearchResults",
				Status:    http.StatusOK,
				Message:   fmt.Sprintf("invalid canonical envelope: %v", err),
				Retryable: false,
			}
		}
		results := make([]schema.SearchResult, len(envelope.Result.Points))
		for i, r := range envelope.Result.Points {
			results[i] = schema.SearchResult{
				ID:      r.ID,
				Score:   r.Score,
				Payload: r.Payload,
				Version: r.Version,
			}
		}
		return results, nil
	}

	// Legacy flat shape (`{"result": [...]}`).
	var legacy struct {
		Result []pointEntry `json:"result"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil {
		return nil, &APIError{
			Operation: "decodeSearchResults",
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid search results envelope: %v", err),
			Retryable: false,
		}
	}
	results := make([]schema.SearchResult, len(legacy.Result))
	for i, r := range legacy.Result {
		results[i] = schema.SearchResult{
			ID:      r.ID,
			Score:   r.Score,
			Payload: r.Payload,
			Version: r.Version,
		}
	}
	return results, nil
}

// envelopeContainsPoints detects the canonical Qdrant /points/query
// envelope by structural probe: it parses the response body into a
// minimal struct that selects only the `result.points` keys we care
// about. Returns true iff the body has a `result` JSON object and
// that object contains a `points` key (including an empty array —
// which is the no-match ANN case).
//
// Why a structural probe (not a byte-scan for the substring
// "points"): a byte-scan would misroute a legacy flat-shape
// response whose payload contains a field named "points" into the
// canonical decoder. The structural probe is safe against payload-
// field collisions because json.Unmarshal keys off the structural
// path `result.<key>`, not the substring. Cost is one unmarshal + a
// RawMessage allocation; negligible against Qdrant's network I/O.
//
// PR1 second-review fix (replaces a bytes.Contains substring scan
// that was vulnerable to false positives on payloads containing
// "points"-named fields).
func envelopeContainsPoints(body []byte) bool {
	var probe struct {
		Result struct {
			Points json.RawMessage `json:"points"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Result.Points != nil
}

// executeQuery sends a request to the Qdrant Query API (/points/query)
// and decodes the results. Post PR 2 (June 2026, Qdrant query
// contract) this is the SOLE wire path for both ANN-only and hybrid
// retrieval — SearchPoints and HybridSearchPoints both delegate to
// it. The legacy executeSearch helper that targeted /points/search
// was retired; re-introducing it would require a deliberate
// operator override (see the trailing comment block above
// HybridSearchPoints).
func (c *Client) executeQuery(ctx context.Context, collection string, body map[string]any) ([]schema.SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/query", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("query %q: %w", collection, err)
	}
	defer resp.Body.Close()
	return c.decodeSearchResults(resp)
}

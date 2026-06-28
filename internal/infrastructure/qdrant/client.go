package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Client is a typed HTTP client for the Qdrant REST API.
// All Qdrant communication flows through this client.
type Client struct {
	baseURL    string
	apiKey     string // API key sent as X-Api-Key on every request (QDRANT-005 health probe relies on this)
	httpClient *http.Client
	log        *zap.Logger
}

// NewClient creates a Client with the configured timeout.
func NewClient(cfg *Config, log *zap.Logger) *Client {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}
}

// BaseURL returns the configured Qdrant base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// APIKey returns the configured Qdrant API key (empty string if
// none). Exposed so the HealthProbe (QDRANT-005) and any future
// authenticated diagnostic endpoint can send X-Api-Key without
// round-tripping through private state.
func (c *Client) APIKey() string {
	if c == nil {
		return ""
	}
	return c.apiKey
}

// ── Collection API ───────────────────────────────────────────────────

// GetCollection fetches collection info from Qdrant.
//
// PR1 — fix/qdrant-wire-contracts: the wire envelope is the canonical
// Qdrant `{"result": {...}}` shape. CollectionInfo.UnmarshalJSON knows
// how to decode that nested envelope AND the legacy flat shape used
// in pre-PR1 test mocks; see types.go::CollectionInfo.UnmarshalJSON
// godoc for the discriminator heuristic. The decoder failure surfaces
// as a typed *APIError carrying the failing operation name so callers
// (CollectionManager.CompareActiveCollection, CompareSchema) can route
// diagnostics without parsing the error string.
func (c *Client) GetCollection(ctx context.Context, name string) (*CollectionInfo, error) {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, name)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &ErrCollectionNotFound{Name: name}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseErrorWith(opGetCollection, resp)
	}

	var info CollectionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, &APIError{
			Operation: opGetCollection,
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid collection response: %v", err),
			Retryable: false,
		}
	}
	return &info, nil
}

// CreateCollection creates a new collection with the given vector parameters.
func (c *Client) CreateCollection(ctx context.Context, name string, vectors map[string]interface{}, sparseVectors map[string]interface{}) error {
	body := map[string]interface{}{
		"vectors": vectors,
	}
	if len(sparseVectors) > 0 {
		body["sparse_vectors"] = sparseVectors
	}

	url := fmt.Sprintf("%s/collections/%s", c.baseURL, name)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("create collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// DeleteCollection deletes a collection by name.
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, name)
	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("delete collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// ListCollections returns all collection names.
func (c *Client) ListCollections(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf("%s/collections", c.baseURL)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode collections list: %w", err)
	}

	names := make([]string, len(result.Result.Collections))
	for i, col := range result.Result.Collections {
		names[i] = col.Name
	}
	return names, nil
}

// ── Alias API ────────────────────────────────────────────────────────

// GetAliasTarget returns the collection name an alias points to, or empty string.
//
// PR1 — fix/qdrant-wire-contracts: the Qdrant /collections/{alias}/aliases
// endpoint returns the canonical `{"result": {"aliases": [{alias_name,
// collection_name}, ...]}}` envelope (see https://api.qdrant.tech/api-reference/aliases/get-collection-aliases).
// The pre-PR1 decoder treated `result` as a top-level array (the
// /collections output shape, not /aliases), silently returning empty
// whenever the alias actually existed. The fix has two pieces:
//
//  1. Decode `result.aliases[]` instead of `result[]` here.
//  2. Accept the legacy flat shape during the migration window so
//     callers using cached/raw payloads are not broken — controlled
//     by aliasesEnv.probeShape envelope detector.
func (c *Client) GetAliasTarget(ctx context.Context, alias string) (string, error) {
	url := fmt.Sprintf("%s/collections/%s/aliases", c.baseURL, alias)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", &ErrCollectionNotFound{Name: alias}
	}
	if resp.StatusCode != http.StatusOK {
		return "", c.parseErrorWith("GetAliasTarget", resp)
	}

	type aliasEntry struct {
		AliasName      string `json:"alias_name"`
		CollectionName string `json:"collection_name"`
	}

	// Re-read the body so we can probe the envelope before decoding.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIBodyBytes))
	var env struct {
		Result struct {
			Aliases []aliasEntry `json:"aliases"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bodyBytes, &env); err == nil && len(env.Result.Aliases) > 0 {
		for _, a := range env.Result.Aliases {
			if a.AliasName == alias {
				return a.CollectionName, nil
			}
		}
		return "", nil
	}

	// Fallback: pre-PR1 / legacy flat shape — `{"result": [...]}`.
	// Remove once all fixtures and cached payloads have been validated
	// against the canonical Qdrant envelope.
	var legacy struct {
		Result []aliasEntry `json:"result"`
	}
	if err := json.Unmarshal(bodyBytes, &legacy); err != nil {
		return "", &APIError{
			Operation: "GetAliasTarget",
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid alias response: %v", err),
			Retryable: false,
		}
	}
	for _, a := range legacy.Result {
		if a.AliasName == alias {
			return a.CollectionName, nil
		}
	}
	return "", nil
}

// UpdateAliases performs a batched alias update (create/delete/switch).
func (c *Client) UpdateAliases(ctx context.Context, actions []map[string]interface{}) error {
	body := map[string]interface{}{
		"actions": actions,
	}
	url := fmt.Sprintf("%s/collections/aliases", c.baseURL)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("update aliases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// CreateAlias creates an alias pointing to a target collection.
func (c *Client) CreateAlias(ctx context.Context, alias, target string) error {
	return c.UpdateAliases(ctx, []map[string]interface{}{
		{
			"create_alias": map[string]string{
				"alias_name":      alias,
				"collection_name": target,
			},
		},
	})
}

// SwitchAlias atomically changes an alias from oldTarget to newTarget.
func (c *Client) SwitchAlias(ctx context.Context, alias, oldTarget, newTarget string) error {
	actions := []map[string]interface{}{}
	if oldTarget != "" {
		actions = append(actions, map[string]interface{}{
			"delete_alias": map[string]string{
				"alias_name": alias,
			},
		})
	}
	actions = append(actions, map[string]interface{}{
		"create_alias": map[string]string{
			"alias_name":      alias,
			"collection_name": newTarget,
		},
	})
	return c.UpdateAliases(ctx, actions)
}

// ── Points API ───────────────────────────────────────────────────────

// UpsertPoints upserts a batch of points into a collection.
func (c *Client) UpsertPoints(ctx context.Context, collection string, points []Point) error {
	if len(points) == 0 {
		return nil
	}

	body := map[string]interface{}{
		"points": points,
	}
	url := fmt.Sprintf("%s/collections/%s/points?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("upsert points to %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// DeletePoints deletes points by ID from a collection.
func (c *Client) DeletePoints(ctx context.Context, collection string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	body := map[string]interface{}{
		"points": ids,
	}
	url := fmt.Sprintf("%s/collections/%s/points/delete?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("delete points from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// CountPoints returns the number of points in a collection.
//
// PR1 — fix/qdrant-wire-contracts: the canonical Qdrant envelope for
// /collections/{name} returns `{"result": {"points_count": N, ...}}`.
// Pre-PR1 the decoder read `result` as a top-level map which silently
// returned 0 against real Qdrant (because `points_count` was one
// level too shallow). The fix mirrors the GetCollection envelope: we
// decode the full CollectionInfo value so the points_count source
// path stays consistent with the readiness / collection-manager
// consumers, and we read the count off .PointTotal (which is mapped
// from `result.points_count`). See types.go::CollectionInfo for the
// envelope contract.
func (c *Client) CountPoints(ctx context.Context, collection string) (int, error) {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, collection)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, &ErrCollectionNotFound{Name: collection}
	}
	if resp.StatusCode != http.StatusOK {
		return 0, c.parseErrorWith(opCountPoints, resp)
	}

	var info CollectionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, &APIError{
			Operation: opCountPoints,
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid collection response: %v", err),
			Retryable: false,
		}
	}
	return info.PointTotal, nil
}

// ScrollPoints iterates over all points in a collection using the Qdrant
// scroll API. Returns the batch of points and the next offset (empty string
// when iteration is complete).
//
// filter is an optional Qdrant filter (nil = no filter). When non-nil, only
// points matching the filter are returned. This is used by the QDRANT-005
// filter smoke runner to validate that payload indexes work correctly.
//
// QDRANT-003 (June 2026): used by VerifyReindex to compare Qdrant point
// IDs against SQLite assets for missing/orphan detection.
func (c *Client) ScrollPoints(ctx context.Context, collection string, offset string, limit int, filter map[string]interface{}) (*ScrollResult, error) {
	body := map[string]interface{}{
		"limit":        limit,
		"with_payload": true,
		"with_vector":  false,
	}
	if offset != "" {
		body["offset"] = offset
	}
	if filter != nil {
		body["filter"] = filter
	}

	url := fmt.Sprintf("%s/collections/%s/points/scroll", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("scroll %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &ErrCollectionNotFound{Name: collection}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	type scrollPoint struct {
		ID      string                 `json:"id"`
		Payload map[string]interface{} `json:"payload,omitempty"`
	}
	var result struct {
		Result struct {
			Points         []scrollPoint `json:"points"`
			NextPageOffset *string       `json:"next_page_offset"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode scroll result: %w", err)
	}

	points := make([]ScrollPoint, len(result.Result.Points))
	for i, p := range result.Result.Points {
		points[i] = ScrollPoint{
			ID:      p.ID,
			Payload: p.Payload,
		}
	}

	nextOffset := ""
	if result.Result.NextPageOffset != nil {
		nextOffset = *result.Result.NextPageOffset
	}

	return &ScrollResult{
		Points:     points,
		NextOffset: nextOffset,
	}, nil
}

// ── Search API ───────────────────────────────────────────────────────

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
func (c *Client) SearchPoints(ctx context.Context, collection string, req SearchRequest) ([]SearchResult, error) {
	body := map[string]interface{}{
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
// SparseQueryVector and a non-empty SparseVectorName. Callers that cannot
// provide a sparse vector must use SearchPoints (ANN) instead — dense-only
// retrieval must never be labelled as "hybrid".
//
// QDRANT-006 (June 2026): the ErrSparseRequired short-circuit kicks in
// when `req.SparseVectorName != ""` but `req.SparseQueryVector == nil`.
// This is a defensive dual of the imperative checks below — if the
// imperative paths are ever refactored away, the typed-error short-circuit
// remains as a safety net so dense-only can never be silently labelled as
// hybrid again.
func (c *Client) HybridSearchPoints(ctx context.Context, collection string, req HybridSearchRequest) ([]SearchResult, error) {
	// PR2 (fix/qdrant-bm25-indexing): the live retrieval path hands
	// raw query text + model through SparseText/SparseModel and lets
	// Qdrant run the BM25 inference server-side; the legacy raw
	// SparseQueryVector is reserved for diagnostic / bulk-from-csv
	// flows. Rejection logic must therefore accept a hybrid request
	// when EITHER (a) SparseQueryVector is set (pre-PR2 path) OR
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

	prefetch := []map[string]interface{}{
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
	// vector. The model defaults to DefaultSparseModel when empty.
	//
	// Precedence: when BOTH SparseText AND SparseQueryVector are set
	// (unexpected but legal), SparseText wins and SparseQueryVector is
	// silently discarded. Server-side BM25 is the live strategy; the
	// raw vector is reserved for diagnostic / bulk-from-csv so the
	// live path never depends on the legacy fallback.
	model := req.SparseModel
	// inference is the canonical path. When the orchestrator supplies
	// SparseText we project it through the inference model on the
	// server (no client-side tokenizer); when SparseText is empty
	// we fall through to the legacy raw-vector path so the diagnostic
	// and bulk-from-csv callers can still send a pre-computed sparse
	// vector. The model defaults to DefaultSparseModel when empty.
	model := req.SparseModel
	if model == "" {
		model = DefaultSparseModel
	}
	if req.SparseText != "" {
		prefetch = append(prefetch, map[string]interface{}{
			"query": map[string]interface{}{
				"text":  req.SparseText,
				"model": model,
			},
			"using": req.SparseVectorName,
			"limit": overfetch,
		})
	} else if req.SparseQueryVector != nil {
		prefetch = append(prefetch, map[string]interface{}{
			"query": map[string]interface{}{
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
		prefetch = append(prefetch, map[string]interface{}{
			"query": req.TranscriptVector,
			"using": req.TranscriptVectorName,
			"limit": overfetch,
		})
	}

	body := map[string]interface{}{
		"prefetch":     prefetch,
		"query":        map[string]interface{}{"fusion": "rrf"},
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

// ── Payload API ─────────────────────────────────────────────────────

// DeletePayloadKeys removes specific payload keys from points in a collection.
// pointIDs must be non-empty. This wraps the Qdrant POST /points/payload/delete
// endpoint, which is the canonical way to strip legacy keys (e.g. drive_link,
// local_path) without mutating vectors or other payload fields.
//
// QDRANT-005 (June 2026): used by LocatorCleaner to scrub legacy locator
// keys from historical points that were upserted before the QDRANT-001
// payload cleanup.
func (c *Client) DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error {
	if len(keys) == 0 || len(pointIDs) == 0 {
		return nil
	}
	body := map[string]interface{}{
		"keys":   keys,
		"points": pointIDs,
	}
	url := fmt.Sprintf("%s/collections/%s/points/payload/delete?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("delete payload keys from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// ── Payload index API ────────────────────────────────────────────────

// CreatePayloadIndex creates a payload field index.
func (c *Client) CreatePayloadIndex(ctx context.Context, collection, field, fieldType string) error {
	body := map[string]interface{}{
		"field_name": field,
		"field_type": fieldType,
	}
	url := fmt.Sprintf("%s/collections/%s/index", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("create index %q on %q: %w", field, collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}// ── HTTP helpers ─────────────────────────────────────────────────

// Operation names used as APIError.Operation discriminator.
// Keep in sync with parseErrorWith call sites; the labels flow
// into log lines (`qdrant.GetCollection: HTTP 404: …`) so per-method
// operators can grep them without parsing the underlying message.
const (
	opGetCollection    = "GetCollection"
	opListCollections  = "ListCollections"
	opCreateCollection = "CreateCollection"
	opDeleteCollection = "DeleteCollection"
	opGetAliasTarget   = "GetAliasTarget"
	opUpdateAliases    = "UpdateAliases"
	opUpsertPoints     = "UpsertPoints"
	opDeletePoints     = "DeletePoints"
	opCountPoints      = "CountPoints"
	opScrollPoints     = "ScrollPoints"
	opSearchPoints     = "SearchPoints"
	opHybridSearch     = "HybridSearchPoints"
	opDeletePayloadKey = "DeletePayloadKeys"
	opCreatePayloadIdx = "CreatePayloadIndex"
)

────

func (c *Client) doJSON(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doRequest(ctx, method, url, bytes.NewReader(data))
}

// prepareRequest is the single source of truth for request headers
// (Content-Type + api-key) — every outbound Qdrant request flows
// through here so the auth contract cannot drift across call sites.
//
// PR1 fix (reviewer feedback): previously `doRequest` and
// `DoWithHTTPClient` each inlined the Content-Type / api-key
// injection, which meant any drift in the auth contract would have
// to be remembered in both places. The helper is internal-only to
// avoid widening the public Client surface.
func (c *Client) prepareRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// PR1 — fix/qdrant-wire-contracts (P0.1):
	//   the API key is now injected by the single shared transport
	//   so the probe (health.go) and every CRUD endpoint carry the
	//   same header. Health no longer reaches around and sets
	//   X-Api-Key by hand.
	//
	// Qdrant accepts both `api-key` and `X-Api-Key` (HTTP headers
	// are case-insensitive). The canonical lowercase form is what
	// the Qdrant docs reference, so we use that.
	//
	// Trimming: a config-side trailing newline or whitespace would
	// otherwise pass the empty-check and send a polluted header
	// (auth failures with no loggable cause). Trim before comparison
	// and before setting.
	if key := strings.TrimSpace(c.apiKey); key != "" {
		req.Header.Set("api-key", key)
	}
	return req, nil
}

func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := c.prepareRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

// DoWithHTTPClient performs a request through a caller-supplied
// *http.Client. The api-key header is still injected by this Client
// so the auth contract stays centralised (PR1 — fix/qdrant-wire-contracts,
// P0.1). Used by HealthProbe.Probe so the probe can keep its own
// per-call Timeout ceiling for defense-in-depth even when Config.Timeout
// is large or unset.
func (c *Client) DoWithHTTPClient(ctx context.Context, hc *http.Client, method, url string, body io.Reader) (*http.Response, error) {
	if hc == nil {
		hc = c.httpClient
	}
	req, err := c.prepareRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return hc.Do(req)
}

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
func (c *Client) decodeSearchResults(resp *http.Response) ([]SearchResult, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseErrorWith("decodeSearchResults", resp)
	}

	type pointEntry struct {
		ID      string                 `json:"id"`
		Score   float64                `json:"score"`
		Payload map[string]interface{} `json:"payload,omitempty"`
		Version int64                  `json:"version,omitempty"`
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
		results := make([]SearchResult, len(envelope.Result.Points))
		for i, r := range envelope.Result.Points {
			results[i] = SearchResult{
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
	results := make([]SearchResult, len(legacy.Result))
	for i, r := range legacy.Result {
		results[i] = SearchResult{
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
func (c *Client) executeQuery(ctx context.Context, collection string, body map[string]interface{}) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/query", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("query %q: %w", collection, err)
	}
	defer resp.Body.Close()
	return c.decodeSearchResults(resp)
}

// parseError converts a non-2xx Qdrant response into a typed *APIError.
//
// PR1 — fix/qdrant-wire-contracts: this is the single canonical entry
// for every wire-level error the client surfaces. Callers MUST use
// the typed value (errors.As) rather than parsing the message string
// downstream. Use parseErrorWith when the call site knows which method
// the request came from so the operation label is meaningful in logs.
func (c *Client) parseError(resp *http.Response) error {
	return c.parseErrorWith("qdrant", resp)
}

// parseErrorWith is parseError + an explicit operation label. All
// Client public methods that issue HTTP requests SHOULD pass their
// op* constant so the labelled error indicates which endpoint
// failed.
func (c *Client) parseErrorWith(op string, resp *http.Response) error {
	if resp == nil {
		return &APIError{
			Operation: op,
			Status:    0,
			Message:   "nil response",
			Retryable: true,
		}
	}
	body := readAPIBody(resp.Body)
	return &APIError{
		Operation: op,
		Status:    resp.StatusCode,
		Message:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, body),
		Body:      body,
		Retryable: classifyRetryability(resp.StatusCode),
	}
}

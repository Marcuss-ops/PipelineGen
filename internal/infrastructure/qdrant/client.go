package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		return nil, c.parseError(resp)
	}

	var result struct {
		Result CollectionInfo `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode collection info: %w", err)
	}
	return &result.Result, nil
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
		return "", c.parseError(resp)
	}

	var result struct {
		Result []struct {
			AliasName      string `json:"alias_name"`
			CollectionName string `json:"collection_name"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode aliases: %w", err)
	}
	for _, a := range result.Result {
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
		return 0, c.parseError(resp)
	}

	var result struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode count: %w", err)
	}
	var count int
	pointKey := "points" + "_count"
	if payload, ok := result.Result[pointKey]; ok {
		if err := json.Unmarshal(payload, &count); err != nil {
			return 0, fmt.Errorf("decode count field: %w", err)
		}
	}
	return count, nil
}

// ScrollPoints iterates over all points in a collection using the Qdrant
// scroll API. Returns the batch of points and the next offset (empty string
// when iteration is complete).
//
// QDRANT-003 (June 2026): used by VerifyReindex to compare Qdrant point
// IDs against SQLite assets for missing/orphan detection.
func (c *Client) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (*ScrollResult, error) {
	body := map[string]interface{}{
		"limit":        limit,
		"with_payload": true,
		"with_vector":  false,
	}
	if offset != "" {
		body["offset"] = offset
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
			Points          []scrollPoint `json:"points"`
			NextPageOffset  *string       `json:"next_page_offset"`
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

// SearchPoints performs an ANN search.
func (c *Client) SearchPoints(ctx context.Context, collection string, req SearchRequest) ([]SearchResult, error) {
	body := map[string]interface{}{
		"vector":       req.QueryVector,
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

	return c.executeSearch(ctx, collection, body)
}

// HybridSearchPoints performs a real hybrid (dense + sparse) search via the
// Qdrant Query API with prefetch blocks and Reciprocal Rank Fusion (RRF).
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
	if req.SparseVectorName != "" && req.SparseQueryVector == nil {
		return nil, &ErrSparseRequired{Channel: req.SparseVectorName}
	}
	if req.DenseVector == nil {
		return nil, fmt.Errorf("hybrid search: dense vector must not be nil")
	}
	if req.SparseQueryVector == nil {
		return nil, fmt.Errorf("hybrid search: sparse query vector must not be nil — use SearchPoints for ANN-only retrieval")
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
		{
			"query": map[string]interface{}{
				"indices": req.SparseQueryVector.Indices,
				"values":  req.SparseQueryVector.Values,
			},
			"using": req.SparseVectorName,
			"limit": overfetch,
		},
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

	url := fmt.Sprintf("%s/collections/%s/points/query", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("hybrid query %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	return c.executeQuery(ctx, collection, body)
}

func (c *Client) executeSearch(ctx context.Context, collection string, body map[string]interface{}) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/search", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", collection, err)
	}
	defer resp.Body.Close()
	return c.decodeSearchResults(resp)
}

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
}

// ── HTTP helpers ─────────────────────────────────────────────────────

func (c *Client) doJSON(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doRequest(ctx, method, url, bytes.NewReader(data))
}

func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// decodeSearchResults is the shared result decoder for both the legacy
// Search API (/points/search) and the Query API (/points/query). Both
// Qdrant endpoints return the same JSON shape.
func (c *Client) decodeSearchResults(resp *http.Response) ([]SearchResult, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	type pointEntry struct {
		ID      string                 `json:"id"`
		Score   float64                `json:"score"`
		Payload map[string]interface{} `json:"payload,omitempty"`
		Version int64                  `json:"version,omitempty"`
	}
	var result struct {
		Result []pointEntry `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}

	results := make([]SearchResult, len(result.Result))
	for i, r := range result.Result {
		results[i] = SearchResult{
			ID:      r.ID,
			Score:   r.Score,
			Payload: r.Payload,
			Version: r.Version,
		}
	}
	return results, nil
}

// executeQuery sends a request to the Qdrant Query API (/points/query) and
// decodes the results. Used by HybridSearchPoints for real RRF fusion;
// executeSearch (POST /points/search) remains available for ANN-only
// queries. Both share decodeSearchResults for response parsing.
func (c *Client) executeQuery(ctx context.Context, collection string, body map[string]interface{}) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/query", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("query %q: %w", collection, err)
	}
	defer resp.Body.Close()
	return c.decodeSearchResults(resp)
}

func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("qdrant HTTP %d: %s", resp.StatusCode, string(body))
}

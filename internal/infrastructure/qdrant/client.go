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

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Client is a typed HTTP client for the Qdrant REST API.
// All Qdrant communication flows through this client.
type Client struct {
	baseURL    string
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
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}
}

// BaseURL returns the configured Qdrant base URL.
func (c *Client) BaseURL() string { return c.baseURL }

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
		Result struct {
			PointsCount int `json:"points_count"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode count: %w", err)
	}
	return result.Result.PointsCount, nil
}

// UpsertVectorAssets converts a batch of VectorAsset domain objects into
// Qdrant points and upserts them into the given collection. Used by the
// admin reindex command to bulk-load assets from SQLite into Qdrant.
func (c *Client) UpsertVectorAssets(ctx context.Context, collection string, assets []VectorAsset) error {
	if len(assets) == 0 {
		return nil
	}
	points := make([]Point, 0, len(assets))
	for _, a := range assets {
		pt, ok := vectorAssetToPoint(a)
		if !ok {
			continue
		}
		points = append(points, pt)
	}
	if len(points) == 0 {
		return nil
	}
	return c.UpsertPoints(ctx, collection, points)
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

// HybridSearchPoints performs a hybrid (dense + sparse) search.
func (c *Client) HybridSearchPoints(ctx context.Context, collection string, req HybridSearchRequest) ([]SearchResult, error) {
	body := map[string]interface{}{
		"vector":       req.DenseVector,
		"using":        req.DenseVectorName,
		"limit":        req.Limit,
		"with_payload": true,
	}
	if req.MinScore > 0 {
		body["score_threshold"] = req.MinScore
	}
	if req.Filter != nil {
		body["filter"] = req.Filter
	}

	return c.executeSearch(ctx, collection, body)
}

func (c *Client) executeSearch(ctx context.Context, collection string, body map[string]interface{}) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/search", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result struct {
		Result []struct {
			ID      string                 `json:"id"`
			Score   float64                `json:"score"`
			Payload map[string]interface{} `json:"payload,omitempty"`
			Version int64                  `json:"version,omitempty"`
		} `json:"result"`
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

func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("qdrant HTTP %d: %s", resp.StatusCode, string(body))
}

// vectorAssetToPoint converts a VectorAsset into a qdrant Point.
// Returns (Point, false) when the asset has no embeddings or no AssetID.
//
// QDRANT-001 (June 2026) point-ID convention (single canonical generator):
//
//	Point.ID = UUIDv5(asset_id)
//
// The asset ID is the canonical media_assets.id primary key; UUIDv5
// (RFC 4122 namespace+DNS) gives a stable, deterministic, opaque Qdrant
// point ID derived from it. Three earlier conventions converged onto
// this one:
//   - legacy Python sync_drive_qdrant.py used UUIDv5(drive_file_id)
//     (REMOVED in QDRANT-001 closure — Python is now HTTP client only).
//   - legacy admin path used UUIDv5(asset_id) (same value computed here).
//   - new Go mapper is `Point.ID = asset.ID` — explicitly NOT the form
//     this function returns; production callers must use this helper.
//
// Any caller outside the qdrant package that needs to write a point
// MUST go through IndexWriter.Port.UpsertVectorAssets which uses this
// function. Do not introduce a second point-ID convention.
func vectorAssetToPoint(a VectorAsset) (Point, bool) {
	vectors := make(map[string]interface{})
	if len(a.TextEmbedding) > 0 {
		vectors["text"] = a.TextEmbedding
	}
	if len(a.VisualEmbedding) > 0 {
		vectors["visual"] = a.VisualEmbedding
	}
	if len(a.TranscriptEmbedding) > 0 {
		vectors["transcript"] = a.TranscriptEmbedding
	}
	if len(vectors) == 0 || strings.TrimSpace(a.AssetID) == "" {
		return Point{}, false
	}
	return Point{
		ID:      uuid.NewSHA1(uuid.NameSpaceDNS, []byte(a.AssetID)).String(),
		Vectors: vectors,
		Payload: map[string]interface{}{
			"asset_id":            a.AssetID,
			"name":                a.Name,
			"source":              a.Source,
			"category":            a.Category,
			"style":               a.Style,
			"media_type":          a.MediaType,
			"search_text":         a.SearchText,
			"drive_link":          a.DriveLink,
			"local_path":          a.LocalPath,
			"tags":                a.Tags,
			"duration_ms":         a.DurationMs,
			"language":            a.Language,
			"youtube_video_id":    a.YouTubeVideoID,
			"youtube_url":         a.YouTubeURL,
			"start_time":          a.StartTime,
			"end_time":            a.EndTime,
			"embedding_version":   a.EmbeddingVersion,
			"search_text_version": a.SearchTextVersion,
			"created_at":          a.CreatedAt,
		},
	}, true
}

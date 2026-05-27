package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// QdrantClient implements the Store interface via Qdrant's REST API.
// It communicates with the Qdrant HTTP endpoint (default port 6333).
//
// API reference: https://qdrant.tech/documentation/api/
type QdrantClient struct {
	baseURL    string
	collection string
	cfg        Config
	httpClient *http.Client
}

// Config holds Qdrant-specific configuration.
type Config struct {
	URL              string
	Collection       string
	TextVectorName   string
	VisualVectorName string
	AudioVectorName  string
	TextDimensions   int
	VisualDimensions int
	AudioDimensions  int
	MinInstantScore  float64
	TimeoutMs        int
}

// NewQdrantClient creates a new Qdrant HTTP client.
func NewQdrantClient(cfg Config) *QdrantClient {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	baseURL := strings.TrimRight(cfg.URL, "/")

	return &QdrantClient{
		baseURL:    baseURL,
		collection: cfg.Collection,
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// qdrantRequest sends an HTTP request to Qdrant and decodes the response.
func (c *QdrantClient) qdrantRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	url := c.baseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return respBody, fmt.Errorf("qdrant HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// EnsureCollection creates the collection with named vector config if missing.
func (c *QdrantClient) EnsureCollection(ctx context.Context) error {
	// Check if collection exists
	_, err := c.qdrantRequest(ctx, "GET", fmt.Sprintf("/collections/%s", c.collection), nil)
	if err == nil {
		return nil // already exists
	}

	// Create collection with named vectors
	vectorsConfig := map[string]interface{}{
		c.cfg.TextVectorName: map[string]interface{}{
			"size":     c.cfg.TextDimensions,
			"distance": "Cosine",
		},
		c.cfg.VisualVectorName: map[string]interface{}{
			"size":     c.cfg.VisualDimensions,
			"distance": "Cosine",
		},
	}
	if c.cfg.AudioVectorName != "" {
		vectorsConfig[c.cfg.AudioVectorName] = map[string]interface{}{
			"size":     c.cfg.AudioDimensions,
			"distance": "Cosine",
		}
	}

	createReq := map[string]interface{}{
		"name":    c.collection,
		"vectors": vectorsConfig,
	}

	_, err = c.qdrantRequest(ctx, "PUT", fmt.Sprintf("/collections/%s", c.collection), createReq)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	return nil
}

// UpsertAsset indexes a single asset as a point with named vectors.
func (c *QdrantClient) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	// Build payload with named vectors
	vectors := make(map[string][]float32)
	if len(asset.TextEmbedding) > 0 {
		vectors[c.cfg.TextVectorName] = asset.TextEmbedding
	}
	if len(asset.VisualEmbedding) > 0 {
		vectors[c.cfg.VisualVectorName] = asset.VisualEmbedding
	}
	if len(asset.AudioEmbedding) > 0 && c.cfg.AudioVectorName != "" {
		vectors[c.cfg.AudioVectorName] = asset.AudioEmbedding
	}

	if len(vectors) == 0 {
		return fmt.Errorf("no embeddings provided for asset %s", asset.AssetID)
	}

	payload := map[string]interface{}{
		"asset_id":   asset.AssetID,
		"source":     asset.Source,
		"name":       asset.Name,
		"local_path": asset.LocalPath,
		"drive_link": asset.DriveLink,
		"category":   asset.Category,
		"style":      asset.Style,
		"media_type": asset.MediaType,
		"duration_ms": asset.DurationMs,
		"tags":       asset.Tags,
	}

	if !asset.CreatedAt.IsZero() {
		payload["created_at"] = asset.CreatedAt.Format(time.RFC3339)
	}

	point := map[string]interface{}{
		"id":      asset.AssetID,
		"vector":  vectors,
		"payload": payload,
	}

	upsertReq := map[string]interface{}{
		"points": []interface{}{point},
	}

	_, err := c.qdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/points?wait=true", c.collection), upsertReq)
	if err != nil {
		return fmt.Errorf("upsert point: %w", err)
	}

	return nil
}

// Search performs ANN search using named vectors.
func (c *QdrantClient) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if len(req.QueryVector) == 0 {
		return nil, fmt.Errorf("empty query vector")
	}

	if req.VectorName == "" {
		req.VectorName = c.cfg.TextVectorName
	}

	if req.Limit <= 0 {
		req.Limit = 10
	}

	minScore := req.MinScore
	if minScore <= 0 {
		minScore = c.cfg.MinInstantScore
	}

	// Build search request
	searchReq := map[string]interface{}{
		"vector": map[string]interface{}{
			"name":   req.VectorName,
			"vector": req.QueryVector,
		},
		"limit":     req.Limit * 2, // Fetch extra for filtering
		"with_payload": true,
		"score_threshold": minScore,
	}

	// Add optional filters
	var mustConditions []map[string]interface{}

	if req.Source != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"key":   "source",
			"match": map[string]interface{}{"value": req.Source},
		})
	}
	if req.Category != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"key":   "category",
			"match": map[string]interface{}{"value": req.Category},
		})
	}
	if req.MediaType != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"key":   "media_type",
			"match": map[string]interface{}{"value": req.MediaType},
		})
	}

	if len(mustConditions) > 0 {
		searchReq["filter"] = map[string]interface{}{
			"must": mustConditions,
		}
	}

	respBody, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/search", c.collection), searchReq)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// Parse Qdrant response
	var qdrantResp struct {
		Result []struct {
			ID      string                 `json:"id"`
			Score   float64                `json:"score"`
			Payload map[string]interface{} `json:"payload"`
		} `json:"result"`
	}

	if err := json.Unmarshal(respBody, &qdrantResp); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(qdrantResp.Result))
	for _, r := range qdrantResp.Result {
		if r.Score < minScore {
			continue
		}

		sr := SearchResult{
			AssetID: r.ID,
			Score:   r.Score,
		}

		if p, ok := r.Payload["source"]; ok {
			sr.Source, _ = p.(string)
		}
		if p, ok := r.Payload["name"]; ok {
			sr.Name, _ = p.(string)
		}
		if p, ok := r.Payload["local_path"]; ok {
			sr.LocalPath, _ = p.(string)
		}
		if p, ok := r.Payload["drive_link"]; ok {
			sr.DriveLink, _ = p.(string)
		}
		if p, ok := r.Payload["category"]; ok {
			sr.Category, _ = p.(string)
		}
		if p, ok := r.Payload["media_type"]; ok {
			sr.MediaType, _ = p.(string)
		}

		results = append(results, sr)
	}

	// Trim to requested limit
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}

	return results, nil
}

// DeleteAsset removes an asset from Qdrant by point ID.
func (c *QdrantClient) DeleteAsset(ctx context.Context, assetID string) error {
	deleteReq := map[string]interface{}{
		"points": []interface{}{assetID},
	}

	_, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/delete?wait=true", c.collection), deleteReq)
	if err != nil {
		return fmt.Errorf("delete point: %w", err)
	}

	return nil
}

// Health checks that Qdrant is reachable.
func (c *QdrantClient) Health(ctx context.Context) error {
	_, err := c.qdrantRequest(ctx, "GET", "/health", nil)
	return err
}

// Close is a no-op for the HTTP client (connections are pooled and auto-closed).
func (c *QdrantClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// compile-time interface check
var _ Store = (*QdrantClient)(nil)
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
	URL               string
	Collection        string
	TextVectorName    string
	VisualVectorName  string
	AudioVectorName   string
	SparseVectorName  string
	TextDimensions    int
	VisualDimensions  int
	AudioDimensions   int
	MinInstantScore   float64
	TimeoutMs         int
	BatchSize         int // max assets per batch upsert (0 = no chunking, default 500)
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

	// HNSW index parameters optimized for recall (not speed):
	// m=16 balances memory (~200 MB per 1M vectors) vs recall quality
	// ef_construct=100 ensures high-quality graph during indexing
	createReq := map[string]interface{}{
		"name":    c.collection,
		"vectors": vectorsConfig,
		"hnsw_config": map[string]interface{}{
			"m":            16,
			"ef_construct": 100,
		},
	}

	// Add sparse vector config for BM25 if name is set
	if c.cfg.SparseVectorName != "" {
		createReq["sparse_vectors"] = map[string]interface{}{
			c.cfg.SparseVectorName: map[string]interface{}{
				"index": map[string]interface{}{
					"on_disk": false,
				},
			},
		}
	}

	_, err = c.qdrantRequest(ctx, "PUT", fmt.Sprintf("/collections/%s", c.collection), createReq)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	return nil
}

// UpsertAsset indexes a single asset as a point with named vectors.
func (c *QdrantClient) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	point, err := c.buildPoint(asset)
	if err != nil {
		return err
	}

	upsertReq := map[string]interface{}{
		"points": []interface{}{point},
	}

	_, err = c.qdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/points?wait=true", c.collection), upsertReq)
	if err != nil {
		return fmt.Errorf("upsert point: %w", err)
	}

	return nil
}

// UpsertAssets indexes multiple assets in a single batch operation.
// Uses Qdrant's batch points API for up to 100x throughput vs sequential upserts.
func (c *QdrantClient) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	if len(assets) == 0 {
		return nil
	}

	points := make([]interface{}, 0, len(assets))
	for _, asset := range assets {
		point, err := c.buildPoint(asset)
		if err != nil {
			return fmt.Errorf("build point %s: %w", asset.AssetID, err)
		}
		points = append(points, point)
	}

	upsertReq := map[string]interface{}{
		"points": points,
	}

	_, err := c.qdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/points?wait=true", c.collection), upsertReq)
	if err != nil {
		return fmt.Errorf("upsert batch of %d points: %w", len(assets), err)
	}

	return nil
}

// buildPoint constructs a Qdrant point (id + vectors + payload) from a VectorAsset.
func (c *QdrantClient) buildPoint(asset VectorAsset) (map[string]interface{}, error) {
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

	if len(vectors) == 0 && asset.SparseBM25 == nil {
		return nil, fmt.Errorf("no embeddings provided for asset %s", asset.AssetID)
	}

	pointVectors := make(map[string]interface{})
	for name, vec := range vectors {
		pointVectors[name] = vec
	}

	if asset.SparseBM25 != nil && c.cfg.SparseVectorName != "" {
		pointVectors[c.cfg.SparseVectorName] = map[string]interface{}{
			"indices": asset.SparseBM25.Indices,
			"values":  asset.SparseBM25.Values,
		}
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
		"tags":        asset.Tags,
		"search_text": asset.SearchText,
	}

	if !asset.CreatedAt.IsZero() {
		payload["created_at"] = asset.CreatedAt.Format(time.RFC3339)
	}

	return map[string]interface{}{
		"id":      asset.AssetID,
		"vector":  pointVectors,
		"payload": payload,
	}, nil
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

	return parseSearchResults(respBody, minScore, req.Limit)
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

// CollectionInfo returns metadata about the collection including point count.
func (c *QdrantClient) CollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	respBody, err := c.qdrantRequest(ctx, "GET", fmt.Sprintf("/collections/%s", c.collection), nil)
	if err != nil {
		return nil, fmt.Errorf("collection info: %w", err)
	}

	var info struct {
		Result struct {
			PointsCount int64 `json:"points_count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &info); err != nil {
		return nil, fmt.Errorf("parse collection info: %w", err)
	}

	return &CollectionInfo{
		PointsCount: info.Result.PointsCount,
	}, nil
}

// Close is a no-op for the HTTP client (connections are pooled and auto-closed).
func (c *QdrantClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// compile-time interface check
var _ Store = (*QdrantClient)(nil)

// parseSearchResults decodes Qdrant search response into SearchResult slice.
func parseSearchResults(respBody []byte, minScore float64, limit int) ([]SearchResult, error) {
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
		sr := searchResultFromPayload(r.ID, r.Score, r.Payload)
		results = append(results, sr)
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func searchResultFromPayload(id string, score float64, payload map[string]interface{}) SearchResult {
	sr := SearchResult{AssetID: id, Score: score}
	if p, ok := payload["source"]; ok {
		sr.Source, _ = p.(string)
	}
	if p, ok := payload["name"]; ok {
		sr.Name, _ = p.(string)
	}
	if p, ok := payload["local_path"]; ok {
		sr.LocalPath, _ = p.(string)
	}
	if p, ok := payload["drive_link"]; ok {
		sr.DriveLink, _ = p.(string)
	}
	if p, ok := payload["category"]; ok {
		sr.Category, _ = p.(string)
	}
	if p, ok := payload["media_type"]; ok {
		sr.MediaType, _ = p.(string)
	}
	if p, ok := payload["style"]; ok {
		sr.Style, _ = p.(string)
	}
	if p, ok := payload["tags"]; ok {
		switch t := p.(type) {
		case []interface{}:
			for _, tag := range t {
				if s, ok := tag.(string); ok {
					sr.Tags = append(sr.Tags, s)
				}
			}
		case []string:
			sr.Tags = t
		}
	}
	if p, ok := payload["search_text"]; ok {
		sr.SearchText, _ = p.(string)
	}
	return sr
}

// HybridSearch performs hybrid dense+sparse search using Qdrant prefetch + RRF fusion.
func (c *QdrantClient) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 10
	}

	denseName := req.DenseVectorName
	if denseName == "" {
		denseName = c.cfg.TextVectorName
	}
	sparseName := req.SparseVectorName
	if sparseName == "" {
		sparseName = c.cfg.SparseVectorName
	}

	prefetchLimit := req.Limit * 10

	// Build prefetch: dense ANN + sparse BM25
	prefetch := []map[string]interface{}{}

	if len(req.DenseVector) > 0 {
		prefetch = append(prefetch, map[string]interface{}{
			"query": req.DenseVector,
			"using": denseName,
			"limit": prefetchLimit,
		})
	}

	if req.SparseVector != nil && len(req.SparseVector.Indices) > 0 && sparseName != "" {
		prefetch = append(prefetch, map[string]interface{}{
			"query": map[string]interface{}{
				"indices": req.SparseVector.Indices,
				"values":  req.SparseVector.Values,
			},
			"using": sparseName,
			"limit": prefetchLimit,
		})
	}

	if len(prefetch) == 0 {
		return nil, fmt.Errorf("no vectors provided for hybrid search")
	}

	searchReq := map[string]interface{}{
		"prefetch":     prefetch,
		"query":        map[string]interface{}{"fusion": "rrf"},
		"limit":        req.Limit,
		"with_payload": true,
	}

	// Add optional filters
	var mustConditions []map[string]interface{}
	if req.Source != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"key": "source", "match": map[string]interface{}{"value": req.Source},
		})
	}
	if req.Category != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"key": "category", "match": map[string]interface{}{"value": req.Category},
		})
	}
	if req.MediaType != "" {
		mustConditions = append(mustConditions, map[string]interface{}{
			"key": "media_type", "match": map[string]interface{}{"value": req.MediaType},
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
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	minScore := req.MinScore
	if minScore <= 0 {
		minScore = c.cfg.MinInstantScore
	}

	return parseSearchResults(respBody, minScore, req.Limit)
}
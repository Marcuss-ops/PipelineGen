// Package qdrant provides the canonical HTTP client for vector search
// and collection management.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
)

const (
	defaultBaseURL      = "http://127.0.0.1:6333"
	defaultCollection   = "media_assets"
	defaultTextVector   = "text"
	defaultVisualVector = "visual"
	defaultAudioVector  = "audio"
	defaultTranscript   = "transcript"
	defaultSparseVector = "bm25_text"
	defaultTimeoutMs    = 5000
	defaultVectorSize   = 768
)

// Service is the canonical Qdrant client.
type Service struct {
	config   Config
	client   *http.Client
	embedder asset.Embedder
}

// NewService creates a new Qdrant Service.
func NewService(cfg Config) *Service {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = defaultTimeoutMs
	}

	var embedder asset.Embedder
	if strings.TrimSpace(cfg.EmbeddingServerURL) != "" {
		embedder = embeddings.NewHTTPTextEmbedder(cfg.EmbeddingServerURL)
	}

	return &Service{
		config:   cfg,
		client:   &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond},
		embedder: embedder,
	}
}

// Enabled returns whether the service is configured.
func (s *Service) Enabled() bool {
	return s != nil && s.config.Enabled
}

// Health checks the Qdrant connection.
func (s *Service) Health(ctx context.Context) error {
	if s == nil || !s.config.Enabled {
		return nil
	}
	resp, err := s.do(ctx, http.MethodGet, "/readyz", nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant readyz returned http %d", resp.StatusCode)
	}
	return nil
}

// EmbedTextForVector generates a text embedding through the configured sidecar.
func (s *Service) EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error) {
	if s == nil || !s.config.Enabled {
		return nil, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if s.embedder == nil {
		return nil, fmt.Errorf("embedding server not configured")
	}
	return s.embedder.Embed(ctx, text)
}

// IndexHealth returns the index health report.
func (s *Service) IndexHealth(ctx context.Context) (*IndexHealthReport, error) {
	if s == nil || !s.config.Enabled {
		return &IndexHealthReport{OK: true}, nil
	}
	info, err := s.collectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &IndexHealthReport{
		OK:              true,
		QdrantPoints:    info.PointsCount,
		DBTotal:         info.PointsCount,
		WithEmbedding:   info.PointsCount,
		DBToQdrantDelta: 0,
	}, nil
}

// OperationCollectionInfo returns collection info for diagnostics.
func (s *Service) OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	if s == nil || !s.config.Enabled {
		return &CollectionInfo{}, nil
	}
	return s.collectionInfo(ctx)
}

// Search performs a dense vector search.
func (s *Service) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if s == nil || !s.config.Enabled {
		return nil, nil
	}
	if len(req.QueryVector) == 0 {
		return nil, fmt.Errorf("query vector is required")
	}
	collection := s.collectionName()
	results, err := s.searchPoints(ctx, collection, req.VectorName, req.QueryVector, req.Limit, req.MinScore, buildFilter(req.Source, req.Category, req.MediaType, req.Language))
	if err != nil {
		return nil, err
	}
	return results, nil
}

// HybridSearch performs a hybrid search by fusing dense searches.
func (s *Service) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if s == nil || !s.config.Enabled {
		return nil, nil
	}
	collection := s.collectionName()
	filter := buildFilter(req.Source, req.Category, req.MediaType, req.Language)

	queries := make([][]SearchResult, 0, 2)
	if len(req.DenseVector) > 0 {
		denseName := req.DenseVectorName
		if denseName == "" {
			denseName = s.textVectorName()
		}
		if denseName != "" {
			results, err := s.searchPoints(ctx, collection, denseName, req.DenseVector, req.Limit, req.MinScore, filter)
			if err != nil {
				return nil, err
			}
			queries = append(queries, results)
		}
	}
	if len(req.TranscriptVector) > 0 {
		transcriptName := req.TranscriptVectorName
		if transcriptName == "" {
			transcriptName = s.transcriptVectorName()
		}
		if transcriptName != "" {
			results, err := s.searchPoints(ctx, collection, transcriptName, req.TranscriptVector, req.Limit, req.MinScore, filter)
			if err != nil {
				return nil, err
			}
			queries = append(queries, results)
		}
	}

	if len(queries) == 0 {
		return nil, fmt.Errorf("hybrid search requires at least one vector")
	}
	return fuseSearchResults(req.Limit, queries...), nil
}

// UpsertAsset inserts or updates a single vector asset.
func (s *Service) UpsertAsset(ctx context.Context, asset VectorAsset) error {
	return s.UpsertAssets(ctx, []VectorAsset{asset})
}

// UpsertAssets inserts or updates multiple vector assets in batch.
func (s *Service) UpsertAssets(ctx context.Context, assets []VectorAsset) error {
	if s == nil || !s.config.Enabled {
		return nil
	}
	if len(assets) == 0 {
		return nil
	}
	points := make([]qdrantPoint, 0, len(assets))
	for _, a := range assets {
		point, ok := qdrantPointFromAsset(a)
		if !ok {
			continue
		}
		points = append(points, point)
	}
	if len(points) == 0 {
		return nil
	}
	body := upsertPointsRequest{Points: points}
	resp, err := s.do(ctx, http.MethodPost, "/collections/"+s.collectionName()+"/points?wait=true", body, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readHTTPError(resp, "upsert points")
	}
	return nil
}

// EnsureCollection creates the underlying Qdrant collection if absent.
func (s *Service) EnsureCollection(ctx context.Context) error {
	if s == nil || !s.config.Enabled {
		return nil
	}
	collection := s.collectionName()
	resp, err := s.do(ctx, http.MethodGet, "/collections/"+collection, nil, false)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if resp.StatusCode != http.StatusNotFound {
			return readHTTPError(resp, "get collection")
		}
	}

	reqBody, err := s.collectionCreateRequest()
	if err != nil {
		return err
	}
	resp, err = s.do(ctx, http.MethodPut, "/collections/"+collection, reqBody, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return readHTTPError(resp, "create collection")
}

// CleanupStalePoints scrolls every point and removes those whose Drive file is trashed.
func (s *Service) CleanupStalePoints(ctx context.Context, validator func(assetID, driveFileID, driveLink string) (bool, error)) (int, error) {
	if s == nil || !s.config.Enabled {
		return 0, nil
	}
	if validator == nil {
		return 0, nil
	}
	var stale []string
	err := s.ScrollAssetIDsPage(ctx, 100, func(batch []string) error {
		for _, id := range batch {
			point, err := s.getPoint(ctx, id)
			if err != nil {
				return err
			}
			assetID := firstPayloadString(point.Payload, "asset_id", "")
			if assetID == "" {
				assetID = id
			}
			ok, err := validator(assetID, firstPayloadString(point.Payload, "drive_file_id", ""), firstPayloadString(point.Payload, "drive_link", ""))
			if err != nil {
				return err
			}
			if !ok {
				stale = append(stale, id)
			}
		}
		return nil
	})
	if err != nil {
		return len(stale), err
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if err := s.DeletePoints(ctx, stale); err != nil {
		return len(stale), err
	}
	return len(stale), nil
}

// ScrollAssetIDsPage streams Qdrant asset IDs in batches through fn.
func (s *Service) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error {
	if s == nil || !s.config.Enabled {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	var offset any
	for {
		var resp scrollResponse
		body := scrollRequest{Limit: batchSize, WithPayload: true, WithVector: false}
		if offset != nil {
			body.Offset = offset
		}
		httpResp, err := s.do(ctx, http.MethodPost, "/collections/"+s.collectionName()+"/points/scroll", body, true)
		if err != nil {
			return err
		}
		if httpResp.StatusCode != http.StatusOK {
			err = readHTTPError(httpResp, "scroll points")
			httpResp.Body.Close()
			return err
		}
		if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
			httpResp.Body.Close()
			return fmt.Errorf("decode scroll response: %w", err)
		}
		httpResp.Body.Close()

		ids := make([]string, 0, len(resp.Result.Points))
		for _, point := range resp.Result.Points {
			if id := pointIDToString(point.ID); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			if err := fn(ids); err != nil {
				return err
			}
		}
		if resp.Result.NextPageOffset == nil {
			return nil
		}
		offset = resp.Result.NextPageOffset
		if len(ids) == 0 {
			return nil
		}
	}
}

// DeletePoints removes qdrant points for the given asset IDs.
func (s *Service) DeletePoints(ctx context.Context, assetIDs []string) error {
	if s == nil || !s.config.Enabled {
		return nil
	}
	ids := make([]string, 0, len(assetIDs))
	for _, id := range assetIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	body := deletePointsRequest{Points: ids}
	resp, err := s.do(ctx, http.MethodPost, "/collections/"+s.collectionName()+"/points/delete?wait=true", body, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readHTTPError(resp, "delete points")
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

func (s *Service) collectionName() string {
	if s == nil {
		return defaultCollection
	}
	if name := strings.TrimSpace(s.config.Collection); name != "" {
		return name
	}
	return defaultCollection
}

func (s *Service) textVectorName() string {
	if s == nil {
		return defaultTextVector
	}
	if name := strings.TrimSpace(s.config.TextVectorName); name != "" {
		return name
	}
	return defaultTextVector
}

func (s *Service) transcriptVectorName() string {
	if s == nil {
		return defaultTranscript
	}
	if name := strings.TrimSpace(s.config.TranscriptVectorName); name != "" {
		return name
	}
	return defaultTranscript
}

func (s *Service) sparseVectorName() string {
	if s == nil {
		return defaultSparseVector
	}
	if name := strings.TrimSpace(s.config.SparseVectorName); name != "" {
		return name
	}
	return defaultSparseVector
}

func (s *Service) baseURL() string {
	if s == nil {
		return defaultBaseURL
	}
	if raw := strings.TrimSpace(s.config.URL); raw != "" {
		return strings.TrimRight(raw, "/")
	}
	return defaultBaseURL
}

func (s *Service) do(ctx context.Context, method, path string, body any, jsonBody bool) (*http.Response, error) {
	var reader io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL()+path, reader)
	if err != nil {
		return nil, err
	}
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey := strings.TrimSpace(s.config.APIKey); apiKey != "" {
		req.Header.Set("api-key", apiKey)
	}
	return s.client.Do(req)
}

func (s *Service) collectionInfo(ctx context.Context) (*CollectionInfo, error) {
	resp, err := s.do(ctx, http.MethodGet, "/collections/"+s.collectionName(), nil, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readHTTPError(resp, "get collection")
	}
	var decoded struct {
		Result struct {
			PointsCount int `json:"points_count"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode collection response: %w", err)
	}
	return &CollectionInfo{PointsCount: decoded.Result.PointsCount}, nil
}

func (s *Service) collectionCreateRequest() (any, error) {
	vectors := make(map[string]any)
	addDense := func(name string, size int) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if size <= 0 {
			size = defaultVectorSize
		}
		vectors[name] = map[string]any{
			"size":     size,
			"distance": "Cosine",
		}
	}

	addDense(s.textVectorName(), s.config.TextDimensions)
	addDense(strings.TrimSpace(s.config.VisualVectorName), s.config.VisualDimensions)
	addDense(strings.TrimSpace(s.config.AudioVectorName), s.config.AudioDimensions)
	addDense(s.transcriptVectorName(), s.config.TranscriptDimensions)

	if len(vectors) == 0 {
		return nil, fmt.Errorf("no vector definitions configured")
	}

	req := map[string]any{
		"vectors": vectors,
	}
	if sparse := s.sparseVectorName(); sparse != "" {
		req["sparse_vectors"] = map[string]any{
			sparse: map[string]any{
				"index": map[string]any{
					"on_disk": true,
				},
			},
		}
	}
	return req, nil
}

func (s *Service) searchPoints(ctx context.Context, collection, vectorName string, vector []float32, limit int, minScore float64, filter *qdrantFilter) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	body := searchRequest{
		Vector:      namedVectorPayload(vectorName, vector),
		Limit:       limit,
		WithPayload: true,
		WithVector:  false,
		ScoreThresh: nil,
		Filter:      filter,
	}
	if minScore > 0 {
		body.ScoreThresh = &minScore
	}
	resp, err := s.do(ctx, http.MethodPost, "/collections/"+collection+"/points/search", body, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readHTTPError(resp, "search points")
	}
	var decoded struct {
		Result []qdrantPointResult `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	out := make([]SearchResult, 0, len(decoded.Result))
	for _, point := range decoded.Result {
		out = append(out, searchResultFromPoint(point))
	}
	return out, nil
}

func (s *Service) getPoint(ctx context.Context, id string) (*qdrantPointResult, error) {
	resp, err := s.do(ctx, http.MethodGet, "/collections/"+s.collectionName()+"/points/"+id+"?with_payload=true&with_vector=false", nil, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readHTTPError(resp, "get point")
	}
	var decoded struct {
		Result qdrantPointResult `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode point response: %w", err)
	}
	return &decoded.Result, nil
}

func qdrantPointFromAsset(a VectorAsset) (qdrantPoint, bool) {
	vectors := make(map[string]any)
	if len(a.TextEmbedding) > 0 {
		vectors[defaultTextVector] = a.TextEmbedding
	}
	if len(a.VisualEmbedding) > 0 {
		vectors[defaultVisualVector] = a.VisualEmbedding
	}
	if len(a.TranscriptEmbedding) > 0 {
		vectors[defaultTranscript] = a.TranscriptEmbedding
	}
	if len(vectors) == 0 || strings.TrimSpace(a.AssetID) == "" {
		return qdrantPoint{}, false
	}
	return qdrantPoint{
		ID:     a.AssetID,
		Vector: vectors,
		Payload: map[string]any{
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

func namedVectorPayload(name string, vector []float32) any {
	name = strings.TrimSpace(name)
	if name == "" {
		return vector
	}
	return map[string]any{
		"name":   name,
		"vector": vector,
	}
}

func buildFilter(source, category, mediaType, language string) *qdrantFilter {
	var must []qdrantCondition
	add := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			must = append(must, qdrantCondition{
				Key: key,
				Match: &qdrantMatch{
					Value: value,
				},
			})
		}
	}
	add("source", source)
	add("category", category)
	add("media_type", mediaType)
	add("language", language)
	if len(must) == 0 {
		return nil
	}
	return &qdrantFilter{Must: must}
}

func fuseSearchResults(limit int, searches ...[]SearchResult) []SearchResult {
	if limit <= 0 {
		limit = 10
	}
	type fused struct {
		result SearchResult
		score  float64
	}
	items := make(map[string]*fused)
	for _, results := range searches {
		for rank, result := range results {
			key := result.AssetID
			if key == "" {
				key = result.QdrantPointID
			}
			if key == "" {
				key = fmt.Sprintf("rank-%d", rank)
			}
			entry := items[key]
			if entry == nil {
				copyResult := result
				entry = &fused{result: copyResult}
				items[key] = entry
			}
			entry.score += 1.0 / float64(rank+60)
			if result.Score > entry.result.Score {
				entry.result = result
			}
		}
	}
	out := make([]SearchResult, 0, len(items))
	for _, item := range items {
		item.result.Score = item.score
		out = append(out, item.result)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].AssetID < out[j].AssetID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func searchResultFromPoint(point qdrantPointResult) SearchResult {
	payload := point.Payload
	tags := payloadStrings(payload, "tags")
	return SearchResult{
		AssetID:        firstPayloadString(payload, "asset_id", pointIDToString(point.ID)),
		QdrantPointID:  pointIDToString(point.ID),
		Score:          point.Score,
		Source:         firstPayloadString(payload, "source", ""),
		Name:           firstPayloadString(payload, "name", ""),
		LocalPath:      firstPayloadString(payload, "local_path", ""),
		DriveLink:      firstPayloadString(payload, "drive_link", ""),
		Category:       firstPayloadString(payload, "category", ""),
		MediaType:      firstPayloadString(payload, "media_type", ""),
		Style:          firstPayloadString(payload, "style", ""),
		Language:       firstPayloadString(payload, "language", ""),
		YouTubeVideoID: firstPayloadString(payload, "youtube_video_id", ""),
		YouTubeURL:     firstPayloadString(payload, "youtube_url", ""),
		StartTime:      firstPayloadString(payload, "start_time", ""),
		EndTime:        firstPayloadString(payload, "end_time", ""),
		Tags:           tags,
		SearchText:     firstPayloadString(payload, "search_text", ""),
	}
}

func firstPayloadString(payload map[string]any, key, fallback string) string {
	if payload == nil {
		return fallback
	}
	if raw, ok := payload[key]; ok {
		switch v := raw.(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(v.String()); trimmed != "" {
				return trimmed
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		}
	}
	return fallback
}

func payloadStrings(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	raw, ok := payload[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return []string{fmt.Sprint(v)}
	}
}

func pointIDToString(v any) string {
	switch id := v.(type) {
	case string:
		return id
	case float64:
		return strconv.FormatInt(int64(id), 10)
	case int:
		return strconv.Itoa(id)
	case json.Number:
		return id.String()
	default:
		return fmt.Sprint(id)
	}
}

func readHTTPError(resp *http.Response, op string) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s failed: http %d: %s", op, resp.StatusCode, msg)
}

// ── Transport shapes ─────────────────────────────────────────────────────

type qdrantFilter struct {
	Must []qdrantCondition `json:"must,omitempty"`
}

type qdrantCondition struct {
	Key   string       `json:"key"`
	Match *qdrantMatch `json:"match,omitempty"`
}

type qdrantMatch struct {
	Value any `json:"value"`
}

type searchRequest struct {
	Vector      any           `json:"vector"`
	Limit       int           `json:"limit"`
	Filter      *qdrantFilter `json:"filter,omitempty"`
	WithPayload bool          `json:"with_payload"`
	WithVector  bool          `json:"with_vector"`
	ScoreThresh *float64      `json:"score_threshold,omitempty"`
}

type scrollRequest struct {
	Offset      any  `json:"offset,omitempty"`
	Limit       int  `json:"limit"`
	WithPayload bool `json:"with_payload"`
	WithVector  bool `json:"with_vector"`
}

type scrollResponse struct {
	Result struct {
		Points         []qdrantPointResult `json:"points"`
		NextPageOffset any                 `json:"next_page_offset"`
	} `json:"result"`
}

type qdrantPoint struct {
	ID      string         `json:"id"`
	Vector  map[string]any `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

type upsertPointsRequest struct {
	Points []qdrantPoint `json:"points"`
}

type deletePointsRequest struct {
	Points []string `json:"points"`
}

type qdrantPointResult struct {
	ID      any            `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

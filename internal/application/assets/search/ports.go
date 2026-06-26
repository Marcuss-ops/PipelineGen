// Package search provides application-layer use cases for media asset search:
// cross-provider search, semantic (Qdrant) search, and scene-based clip recommendation.
package search

import "context"

// ── Provider search ports ─────────────────────────────────────────────

// SearchRequest is the canonical search input.
type SearchRequest struct {
	Query     string
	MediaType string // "", "video", "image", "audio", "all"
	Limit     int
	Sort      string
}

// SearchCandidate is a single search result from a provider.
type SearchCandidate struct {
	SourceRef    string
	Title        string
	ThumbnailURL string
	PreviewURL   string
	Duration     float64
	Score        float64
}

// SearchResult is the output of a provider search.
type SearchResult struct {
	Candidates    []SearchCandidate
	NextPageToken string
}

// SearchProviderPort searches media from a single source.
type SearchProviderPort interface {
	Name() string
	Capabilities() []string
	Search(ctx context.Context, req SearchRequest) (*SearchResult, error)
}

// SearchProviderRegistry fans out searches across registered providers.
type SearchProviderRegistry interface {
	SearchProviders() []SearchProviderPort
}

// ── Semantic search ports ─────────────────────────────────────────────

// SemanticSearchRequest is the input for a vector search.
type SemanticSearchRequest struct {
	Query      string
	VectorName string // "text", "visual", "audio"
	Mode       string // "ann" or "hybrid"
	Limit      int
	MinScore   float64
	Source     string
	MediaType  string
}

// SemanticSearchResult is the output of a vector search.
type SemanticSearchResult struct {
	Query    string               `json:"query"`
	Vector   string               `json:"vector"`
	Mode     string               `json:"mode"`
	MinScore float64              `json:"min_score"`
	Count    int                  `json:"count"`
	Results  []VectorSearchResult `json:"results"`
}

// VectorSearchPort combines embedding generation with vector-store access.
// The service owns query construction and delegates embedding + retrieval
// through this narrow port.
type VectorSearchPort interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
	VectorStore() VectorStorePort
}

// VectorStorePort performs vector and hybrid retrieval against the
// configured backend.
type VectorStorePort interface {
	Search(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error)
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error)
}

// ── Recommendation ports ──────────────────────────────────────────────

// RecommendRequest is the input for scene-based clip recommendation.
type RecommendRequest struct {
	ScriptText string
	Language   string
	Source     string
	MediaType  string
	TopK       int
	MinScore   float64
}

// RecommendClipItem is a single recommended clip.
type RecommendClipItem struct {
	AssetID   string
	Title     string
	Score     float64
	Source    string
	MediaType string
	DriveLink string
	Tags      []string
	Reason    string
}

// RecommendSceneResult is the result for one scene.
type RecommendSceneResult struct {
	Scene           string
	SceneIndex      int
	Query           string
	Recommendations []RecommendClipItem
}

// RecommendResult is the full recommendation output.
type RecommendResult struct {
	ScriptPreview string
	SceneCount    int
	Scenes        []RecommendSceneResult
	TotalClips    int
	Language      string
}

// ── Local search ports ────────────────────────────────────────────────

// CatalogSearchResult is a single catalog hit.
type CatalogSearchResult struct {
	ID    string
	Name  string
	Type  string
	Score float64
}

// LocalCatalogPort searches the internal catalog.
type LocalCatalogPort interface {
	SearchAll(ctx context.Context, query string) ([]CatalogSearchResult, error)
}

// LocalClipPort searches local clips.
type LocalClipPort interface {
	SearchClips(ctx context.Context, source, query string) ([]LocalClipResult, error)
}

// LocalClipResult is a single local clip match.
type LocalClipResult struct {
	ID   string
	Name string
}

// ── Config port ───────────────────────────────────────────────────────

// VectorConfig holds named vector dimensions for search.
type VectorConfig struct {
	TextVectorName       string
	VisualVectorName     string
	AudioVectorName      string
	TranscriptVectorName string
	MinInstantScore      float64
}

// ConfigPort provides vector search configuration.
type ConfigPort interface {
	VectorConfig() VectorConfig
}

// Logger is a narrow logging port.
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

// ── Vector search domain types (application-level, no infrastructure deps) ──

// VectorSearchRequest is the input for an ANN vector search.
// Mirrors qdrant.SearchRequest without the infrastructure import.
type VectorSearchRequest struct {
	QueryVector []float32
	VectorName  string
	Limit       int
	MinScore    float64
	Source      string
	Category    string
	MediaType   string
	Language    string
}

// VectorSearchResult is a single match from a vector search.
// Mirrors qdrant.SearchResult without the infrastructure import.
type VectorSearchResult struct {
	AssetID        string   `json:"asset_id"`
	QdrantPointID  string   `json:"qdrant_point_id,omitempty"`
	Score          float64  `json:"score"`
	Reason         string   `json:"reason,omitempty"`
	Source         string   `json:"source"`
	Name           string   `json:"name"`
	LocalPath      string   `json:"local_path,omitempty"`
	DriveLink      string   `json:"drive_link,omitempty"`
	Category       string   `json:"category,omitempty"`
	MediaType      string   `json:"media_type,omitempty"`
	Style          string   `json:"style,omitempty"`
	Language       string   `json:"language,omitempty"`
	YouTubeVideoID string   `json:"youtube_video_id,omitempty"`
	YouTubeURL     string   `json:"youtube_url,omitempty"`
	StartTime      string   `json:"start_time,omitempty"`
	EndTime        string   `json:"end_time,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
}

// HybridSearchRequest combines dense and sparse vectors for hybrid search.
// Mirrors qdrant.HybridSearchRequest without the infrastructure import.
type HybridSearchRequest struct {
	QueryText            string
	DenseVector          []float32
	DenseVectorName      string
	TranscriptVector     []float32
	TranscriptVectorName string
	SparseVectorName     string
	Limit                int
	MinScore             float64
	Source               string
	Category             string
	MediaType            string
	Language             string
}

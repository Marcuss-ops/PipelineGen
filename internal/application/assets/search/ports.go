// Package search provides application-layer use cases for media asset search:
// cross-provider search across registered providers, local catalog, and local clips.
//
// Semantic search (vector/hybrid) has been consolidated into
// internal/application/mediasearch. This package retains the vector-store
// port types used by mediasearch (VectorStorePort, VectorSearchRequest, etc.).
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

// ── Typed cross-provider response ─────────────────────────────────────

// CrossSearchResponse is the typed result of a cross-provider search.
type CrossSearchResponse struct {
	Query   string                    `json:"query"`
	Type    string                    `json:"type"`
	Results map[string]ProviderResult `json:"results"`
}

// ProviderResult holds results from a single provider or local source.
type ProviderResult struct {
	Count   int               `json:"count"`
	Results []SearchCandidate `json:"results"`
	Source  string            `json:"source,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// ── Vector store ports (used by mediasearch) ──────────────────────────

// VectorStorePort is the canonical vector store interface consumed by
// mediasearch. Implementations live in infrastructure/qdrant.
type VectorStorePort interface {
	Search(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error)
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error)
}

// VectorSearchRequest is the input for an ANN vector search.
type VectorSearchRequest struct {
	QueryVector []float32
	VectorName  string
	Limit       int
	MinScore    float64
	Source      string
	Category    string
	MediaType   string
	Language    string
	WorkspaceID string // QDRANT-004: tenant isolation filter (applied to Qdrant payload)
}

// VectorSearchResult is a single match from a vector search.
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
	WorkspaceID          string // QDRANT-004: tenant isolation filter (applied to Qdrant payload)
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

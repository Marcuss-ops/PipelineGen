// Package qdrant — core types for vector search operations.
// Recreated after the original types were removed from the remote (June 2026).
package qdrant

import "time"

// Config holds qdrant service configuration.
type Config struct {
	Enabled  bool
	Host     string
	Port     int
	APIKey   string
	UseTLS   bool
	GrpcPort int
}

// SearchRequest is a Qdrant dense vector search request.
type SearchRequest struct {
	QueryVector []float32 `json:"query_vector"`
	VectorName  string    `json:"vector_name"`
	Limit       int       `json:"limit"`
	MinScore    float64   `json:"min_score"`
	Source      string    `json:"source"`
	Category    string    `json:"category"`
	MediaType   string    `json:"media_type"`
	Language    string    `json:"language"`
}

// HybridSearchRequest is a Qdrant hybrid (dense + sparse) search request.
type HybridSearchRequest struct {
	QueryText            string    `json:"query_text"`
	DenseVector          []float32 `json:"dense_vector"`
	DenseVectorName      string    `json:"dense_vector_name"`
	TranscriptVector     []float32 `json:"transcript_vector"`
	TranscriptVectorName string    `json:"transcript_vector_name"`
	SparseVectorName     string    `json:"sparse_vector_name"`
	Limit                int       `json:"limit"`
	MinScore             float64   `json:"min_score"`
	Source               string    `json:"source"`
	Category             string    `json:"category"`
	MediaType            string    `json:"media_type"`
	Language             string    `json:"language"`
}

// SearchResult is a single Qdrant search result.
type SearchResult struct {
	AssetID        string   `json:"asset_id"`
	QdrantPointID  string   `json:"qdrant_point_id"`
	Score          float64  `json:"score"`
	Reason         string   `json:"reason"`
	Source         string   `json:"source"`
	Name           string   `json:"name"`
	LocalPath      string   `json:"local_path"`
	DriveLink      string   `json:"drive_link"`
	Category       string   `json:"category"`
	MediaType      string   `json:"media_type"`
	Style          string   `json:"style"`
	Language       string   `json:"language"`
	YouTubeVideoID string   `json:"youtube_video_id"`
	YouTubeURL     string   `json:"youtube_url"`
	StartTime      string   `json:"start_time"`
	EndTime        string   `json:"end_time"`
	Tags           []string `json:"tags"`
	SearchText     string   `json:"search_text"`
}

// VectorAsset is a Qdrant vector point with all embeddings.
type VectorAsset struct {
	AssetID             string    `json:"asset_id"`
	Name                string    `json:"name"`
	Source              string    `json:"source"`
	Category            string    `json:"category"`
	Style               string    `json:"style"`
	MediaType           string    `json:"media_type"`
	SearchText          string    `json:"search_text"`
	DriveLink           string    `json:"drive_link"`
	LocalPath           string    `json:"local_path"`
	Tags                []string  `json:"tags"`
	TextEmbedding       []float32 `json:"text_embedding"`
	VisualEmbedding     []float32 `json:"visual_embedding"`
	TranscriptEmbedding []float32 `json:"transcript_embedding"`
	DurationMs          int       `json:"duration_ms"`
	Language            string    `json:"language"`
	YouTubeVideoID      string    `json:"youtube_video_id"`
	YouTubeURL          string    `json:"youtube_url"`
	StartTime           string    `json:"start_time"`
	EndTime             string    `json:"end_time"`
	EmbeddingVersion    string    `json:"embedding_version"`
	SearchTextVersion   string    `json:"search_text_version"`
	CreatedAt           time.Time `json:"created_at"`
}

// Version constants.
const (
	CurrentEmbeddingVersion  = "v2"
	CurrentSearchTextVersion = "v1"
	BM25SchemaVersion        = "v1"
)

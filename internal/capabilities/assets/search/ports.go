// Package search provides application-layer use cases for media asset search:
// cross-provider search across registered providers, local catalog, and local clips.
//
// Semantic search (vector/hybrid) has been consolidated into
// internal/application/mediasearch. This package retains the vector-store
// port types used by mediasearch (VectorStorePort, VectorSearchRequest, etc.).
package search

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/pkg/bm25"
)

// ── SearchBackend & Logger ports ──────────────────────────────────────

type SearchBackend interface {
	Name() string
	Capabilities() []Capability
	Universe() SearchUniverse
	Search(ctx context.Context, q Query) ([]Candidate, error)
}

type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}

type ChannelEncoder interface {
	EmbedTextQuery(ctx context.Context, text string) ([]float32, error)
}

type EmbeddingChannelRegistry interface {
	EmbedQuery(ctx context.Context, channel string, text string) ([]float32, error)
}

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
// mediasearch. Implementations live in platform/qdrant.
type VectorStorePort interface {
	Search(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error)
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]VectorSearchResult, error)
}

// VectorSearchRequest is the input for an ANN vector search.
type VectorSearchRequest struct {
	QueryVector    []float32
	VectorName     string
	Limit          int
	MinScore       float64
	Source         string
	Category       string
	MediaType      string
	Language       string
	LifecycleState []string // searchable lifecycle allow-list propagated to the Qdrant filter
	WorkspaceID    string   // QDRANT-004: tenant isolation filter (applied to Qdrant payload)
	IsSystem       bool     // ADMIN scope bypass — skips workspace isolation for admin users
}

// VectorSearchResult is a single match from a vector search.
//
// QDRANT-001 (June 2026): LocalPath and DriveLink have been removed.
// Server-internal locators do not belong in the search contract;
// clients that need a signed URL for an asset should go through
// the delivery service (delivery.Signer.BuildAuthorizedURL).
type VectorSearchResult struct {
	AssetID        string   `json:"asset_id"`
	QdrantPointID  string   `json:"qdrant_point_id,omitempty"`
	Score          float64  `json:"score"`
	Reason         string   `json:"reason,omitempty"`
	Source         string   `json:"source"`
	Name           string   `json:"name"`
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
//
// PR2 (fix/qdrant-bm25-indexing): server-side BM25 inference is the
// canonical strategy. The orchestrator passes the raw query text via
// SparseText (plus SparseModel, defaulting to "qdrant/bm25"); Qdrant
// then tokenizes + weights + projects the sparse vector on the server.
// The legacy client-side raw vector (SparseVector) is retained ONLY
// for diagnostic + bulk-from-csv flows that already have a
// pre-computed SparseVector; live retrieval MUST go through
// SparseText. Mode=hybrid with SparseVectorName == "" AND
// SparseVector == nil is a programming error and must fail closed
// (ErrHybridRequiresSparse) before reaching the adapter.
type HybridSearchRequest struct {
	QueryText            string
	DenseVector          []float32
	DenseVectorName      string
	TranscriptVector     []float32
	TranscriptVectorName string
	SparseVectorName     string
	// SparseText (PR2 preferred): raw text that Qdrant tokenizes
	// server-side via the SparseModel. When non-empty, the adapter
	// projects it before sending the query; this is the canonical
	// path for live retrieval.
	SparseText string
	// SparseModel is the inference model used to project SparseText
	// into a sparse vector server-side. Empty falls back to
	// "qdrant/bm25" (Qdrant server-side default).
	SparseModel string
	// SparseVector (PR2 legacy): pre-computed sparse vector from the
	// deprecated client-side bm25.Tokenize. Used only when SparseText
	// is empty.
	SparseVector   *bm25.SparseVector
	Limit          int
	MinScore       float64
	Source         string
	Category       string
	MediaType      string
	Language       string
	LifecycleState []string // searchable lifecycle allow-list propagated to the Qdrant filter
	WorkspaceID    string   // QDRANT-004: tenant isolation filter (applied to Qdrant payload)
	IsSystem       bool     // admin/reconcile flag — skips workspace must-clause in CompileQdrantFilter
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
//
// QDRANT-004 PR1 (June 2026): SparseVectorName is the canonical BM25
// channel in the Qdrant IndexSchema (default "bm25_text" — matches
// qdrant.DefaultV3Schema().SparseVectors). The orchestrator fails
// closed with ErrHybridRequiresSparse when mode=hybrid is requested
// with SparseVectorName == "" (or with a query that BM25 cannot
// tokenize into ≥1 valid token).
//
// MinInstantScore stays a single-tunable scalar; per-channel scores
// are owned by the Qdrant IndexSchema + Searcher’s RRF fusion, not by
// orchestrator-side heuristics.
type VectorConfig struct {
	TextVectorName       string
	VisualVectorName     string
	AudioVectorName      string
	TranscriptVectorName string
	SparseVectorName     string
	MinInstantScore      float64
}

// ConfigPort provides vector search configuration.
type ConfigPort interface {
	VectorConfig() VectorConfig
}

// ── Canonical channel names ──────────────────────────────────────────

const (
	ChannelText       = "text"
	ChannelTranscript = "transcript"
	ChannelVisual     = "visual"
	ChannelAudio      = "audio"
	ChannelSparse     = "bm25_text"
)

// CanonicalChannelNames returns the closed set of canonical vector channel names.
func CanonicalChannelNames() []string {
	return []string{ChannelText, ChannelTranscript, ChannelVisual, ChannelAudio, ChannelSparse}
}

// QueryEmbedder embeds search queries.
type QueryEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type MediaReadRepository interface {
	GetMany(ctx context.Context, actor Actor, assetIDs []string) ([]MediaAsset, error)
}

type AssetDeliveryService interface {
	BuildAuthorizedURL(ctx context.Context, workspace Actor, assetID string) (string, error)
}

var SearchableLifecycleStates = []string{"ACTIVE", "INDEXED", "READY", "PUBLISHED"}

func IsKnownChannel(ch string) bool {
	for _, c := range CanonicalChannelNames() {
		if c == ch {
			return true
		}
	}
	return false
}

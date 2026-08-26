package retrieved

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// RetrievalSearchOptions contains options for image retrieval searches.
type RetrievalSearchOptions struct {
	Language string
	Lang     string
	Subject  string
	Limit    int
}

// RetrievalSearchResult represents a candidate found by a retrieval provider.
type RetrievalSearchResult struct {
	AssetID       string
	Origin        detail.ImageOrigin
	Provider      detail.ImageProvider
	Name          string
	PreviewURL    string
	ImageURL      string
	PageURL       string
	SourcePageURL string
	ThumbnailURL  string
	DriveLink     string
	LegacyFileMD5 string
	Title         string
	Author        string
	License       string
	Description   string
	StyleID       string
	StyleVersion  string
	Width         int
	Height        int
	Score         float64
}

// SearchRequest represents a request for the SearchServicePort.
type SearchRequest struct {
	Query string
	Lang  string
	Tags  []string
}

// SearchResponse represents a response from the SearchServicePort.
type SearchResponse struct {
	Assets     []detail.ImageAsset
	Result     *RetrievalSearchResult
	Results    []RetrievalSearchResult
	SubService string
}

// Provider aliases the canonical RetrievalProvider interface.
type Provider = RetrievalProvider

// Registry is the user-spec'd interface declaring the single Resolve method.
type Registry interface {
	Resolve(ids []string) ([]Provider, error)
}

// RetrievalRegistryImpl aliases RetrievalProviderRegistry.
type RetrievalRegistryImpl = RetrievalProviderRegistry

// RetrievalSearchRequest aliases RetrievalSearchOptions.
type RetrievalSearchRequest = RetrievalSearchOptions

// RetrievedCandidate aliases RetrievalSearchResult.
type RetrievedCandidate = RetrievalSearchResult

// ErrProviderNotFound is the user-spec sentinel returned by the Registry.Resolve method.
var ErrProviderNotFound = errors.New("retrieved: provider id not found in registry")

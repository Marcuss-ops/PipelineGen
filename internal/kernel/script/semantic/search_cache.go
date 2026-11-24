package semantic

import (
	"context"
	"time"
)

// SemanticAssetHit is one untrusted retrieval candidate.
type SemanticAssetHit struct {
	AssetID               string
	URL                   string
	SHA256                string
	DriveLink             string `json:"-"`
	MediaType             string
	Provider              string
	Entity                string
	Width                 int
	Height                int
	Score                 float64
	RelevanceScore        float64
	SemanticScore         float64
	TechnicalQualityScore float64
	SemanticStatus        string
	VerifierVersion       string
}

// SemanticAssetSearchResponse is the provider answer for one query.
type SemanticAssetSearchResponse struct {
	Hits     []SemanticAssetHit
	Provider string
	RecallMS int64
	VerifyMS int64
}

// SemanticSearchCacheEntry is one remembered provider answer.
type SemanticSearchCacheEntry struct {
	Response     SemanticAssetSearchResponse
	StoredAt     time.Time
	ExpiresAt    time.Time
	Provider     string
	ProviderLane string
}

// SemanticSearchCache is the narrow cache contract the batch planner needs.
type SemanticSearchCache interface {
	Get(ctx context.Context, key string) (SemanticSearchCacheEntry, bool)
	Put(ctx context.Context, key string, entry SemanticSearchCacheEntry)
}

// SemanticSearchCacheInvalidator drops every answer produced by one provider.
type SemanticSearchCacheInvalidator interface {
	InvalidateProvider(ctx context.Context, provider string) (int64, error)
}

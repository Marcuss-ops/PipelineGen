// Package images — image_contract_aliases.go: compatibility facade for the
// shared image transport/read contracts now owned by images/contract.
//
// The contract package is a leaf: it depends only on stdlib, yaml and the
// kernel asset types. Keeping these aliases preserves every existing
// images.ImageFilter / images.ImageSearchResult / images.DestinationResolver
// call site while the root package contracts toward its capability surface.
package images

import imgcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/contract"

type (
	// ImageSearchTerritory is the retrieved/generated/all territory selector.
	ImageSearchTerritory = imgcontract.ImageSearchTerritory
	// ImageSearchResult is the unified search response DTO.
	ImageSearchResult = imgcontract.ImageSearchResult
	// ImageSearchResults is the search response envelope.
	ImageSearchResults = imgcontract.ImageSearchResults
	// StyleInfo is the style response DTO.
	StyleInfo = imgcontract.StyleInfo
	// StylesResponse is the styles response envelope.
	StylesResponse = imgcontract.StylesResponse
	// GoogleAccountingConfig is the optional Google Accounting integration.
	GoogleAccountingConfig = imgcontract.GoogleAccountingConfig
	// Destination is the resolved Drive destination.
	Destination = imgcontract.Destination
	// SearchResult carries an asset plus retrieval trace fields.
	SearchResult = imgcontract.SearchResult
	// ImageFilter is the canonical catalog search filter.
	ImageFilter = imgcontract.ImageFilter
	// RepositoryListFilter is the repository read-request filter.
	RepositoryListFilter = imgcontract.RepositoryListFilter
	// RepositoryImageRow is the repository read-model row.
	RepositoryImageRow = imgcontract.RepositoryImageRow
	// SubjectTagsService extracts a subject slug and tags.
	SubjectTagsService = imgcontract.SubjectTagsService
	// RemoteFetchPort is the minimal HTTP transport surface.
	RemoteFetchPort = imgcontract.RemoteFetchPort
	// DestinationResolver resolves a logical destination key.
	DestinationResolver = imgcontract.DestinationResolver
	// YamlResolver is the YAML-backed destination resolver.
	YamlResolver = imgcontract.YamlResolver
)

const (
	TerritoryRetrieved = imgcontract.TerritoryRetrieved
	TerritoryGenerated = imgcontract.TerritoryGenerated
	TerritoryAll       = imgcontract.TerritoryAll

	DefaultLimit         = imgcontract.DefaultLimit
	MaxLimit             = imgcontract.MaxLimit
	MaxListImagesLimit   = imgcontract.MaxListImagesLimit
	DefaultResolvedLimit = imgcontract.DefaultResolvedLimit

	SourceTypeKey  = imgcontract.SourceTypeKey
	RetrieverKey   = imgcontract.RetrieverKey
	PageURLKey     = imgcontract.PageURLKey
	ImageURLKey    = imgcontract.ImageURLKey
	LicenseKey     = imgcontract.LicenseKey
	AuthorKey      = imgcontract.AuthorKey
	SearchQueryKey = imgcontract.SearchQueryKey
)

var (
	ErrDestinationNotFound = imgcontract.ErrDestinationNotFound
	ErrEmptyDescription    = imgcontract.ErrEmptyDescription
	ErrNoSubjectDerivable  = imgcontract.ErrNoSubjectDerivable
)

// ResolvedLimit clamps a caller-supplied search limit.
func ResolvedLimit(l int) int {
	return imgcontract.ResolvedLimit(l)
}

// NewYamlResolver parses yamlPath into a destination resolver.
func NewYamlResolver(yamlPath, fallbackID string) (*YamlResolver, error) {
	return imgcontract.NewYamlResolver(yamlPath, fallbackID)
}

// NewYamlResolverFromMap builds an in-memory destination resolver.
func NewYamlResolverFromMap(entries map[string]string, fallbackID string) *YamlResolver {
	return imgcontract.NewYamlResolverFromMap(entries, fallbackID)
}

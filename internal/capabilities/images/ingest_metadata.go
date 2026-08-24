package images

// contextKey and its consts are shared across sub-services for
// per-request metadata tagging provenance. MetadataService owns
// the canonical tag/upload methods (metadata_service.go).
type contextKey string

const (
	SourceTypeKey  = contextKey("source_type")
	RetrieverKey   = contextKey("retriever")
	PageURLKey     = contextKey("page_url")
	ImageURLKey    = contextKey("image_url")
	LicenseKey     = contextKey("license")
	AuthorKey      = contextKey("author")
	SearchQueryKey = contextKey("search_query")
)

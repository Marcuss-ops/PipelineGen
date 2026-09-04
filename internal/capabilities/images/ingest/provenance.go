package ingest

// ContextKey is the canonical typed key for per-request image provenance.
type ContextKey string

const (
	SourceTypeKey  ContextKey = "source_type"
	RetrieverKey   ContextKey = "retriever"
	PageURLKey     ContextKey = "page_url"
	ImageURLKey    ContextKey = "image_url"
	LicenseKey     ContextKey = "license"
	AuthorKey      ContextKey = "author"
	SearchQueryKey ContextKey = "search_query"
)

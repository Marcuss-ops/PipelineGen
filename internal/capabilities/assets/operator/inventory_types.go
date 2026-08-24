package assets

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// AssetInventoryReader is the canonical read-only port used by the
// operator console to render the Content Library, Asset Inspector and
// filter facets. It is a projection, not a domain entity: every method
// returns already-shaped UI data and never exposes raw SQL rows.
type AssetInventoryReader interface {
	// List returns a page of assets matching the supplied query.
	List(ctx context.Context, query AssetInventoryQuery) (AssetInventoryPage, error)

	// Get returns the full operator inspection view for a single asset.
	Get(ctx context.Context, assetID string) (*AssetInspection, error)

	// Facets returns the counts used to populate the filter sidebar.
	// Values are derived from canonical Go enums and live DB rows.
	Facets(ctx context.Context) (*AssetInventoryFacets, error)
}

// AssetInventoryQuery is the filter/pagination surface for List.
type AssetInventoryQuery struct {
	Source         string
	Provider       string
	MediaType      string
	LifecycleState string
	AssetState     string
	IndexState     string
	Search         string
	Limit          int
	Offset         int
}

// AssetInventoryPage is the response shape for List.
type AssetInventoryPage struct {
	Items      []*AssetInventoryItem `json:"items"`
	Total      int64                 `json:"total"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

// AssetInventoryItem is the single row projection shown in the Content
// Library. It deliberately keeps the three orthogonal state machines
// separate so the UI can render distinct badges instead of a single
// overloaded status.
type AssetInventoryItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Filename  string `json:"filename"`
	Source    string `json:"source"`
	Provider  string `json:"provider"`
	MediaType string `json:"media_type"`

	LifecycleState asset.LifecycleState `json:"lifecycle_state"`
	AssetState     asset.AssetState     `json:"asset_state"`
	IndexState     asset.IndexState     `json:"index_state"`

	IndexHealth IndexHealthView `json:"index_health"`

	HasLocalFile bool `json:"has_local_file"`
	HasDriveFile bool `json:"has_drive_file"`
	HasEmbedding bool `json:"has_embedding"`

	ContentHash        string `json:"content_hash,omitempty"`
	IndexedContentHash string `json:"indexed_content_hash,omitempty"`
	EmbeddingVersion   string `json:"embedding_version,omitempty"`
	CollectionVersion  string `json:"collection_version,omitempty"`

	PendingOutboxEvents int    `json:"pending_outbox_events"`
	LastError           string `json:"last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AssetInspection is the 360-degree operator view for a single asset.
type AssetInspection struct {
	AssetInventoryItem

	// Metadata is the raw metadata_json map surfaced for the advanced
	// tabs. It is typed so the UI does not need to parse JSON.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Locations are all known physical locations for this asset.
	Locations []*asset.Location `json:"locations,omitempty"`

	// Processing contains the latest processing records per step.
	Processing []asset.ProcessingRecord `json:"processing,omitempty"`

	// OutboxEvents lists the recent outbox activity for the asset.
	OutboxEvents []OutboxEventProjection `json:"outbox_events,omitempty"`
}

// OutboxEventProjection is a lightweight operator view of an outbox row.
type OutboxEventProjection struct {
	EventType    string    `json:"event_type"`
	AggregateID  string    `json:"aggregate_id"`
	EventKey     string    `json:"event_key"`
	Status       string    `json:"status"`
	AttemptCount int       `json:"attempt_count"`
	LastError    string    `json:"last_error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AssetInventoryFacets groups filter counts. Each group is a closed
// list of canonical values so the UI never has to hardcode labels.
type AssetInventoryFacets struct {
	MediaTypes      []FacetGroup `json:"media_types"`
	LifecycleStates []FacetGroup `json:"lifecycle_states"`
	AssetStates     []FacetGroup `json:"asset_states"`
	IndexStates     []FacetGroup `json:"index_states"`
	Sources         []FacetGroup `json:"sources"`
	Providers       []FacetGroup `json:"providers"`
}

// FacetGroup is one facet value with its human-readable label and count.
type FacetGroup struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// IndexHealthCode is the canonical visual projection of the index state
// for the UI. It is not persisted; it is computed on read by the
// IndexHealthResolver.
type IndexHealthCode string

const (
	IndexHealthNotIndexable IndexHealthCode = "NOT_INDEXABLE"
	IndexHealthPending      IndexHealthCode = "PENDING"
	IndexHealthEmbedding    IndexHealthCode = "EMBEDDING"
	IndexHealthIndexing     IndexHealthCode = "INDEXING"
	IndexHealthIndexed      IndexHealthCode = "INDEXED"
	IndexHealthStale        IndexHealthCode = "STALE"
	IndexHealthRetryWait    IndexHealthCode = "RETRY_WAIT"
	IndexHealthFailed       IndexHealthCode = "FAILED"
	IndexHealthDeleted      IndexHealthCode = "DELETED"
	IndexHealthUnknown      IndexHealthCode = "UNKNOWN"
)

// IndexHealthView is the wire shape returned by the resolver.
type IndexHealthView struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	Severity    string `json:"severity"`
	Description string `json:"description,omitempty"`
	Terminal    bool   `json:"terminal"`
	Retryable   bool   `json:"retryable"`
}

// StatusView is the generic badge data returned by the backend for any
// status (lifecycle, asset_state, index health). The UI only renders the
// supplied code, label and severity; it never interprets domain rules.
type StatusView struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	Severity    string `json:"severity"`
	Description string `json:"description,omitempty"`
	Terminal    bool   `json:"terminal"`
	Retryable   bool   `json:"retryable"`
}

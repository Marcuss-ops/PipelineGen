// Package persistence — AssetCommitter (PR-ASSET-COMMITTER, July 2026).
//
// AssetCommitter is the SOLE canonical owner of the asset commit
// boundary. Every code path that durably creates or updates an asset
// (media_assets row), its locations (asset_locations), its typed
// metadata (metadata_json), and the canonical asset.index.requested
// outbox event MUST route through this port.
//
// Design goals:
//   - One transaction for all four writes.
//   - Typed metadata so producers cannot silently drop canonical keys
//     (content_hash, source_version, publish_action, etc.).
//   - Caller-owned-tx mode (CommitTx) for orchestrators like JobFinalizer
//     that already manage a transaction.
//   - Self-owned-tx mode (CommitAndIndex) for standalone producers like
//     the YouTube and Artlist atomic writers.
//
// godlike/06 SSOT: after cutover, no other package is allowed to
// INSERT/UPSERT into media_assets, asset_locations, or outbox_events
// for asset.index.requested directly. AssetCommitter is the single
// point of commit.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// TypedMetadata carries the canonical semantic fields that must be
// persisted inside media_assets.metadata_json. Using a struct instead
// of a raw map prevents callers from forgetting canonical keys and
// gives the committer a single place to merge source-specific extras.
//
// Extra is reserved for source-specific fields that do not yet
// deserve a first-class column. The committer merges Extra on top
// of the typed fields (with typed fields winning on conflict for
// the canonical keys).
type TypedMetadata struct {
	Title          string
	Origin         string
	Description    string
	SourceVersion  string
	PublishAction  string
	SizeBytes      int64
	Round          int
	Event          string
	Subject        string
	Tags           []string
	Category       string
	SourceProvider string
	SourceVideoID  string
	SourceTitle    string
	SourceChannel  string
	DrivePath      string
	IndexingStatus string
	StartSec       float64
	EndSec         float64
	Slug           string

	// Summary/Topics/Speakers/MentionedPeople/Hook/QualityScore/
	// SponsorSegment are the canonical semantic-enrichment fields
	// produced by the metadata analyzer (PR-ASSET-COMMITTER-
	// ENRICHMENT, August 2026). They were formerly hand-marshalled
	// into Extra by the YouTube metadata writer as a SECOND post-commit
	// write; promoting them here makes the single canonical commit
	// carry the complete semantic snapshot atomically.
	Summary         string
	Topics          []string
	Speakers        []string
	MentionedPeople []string
	Hook            string
	QualityScore    float64
	SponsorSegment  bool

	Extra map[string]any
}

// ToMap serialises the typed metadata into the canonical map shape
// written to media_assets.metadata_json. Canonical keys are written
// in snake_case to match the existing Qdrant PayloadMapper and the
// SourceVersionFor helper.
func (m TypedMetadata) ToMap() map[string]any {
	out := make(map[string]any)
	setIfNotEmpty := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	setIfNotEmpty("title", m.Title)
	setIfNotEmpty("origin", m.Origin)
	setIfNotEmpty("description", m.Description)
	setIfNotEmpty("source_version", m.SourceVersion)
	setIfNotEmpty("publish_action", m.PublishAction)
	setIfNotEmpty("event", m.Event)
	setIfNotEmpty("subject", m.Subject)
	setIfNotEmpty("category", m.Category)
	setIfNotEmpty("source_provider", m.SourceProvider)
	setIfNotEmpty("source_video_id", m.SourceVideoID)
	setIfNotEmpty("source_title", m.SourceTitle)
	setIfNotEmpty("source_channel", m.SourceChannel)
	setIfNotEmpty("drive_path", m.DrivePath)
	setIfNotEmpty("indexing_status", m.IndexingStatus)
	setIfNotEmpty("slug", m.Slug)
	setIfNotEmpty("summary", m.Summary)
	setIfNotEmpty("hook", m.Hook)
	if len(m.Topics) > 0 {
		out["topics"] = m.Topics
	}
	if len(m.Speakers) > 0 {
		out["speakers"] = m.Speakers
	}
	if len(m.MentionedPeople) > 0 {
		out["mentioned_people"] = m.MentionedPeople
	}
	if m.QualityScore != 0 {
		out["quality_score"] = m.QualityScore
	}
	if m.SponsorSegment {
		out["sponsor_segment"] = true
	}
	if m.SizeBytes != 0 {
		out["size_bytes"] = m.SizeBytes
	}
	if m.Round != 0 {
		out["round"] = m.Round
	}
	if m.StartSec != 0 {
		out["start_sec"] = m.StartSec
	}
	if m.EndSec != 0 {
		out["end_sec"] = m.EndSec
	}
	if len(m.Tags) > 0 {
		out["tags"] = m.Tags
	}
	for k, v := range m.Extra {
		// Typed fields win over Extra for canonical keys.
		if _, exists := out[k]; !exists && v != nil {
			out[k] = v
		}
	}
	return out
}

// LocationCommit describes one row to write into asset_locations.
// At least one location should be marked primary (IsPrimary=true);
// the committer will treat the first location as primary if none is.
type LocationCommit struct {
	Kind          string
	Provider      string
	ExternalID    string
	URI           string
	WebViewLink   string
	DownloadURL   string
	MimeType      string
	FileSizeBytes int64
	FileHash      string
	IsPrimary     bool
}

// CommitRequest is the canonical input for committing an asset.
//
// Required fields: AssetID, Source, Filename, MediaType, LifecycleState.
// ContentHash is required only when an index outbox event is requested;
// discovery-only assets may legitimately have unknown content bytes.
// Locations may be empty for callers that do not yet
// have a concrete storage location (e.g. discovery-only staging rows).
// OutboxEvent is an additional event emitted atomically with the asset commit.
// It is intentionally transport-neutral so application producers can request
// durable post-commit work without importing the SQLite outbox adapter.
type OutboxEvent struct {
	EventType     string
	AggregateID   string
	AggregateType string
	PayloadJSON   string
	EventKey      string
}

type CommitRequest struct {
	AssetID        string
	Source         string
	Name           string
	Filename       string
	MediaType      string
	Category       string
	DurationMs     int64
	ContentHash    string
	Description    string
	SearchText     string
	LifecycleState string
	IndexState     string
	LocalPath      string
	FolderID       string
	FolderPath     string
	ThumbnailURL   string
	SourceURL      string

	// AssetVersion is the source-specific version label (e.g. "v1").
	AssetVersion string
	// AssetLocation is the canonical source URI/path for the asset.
	AssetLocation string
	// Rendition is the technical rendition label (e.g. "1080p").
	Rendition string
	// Title is the canonical title, persisted to both metadata_json
	// and the dedicated media_assets.title column.
	Title string
	// SourceProvider is persisted to media_assets.source_provider.
	SourceProvider string
	// SourceVideoID is persisted to media_assets.source_video_id.
	SourceVideoID string
	// StartMs/EndMs are millisecond timestamps persisted to
	// media_assets.start_ms/end_ms.
	StartMs int64
	EndMs   int64

	// Metadata is the typed metadata envelope.
	Metadata TypedMetadata

	// Taxonomy is the canonical media taxonomy (namespace, media_type,
	// asset_kind, source_type, semantic_role). When non-zero it is
	// validated and persisted to the media_assets taxonomy columns in the
	// SAME upsert as the asset row. Zero is accepted so legacy producers
	// can converge incrementally without regressing existing writes.
	Taxonomy mediaregistry.AssetTaxonomy

	// Locations are the storage locations to upsert.
	Locations []LocationCommit

	// EmitIndexEvent controls whether an asset.index.requested outbox
	// event is emitted inside the same transaction. Most callers want
	// true; discovery-only paths may set false.
	EmitIndexEvent bool

	// IndexPriority is the outbox scheduling priority stamped on the
	// emitted asset.index.requested event (migration 186). 0 means the
	// adapter default (normal). Producers whose assets unblock script
	// generation (stock pipeline finalizer) set persistence.IndexPriorityHigh
	// so ClaimNext claims their index request before a bulk-folder-sync
	// backlog.
	IndexPriority int

	// RequestedAt is the timestamp used for the outbox event. When
	// zero, the adapter uses time.Now().
	RequestedAt time.Time

	// AdditionalOutboxEvents are emitted in the same transaction as the
	// canonical asset row and index event. Use this for durable external
	// work such as Drive delivery; never perform that work inline here.
	AdditionalOutboxEvents []OutboxEvent
}

// IndexPriorityHigh is the outbox scheduling priority for
// script-required asset.index.requested events. Mirrors
// outboxevents.PriorityHigh (infra owns the canonical constant; this
// is the application-layer alias so application producers do not
// import the infrastructure package).
const IndexPriorityHigh = 10

// CommitResult carries the outcome of a successful commit.
type CommitResult struct {
	AssetRowsAffected    int64
	OutboxEventKey       string
	OutboxInserted       bool
	OutboxExistingStatus string // status of the existing outbox row when OutboxInserted=false; empty otherwise
	AdditionalOutbox     []AdditionalOutboxResult
}

// AdditionalOutboxResult describes whether a post-commit intent was newly
// inserted or already existed, including the existing terminal status when
// applicable.
type AdditionalOutboxResult struct {
	EventKey       string
	Inserted       bool
	ExistingStatus string
}

// AssetCommitRequest is the canonical alias of CommitRequest, exposed at
// the application-layer port boundary. The user-facing surface name
// (AssetCommitRequest / CommittedAsset) makes the single-function
// Contract (CommitAsset) self-documenting at the call site:
//
//	persistence.AssetCommitter.CommitAsset(ctx, persistence.AssetCommitRequest) (persistence.CommittedAsset, error)
//
// is the canonical read; underlying type identity is preserved
// (alias, not new struct), so existing callers passing CommitRequest
// continue to compile byte-equivalent.
type AssetCommitRequest = CommitRequest

// CommittedAsset is the canonical alias of CommitResult — see
// AssetCommitRequest for the rationale.
type CommittedAsset = CommitResult

// Transaction is the narrow write surface consumed by AssetCommitter.
// Production concrete: *sql.Tx.
type Transaction interface {
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) Row
}

// Result is the narrow result surface from Transaction.ExecContext.
type Result = sql.Result

// Row is the narrow result surface from Transaction.QueryRowContext.
type Row = *sql.Row

// AssetCommitter is the canonical port for committing an asset and
// its indexing outbox event in a single atomic transaction.
type AssetCommitter interface {
	// CommitTx writes the asset, locations, metadata and optional
	// outbox event inside the caller-owned transaction.
	CommitTx(ctx context.Context, tx Transaction, req CommitRequest) (CommitResult, error)

	// CommitAndIndex opens a new transaction, calls CommitTx, and
	// commits it. Use this for standalone producers that do not
	// already own a transaction.
	CommitAndIndex(ctx context.Context, req CommitRequest) (CommitResult, error)

	// CommitAsset is the SINGLE canonical user-facing entry point
	// for the asset commit surface (PR-ASSET-COMMITTER-COMMITASSET,
	// July 2026). It opens a fresh SQLite transaction, writes
	// media_assets + asset_locations + typed metadata (metadata_json)
	// + the asset.index.requested outbox event, and commits
	// atomically. Caller-owned-tx orchestrators should use CommitTx;
	// standalone producers should use CommitAsset (or its
	// back-compat alias CommitAndIndex).
	//
	// godlike/06 SSOT: CommitAsset is the ONLY canonical producer
	// of the asset.index.requested outbox event. After cutover, no
	// other code path is allowed to insert into outbox_events for
	// this event_type — the dispatcher is the only consumer.
	CommitAsset(ctx context.Context, req AssetCommitRequest) (CommittedAsset, error)
}

// ── Typed sentinels ──────────────────────────────────────────────────

var (
	ErrAssetCommitAssetIDRequired       = errors.New("asset commit: AssetID is required")
	ErrAssetCommitSourceRequired        = errors.New("asset commit: Source is required")
	ErrAssetCommitFilenameRequired      = errors.New("asset commit: Filename is required")
	ErrAssetCommitMediaTypeRequired     = errors.New("asset commit: MediaType is required")
	ErrAssetCommitContentHashRequired   = errors.New("asset commit: ContentHash is required")
	ErrAssetCommitLifecycleRequired     = errors.New("asset commit: LifecycleState is required")
	ErrAssetCommitIndexTaxonomyRequired = errors.New("asset commit: indexable assets require complete valid taxonomy")
	ErrAssetCommitOutboxTerminal        = errors.New("asset commit: outbox event suppressed by existing terminal row")
)

// Validate performs the pre-flight validation shared by all adapters.
func (r CommitRequest) Validate() error {
	if r.AssetID == "" {
		return ErrAssetCommitAssetIDRequired
	}
	if r.Source == "" {
		return ErrAssetCommitSourceRequired
	}
	if r.Filename == "" {
		return ErrAssetCommitFilenameRequired
	}
	if r.MediaType == "" {
		return ErrAssetCommitMediaTypeRequired
	}
	if r.EmitIndexEvent && r.ContentHash == "" {
		return ErrAssetCommitContentHashRequired
	}
	if r.EmitIndexEvent {
		taxonomy := r.Taxonomy
		if taxonomy.AssetID == "" {
			taxonomy.AssetID = r.AssetID
		}
		if taxonomy.IsZero() || taxonomy.Namespace == "" || taxonomy.SourceType == "" || taxonomy.MediaType == "" || taxonomy.AssetKind == "" || taxonomy.MediaType != mediaregistry.MediaType(r.MediaType) {
			return ErrAssetCommitIndexTaxonomyRequired
		}
		if err := taxonomy.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrAssetCommitIndexTaxonomyRequired, err)
		}
	}
	if r.LifecycleState == "" {
		return ErrAssetCommitLifecycleRequired
	}
	if !r.Taxonomy.IsZero() {
		taxonomy := r.Taxonomy
		if taxonomy.AssetID == "" {
			taxonomy.AssetID = r.AssetID
		}
		if taxonomy.AssetID != r.AssetID {
			return fmt.Errorf("asset commit: Taxonomy.AssetID must be empty or match AssetID")
		}
		if err := taxonomy.Validate(); err != nil {
			return fmt.Errorf("asset commit: %w", err)
		}
	}
	return nil
}

// Package mediaregistry defines the canonical registry contracts.
//
// SQLite owns the registry state. Qdrant, backups and other systems are
// projections/artifacts and must identify the registry revision they came
// from. The package contains no database or network code.
package mediaregistry

import (
	"context"
	"errors"
	"fmt"
)

type Event struct {
	EventID     string
	AssetID     string
	EventType   string
	RunID       string
	Actor       string
	BeforeHash  string
	AfterHash   string
	PayloadJSON string
	GitSHA      string
	AppVersion  string
	CreatedAt   string
}

type Run struct {
	RunID              string
	RunType            string
	Status             string
	StartedAt          string
	CompletedAt        string
	GitSHA             string
	ParametersJSON     string
	AssetsSeen         int
	AssetsCreated      int
	AssetsUpdated      int
	TranscriptsBefore  int
	TranscriptsAfter   int
	DescriptionsBefore int
	DescriptionsAfter  int
	QdrantBefore       int
	QdrantAfter        int
	Error              string
}

// ProjectionStatus is the persisted lifecycle of a rebuildable projection.
// A projection may only become ACTIVE after it has been validated at the
// same registry sequence from which it was built.
type ProjectionStatus string

const (
	ProjectionBuilding   ProjectionStatus = "BUILDING"
	ProjectionValidating ProjectionStatus = "VALIDATING"
	ProjectionReady      ProjectionStatus = "READY"
	ProjectionActive     ProjectionStatus = "ACTIVE"
	ProjectionRetired    ProjectionStatus = "RETIRED"
	ProjectionFailed     ProjectionStatus = "FAILED"
	// ProjectionFailedCleaned marks a FAILED projection whose physical
	// collection has been verified cleaned up. Terminal: the attempt
	// history is preserved (the row is retained), but the record is no
	// longer indistinguishable from an un-cleaned FAILED partial.
	ProjectionFailedCleaned ProjectionStatus = "FAILED_CLEANED"
)

var (
	ErrInvalidProjectionTransition = errors.New("mediaregistry: invalid projection state transition")
	ErrProjectionSequenceLag       = errors.New("mediaregistry: projection sequence lags registry")
	ErrProjectionSequenceAhead     = errors.New("mediaregistry: projection sequence is ahead of registry")
)

// Projection is the durable metadata for one Qdrant (or future) projection.
type Projection struct {
	ProjectionID        string
	ProjectionType      string
	CollectionName      string
	AliasName           string
	Status              string
	SourceRegistrySeq   int64
	EmbeddingModel      string
	EmbeddingDimensions int
	AssetCount          int
	TranscriptCount     int
	CollectionHash      string
	QdrantVersion       string
	CreatedAt           string
	ActivatedAt         string
}

// ValidateProjectionTransition enforces the projection state machine. A
// failed build/validation is terminal for that projection identity; retrying
// creates a new projection build rather than reviving a partially-built one.
func ValidateProjectionTransition(from, to ProjectionStatus) error {
	if from == to {
		return nil
	}
	allowed := map[ProjectionStatus]map[ProjectionStatus]bool{
		ProjectionBuilding:   {ProjectionValidating: true, ProjectionFailed: true},
		ProjectionValidating: {ProjectionReady: true, ProjectionFailed: true},
		ProjectionReady:      {ProjectionActive: true, ProjectionFailed: true},
		ProjectionActive:     {ProjectionRetired: true},
		// A retired projection may be reactivated only by an explicit
		// rollback to a previously known-good target.
		ProjectionRetired:       {ProjectionActive: true},
		ProjectionFailed:        {ProjectionFailedCleaned: true},
		ProjectionFailedCleaned: {},
	}
	if allowed[from][to] {
		return nil
	}
	return fmtProjectionTransitionError(from, to)
}

// ValidateProjectionSequence returns a typed error unless the projection was
// built from exactly the current canonical registry sequence. Lagging builds
// are stale and ahead builds indicate a wrong registry/restore; neither may be
// activated.
func ValidateProjectionSequence(projectionSequence, registrySequence int64) error {
	if projectionSequence < 0 || registrySequence < 0 {
		return fmtProjectionSequenceError(ErrInvalidProjectionTransition, projectionSequence, registrySequence)
	}
	if projectionSequence < registrySequence {
		return fmtProjectionSequenceError(ErrProjectionSequenceLag, projectionSequence, registrySequence)
	}
	if projectionSequence > registrySequence {
		return fmtProjectionSequenceError(ErrProjectionSequenceAhead, projectionSequence, registrySequence)
	}
	return nil
}

func fmtProjectionTransitionError(from, to ProjectionStatus) error {
	return fmt.Errorf("%w: %s -> %s", ErrInvalidProjectionTransition, from, to)
}

func fmtProjectionSequenceError(kind error, projectionSequence, registrySequence int64) error {
	return fmt.Errorf("%w: projection_seq=%d registry_seq=%d", kind, projectionSequence, registrySequence)
}

type Backup struct {
	BackupID       string
	BackupType     string
	SourceRevision int64
	Path           string
	RemoteURI      string
	SHA256         string
	SizeBytes      int64
	Status         string
	AppGitSHA      string
	QdrantVersion  string
	CreatedAt      string
	VerifiedAt     string
	RestoredAt     string
}

type Ledger interface {
	AppendEvent(context.Context, Event) (int64, error)
	StartRun(context.Context, Run) error
	FinishRun(context.Context, Run) error
	RegisterProjection(context.Context, Projection) error
	RegisterBackup(context.Context, Backup) error
	LatestEventSequence(context.Context) (int64, error)
}

// QdrantSequenceReader is an optional refinement of Ledger. It reports the
// latest registry event that can affect the Qdrant projection, excluding
// non-indexable artifact events while preserving fail-closed sequence checks.
type QdrantSequenceReader interface {
	LatestQdrantEventSequence(context.Context) (int64, error)
}

type CountsReader interface {
	ReadCounts(context.Context) (Counts, error)
}

// ProjectionReader is an optional durable read surface used by the
// Projection Manager to hydrate lifecycle state after a process restart.
// It is separate from Ledger so existing ledger implementations remain
// source-compatible while the registry read model evolves.
type ProjectionReader interface {
	ListProjections(context.Context) ([]Projection, error)
}

// ProjectionSequenceAdvancer is an optional refinement of Ledger. It advances
// the ACTIVE projection's source_registry_seq to the latest Qdrant-eligible
// registry sequence after an incremental index upsert. Without this, the
// ACTIVE projection's checkpoint only moves on a full reindex, so the startup
// sequence gate (ValidateProjectionSequence) sees a stale projection and
// fails closed on every restart after new assets are committed + embedded.
type ProjectionSequenceAdvancer interface {
	AdvanceActiveProjectionSequence(context.Context) error
}

// AssetSource is a single provenance record: one logical asset, one place it
// was discovered. An asset may have MULTIPLE sources — the same bytes found
// on Drive, YouTube and a manual upload share ONE content object but keep
// every provenance trail. Identity is established ONLY by content_sha256
// (CAS invariant); sources are metadata, never identity.
type AssetSource struct {
	SourceID      string // deterministic: sha256(asset_id|source_type|source_uri|source_version)
	AssetID       string
	ContentSHA256 string // link to content_objects.sha256 when known
	SourceType    string // drive | youtube | artlist | manual | url
	SourceURI     string
	SourceVersion string // etag / provider version / modified_time
	DiscoveredAt  string // RFC3339 UTC
	IsPrimary     bool
}

var (
	// ErrAssetSourceInvalid is returned when an AssetSource (or a content
	// link) fails the identity contract: empty source_id / asset_id /
	// source_type / source_uri / discovered_at.
	ErrAssetSourceInvalid = errors.New("mediaregistry: invalid asset source")
)

// AssetContentRegistry links logical assets to physical content objects and
// records provenance. It is the application-layer gate for the CAS
// invariant: byte deduplication never drops provenance.
//
// godlike/07 fail-closed contract: empty identity fields return typed
// errors; a nil database surfaces the adapter's ErrNotWired.
type AssetContentRegistry interface {
	// LinkContent sets media_assets.content_sha256 for assetID. Idempotent
	// upsert of the logical→physical link. Errors: ErrAssetSourceInvalid on
	// empty inputs; an error when assetID does not exist (fail-closed: we
	// never link content to a phantom asset).
	LinkContent(ctx context.Context, assetID, contentSHA256 string) error

	// ContentForAsset returns the linked content sha256 for assetID, or ""
	// when no link exists (missing asset and missing link are both empty).
	ContentForAsset(ctx context.Context, assetID string) (string, error)

	// RegisterSource upserts a provenance record. Idempotent on SourceID:
	// re-registering the same source refreshes content_sha256, version and
	// primary flag without duplicating the row.
	RegisterSource(ctx context.Context, src AssetSource) error

	// SourcesForAsset returns all provenance records for assetID, primary
	// sources first, then discovery order.
	SourcesForAsset(ctx context.Context, assetID string) ([]AssetSource, error)
}

type TaxonomyWriter interface {
	UpsertTaxonomy(context.Context, AssetTaxonomy) error
}

package models

import (
	"time"
)

// LocationKind enumerates storage backends an asset can physically live on.
// Mirrors the asset_locations.location_kind CHECK constraint.
type LocationKind string

const (
	LocationKindLocal  LocationKind = "local"
	LocationKindDrive  LocationKind = "drive"
	LocationKindS3     LocationKind = "s3"
	LocationKindR2     LocationKind = "r2"
	LocationKindGCS    LocationKind = "gcs"
	LocationKindMinIO  LocationKind = "minio"
	LocationKindHTTP   LocationKind = "http"
)

// AssetLocation is the canonical representation of "where does this asset's
// bytes physically live". One asset can have multiple locations (a download
// to /tmp + a Drive mirror + an S3 archive, for example).
//
// `IsPrimary = true` means the storage router should pick this location for
// reads; there is exactly one primary per asset (enforced by a partial
// UNIQUE index in 036_canonical_asset_subtables.sql).
type AssetLocation struct {
	ID          string       `json:"id"`
	AssetID     string       `json:"asset_id"`
	LocationKind LocationKind `json:"location_kind"`
	URI         string       `json:"uri"`
	Path        string       `json:"path,omitempty"`
	ExternalID  string       `json:"external_id,omitempty"`
	IsPrimary   bool         `json:"is_primary"`
	Status      string       `json:"status"`
	Checksum    string       `json:"checksum,omitempty"`
	SizeBytes   int64        `json:"size_bytes,omitempty"`
	MimeType    string       `json:"mime_type,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// ValidLocationKinds is the canonical set used by validation. Mirrors the
// asset_locations.location_kind CHECK constraint.
var ValidLocationKinds = map[LocationKind]bool{
	LocationKindLocal: true,
	LocationKindDrive: true,
	LocationKindS3:    true,
	LocationKindR2:    true,
	LocationKindGCS:   true,
	LocationKindMinIO: true,
	LocationKindHTTP:  true,
}

// ProcessingStep enumerates the canonical processing pipeline steps an
// asset can be in. Mirrors the asset_processing.step CHECK constraint.
type ProcessingStep string

const (
	ProcessingStepDownload     ProcessingStep = "download"
	ProcessingStepNormalize    ProcessingStep = "normalize"
	ProcessingStepTranscribe   ProcessingStep = "transcribe"
	ProcessingStepTranslate    ProcessingStep = "translate"
	ProcessingStepEmbedText    ProcessingStep = "embed_text"
	ProcessingStepEmbedVisual  ProcessingStep = "embed_visual"
	ProcessingStepEmbedAudio   ProcessingStep = "embed_audio"
	ProcessingStepIndexQdrant  ProcessingStep = "index_qdrant"
	ProcessingStepThumbnail    ProcessingStep = "thumbnail"
	ProcessingStepDedup        ProcessingStep = "dedup"
)

// ProcessingStatus mirrors asset_processing.status CHECK constraint.
type ProcessingStatus string

const (
	ProcessingStatusPending   ProcessingStatus = "pending"
	ProcessingStatusRunning   ProcessingStatus = "running"
	ProcessingStatusCompleted ProcessingStatus = "completed"
	ProcessingStatusFailed    ProcessingStatus = "failed"
	ProcessingStatusSkipped   ProcessingStatus = "skipped"
)

// AssetProcessingStep tracks the latest state of a single pipeline step for
// an asset. UNIQUE(asset_id, step) means at most one row per (asset, step)
// combination; re-running the same step is an upsert that updates counters.
//
// Granular attempt history is NOT stored here — that lives in the jobs +
// job_events tables which already provide append-only event sourcing.
type AssetProcessingStep struct {
	ID            string           `json:"id"`
	AssetID       string           `json:"asset_id"`
	Step          ProcessingStep   `json:"step"`
	Status        ProcessingStatus `json:"status"`
	AttemptCount  int              `json:"attempt_count"`
	MaxAttempts   int              `json:"max_attempts"`
	LastError     string           `json:"last_error,omitempty"`
	LastAttemptAt *time.Time       `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time       `json:"last_success_at,omitempty"`
	WorkerID      string           `json:"worker_id,omitempty"`
	MetadataJSON  string           `json:"metadata_json,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// ValidProcessingSteps is the canonical set used by validation.
var ValidProcessingSteps = map[ProcessingStep]bool{
	ProcessingStepDownload:    true,
	ProcessingStepNormalize:   true,
	ProcessingStepTranscribe:  true,
	ProcessingStepTranslate:   true,
	ProcessingStepEmbedText:   true,
	ProcessingStepEmbedVisual: true,
	ProcessingStepEmbedAudio:  true,
	ProcessingStepIndexQdrant: true,
	ProcessingStepThumbnail:   true,
	ProcessingStepDedup:       true,
}

// ValidProcessingStatuses is the canonical status set.
var ValidProcessingStatuses = map[ProcessingStatus]bool{
	ProcessingStatusPending:   true,
	ProcessingStatusRunning:   true,
	ProcessingStatusCompleted: true,
	ProcessingStatusFailed:    true,
	ProcessingStatusSkipped:   true,
}

// RelationKind enumerates the canonical relationships between two assets.
// Mirrors the asset_relations.relation_kind CHECK constraint.
type RelationKind string

const (
	RelationKindDerivedFrom  RelationKind = "derived_from"
	RelationKindPartOf       RelationKind = "part_of"
	RelationKindUsedBy       RelationKind = "used_by"
	RelationKindVersionOf    RelationKind = "version_of"
	RelationKindDuplicateOf  RelationKind = "duplicate_of"
	RelationKindTranscriptOf RelationKind = "transcript_of"
)

// AssetRelation is a directed edge in the asset graph: parent → child with a
// typed relationship. UNIQUE(parent, child, kind) prevents duplicate edges;
// CHECK(parent != child) prevents self-loops.
//
// Examples:
//   project → clip           (kind = "part_of")
//   clip    → its thumbnail  (kind = "derived_from")
//   clip    → its transcript (kind = "transcript_of")
type AssetRelation struct {
	ID            string       `json:"id"`
	ParentAssetID string       `json:"parent_asset_id"`
	ChildAssetID  string       `json:"child_asset_id"`
	RelationKind  RelationKind `json:"relation_kind"`
	MetadataJSON  string       `json:"metadata_json,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
}

// ValidRelationKinds is the canonical set used by validation.
var ValidRelationKinds = map[RelationKind]bool{
	RelationKindDerivedFrom:  true,
	RelationKindPartOf:       true,
	RelationKindUsedBy:       true,
	RelationKindVersionOf:    true,
	RelationKindDuplicateOf:  true,
	RelationKindTranscriptOf: true,
}

// VersionChangeKind enumerates the changes that mint a new asset_versions row.
// Mirrors the asset_versions.change_kind CHECK constraint.
type VersionChangeKind string

const (
	VersionChangeKindCreated   VersionChangeKind = "created"
	VersionChangeKindUpdated   VersionChangeKind = "updated"
	VersionChangeKindReplaced  VersionChangeKind = "replaced"
	VersionChangeKindDeleted   VersionChangeKind = "deleted"
	VersionChangeKindRestored  VersionChangeKind = "restored"
)

// AssetVersion is a point-in-time snapshot of an asset's metadata. Versions
// form a per-asset monotonic sequence (1, 2, 3, ...). UNIQUE(asset_id, version)
// guarantees the sequence is gap-free from the application's perspective.
//
// IMPORTANT: AssetVersion.asset_id is intentionally NOT a foreign key in the
// schema — version rows must survive the deletion of the audit subject.
// Orphan versions are a valid audit trail.
type AssetVersion struct {
	ID           string            `json:"id"`
	AssetID      string            `json:"asset_id"`
	Version      int               `json:"version"`
	SnapshotJSON string            `json:"snapshot_json"`
	ChangeKind   VersionChangeKind `json:"change_kind"`
	ChangedBy    string            `json:"changed_by,omitempty"`
	ChangeReason string            `json:"change_reason,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

// ValidVersionChangeKinds is the canonical set used by validation.
var ValidVersionChangeKinds = map[VersionChangeKind]bool{
	VersionChangeKindCreated:  true,
	VersionChangeKindUpdated:  true,
	VersionChangeKindReplaced: true,
	VersionChangeKindDeleted:  true,
	VersionChangeKindRestored: true,
}

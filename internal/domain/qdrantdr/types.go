// Package qdrantdr — canonical DR/snapshot types shared across layers.
//
// PR-QDRANT-WIRE-MIRROR (June 2026): unifies the previously duplicated
// SnapshotDescription, RetentionConfig, and RetentionResult types that
// lived as field-for-field mirrors in both internal/infrastructure/qdrant
// and internal/application/qdrant/dr. After unification, both layers
// consume the same domain types; the dr_adapter.go translation functions
// become no-ops.
//
// Placement: internal/domain/ (Clean Architecture innermost layer).
// Both infra and application packages may import domain/; domain imports
// only the standard library.
package qdrantdr

import "time"

// SnapshotDescription is the canonical DR shape for a single Qdrant
// collection snapshot. Identical across wire (REST decode) and
// application-layer use.
//
// JSON tags match the Qdrant REST wire format so both the infra-layer
// RPC decoders and the admin CLI (json.MarshalIndent) produce the same
// output.
type SnapshotDescription struct {
	Name         string    `json:"name"`
	CreationTime time.Time `json:"creation_time"`
	Size         int64     `json:"size"`
	Checksum     string    `json:"checksum,omitempty"`
}

// RetentionConfig configures a single Qdrant retention sweep.
//
// The full 5-field shape is canonical. The application-layer dr
// services consume RetentionDays / KeepLastN / ProtectedRollbackTarget;
// MaxAgeSeconds and AgingTable are infra-side concerns used by
// CollectionManager.CleanupWithConfig. Application-layer callers may
// leave them at zero/nil — the sweep falls back to the keep_last_n
// alpha cut (deterministic safe-floor).
type RetentionConfig struct {
	RetentionDays           int
	KeepLastN               int
	ProtectedRollbackTarget string
	MaxAgeSeconds           int
	// AgingTable is OPTIONAL. When non-nil, the infra-side
	// CollectionManager reads per-collection creation timestamps
	// from it. The concrete type lives in infra/qdrant (AgingTable
	// interface); the domain package carries this as `any` so it
	// stays free of infra imports.
	AgingTable any
}

// RetentionResult is the canonical DR shape for the retention
// executor's outcome. JSON tags mirror the wire format so the admin
// CLI and the infra-side CollectionManager agree on the output shape.
type RetentionResult struct {
	CollectionsDropped int      `json:"collections_dropped"`
	CollectionsKept    int      `json:"collections_kept"`
	DroppedNames       []string `json:"dropped_names,omitempty"`
	Errors             []string `json:"errors,omitempty"`
	// ProtectedKept lists collections that were KEPT explicitly
	// because of protected_rollback_target or keep_last_n floor.
	ProtectedKept []string `json:"protected_kept,omitempty"`
}

// LocatorCleanupReport is the shared pure-data result of scanning Qdrant
// points for legacy locator payload keys. Both the infrastructure cleaner
// and the application maintenance port use this exact shape; keeping it in
// the dependency-free domain package makes the adapter a pass-through rather
// than a field-by-field projection.
type LocatorCleanupReport struct {
	DryRun bool `json:"dry_run"`
	// CompleteScan is true only when the cleaner reached Qdrant's empty
	// cursor without a scroll error. Consumers must fail closed when false.
	CompleteScan        bool     `json:"complete_scan"`
	Collection          string   `json:"collection"`
	TotalPointsScrolled int      `json:"total_points_scrolled"`
	PointsWithDriveLink int      `json:"points_with_drive_link"`
	PointsWithLocalPath int      `json:"points_with_local_path"`
	PointsAffected      int      `json:"points_affected"`
	KeysRemoved         int      `json:"keys_removed"`
	BatchCount          int      `json:"batch_count"`
	AllocCapacity       int      `json:"alloc_capacity,omitempty"`
	Errors              []string `json:"errors,omitempty"`
}

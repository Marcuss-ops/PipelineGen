// dr/types.go — QDRANT-005C PR3 (June 2026): DR-owned canonical types.
//
// The 3 structs in this file are the application-layer canonical
// shapes for DR/snapshots state. The infrastructure layer
// (internal/infrastructure/qdrant) keeps mirror copies of these for
// its own wire-bound methods (qdrant.Client.{Create,List,Restore}Snapshot
// returns qdrant.SnapshotDescription; qdrant.CollectionManager.
// CleanupWithConfig takes qdrant.RetentionConfig). Translation happens
// in dr_adapter.go at the seam — keeping the dr package free of any
// import dependency on the infrastructure package, which would form
// a cycle (infra → dr → infra is forbidden by Go's compiler).
//
// Domain rule (Clean Architecture): ports own types. The application
// layer is the source of truth for these shapes; the infra layer
// adapts its wire-bound methods to satisfy the ports at the seam.
//
// QDRANT-005C (June 2026): NO fields that the application layer does
// not consume live here. Specifically dr.RetentionConfig does NOT
// carry qdrant.AgingTable — the aging registry is purely an infra-side
// concern, and the application-layer DR surface does not orchestrate
// aging yet (the SQLite-backed aging registry migration is part of a
// follow-up QDRANT-005 ramp).
package dr

import "time"

// SnapshotDescription is the application-layer DR shape for a single
// collection snapshot. Field-for-field mirror of
// qdrant.SnapshotDescription so qdrant.Client.{Create,List}Snapshots
// return values can be losslessly translated to dr.SnapshotDescription
// in SnapshotStoreAdapter.
//
// JSON tags are deliberately omitted: the application layer does not
// marshal this shape to REST callers; the admin CLI does, via
// `json.MarshalIndent(snaps, "", "  ")`, and Go's default zero-value +
// capitalized field names match the wire shape that the CLI already
// prints (so no surprise on the operator's screen).
type SnapshotDescription struct {
	Name         string
	CreationTime time.Time
	Size         int64
	Checksum     string
}

// RetentionConfig is the application-layer DR shape for one retention
// sweep. Field-for-field mirror of qdrant.RetentionConfig (the infra
// side also has MaxAgeSeconds + AgingTable which the application layer
// does not orchestrate yet — those are deliberately omitted here).
type RetentionConfig struct {
	RetentionDays           int
	KeepLastN               int
	ProtectedRollbackTarget string
}

// RetentionResult is the application-layer DR shape for the retention
// executor's outcome. Field-for-field mirror of qdrant.RetentionResult
// with Errors (the infra-side surfaces a per-drop error list) likewise
// mirrored so the application-layer DR consumer can log + report it
// without reaching back to the infra shapes.
type RetentionResult struct {
	CollectionsDropped int
	CollectionsKept    int
	DroppedNames       []string
	Errors             []string
	ProtectedKept      []string
}

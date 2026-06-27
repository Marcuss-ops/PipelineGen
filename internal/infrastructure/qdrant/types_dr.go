// types_dr.go — QDRANT-005C PR3 infra-side DR/snapshot types.
//
// Both struct families live side-by-side:
//
//   qdrant.SnapshotDescription — WIRE / codec shape. Used by client_dr.go
//     (RPC decode) + collection_manager.go (manager wrappers). The
//     dr_adapter.go SnapshotStoreAdapter translates this wire shape
//     into dr.SnapshotDescription (application-layer canonical).
//
//   dr.SnapshotDescription — APPLICATION-LAYER canonical mirror. Lives
//     in internal/application/qdrant/dr/types.go. The dr package
//     (ports/services) does NOT import qdrant — dr_adapter.go bridges
//     at the seam so that the cycle breaks.
//
//   PointPayload — Infra only. Both consumers (reaper.Reaper +
//     Client.OverwritePayload) live in the qdrant package. Moving
//     PointPayload to dr would force the reaper into a back-dep on
//     the application layer — forbidden by AGENTS.md.
//
// Cycle break (June 2026): dr.SnapshotDescription + dr.RetentionConfig
// + dr.RetentionResult moved to internal/application/qdrant/dr/types.go
// as the dr-owned canonical shapes. This file keeps the wire-side copies
// for the Infra layer's existing REST-method callers.

package qdrant

import "time"

// SnapshotDescription is the qdrant.REST wire representation of a
// single collection snapshot. Used by:
//   - client_dr.go::{Create,List}Snapshot RPC decoders
//   - collection_manager.go::{Create,List,Restore}Snapshot wrappers
//   - dr_adapter.go's SnapshotStoreAdapter (translates to dr.SnapshotDescription)
//
// Field mapping to Qdrant REST JSON:
//   `name`           — snapshot identifier
//   `creation_time`  — RFC3339 timestamp
//   `size`           — byte size on disk (int)
//   `checksum`       — Qdrant-server-side checksum (string, may be empty)
//
// QDRANT-005C (June 2026): the wire response does NOT embed a download
// URL — Qdrant returns the URL from a SEPARATE endpoint (GET
// /collections/{n}/snapshots/{name}). qdrant.Client.GetSnapshotURL
// is the canonical way to resolve a snapshot's URL when restoring.
type SnapshotDescription struct {
	Name         string    `json:"name"`
	CreationTime time.Time `json:"creation_time"`
	Size         int64     `json:"size"`
	Checksum     string    `json:"checksum,omitempty"`
}

// PointPayload is the per-point payload write shape used by the
// Qdrant REST /points/payload endpoint with `merge=true`. Distinct
// from Point: Point carries vectors, PointPayload does NOT. The
// canonical use is the reaper service which needs to overwrite a
// subset of payload keys without touching vectors (UpsertPoints
// would null vectors on partial payload, which was the prior bug
// the reaper commit 07292503 fixed in the qdrant.reaper path).
//
// Per-point shape mirrors Qdrant REST:
//   `id`      — point ID (string or int; the reaper handles strings)
//   `payload` — map[string]interface{} of payload fields to merge in
type PointPayload struct {
	ID      string                 `json:"id"`
	Payload map[string]interface{} `json:"payload"`
}

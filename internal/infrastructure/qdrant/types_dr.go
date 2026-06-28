// types_dr.go — QDRANT-005C PR3 / PR-QDRANT-WIRE-MIRROR (June 2026).
//
// PR-QDRANT-WIRE-MIRROR (June 2026): SnapshotDescription, RetentionConfig,
// and RetentionResult were unified in internal/domain/qdrantdr/. The
// type alias below keeps existing callers compiling while the canonical
// shape lives in the domain layer.
//
// PointPayload — Infra only. Both consumers (reaper.Reaper +
// Client.OverwritePayload) live in the qdrant package.

package qdrant

import "github.com/Marcuss-ops/PipelineGen/internal/domain/qdrantdr"

// SnapshotDescription is the canonical DR snapshot shape (type alias).
type SnapshotDescription = qdrantdr.SnapshotDescription

// PointPayload is the per-point payload write shape used by the
// Qdrant REST /points/payload endpoint with `merge=true`. Distinct
// from Point: Point carries vectors, PointPayload does NOT. The
// canonical use is the reaper service which needs to overwrite a
// subset of payload keys without touching vectors (UpsertPoints
// would null vectors on partial payload, which was the prior bug
// the reaper commit 07292503 fixed in the qdrant.reaper path).
//
// Per-point shape mirrors Qdrant REST:
//
//	`id`      — point ID (string or int; the reaper handles strings)
//	`payload` — map[string]interface{} of payload fields to merge in
type PointPayload struct {
	ID      string                 `json:"id"`
	Payload map[string]interface{} `json:"payload"`
}

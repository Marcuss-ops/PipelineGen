// Package qdrant — canonical forward-mapping file pointer.
//
// QDRANT-001 rebase-resolution (June 2026): the previous incarnation of
// this file held the duplicate identity-form forward mapping
// (`AssetIDPrefix + id`) and a reverse helper
// (`PointIDToAssetID(pointID)`). Both have been deleted as part of the
// zero-legacy cleanup:
//
//   - Forward canonical now lives in ./pointid.go and uses UUID v5 SHA-1
//     with the project-namespacing boundary. There is exactly ONE
//     declaration of `AssetIDToQdrantPointID` in the qdrant package,
//     enforced by anti-regression gate #8 (see
//     architecture/qdrant/001-sidecar-and-pointid.md).
//
//   - `PointIDToAssetID` and the `AssetIDPrefix` constant were removed
//     because UUID v5 hashes are one-way and the only caller
//     (index_writer.go::ValidatePoint) was rewired to read the
//     canonical asset_id directly from `point.Payload["asset_id"]`.
//
// This file is kept as a doc-only pointer to `./pointid.go` so future
// agents who grep for "AssetIDToQdrantPointID" still find a
// single-source-of-truth package-locality signal without spelunking
// through the entire qdrant/ tree.
//
// Legacy compatibility window: any in-flight Qdrant collection indexed
// under the pre-ratchet identity scheme (`"asset:<id>"` points) is
// NOT directly reversible; QDRANT-005 reconcile territory will handle
// point migration to UUID v5 through a background rebuild.
package qdrant

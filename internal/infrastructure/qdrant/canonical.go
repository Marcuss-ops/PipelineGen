// Package qdrant — canonical.go (QDRANT-001, June 2026)
//
// Single source of truth for the AssetID → QdrantPointID mapping.
//
// QDRANT-001 acceptance criterion: "Point ID non centralizzato" was
// flagged BLOCKED in the reanalysis because the mapper previously
// assigned IDs inline (`ID: asset.ID`). A future writer could adopt
// a different strategy silently — namespacing by tenant, hashing for
// collision avoidance, etc. This file pins the contract:
//
//   - AssetIDToQdrantPointID(assetID string) string
//     Translates the canonical media_assets.id into the Qdrant
//     point ID. Today the mapping is the identity function
//     (Qdrant point ID == AssetID), but having a single call site
//     lets us introduce namespacing, hashing, or migration shims
//     without touching every writer / reader / reindex path.
//
// Implementations MUST treat this function as the only legal way to
// derive a point ID from an asset identity; raw `asset.ID` literals
// in the Qdrant package are an anti-pattern and a follow-up CI gate
// (`grep -RE '\\bID:[[:space:]]*asset\\.ID\\b' internal/infrastructure/qdrant`)
// should fail if any are reintroduced outside tests.
package qdrant

import "strings"

// AssetIDPrefix is the namespace marker Qdrant point IDs start with.
// Doubles as a sanity check: a point ID without this prefix is
// almost certainly a bug in the caller (e.g. using an external
// source's identifier).
const AssetIDPrefix = "asset:"

// AssetIDToQdrantPointID returns the canonical Qdrant point ID for
// the given media_assets.id.
//
// The mapping is currently the identity function (prefixed with
// AssetIDPrefix for forward-compat with namespacing). The mapping
// is exported so all writers (IndexWriter, PayloadMapper,
// Reconciler) and readers (Searcher, SQLiteAssetStore) share the
// same translation rule.
//
// Contract:
//   - The empty string maps to the empty string (callers must reject
//     empty point IDs at the validation layer).
//   - Whitespace-only asset IDs are trimmed and rejected.
func AssetIDToQdrantPointID(assetID string) string {
	id := strings.TrimSpace(assetID)
	if id == "" {
		return ""
	}
	return AssetIDPrefix + id
}

// PointIDToAssetID reverses AssetIDToQdrantPointID. Returns the bare
// assetID (no namespace prefix). Empty point IDs map to empty asset
// IDs. Point IDs WITHOUT the AssetIDPrefix are still accepted as raw
// IDs to keep the function tolerant to legacy writers that haven't
// picked up the canonical naming yet.
func PointIDToAssetID(pointID string) string {
	id := strings.TrimSpace(pointID)
	if id == "" {
		return ""
	}
	return strings.TrimPrefix(id, AssetIDPrefix)
}

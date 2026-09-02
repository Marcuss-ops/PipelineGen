// Package media is the PostgreSQL + pgvector canonical media write family.
//
// PostgresMediaCommitter is the PostgreSQL implementation of
// persistence.AssetCommitter / persistence.AssetMutationCommitter /
// persistence.CanonicalAssetWriter. It mirrors SQLiteMediaCommitter
// (internal/platform/sqlite/assets/imagesregistry) statement-for-statement
// so the media-domain cutover (FASE 7 of the staged migration) is a
// composition-root swap of the same port — producers (YouTube, Artlist,
// Images, Voiceover, recovery) keep calling the identical
// persistence.AssetCommitter surface.
//
// The canonical 8-step commit sequence (identical to SQLiteMediaCommitter):
//
//	 1. resolve canonical identity (asset existence → Created, source id)
//	 2. upsert media_assets (+ asset_locations + typed metadata + taxonomy
//	    columns namespace / asset_kind / source_type / semantic_role)
//	 3. RegisterSource (media_asset_sources)
//	 4. LinkContent (content_sha256) when the bytes are known
//	 5. upsert transcript/text tracks (asset_text_tracks)
//	 6. append registry event (registry_events) → registry seq
//	 7. write asset.index.requested outbox event when indexable
//	 8. single COMMIT (all-or-nothing)
//
// godlike/06 SSOT: after cutover, no other package is allowed to
// INSERT/UPSERT into media_assets, asset_locations, or outbox_events
// (asset.index.requested) directly. The parity suite
// (parity_test.go) proves this adapter behaves identically to the
// SQLite canonical writer against the same DSN-gated fixture.
//
// Non-goals: no business logic beyond the SQLite mirror; no direct
// producer calls; no embedding generation (that belongs to the
// enrichment pipeline writers).

package media

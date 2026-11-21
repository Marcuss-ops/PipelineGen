package migration

import "testing"

// TestMediaSelfBootstrapDDLOrderContract pins the canonical self-bootstrap
// order for the media PostgreSQL domain (migration-museum audit point 9).
// Application order is part of the contract because 002 uses
// CREATE TABLE IF NOT EXISTS: on a database that already carries the retired
// face descriptor columns, only 009's DROP converges the shape, so 009 must
// be applied AFTER 002 — a hand-copied runner list that omits or reorders it
// reintroduces dead columns on every fresh database it bootstraps.
func TestMediaSelfBootstrapDDLOrderContract(t *testing.T) {
	want := []string{
		MediaSchemaDDL,                // 001_media_schema.sql
		MediaVectorSurfacesDDL,        // 002_media_vector_surfaces.sql
		MediaHNSWIndexesDDL,           // 003_media_hnsw_indexes.sql
		MediaTimestampsTimestamptzDDL, // 004_media_timestamps_timestamptz.sql
		MediaAssetVersionsDDL,         // 006_media_asset_versions.sql
		MediaClipFoldersDDL,           // 007_media_clip_folders.sql
		MediaAssetProcessingDDL,       // 008_media_asset_processing.sql
		MediaDropAssetFacesDDL,        // 009_drop_media_asset_faces.sql — after 002
		MediaLegacyFileMd5ConvergeDDL, // 012_media_legacy_file_md5_converge.sql
		MediaThumbURLContractDDL,      // 013_media_thumb_url_contract.sql
		MediaClipRenderCacheDDL,       // 015_clip_render_cache.sql
		MediaLocalizedRenderReuseDDL,  // 016_localized_render_reuse.sql — LAST
	}
	if len(MediaSelfBootstrapDDL) != len(want) {
		t.Fatalf("MediaSelfBootstrapDDL has %d entries, want %d — new PG media migrations must be embedded AND appended here (010/011-style deployment-managed one-shots excepted, see the var's doc comment)",
			len(MediaSelfBootstrapDDL), len(want))
	}
	for i := range want {
		if MediaSelfBootstrapDDL[i] != want[i] {
			t.Fatalf("MediaSelfBootstrapDDL[%d] diverged from the canonical order — see embed_ddl.go's order contract", i)
		}
	}
	last := len(MediaSelfBootstrapDDL) - 1
	if MediaSelfBootstrapDDL[last] != MediaLocalizedRenderReuseDDL {
		t.Fatalf("MediaLocalizedRenderReuseDDL must be the LAST self-bootstrap entry: RA-13 appends the new cache tables after the convergence migrations")
	}
	if MediaSelfBootstrapDDL[last-1] != MediaClipRenderCacheDDL {
		t.Fatalf("MediaClipRenderCacheDDL must run directly before the localized reuse cache")
	}
	thumb := -1
	converge := -1
	for i, ddl := range MediaSelfBootstrapDDL {
		switch ddl {
		case MediaThumbURLContractDDL:
			thumb = i
		case MediaLegacyFileMd5ConvergeDDL:
			converge = i
		}
	}
	if thumb != converge+1 {
		t.Fatalf("MediaLegacyFileMd5ConvergeDDL must run directly before the thumb contract (got %d and %d)", converge, thumb)
	}
}

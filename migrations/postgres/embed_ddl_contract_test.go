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
		MediaThumbURLContractDDL,      // 013_media_thumb_url_contract.sql — LAST
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
	if MediaSelfBootstrapDDL[last] != MediaThumbURLContractDDL {
		t.Fatalf("MediaThumbURLContractDDL must be the LAST self-bootstrap entry: it contracts the thumb_url mirror and must run after the table its guard probes (001)")
	}
	if MediaSelfBootstrapDDL[last-1] != MediaLegacyFileMd5ConvergeDDL {
		t.Fatalf("MediaLegacyFileMd5ConvergeDDL must run directly before the thumb contract: both converge columns of the tables 001/006 create and must come after them")
	}
}

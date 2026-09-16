// Package media_test — media_folder_resolver_test.go pins the MEDIA-SSOT P2-9
// Phase 2 folder-resolution migration and the FOLDER SEMANTICS DECISION recorded
// in media_folder_resolver.go.
//
// The retired SQLite statement was
//
//	SELECT COALESCE(NULLIF(folder_id, ''), drive_folder_id, '') FROM media_assets WHERE id = ?
//
// The PostgreSQL SSOT has no drive_folder_id column, and the operational SQLite
// media_assets table holds 0 rows, so the fallback was deleted rather than
// ported. These tests assert the surviving contract:
//
//   - a populated folder_id resolves
//   - an empty folder_id resolves to "" (the caller then keeps its legacy path)
//   - an unknown asset resolves to "" WITH NO ERROR (parity: the retired
//     statement mapped sql.ErrNoRows to an empty string, and finalization's
//     ArtifactFolderResolver contract treats "" as "not resolved")
package media_test

import (
	"context"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func TestMediaFolderResolverReadsCanonicalFolderID(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		withFolder    = "yt_folder_resolver_with_v1"
		withoutFolder = "yt_folder_resolver_without_v1"
	)
	for _, id := range []string{withFolder, withoutFolder} {
		seedIndexableAsset(t, db, id)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET folder_id = 'folder-847' WHERE id = $1`, withFolder); err != nil {
		t.Fatalf("shape folder fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE media_assets SET folder_id = '' WHERE id = $1`, withoutFolder); err != nil {
		t.Fatalf("shape empty-folder fixture: %v", err)
	}

	r := pgmedia.NewMediaFolderResolver(db)

	got, err := r.ResolveArtifactFolder(ctx, withFolder)
	if err != nil {
		t.Fatalf("ResolveArtifactFolder(withFolder): %v", err)
	}
	if got != "folder-847" {
		t.Errorf("folder = %q, want %q", got, "folder-847")
	}

	got, err = r.ResolveArtifactFolder(ctx, withoutFolder)
	if err != nil {
		t.Fatalf("ResolveArtifactFolder(withoutFolder): %v", err)
	}
	if got != "" {
		t.Errorf("folder = %q, want empty (caller keeps the legacy path)", got)
	}

	// Unknown asset: not an error. This is the behaviour the finalization
	// contract depends on; an error here would turn an unresolved overlay
	// destination into a failed job.
	got, err = r.ResolveArtifactFolder(ctx, "yt_folder_resolver_absent_v1")
	if err != nil {
		t.Fatalf("ResolveArtifactFolder(absent) err = %v, want nil (not-resolved is not a failure)", err)
	}
	if got != "" {
		t.Errorf("folder = %q, want empty for an unknown parent video", got)
	}
}

// TestMediaFolderResolverHasNoDriveFolderIDFallback is the executable form of
// the folder-semantics decision: the SSOT must not carry the legacy column, so
// no future change can quietly re-add the fallback without failing here.
func TestMediaFolderResolverHasNoDriveFolderIDFallback(t *testing.T) {
	db := newMediaTestDB(t)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = 'media_assets' AND column_name = 'drive_folder_id'`).Scan(&count); err != nil {
		t.Fatalf("read media_assets columns: %v", err)
	}
	if count != 0 {
		t.Fatal("media_assets.drive_folder_id exists on the SSOT — the folder fallback was deleted on 2026-09-16 (0 rows depended on it); re-adding the column means re-deciding that choice deliberately")
	}
}

// TestMediaFolderResolverFailsClosedWithoutHandle pins the no-fallback contract:
// an unwired resolver errors instead of reporting "not resolved", because an
// empty result is a legitimate success that the caller cannot distinguish from
// an unavailable media plane.
func TestMediaFolderResolverFailsClosedWithoutHandle(t *testing.T) {
	requirePostgresDSN(t)
	var r *pgmedia.MediaFolderResolver
	if _, err := r.ResolveArtifactFolder(context.Background(), "any"); err == nil {
		t.Fatal("expected a fail-closed error from a nil media folder resolver")
	}
	if _, err := (&pgmedia.MediaFolderResolver{}).ResolveArtifactFolder(context.Background(), "any"); err == nil {
		t.Fatal("expected a fail-closed error from a resolver without a media handle")
	}
}

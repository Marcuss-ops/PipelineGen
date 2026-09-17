package artifacts

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	_ "github.com/mattn/go-sqlite3"
)

type contentHashDetailsReader struct {
	hashes map[string]string
}

func (r *contentHashDetailsReader) Get(_ context.Context, id string) (*asset.Details, error) {
	return &asset.Details{Asset: &asset.Asset{
		ID:       id,
		Metadata: asset.Metadata{"binary_sha256": r.hashes[id]},
	}}, nil
}

func TestClipsRegistryFindByContentHashUsesOnlyLiveCanonicalHashes(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		binary_sha256 TEXT NOT NULL DEFAULT '',
		content_sha256 TEXT NOT NULL DEFAULT '',
		legacy_file_md5 TEXT NOT NULL DEFAULT '',
		lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE'
	)`); err != nil {
		t.Fatal(err)
	}

	const contentSHA = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	const binarySHA = "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	for _, row := range []struct {
		id, binary, content, legacy, state string
	}{
		{id: "asset-content", content: contentSHA, legacy: "legacy-only"},
		{id: "asset-binary", binary: binarySHA},
		{id: "asset-deleted", content: "deleted-sha", state: "DELETED"},
		{id: "asset-legacy", legacy: "legacy-only"},
	} {
		if _, err := db.Exec(`INSERT INTO media_assets(id, binary_sha256, content_sha256, legacy_file_md5, lifecycle_state) VALUES (?, ?, ?, ?, ?)`,
			row.id, row.binary, row.content, row.legacy, row.state); err != nil {
			t.Fatal(err)
		}
	}

	reader := &contentHashDetailsReader{hashes: map[string]string{
		"asset-content": contentSHA,
		"asset-binary":  binarySHA,
	}}
	registry := NewClipsRegistry(db, reader, nil, nil)

	for _, tc := range []struct {
		input, wantID, wantHash string
	}{
		{input: "  " + contentSHA + "  ", wantID: "asset-content", wantHash: contentSHA},
		{input: binarySHA, wantID: "asset-binary", wantHash: binarySHA},
	} {
		got, err := registry.FindByContentHash(context.Background(), tc.input)
		if err != nil {
			t.Fatalf("FindByContentHash(%q): %v", tc.input, err)
		}
		if got == nil || got.ID != tc.wantID || got.ContentHash != tc.wantHash {
			t.Fatalf("FindByContentHash(%q) = %#v, want ID=%q and canonical hash %q", tc.input, got, tc.wantID, tc.wantHash)
		}
	}

	for _, absent := range []string{"legacy-only", "deleted-sha", ""} {
		got, err := registry.FindByContentHash(context.Background(), absent)
		if err != nil {
			t.Fatalf("FindByContentHash(%q): %v", absent, err)
		}
		if got != nil {
			t.Errorf("FindByContentHash(%q) = %#v, want no live canonical match", absent, got)
		}
	}
}

func TestClipsRegistryFindByContentHashFailsClosedWithoutDatabase(t *testing.T) {
	registry := NewClipsRegistry(nil, nil, nil, nil)
	if _, err := registry.FindByContentHash(context.Background(), "some-sha256"); err == nil {
		t.Fatal("FindByContentHash succeeded with no content lookup database")
	}
	if _, err := (*ClipsRegistry)(nil).FindByContentHash(context.Background(), "some-sha256"); err == nil {
		t.Fatal("FindByContentHash succeeded on a nil registry")
	}
}

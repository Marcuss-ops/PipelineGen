package vidrush

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const testContentSHA256 = "d5beb042619b6d8b3f99d5beb042619b6d8b3f99d5beb042619b6d8b3f99d5be"

func newContentReuseDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		content_sha256 TEXT NOT NULL DEFAULT '',
		drive_link TEXT NOT NULL DEFAULT '',
		drive_file_id TEXT NOT NULL DEFAULT '',
		deleted_at TEXT NOT NULL DEFAULT '',
		index_state TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestReusePublishedImageByContentSkipsTheUploadIdentity pins the dedup
// contract: the same bytes already published under ANOTHER asset id are reused
// instead of uploaded again, and the returned candidate carries the canonical
// asset identity.
func TestReusePublishedImageByContentSkipsTheUploadIdentity(t *testing.T) {
	db := newContentReuseDB(t)
	if _, err := db.Exec(`INSERT INTO media_assets (id, content_sha256, drive_link, index_state)
		VALUES ('entity-image-11', ?, 'https://drive.example/existing', 'INDEXED')`, testContentSHA256); err != nil {
		t.Fatal(err)
	}
	finalizer := &vidRushArtifactFinalizer{mediaDB: db, log: zap.NewNop()}
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID: "entity-image-99", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "Corrado Augias", SourceURL: "https://images.example/augias-reupload.jpg",
	}
	reused, ok := finalizer.reusePublishedImageByContent(context.Background(), scriptports.VerifiedArtifact{
		Candidate: candidate, ContentSHA256: testContentSHA256, MIMEType: "image/jpeg",
	})
	if !ok {
		t.Fatal("expected the published bytes to be reused by content address")
	}
	if reused.AssetID != "entity-image-11" || reused.DriveLink != "https://drive.example/existing" {
		t.Fatalf("reused = %+v, want the canonical existing asset", reused)
	}
	if reused.PersistenceStatus != scriptpkg.VidRushStatusPersisted || reused.IndexStatus != "INDEXED" {
		t.Fatalf("reused status = %q/%q, want persisted/INDEXED", reused.PersistenceStatus, reused.IndexStatus)
	}
}

// TestReusePublishedImageByContentRefusesNonCanonicalDigest proves the guard:
// a legacy/short digest is absence of content identity, never a license to
// reuse a different file.
func TestReusePublishedImageByContentRefusesNonCanonicalDigest(t *testing.T) {
	db := newContentReuseDB(t)
	if _, err := db.Exec(`INSERT INTO media_assets (id, content_sha256, drive_link)
		VALUES ('entity-image-12', ?, 'https://drive.example/existing')`, testContentSHA256); err != nil {
		t.Fatal(err)
	}
	finalizer := &vidRushArtifactFinalizer{mediaDB: db, log: zap.NewNop()}
	if _, ok := finalizer.reusePublishedImageByContent(context.Background(), scriptports.VerifiedArtifact{
		Candidate:     scriptpkg.SegmentAssetCandidate{AssetID: "entity-image-13", Provider: scriptpkg.VidRushProviderInternetImages},
		ContentSHA256: "d41d8cd98f00b204e9800998ecf8427e",
	}); ok {
		t.Fatal("an MD5-shaped digest must not authorize content reuse")
	}
}

// TestStampEntityImageSubjectMetadataMakesTheRowDiscoverable pins the subject
// projection that the local reuse lookup resolves against.
func TestStampEntityImageSubjectMetadataMakesTheRowDiscoverable(t *testing.T) {
	metadata := map[string]any{}
	stampEntityImageSubjectMetadata(metadata, scriptpkg.SegmentAssetCandidate{
		Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Corrado Augias",
		EntityID: "person:corrado-augias", Query: "Corrado Augias portrait",
		RetrievalProvider: "duckduckgo",
	})
	if metadata["subject_id"] != "corrado-augias" {
		t.Fatalf("subject_id = %v, want corrado-augias", metadata["subject_id"])
	}
	if metadata["canonical_entity_id"] != "person:corrado-augias" || metadata["entity_type"] != "person" {
		t.Fatalf("identity projection = %v", metadata)
	}
	if metadata["origin"] != "retrieved" || metadata["provider"] != "duckduckgo" {
		t.Fatalf("provenance projection = %v", metadata)
	}
	searchText, _ := metadata["search_text"].(string)
	for _, want := range []string{"corrado-augias", "corrado-augias-portrait"} {
		if !containsTerm(searchText, want) {
			t.Fatalf("search_text = %q, want it to carry %q", searchText, want)
		}
	}
}

func containsTerm(haystack, needle string) bool {
	for _, field := range splitFields(haystack) {
		if field == needle {
			return true
		}
	}
	return false
}

func splitFields(value string) []string {
	var out []string
	current := ""
	for _, r := range value {
		if r == ' ' {
			if current != "" {
				out = append(out, current)
				current = ""
			}
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

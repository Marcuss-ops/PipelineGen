// Package assets — content_hash_index_revision_test.go pins the
// content_sha256 / index_revision separation (godlike/06 SSOT).
//
// Invariant under test: content_hash is BYTE identity and must NEVER
// change when text tracks/metadata change; index_revision is the
// SEPARATE fingerprint that folds the indexable surface. Before this
// separation, UpdateClipMetadataTextsAndRequestIndex folded text-track
// hashes INTO content_hash (m.ContentHash = ComputeContentHashWithTextTracks(...)),
// corrupting byte identity and forcing the supersede gate to compare
// byte-identity against a metadata-derived value.
package assets

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestComputeIndexRevision_FoldsTextTracksButKeepsContentHashDistinct
// pins the algorithm: the index revision is derived FROM contentHash +
// text tracks, and the derivation is deterministic + order-stable,
// while the caller (writer) is expected to keep contentHash untouched.
func TestComputeIndexRevision_FoldsTextTracksButKeepsContentHashDistinct(t *testing.T) {
	t.Parallel()
	base := "sha256:byte-identity-64-hex"

	// Empty tracks → revision == byte identity (no fold needed).
	if got := ComputeIndexRevision(base, nil); got != base {
		t.Fatalf("ComputeIndexRevision(base, nil) = %q, want %q", got, base)
	}

	tracks := []asset.TextTrack{
		{LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextHash: "sha256:en"},
		{LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextHash: "sha256:it"},
	}
	rev := ComputeIndexRevision(base, tracks)
	if rev == base || rev == "" {
		t.Fatalf("ComputeIndexRevision(base, tracks) = %q, want a distinct non-empty revision", rev)
	}

	// Determinism: reversed order must produce the same revision.
	reversed := []asset.TextTrack{tracks[1], tracks[0]}
	if got := ComputeIndexRevision(base, reversed); got != rev {
		t.Fatalf("ComputeIndexRevision must be order-stable: %q != %q", got, rev)
	}

	// The legacy alias must agree with the canonical function.
	if got := ComputeContentHashWithTextTracks(base, tracks); got != rev {
		t.Fatalf("ComputeContentHashWithTextTracks alias = %q, want %q", got, rev)
	}
}

// TestUpdateMediaAssetsMetadataTx_SeparatesContentHashAndIndexRevision
// pins the persistence boundary: the SAME transaction writes content_hash
// (byte identity, verbatim from m.ContentHash) AND index_revision (the
// separate snapshot fingerprint, verbatim from m.SourceVersion) — the two
// are never conflated.
func TestUpdateMediaAssetsMetadataTx_SeparatesContentHashAndIndexRevision(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	clipID := "yt_sep_0_60_v1"
	seedMediaAsset(t, db, clipID)

	m := youtubetypes.CanonicalClipMetadata{
		ClipID:        clipID,
		ContentHash:   "sha256:byte-identity",
		SourceVersion: "sha256:index-revision-folds-tracks",
		Summary:       "test",
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := updateMediaAssetsMetadataTx(context.Background(), tx, clipID, m, "2026-08-16T00:00:00Z"); err != nil {
		t.Fatalf("updateMediaAssetsMetadataTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	md := readMetadataJSON(t, db, clipID)
	if !strings.Contains(md, `"content_hash":"sha256:byte-identity"`) {
		t.Errorf("metadata_json must keep content_hash as byte identity; got %s", md)
	}
	if !strings.Contains(md, `"index_revision":"sha256:index-revision-folds-tracks"`) {
		t.Errorf("metadata_json must persist the separate index_revision; got %s", md)
	}
	// The two MUST differ — content_hash is byte identity, index_revision
	// folds the indexable surface.
	if strings.Contains(md, `"content_hash":"sha256:index-revision-folds-tracks"`) {
		t.Errorf("index_revision value must NOT leak into content_hash; got %s", md)
	}
}

// TestCommitTxRaw_SeparatesContentHashAndIndexRevision pins the canonical
// commit boundary (SQLiteAssetCommitter.CommitTxRaw): content_hash stays
// BYTE identity while index_revision is written as the SEPARATE supersede
// fingerprint from Metadata.SourceVersion. The two must never be conflated
// (godlike/06: content_sha256 vs index_revision).
func TestCommitTxRaw_SeparatesContentHashAndIndexRevision(t *testing.T) {
	t.Parallel()
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := persistence.CommitRequest{
		AssetID:        "yt_sep_commit_0_60_v1",
		Source:         "youtube",
		Filename:       "clip.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:byte-identity",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
		Metadata: persistence.TypedMetadata{
			SourceVersion: "sha256:index-revision-folds-tracks",
		},
	}
	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex: %v", err)
	}

	md := readMetadataJSON(t, db, req.AssetID)
	if !strings.Contains(md, `"content_hash":"sha256:byte-identity"`) {
		t.Errorf("metadata_json must keep content_hash as byte identity; got %s", md)
	}
	if !strings.Contains(md, `"index_revision":"sha256:index-revision-folds-tracks"`) {
		t.Errorf("metadata_json must persist the separate index_revision; got %s", md)
	}
	if strings.Contains(md, `"content_hash":"sha256:index-revision-folds-tracks"`) {
		t.Errorf("index_revision value must NOT leak into content_hash; got %s", md)
	}
}

// TestCommitTxRaw_IndexRevisionFallsBackToContentHash pins the
// byte-identity-only snapshot: when Metadata.SourceVersion is empty, the
// supersede fingerprint collapses to content_sha256 (no text-track/metadata
// fold) and both keys carry the same byte identity.
func TestCommitTxRaw_IndexRevisionFallsBackToContentHash(t *testing.T) {
	t.Parallel()
	db := newAtomicWriterDB(t)
	box := outboxevents.NewRepository(db)
	committer := NewSQLiteAssetCommitter(db, box, nil)

	req := persistence.CommitRequest{
		AssetID:        "yt_sep_commit_fb_0_60_v1",
		Source:         "youtube",
		Filename:       "clip.mp4",
		MediaType:      "video",
		ContentHash:    "sha256:byte-identity",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
	}
	if _, err := committer.CommitAndIndex(context.Background(), req); err != nil {
		t.Fatalf("CommitAndIndex: %v", err)
	}

	md := readMetadataJSON(t, db, req.AssetID)
	if !strings.Contains(md, `"content_hash":"sha256:byte-identity"`) {
		t.Errorf("metadata_json must keep content_hash as byte identity; got %s", md)
	}
	if !strings.Contains(md, `"index_revision":"sha256:byte-identity"`) {
		t.Errorf("empty SourceVersion must fall back to content_hash for index_revision; got %s", md)
	}
}

// TestUpdateMediaAssetsMetadataTx_OmitsIndexRevisionWhenEmpty keeps the
// legacy no-fingerprint contract: an empty SourceVersion writes no
// index_revision key (json_patch leaves the prior bag untouched).
func TestUpdateMediaAssetsMetadataTx_OmitsIndexRevisionWhenEmpty(t *testing.T) {
	t.Parallel()
	db := testDBForMetadataWriter(t)
	clipID := "yt_sep_empty_0_60_v1"
	seedMediaAsset(t, db, clipID)

	m := youtubetypes.CanonicalClipMetadata{
		ClipID:        clipID,
		ContentHash:   "sha256:byte-identity",
		SourceVersion: "",
		Summary:       "test",
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := updateMediaAssetsMetadataTx(context.Background(), tx, clipID, m, "2026-08-16T00:00:00Z"); err != nil {
		t.Fatalf("updateMediaAssetsMetadataTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	md := readMetadataJSON(t, db, clipID)
	if strings.Contains(md, `"index_revision"`) {
		t.Errorf("empty SourceVersion must not write index_revision; got %s", md)
	}
}

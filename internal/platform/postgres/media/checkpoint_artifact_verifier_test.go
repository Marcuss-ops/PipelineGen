// Package media_test — checkpoint_artifact_verifier_test.go pins the
// checkpoint resume artifact port against the live PostgreSQL media SSOT.
//
// DSN-gated like the rest of the package's fixtures: without TEST_POSTGRES_DSN
// the test SKIPs (never a fake pass). The fixture inserts one media_assets row
// whose content_sha256 is the artifact digest, which is exactly what the
// canonical final-audio publisher commits.
package media_test

import (
	"context"
	"strings"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

const checkpointArtifactDigest = "8f14e45fceea167a5a36dedd4bea2543e2f3c1ab0d1c0a3e7d6f2b0d3b1c4a5e"

// TestMediaArtifactVerifierResolvesRegisteredDigest pins the two decidable
// outcomes of the port:
//
//   - the recorded digest is registered in media_assets -> (Exists, Matches);
//   - a digest no asset references -> definitive staleness, NOT an error
//     (the resolver invalidates the checkpoint and re-renders).
func TestMediaArtifactVerifierResolvesRegisteredDigest(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO media_assets (id, source, name, lifecycle_state, index_state, content_sha256)
		 VALUES ($1, 'voiceover', 'voiceover [en].m4a', 'ACTIVE', 'DISCOVERED', $2)`,
		"asset_checkpoint_verifier", checkpointArtifactDigest); err != nil {
		t.Fatalf("seed media asset: %v", err)
	}

	verifier, err := pgmedia.NewMediaArtifactVerifier(db)
	if err != nil {
		t.Fatalf("NewMediaArtifactVerifier: %v", err)
	}

	status, err := verifier.VerifyArtifact(ctx, checkpointArtifactDigest, "https://drive.google.com/file/d/example/view")
	if err != nil {
		t.Fatalf("VerifyArtifact(registered): %v", err)
	}
	if !status.Exists || !status.SHA256Matches {
		t.Fatalf("VerifyArtifact(registered) = %+v, want Exists+SHA256Matches", status)
	}

	missing, err := verifier.VerifyArtifact(ctx, strings.Repeat("a", 64), "")
	if err != nil {
		t.Fatalf("VerifyArtifact(unknown) must be a definitive miss, not an error: %v", err)
	}
	if missing.Exists || missing.SHA256Matches {
		t.Fatalf("VerifyArtifact(unknown) = %+v, want Exists=false SHA256Matches=false", missing)
	}

	// An empty digest is a caller bug: never invent availability.
	empty, err := verifier.VerifyArtifact(ctx, "   ", "")
	if err != nil {
		t.Fatalf("VerifyArtifact(empty) must not error: %v", err)
	}
	if empty.Exists || empty.SHA256Matches {
		t.Fatalf("VerifyArtifact(empty) = %+v, want no availability", empty)
	}
}

// TestNewMediaArtifactVerifierRejectsNilDB pins the fail-fast constructor:
// the composition root must not fabricate a verifier with no SSOT handle.
func TestNewMediaArtifactVerifierRejectsNilDB(t *testing.T) {
	if _, err := pgmedia.NewMediaArtifactVerifier(nil); err == nil {
		t.Fatal("NewMediaArtifactVerifier(nil) = nil error, want fail-fast")
	}
}

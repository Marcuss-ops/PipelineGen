// Package media — checkpoint_artifact_verifier.go: the checkpoint resume
// artifact port (capabilities/checkpoint.ArtifactVerifier) backed by the
// PostgreSQL media SSOT.
//
// WHY THIS ADAPTER EXISTS. The durable per-unit checkpoint records the
// artifact a completed unit produced so resume can refuse to reuse a
// completion whose bytes are gone. The runner's audio unit records the
// published voiceover artifact (`runner_phase_audio.go`: ArtifactSHA256 =
// finalAudio.FinalAudioSHA256, ArtifactURI = finalAudio.DriveLink). That
// artifact's durable home is the media SSOT — final_audio_publisher.go
// registers it through the canonical PostgresMediaCommitter into
// media_assets.content_sha256 — so the ONLY adapter that can answer
// "does this artifact still exist?" is the one that reads the SSOT. The CAS
// verifier (internal/platform/cas/artifact_verifier.go) answers the same
// question for CAS-addressed units; the two are not interchangeable.
//
// FAIL-CLOSED, BY CONSTRUCTION. The row is keyed by the recorded digest, so
// a hit means the bytes are registered under exactly that SHA-256
// (`SHA256Matches: true` is not a second measurement — the query IS the
// comparison). A miss returns Exists=false and the resolver re-renders.
// A transport failure is returned as an error, which the resolver treats as
// "cannot judge" → EXECUTE, never an unverified SKIP.
//
// The recorded URI is advisory here (exactly like the CAS verifier): for a
// media-SSOT artifact the recorded identity is the content digest, and the
// Drive link is a delivery location, not the address.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// MediaArtifactVerifier implements capcheckpoint.ArtifactVerifier against the
// PostgreSQL media SSOT (media_assets.content_sha256).
type MediaArtifactVerifier struct {
	resolver *PostgresCanonicalIdentityResolver
}

// NewMediaArtifactVerifier constructs the adapter. Fail-fast: a nil database
// is a programmer error, not a silent no-op (godlike/07) — the caller that
// has no media SSOT handle must not construct one at all.
func NewMediaArtifactVerifier(db *sql.DB) (*MediaArtifactVerifier, error) {
	if db == nil {
		return nil, errors.New("media artifact verifier (postgres): nil database")
	}
	resolver, err := NewPostgresCanonicalIdentityResolver(db)
	if err != nil {
		return nil, fmt.Errorf("media artifact verifier (postgres): %w", err)
	}
	return &MediaArtifactVerifier{resolver: resolver}, nil
}

var _ capcheckpoint.ArtifactVerifier = (*MediaArtifactVerifier)(nil)

// VerifyArtifact reports whether the recorded digest is still registered in
// the media SSOT. A missing asset is (Exists=false, nil) — a definitive
// staleness the resolver invalidates — while a transport/query failure is an
// error, which the resolver degrades to EXECUTE without invalidating.
func (v *MediaArtifactVerifier) VerifyArtifact(ctx context.Context, sha256, uri string) (capcheckpoint.ArtifactStatus, error) {
	if v == nil || v.resolver == nil {
		return capcheckpoint.ArtifactStatus{}, errors.New("media artifact verifier (postgres): not wired")
	}
	digest := strings.TrimSpace(sha256)
	if digest == "" {
		// The resolver only asks about units that recorded an artifact, so an
		// empty digest is a caller bug: report "missing" rather than a fake
		// pass. Never invent availability.
		return capcheckpoint.ArtifactStatus{Exists: false, SHA256Matches: false}, nil
	}
	_, err := v.resolver.ResolveContent(ctx, digest)
	if err == nil {
		return capcheckpoint.ArtifactStatus{Exists: true, SHA256Matches: true}, nil
	}
	if errors.Is(err, capregistry.ErrCanonicalIdentityNotFound) {
		return capcheckpoint.ArtifactStatus{Exists: false, SHA256Matches: false}, nil
	}
	return capcheckpoint.ArtifactStatus{}, fmt.Errorf("media artifact verifier (postgres): verify %s: %w", digest, err)
}

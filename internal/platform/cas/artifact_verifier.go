package cas

import (
	"context"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
)

// ArtifactVerifier adapts the CAS store to the checkpoint resume port:
// a recorded artifact is resumable only when its bytes still exist and
// re-hash to the recorded digest. Verify re-hashes the on-disk bytes —
// resume never trusts the recorded row alone.
type ArtifactVerifier struct {
	store *Store
}

// NewArtifactVerifier constructs the adapter. Fail-closed: a nil store
// yields a verifier that fails every verification.
func NewArtifactVerifier(store *Store) *ArtifactVerifier {
	return &ArtifactVerifier{store: store}
}

// VerifyArtifact verifies the artifact at the recorded digest. The uri is
// advisory for CAS-backed artifacts (the address IS the digest); when the
// store is not wired, verification fails closed (never a silent pass).
func (v *ArtifactVerifier) VerifyArtifact(ctx context.Context, sha256, uri string) (capcheckpoint.ArtifactStatus, error) {
	if v == nil || v.store == nil {
		return capcheckpoint.ArtifactStatus{}, ErrNotWired
	}
	result, err := v.store.Verify(ctx, sha256)
	if err != nil {
		return capcheckpoint.ArtifactStatus{}, err
	}
	return capcheckpoint.ArtifactStatus{Exists: result.Exists, SHA256Matches: result.Verified}, nil
}

var _ capcheckpoint.ArtifactVerifier = (*ArtifactVerifier)(nil)

package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"go.uber.org/zap"
)

// ArtifactPreparation is the concrete implementation of
// finalization.ArtifactPreparationService.
//
// It validates a verified artifact, delegates publication to a
// finalization.PublisherPort, and returns the PublishedArtifact with
// its canonical location.
//
// Validation (fail-fast):
//   - ArtifactID is non-empty
//   - Kind is recognized
//   - LocalPath exists and is readable
//   - SHA256 is non-empty and matches content
//   - SizeBytes > 0
//   - IdempotencyKey is non-empty
type ArtifactPreparation struct {
	publisher finalization.PublisherPort
	log       *zap.Logger
}

// NewArtifactPreparation creates an ArtifactPreparation with the given
// publisher (nil-safe; if nil, Prepare will always return an error
// for publication — useful for testing with a stub).
func NewArtifactPreparation(pub finalization.PublisherPort, log *zap.Logger) *ArtifactPreparation {
	if log == nil {
		log = zap.NewNop()
	}
	return &ArtifactPreparation{
		publisher: pub,
		log:       log,
	}
}

// Compile-time assertion.
var _ finalization.ArtifactPreparationService = (*ArtifactPreparation)(nil)

// Prepare validates, hashes, and publishes a verified artifact, returning
// the published artifact with its canonical location.
func (s *ArtifactPreparation) Prepare(
	ctx context.Context,
	artifact finalization.VerifiedArtifact,
) (finalization.PublishedArtifact, error) {
	// Fail-fast validation.
	if err := s.validate(ctx, artifact); err != nil {
		return finalization.PublishedArtifact{}, err
	}

	// Publish to remote storage.
	if s.publisher == nil {
		return finalization.PublishedArtifact{},
			fmt.Errorf("artifact preparation: no publisher configured for artifact %s", artifact.ArtifactID)
	}

	location, err := s.publisher.Publish(ctx, artifact)
	if err != nil {
		return finalization.PublishedArtifact{},
			fmt.Errorf("artifact preparation: publish %s: %w", artifact.ArtifactID, err)
	}

	s.log.Info("artifact published",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.String("kind", string(artifact.Kind)),
		zap.String("provider", location.Provider),
		zap.String("file_id", location.FileID),
		zap.String("action", string(location.Action)),
	)

	return finalization.PublishedArtifact{
		ArtifactID:     artifact.ArtifactID,
		Kind:           artifact.Kind,
		Filename:       artifact.Filename,
		MIMEType:       artifact.MIMEType,
		SizeBytes:      artifact.SizeBytes,
		SHA256:         artifact.SHA256,
		SourceVersion:  artifact.SourceVersion,
		Requirement:    artifact.Requirement,
		IdempotencyKey: artifact.IdempotencyKey,
		Description:    artifact.Description,
		Source:         artifact.Source,
		ProjectID:      artifact.ProjectID,
		Language:       artifact.Language,
		Location:       location,
	}, nil
}

// validate performs fail-fast checks on the verified artifact.
// P0.5 (July 2026): upgraded from non-empty-string-only checks to
// real on-disk verification via remote.VerifyArtifact (os.Stat +
// SHA-256 file hash). The idempotency-key derivation check is
// deferred until the caller threads a jobID through the context
// or the VerifiedArtifact struct.
func (s *ArtifactPreparation) validate(ctx context.Context, a finalization.VerifiedArtifact) error {
	// Lightweight pre-checks (fail-fast before touching disk).
	if a.ArtifactID == "" {
		return fmt.Errorf("artifact validation: ArtifactID is empty")
	}
	if a.Kind == "" {
		return fmt.Errorf("artifact validation: Kind is empty (artifact=%s)", a.ArtifactID)
	}
	if a.LocalPath == "" {
		return fmt.Errorf("artifact validation: LocalPath is empty (artifact=%s)", a.ArtifactID)
	}
	if a.SHA256 == "" {
		return fmt.Errorf("artifact validation: SHA256 is empty (artifact=%s)", a.ArtifactID)
	}
	if a.SizeBytes <= 0 {
		return fmt.Errorf("artifact validation: SizeBytes is %d (artifact=%s)", a.SizeBytes, a.ArtifactID)
	}
	if a.IdempotencyKey == "" {
		return fmt.Errorf("artifact validation: IdempotencyKey is empty (artifact=%s)", a.ArtifactID)
	}

	// Real on-disk verification (P0.5).
	// Empty jobID skips idempotency-key derivation check; file
	// existence / size / SHA-256 are always enforced.
	if err := remote.VerifyArtifact(ctx, "", a); err != nil {
		return fmt.Errorf("artifact verification failed for %s: %w", a.ArtifactID, err)
	}
	return nil
}

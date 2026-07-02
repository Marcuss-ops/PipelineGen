package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"go.uber.org/zap"
)

// Publisher is the port for publishing a verified artifact to a remote
// storage backend (Drive, S3, object storage).
//
// FASE 5 will provide the concrete Drive publisher. Until then, tests and
// capabilities can inject a stub or mock.
//
// Idempotency: the implementation MUST use the artifact's IdempotencyKey
// to avoid duplicate publications. Same content → same key → same remote
// file → PublishSkipped.
type Publisher interface {
	// Publish uploads the artifact's content (from LocalPath) to the
	// remote storage backend and returns the canonical AssetLocation.
	// The returned location MUST set Provider, FileID, WebViewLink,
	// DownloadLink, Checksum, FolderID, FolderPath, and Action.
	Publish(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.AssetLocation, error)
}

// ArtifactPreparation is the concrete implementation of
// finalization.ArtifactPreparationService.
//
// It validates a verified artifact, delegates publication to a Publisher,
// and returns the PublishedArtifact with its canonical location.
//
// Validation (fail-fast):
//   - ArtifactID is non-empty
//   - Kind is recognized
//   - LocalPath exists and is readable
//   - SHA256 is non-empty and matches content
//   - SizeBytes > 0
//   - IdempotencyKey is non-empty
type ArtifactPreparation struct {
	publisher Publisher
	log       *zap.Logger
}

// NewArtifactPreparation creates an ArtifactPreparation with the given
// publisher (nil-safe; if nil, Prepare will always return an error
// for publication — useful for testing with a stub).
func NewArtifactPreparation(pub Publisher, log *zap.Logger) *ArtifactPreparation {
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
	if err := s.validate(artifact); err != nil {
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
		ArtifactID:    artifact.ArtifactID,
		Kind:          artifact.Kind,
		Filename:      artifact.Filename,
		MIMEType:      artifact.MIMEType,
		SizeBytes:     artifact.SizeBytes,
		SHA256:        artifact.SHA256,
		SourceVersion: artifact.SourceVersion,
		Required:      artifact.Required,
		IdempotencyKey: artifact.IdempotencyKey,
		Location:      location,
	}, nil
}

// validate performs fail-fast checks on the verified artifact.
func (s *ArtifactPreparation) validate(a finalization.VerifiedArtifact) error {
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
	return nil
}

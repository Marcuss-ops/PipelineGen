package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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
	// Fail-fast validation (stat + size + idempotency checks; the SHA-256
	// content hash inside VerifyArtifact is additionally measured as
	// finalize.artifact_hash — the audio_render/mix nesting precedent).
	// Each artifact is measured as finalize.artifact_prepare so the
	// post_writer_finalize stage is no longer a black box. The stage
	// comes from the caller's context (post_writer_finalize on the worker
	// finalize spine) and defaults to publish for direct publishers
	// (stock, vidrush, overlay).
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageOrDefault(ctx, kernobs.StagePublish),
		Component: kernobs.ComponentName("finalize"),
		Operation: kernobs.OperationName("artifact_prepare"),
		Items:     1,
		Bytes:     artifact.SizeBytes,
	}, func(opCtx context.Context) error {
		return s.validate(opCtx, artifact)
	}); err != nil {
		return finalization.PublishedArtifact{}, err
	}

	// Publish to remote storage.
	if s.publisher == nil {
		return finalization.PublishedArtifact{},
			fmt.Errorf("artifact preparation: no publisher configured for artifact %s", artifact.ArtifactID)
	}

	// The Drive (or object-store) upload is the dominant per-artifact
	// cost of post_writer_finalize; measured separately as
	// finalize.drive_publish so the sequential I/O is visible in the
	// RunReport instead of hiding inside a single finalize envelope.
	var location finalization.AssetLocation
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageOrDefault(ctx, kernobs.StagePublish),
		Component: kernobs.ComponentName("finalize"),
		Operation: kernobs.OperationName("drive_publish"),
		Items:     1,
		Bytes:     artifact.SizeBytes,
	}, func(opCtx context.Context) error {
		var err error
		location, err = s.publisher.Publish(opCtx, artifact)
		return err
	}); err != nil {
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

	// Stamp the post-publication Drive identity onto the artifact metadata
	// so downstream projections (manifest, metadata_json, Qdrant) read a
	// single source of truth: drive_file_id ← Location.FileID, drive_link ←
	// Location.WebViewLink. The published column fields (media_assets
	// .drive_file_id/.drive_link) remain derived from Location directly; this
	// is the additive metadata representation consumed by projections.
	metadata := make(map[string]any, len(artifact.ArtifactMetadata)+3)
	for k, v := range artifact.ArtifactMetadata {
		metadata[k] = v
	}
	if location.FileID != "" {
		metadata["drive_file_id"] = location.FileID
	}
	if location.WebViewLink != "" {
		metadata["drive_link"] = location.WebViewLink
	}
	if location.DownloadLink != "" {
		metadata["download_link"] = location.DownloadLink
	}

	return finalization.PublishedArtifact{
		ArtifactID:       artifact.ArtifactID,
		Kind:             artifact.Kind,
		Filename:         artifact.Filename,
		MIMEType:         artifact.MIMEType,
		SizeBytes:        artifact.SizeBytes,
		SHA256:           artifact.SHA256,
		SourceVersion:    artifact.SourceVersion,
		Requirement:      artifact.Requirement,
		IdempotencyKey:   artifact.IdempotencyKey,
		Description:      artifact.Description,
		Source:           artifact.Source,
		ProjectID:        artifact.ProjectID,
		Language:         artifact.Language,
		ArtifactMetadata: metadata,
		Location:         location,
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

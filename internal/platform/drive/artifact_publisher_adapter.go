// Package drive — artifact_publisher_adapter.go (P0.4, July 2026).
//
// ArtifactPublisherAdapter is the canonical bridge between the
// domain-level finalization.PublisherPort and the application-level
// delivery.Publisher. It:
//
//  1. Maps finalization.ArtifactKind → delivery.DestinationKey
//     (deterministic switch so every artifact kind routes to the
//     correct Drive destination).
//
//  2. Verifies the artifact's SHA-256 matches the on-disk file
//     (defence-in-depth: a mutated-on-disk file or a file moved
//     between verification and upload MUST NOT be uploaded with the
//     wrong hash — the verify-before-publish gate catches drift).
//
//  3. Threads VerifiedArtifact.IdempotencyKey into PublishRequest
//     with ConflictPolicy=ConflictSkip so the Drive-side dedup
//     (filename-based + description-idempotency-key compound) can
//     collapse retries into a PublishActionSkipped without
//     re-uploading identical content.
//
//  4. Converts delivery.PublishResult → finalization.AssetLocation
//     (the canonical wire format the ArtifactPreparation.Prepare
//     expects for building PublishedArtifact).
//
// godlike/06 SSOT: this adapter is the ONE canonical bridge between
// finalization → delivery. Every capability that needs to publish a
// VerifiedArtifact to Drive MUST go through this adapter (or a
// structurally identical one that satisfies the same port). No
// capability may hand-wire delivery.Publisher.Publish directly.
//
// Compile-time assertion:
//
//	var _ finalization.PublisherPort = (*ArtifactPublisherAdapter)(nil)
//
// ensures that future drift on the PublisherPort signature is a build
// failure, not a runtime panic.
package drive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// ── Sentinel errors ─────────────────────────────────────────────────

// ErrArtifactKindUnmapped is returned when a VerifiedArtifact.Kind
// has no corresponding delivery.DestinationKey. The caller MUST
// either add the mapping here or route the artifact through an
// alternate publisher.
var ErrArtifactKindUnmapped = errors.New("artifact publisher: artifact kind has no Drive destination mapping")

// ErrArtifactHashMismatch is returned when the on-disk file's SHA-256
// does not match the VerifiedArtifact.SHA256. This is a defence-in-
// depth gate: the artifact was verified locally before being handed
// to this publisher; if the hash diverges, the file was mutated or
// moved between verification and upload.
var ErrArtifactHashMismatch = errors.New("artifact publisher: on-disk SHA-256 does not match VerifiedArtifact.SHA256")

// ErrArtifactPublisherNotConfigured is returned when the adapter
// was constructed with a nil Publisher (composition-time wiring gap).
var ErrArtifactPublisherNotConfigured = errors.New("artifact publisher: delivery.Publisher is nil (composition-time wiring gap)")

// ErrResolvedFolderNotVerified is returned when an application envelope
// supplies a direct Drive folder ID without the corresponding resolution
// proof. Publishing must fail closed instead of guessing a destination.
var ErrResolvedFolderNotVerified = errors.New("artifact publisher: resolved folder ID is not verified")

// ── Adapter ─────────────────────────────────────────────────────────

// ArtifactPublisherAdapter implements finalization.PublisherPort by
// wrapping delivery.Publisher.
//
// Drive cutover P0.4 (July 2026): this adapter supersedes the
// previously-hand-wired deliveryToFinalizerPublisherAdapter in
// internal/capabilities/assets/providers/stock/stockpipeline/run_orchestrator.go
// (which hardcoded DestinationStock and skipped SHA-256 verification).
// The per-kind mapping + verify-before-publish gate make this the
// canonical upload surface for ALL capabilities.
type ArtifactPublisherAdapter struct {
	pub delivery.Publisher
	log *zap.Logger
}

// Compile-time assertion: adapter satisfies finalization.PublisherPort.
var _ finalization.PublisherPort = (*ArtifactPublisherAdapter)(nil)

// NewArtifactPublisherAdapter creates the canonical adapter. Returns
// an adapter even when pub is nil (the nil-receiver guard on Publish
// surfaces ErrArtifactPublisherNotConfigured rather than a nil-pointer
// panic — the same fail-closed pattern as C6 Adapter).
func NewArtifactPublisherAdapter(pub delivery.Publisher, log *zap.Logger) *ArtifactPublisherAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &ArtifactPublisherAdapter{pub: pub, log: log}
}

// Publish implements finalization.PublisherPort.
//
// Steps:
//  1. Verify SHA-256 of local file matches VerifiedArtifact.SHA256.
//  2. Map ArtifactKind → Delivery.DestinationKey.
//  3. Build delivery.PublishRequest with ConflictSkip + Description
//     carrying IdempotencyKey (Drive-side dedup).
//  4. Delegate to delivery.Publisher.Publish.
//  5. Convert delivery.PublishResult → finalization.AssetLocation.
func (a *ArtifactPublisherAdapter) Publish(
	ctx context.Context,
	artifact finalization.VerifiedArtifact,
) (finalization.AssetLocation, error) {
	if a.pub == nil {
		return finalization.AssetLocation{}, ErrArtifactPublisherNotConfigured
	}

	// Step 1: Verify content hash (defence-in-depth).
	if err := a.verifySHA256(artifact); err != nil {
		return finalization.AssetLocation{}, err
	}

	// Step 2: Map kind → destination.
	destKey, err := mapKindToDestination(artifact.Kind)
	if err != nil {
		return finalization.AssetLocation{}, err
	}

	if strings.TrimSpace(artifact.ResolvedFolderID) != "" && !artifact.RootFolderResolved {
		return finalization.AssetLocation{}, fmt.Errorf("%w: artifact=%s", ErrResolvedFolderNotVerified, artifact.ArtifactID)
	}

	// Step 3: Build publish request with idempotency-key identity (P0.6).
	// The IdempotencyKey is derived from SHA-256(dest:artifactID:sha256:version)
	// and becomes the authoritative identity for Drive conflict detection.
	// Description carries human-readable context for the Drive UI (no longer
	// a hash workaround — P0.4 deprecated).
	//
	// DoD #3 (July 2026): Tags + ProjectID + Language are threaded
	// through PublishRequest for downstream Qdrant indexing. The
	// adapter does NOT derive Tags from VerifiedArtifact fields
	// (VerifiedArtifact has no tags surface) — forward-pointer to
	// the per-capability finalizer that populates Tags before
	// VerifiedArtifact reaches this adapter. Today Tags is nil
	// (the outbox handler tolerates an empty slice as "no keywords
	// yet"). ProjectID is empty because VerifiedArtifact carries
	// ArtifactID (per-artifact identity, not project grouping);
	// Language is empty because VerifiedArtifact has no BCP-47 field.
	// Both propagate when the upstream finalizer adds them to the
	// VerifiedArtifact envelope.
	idemKey := delivery.DeriveIdempotencyKey(destKey, artifact.ArtifactID, artifact.SHA256, artifact.SourceVersion)
	group, subject, provider := stockArtifactPathParts(artifact)
	req := delivery.PublishRequest{
		Destination:    destKey,
		LocalPath:      artifact.LocalPath,
		Filename:       artifact.Filename,
		Description:    fmt.Sprintf("artifact %s v%d (%s)", artifact.ArtifactID, artifact.SourceVersion, artifact.Kind),
		AssetID:        artifact.ArtifactID,
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: idemKey,
		ContentHash:    artifact.SHA256,
		SourceVersion:  artifact.SourceVersion,
		Group:          group,
		Subject:        subject,
		Category:       group,
		Style:          "vidrush",
		Provider:       provider,
		ProjectID:      artifact.ProjectID,
		Language:       artifact.Language,
		Tags:           nil, // DoD #3: populated by per-capability finalizer (forward-pointer)
		// ParentFolderID is retained only for legacy envelopes.
		ParentFolderID: artifact.ParentFolderID,
		// A stock run resolves its named Drive folder before publishing.
		// Pin the canonical destination explicitly so the path builder
		// cannot recreate legacy category/video subfolders.
		DestinationFolderID: func() string {
			if artifact.RootFolderResolved {
				return strings.TrimSpace(artifact.ResolvedFolderID)
			}
			return ""
		}(),
		DestinationSubpath: artifactDriveSubpath(artifact),
	}

	// Step 4: Delegate to canonical Drive publisher.
	result, err := a.pub.Publish(ctx, req)
	if err != nil {
		return finalization.AssetLocation{},
			fmt.Errorf("artifact publisher: publish %s (kind=%s dest=%s): %w",
				artifact.ArtifactID, artifact.Kind, destKey, err)
	}

	// Step 5: Convert delivery.PublishResult → finalization.AssetLocation.
	loc := finalization.AssetLocation{
		Provider:     "drive",
		FileID:       result.FileID,
		WebViewLink:  result.WebViewLink,
		DownloadLink: result.DownloadLink,
		Checksum:     result.MD5Checksum,
		FolderID:     result.FolderID,
		FolderPath:   result.FolderPath,
		Action:       translatePublishAction(result.Action),
	}

	a.log.Info("artifact published to Drive",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.String("kind", string(artifact.Kind)),
		zap.String("dest", string(destKey)),
		zap.String("file_id", result.FileID),
		zap.String("action", string(result.Action)),
	)

	return loc, nil
}

// artifactDriveSubpath returns the canonical child path for specialized
// artifacts. Overlay output is always colocated below the already-created
// per-video artifact folder, never below a second global Drive root.
// Explicit DriveSubpath remains available for future sidecar families.
func artifactDriveSubpath(artifact finalization.VerifiedArtifact) []string {
	if len(artifact.DriveSubpath) > 0 {
		return append([]string(nil), artifact.DriveSubpath...)
	}
	// RenderingGen emits source=chronon with an explicit drive_subpath;
	// the fallback keeps overlay output colocated under /overlay/ even
	// when the explicit subpath is absent (legacy "overlay" source is
	// retained for back-compat with pre-chronon producers).
	switch strings.ToLower(strings.TrimSpace(artifact.Source)) {
	case "overlay", "chronon":
		return []string{"overlay"}
	}
	return nil
}

// stockArtifactPathParts derives the Drive folder path segments for
// stock-pipeline artifacts. The folder tree is keyed off a readable
// run label derived from the run fingerprint embedded in ArtifactID,
// so Drive paths remain per-run without recreating a redundant
// "stock" subfolder under the stock root folder.
func stockArtifactPathParts(artifact finalization.VerifiedArtifact) (group, subject, provider string) {
	// VidRush assets carry their provider as the canonical source. Keep their
	// Drive layout deterministic and independent from stock/youtube run IDs.
	switch strings.ToLower(strings.TrimSpace(artifact.Source)) {
	case "artlist", "internet_images", "image_generation":
		group = "vidrush"
		subject = stockFolderLeafName(firstNonEmpty(artifact.PathLeafName, artifact.Filename))
		return group, subject, strings.ToLower(strings.TrimSpace(artifact.Source))
	}
	// When the stock orchestrator already resolved the root folder
	// (run_orchestratorResilient created the folder_name subfolder),
	// preserve semantic group/subject values for the delivery mapper.
	// The resolved destination ID still pins the physical location, so
	// these values are validation metadata and do not create another
	// Drive path. Returning empty values here makes the canonical mapper
	// reject the run-level metadata artifact before publication.
	if artifact.RootFolderResolved {
		group := strings.TrimSpace(artifact.RootFolderName)
		subject := stockFolderLeafName(artifact.PathLeafName)
		if subject == "" {
			subject = stockFolderLeafName(artifact.Filename)
		}
		return group, subject, "youtube"
	}
	parts := strings.Split(artifact.ArtifactID, ":")
	group = stockRunFolderName(artifact.RootFolderName)
	subject = stockFolderLeafName(artifact.PathLeafName)
	switch {
	case len(parts) >= 5 && parts[0] == "stock" && parts[2] == "timestamp" && parts[4] == "video":
		if group == "" {
			group = stockRunFolderName(parts[1])
		}
		if subject == "" {
			subject = "timestamp_" + parts[3]
		}
	case len(parts) >= 5 && parts[0] == "stock" && parts[2] == "timestamp" && parts[4] == "metadata":
		if group == "" {
			group = stockRunFolderName(parts[1])
		}
		if subject == "" {
			subject = "timestamp_" + parts[3]
		}
	case len(parts) >= 4 && parts[0] == "stock" && parts[2] == "chunk":
		if group == "" {
			group = stockRunFolderName(parts[1])
		}
		if subject == "" {
			subject = "chunk_" + parts[3]
		}
	case len(parts) >= 3 && parts[0] == "stock" && parts[2] == "metadata":
		if group == "" {
			group = stockRunFolderName(parts[1])
		}
		if subject == "" {
			subject = "metadata"
		}
	default:
		if group == "" {
			group = stockRunFolderName("")
		}
		if subject == "" {
			subject = artifact.Filename
		}
	}
	return group, subject, provider
}

func stockRunFolderName(runFingerprint string) string {
	runFingerprint = pathutil.SafeFolderName(strings.TrimSpace(runFingerprint))
	if runFingerprint == "" {
		return "run"
	}
	if isHexString(runFingerprint) {
		if len(runFingerprint) > 12 {
			runFingerprint = runFingerprint[:12]
		}
		return "run_" + runFingerprint
	}
	return runFingerprint
}

func stockFolderLeafName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = pathutil.SafeFolderName(name)
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ── Kind → DestinationKey mapping ───────────────────────────────────

// mapKindToDestination maps a finalization.ArtifactKind to a
// delivery.DestinationKey. Returns ErrArtifactKindUnmapped for
// unrecognised kinds so callers fail loudly rather than silently
// routing to a default destination.
//
// Mapping table (P0.4, July 2026):
//
//	KindVideo       → DestinationYouTubeClip (default for video)
//	KindImage       → DestinationImage
//	KindAudio       → DestinationYouTubeClip (audio clips also route to clips)
//	KindDocument    → DestinationDocument
//	KindScript      → DestinationScript
//	KindVoiceover   → DestinationVoiceover
//	KindSoundEffect → DestinationSoundEffect
//	KindMetadata    → DestinationStock (metadata artifacts from stock pipeline)
//	KindArchive     → DestinationStock (archive artifacts from stock pipeline)
func mapKindToDestination(kind finalization.ArtifactKind) (delivery.DestinationKey, error) {
	switch kind {
	case finalization.KindVideo:
		return delivery.DestinationYouTubeClip, nil
	case finalization.KindImage:
		return delivery.DestinationImage, nil
	case finalization.KindAudio:
		return delivery.DestinationYouTubeClip, nil
	case finalization.KindDocument:
		return delivery.DestinationDocument, nil
	case finalization.KindScript:
		return delivery.DestinationScript, nil
	case finalization.KindVoiceover:
		return delivery.DestinationVoiceover, nil
	case finalization.KindSoundEffect:
		return delivery.DestinationSoundEffect, nil
	case finalization.KindMetadata:
		return delivery.DestinationStock, nil
	case finalization.KindArchive:
		return delivery.DestinationStock, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrArtifactKindUnmapped, kind)
	}
}

// ── SHA-256 verification ────────────────────────────────────────────

// verifySHA256 computes the SHA-256 hash of the file at
// artifact.LocalPath and compares it against artifact.SHA256.
// Returns ErrArtifactHashMismatch if they differ.
func (a *ArtifactPublisherAdapter) verifySHA256(artifact finalization.VerifiedArtifact) error {
	f, err := os.Open(artifact.LocalPath)
	if err != nil {
		return fmt.Errorf("artifact publisher: cannot open %s for hash verification: %w", artifact.LocalPath, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("artifact publisher: cannot read %s for hash verification: %w", artifact.LocalPath, err)
	}
	actual := hex.EncodeToString(h.Sum(nil))

	if actual != artifact.SHA256 {
		return fmt.Errorf("%w: artifact=%s expected=%s actual=%s",
			ErrArtifactHashMismatch, artifact.ArtifactID, artifact.SHA256, actual)
	}
	return nil
}

// ── Action translation ──────────────────────────────────────────────

// translatePublishAction converts delivery.PublishAction to
// finalization.PublishAction.
//
// Mirrors the existing translateDeliveryAction in
// stockpipeline/run_orchestrator.go (pre-P0.4 hand-wired adapter).
// Post-P0.4, the stockpipeline adapter is retired; this is the
// canonical translation point.
func translatePublishAction(a delivery.PublishAction) finalization.PublishAction {
	switch a {
	case delivery.PublishActionCreated:
		return finalization.PublishCreated
	case delivery.PublishActionUpdated:
		return finalization.PublishUpdated
	case delivery.PublishActionSkipped:
		return finalization.PublishSkipped
	case delivery.PublishActionRenamed:
		return finalization.PublishRenamed
	default:
		return finalization.PublishAction("")
	}
}

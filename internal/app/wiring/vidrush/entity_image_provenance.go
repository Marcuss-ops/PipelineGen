// Package vidrush — entity_image_provenance.go owns the image-identity
// projection of the VidRush finalizer: the content-addressed reuse lookup that
// runs BEFORE a Drive upload, and the subject/entity metadata that makes a
// finalized image discoverable by the local reuse path afterwards.
package vidrush

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
	"go.uber.org/zap"
)

// reusePublishedImageByContent returns the already-published canonical asset
// whose bytes are identical to this artifact, if any.
//
// The identity of a downloaded picture is its content address, not the URL or
// provider that served it. Without this lookup the same photograph acquired
// from two providers (or through two entity aliases) was uploaded twice, and
// the library accumulated several Drive links for one file. The PG media SSOT
// is the reuse source because it is the only durable store the finalizer writes
// through; the catalog's materialization rows are projections of that write.
func (f *vidRushArtifactFinalizer) reusePublishedImageByContent(ctx context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, bool) {
	if f == nil || f.mediaDB == nil || !isPublishedImageCandidate(artifact.Candidate) {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	contentSHA256 := asset.ResolveContentAddress(artifact.ContentSHA256)
	if contentSHA256 == "" {
		// A digest that is not a canonical sha256 is absence of content
		// identity: it can never authorize a reuse decision.
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	var assetID, driveLink, driveFileID string
	err := f.mediaDB.QueryRowContext(ctx, `
		SELECT id, COALESCE(drive_link, ''), COALESCE(drive_file_id, '')
		FROM media_assets
		WHERE content_sha256 = $1
		  AND (COALESCE(drive_link, '') <> '' OR COALESCE(drive_file_id, '') <> '')
		  AND COALESCE(deleted_at, '') = ''
		ORDER BY CASE WHEN id = $2 THEN 0 ELSE 1 END, id
		LIMIT 1`, contentSHA256, strings.TrimSpace(artifact.Candidate.AssetID)).Scan(&assetID, &driveLink, &driveFileID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && f.log != nil {
			// A lookup failure is not a reason to fail the artifact: it only
			// means this upload cannot prove dedup, so it takes the normal
			// publication path.
			f.log.Warn("vidrush finalizer: content-address reuse lookup failed",
				zap.String("content_sha256", contentSHA256), zap.Error(err))
		}
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	assetID, driveLink = strings.TrimSpace(assetID), strings.TrimSpace(driveLink)
	if driveLink == "" {
		// Legacy rows may carry only the Drive file id. The reused candidate
		// still needs a navigable link; the id is its canonical form.
		driveLink = urlutil.DriveFileURL(strings.TrimSpace(driveFileID))
	}
	if assetID == "" || driveLink == "" {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	reused := artifact.Candidate
	reused.AssetID = assetID
	reused.DriveLink = driveLink
	reused.ContentSHA256 = contentSHA256
	reused.LocalPath = artifact.LocalPath
	reused.MIMEType = firstNonEmpty(artifact.MIMEType, reused.MIMEType, "application/octet-stream")
	reused.Width, reused.Height, reused.DurationMs = artifact.Width, artifact.Height, artifact.DurationMs
	reused.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	reused.VerificationStatus = scriptpkg.VidRushStatusVerified
	reused.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	reused.IndexStatus = "pending"
	if current, stateErr := readVidRushIndexState(ctx, f.mediaDB, assetID); stateErr == nil && current != "" {
		reused.IndexStatus = current
	}
	if f.log != nil {
		f.log.Info("vidrush finalizer: reused published asset for identical content",
			zap.String("asset_id", assetID), zap.String("content_sha256", contentSHA256),
			zap.String("provider", reused.Provider), zap.String("entity", reused.Entity))
	}
	return reused, true
}

// isPublishedImageCandidate limits content-address reuse to the image families
// this finalizer owns. A non-image artifact must keep its own publication
// contract.
func isPublishedImageCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	switch strings.ToLower(strings.TrimSpace(candidate.Provider)) {
	case scriptpkg.VidRushProviderInternetImages, scriptpkg.VidRushProviderImageGeneration:
		return true
	default:
		return false
	}
}

// stampEntityImageSubjectMetadata adds the durable subject projection to the
// canonical media row. ExistingImages resolves a subject through
// subject_id/search_text, so a row without this projection is invisible to the
// reuse path even though its bytes are already on Drive.
//
// subject_id is the slug of the ENTITY (stable across query variants), while
// search_text also carries the raw entity, the provider query and the canonical
// entity id, all slug-normalized, so a lookup keyed by any of those surfaces
// finds the row.
func stampEntityImageSubjectMetadata(metadata map[string]any, candidate scriptpkg.SegmentAssetCandidate) {
	if metadata == nil {
		return
	}
	entity := strings.TrimSpace(candidate.Entity)
	query := strings.TrimSpace(candidate.Query)
	canonicalEntityID := strings.TrimSpace(candidate.EntityID)
	if strings.HasPrefix(strings.ToLower(canonicalEntityID), "entity:") {
		// The neutral segment stamp is provenance, not an entity identity.
		canonicalEntityID = ""
	}
	subject := textutil.Slugify(firstNonEmpty(entity, query))
	if subject != "" {
		metadata["subject_id"] = subject
	}
	if entity != "" {
		metadata["entity"] = entity
	}
	if canonicalEntityID != "" {
		metadata["canonical_entity_id"] = canonicalEntityID
		metadata["entity_type"] = strings.ToLower(strings.SplitN(canonicalEntityID, ":", 2)[0])
	}
	if query != "" {
		metadata["query"] = query
	}
	// Origin is the image territory: a provider acquisition is retrieved, a
	// generated image is generated. The canonical reuse read filters on
	// origin='retrieved', so mislabelling a generated image here would let it
	// satisfy a retrieval lookup.
	origin := "retrieved"
	if strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderImageGeneration) {
		origin = "generated"
	}
	metadata["origin"] = origin
	metadata["provider"] = firstNonEmpty(candidate.RetrievalProvider, candidate.Provider)
	terms := make([]string, 0, 6)
	seen := make(map[string]struct{}, 6)
	appendTerm := func(value string) {
		value = textutil.Slugify(value)
		if value == "" {
			return
		}
		if _, dup := seen[value]; dup {
			return
		}
		seen[value] = struct{}{}
		terms = append(terms, value)
	}
	appendTerm(entity)
	appendTerm(query)
	appendTerm(canonicalEntityID)
	if len(terms) > 0 {
		metadata["search_text"] = strings.Join(terms, " ")
		metadata["search_terms"] = strings.Join(terms, " ")
	}
}

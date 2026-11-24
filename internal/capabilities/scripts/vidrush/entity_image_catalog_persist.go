package vidrush

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// persistEntityImageCatalogCandidates promotes a successful PERSON provider
// search into the durable catalog. It stores URLs only; acquisition remains
// owned by VidRushMaterializationProcessor and its common finalizer.
func persistEntityImageCatalogCandidates(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate) error {
	if repo == nil || identity.CanonicalEntityID == "" {
		return nil
	}
	providerTotal := len(results)
	accepted, rejected := filterPersonEntityImageCandidatesWithRejections(identity, results)
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        entitycatalog.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		return err
	}
	rank := 0
	persisted := 0
	for _, result := range accepted {
		if !scriptpkg.IsInternetImageSourceProvider(result.Provider) || strings.TrimSpace(result.SourceURL) == "" {
			continue
		}
		rank++
		if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          entityImageCatalogProviderFor(result),
			Rank:              rank,
			SourceURL:         result.SourceURL,
			SourcePageURL:     result.SourcePageURL,
			ThumbnailURL:      result.PreviewURL,
			Width:             result.Width,
			Height:            result.Height,
			Status:            entitycatalog.CandidateStatusFresh,
			SemanticStatus:    result.SemanticStatus,
			SemanticScore:     result.SemanticScore,
			ContentSHA256:     entityImageCatalogContentSHA256(result.ContentSHA256),
			VerifierVersion:   result.VerifierVersion,
			TechnicalScore:    result.TechnicalQualityScore,
			QualityReason:     result.QualityReason,
		}); err != nil {
			return err
		}
		persisted++
	}
	if persisted == 0 {
		return repo.SetRefreshState(ctx, identity.CanonicalEntityID, entitycatalog.RefreshStatusFailed, nowUTC(),
			"no PERSON image candidate passed the identity/quality filter: "+formatPersonImageRejectionSummary(providerTotal, rejected))
	}
	return repo.SetRefreshState(ctx, identity.CanonicalEntityID, entitycatalog.RefreshStatusSucceeded, nowUTC(), "")
}

// Exclude cached/provider URLs whose durable catalog state says they cannot
// be materialized. A cached response must not bypass a broken/retired row.
func excludeUnavailableEntityImageCatalogCandidates(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if repo == nil || identity.CanonicalEntityID == "" || len(candidates) == 0 {
		return candidates, nil
	}
	rows, err := repo.ListCandidates(ctx, identity.CanonicalEntityID, 1000)
	if err != nil {
		if errors.Is(err, entitycatalog.ErrEntityNotFound) {
			return candidates, nil
		}
		return nil, fmt.Errorf("entity image catalog: list candidate states for %s: %w", identity.CanonicalEntityID, err)
	}
	blocked := make(map[string]struct{})
	for _, row := range rows {
		if row.Status == entitycatalog.CandidateStatusBroken || row.Status == entitycatalog.CandidateStatusRetired {
			blocked[strings.ToLower(strings.TrimSpace(row.SourceURL))] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		return candidates, nil
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, denied := blocked[strings.ToLower(strings.TrimSpace(candidate.SourceURL))]; !denied {
			out = append(out, candidate)
		}
	}
	return out, nil
}

// personCatalogCandidateIdentity resolves the canonical PERSON identity a
// candidate is allowed to write into the durable catalog.
//
// The durable catalog is PERSON-only. A candidate whose EntityID names another
// entity type (org:/place:/object:/...) is never promoted here, and a caller
// that established an identity (fallback) is the authority: a candidate that
// canonicalizes to a DIFFERENT person is skipped instead of minting a second
// pool. Discovery candidates whose Entity is really the contextual scene query
// ("tripoli-libya-1930s-colonial-streets-...") therefore cannot create a
// person:<scene-phrase> row.
func personCatalogCandidateIdentity(candidate scriptpkg.SegmentAssetCandidate, fallback entitycatalog.PersonIdentity, graph *entitycatalog.PersonAliasGraph) (entitycatalog.PersonIdentity, bool) {
	suppliedID := strings.ToLower(strings.TrimSpace(candidate.EntityID))
	switch {
	case strings.HasPrefix(suppliedID, "person:"):
		// The candidate already carries the canonical PERSON identity.
	case suppliedID == "" || strings.HasPrefix(suppliedID, "entity:"):
		// "entity:<provenance-slug>" is the neutral segment stamp, not a type
		// claim; the surface name must still resolve as a PERSON below.
		suppliedID = ""
	default:
		// org:/place:/object:/brand:/local:/... — never promote to PERSON.
		return entitycatalog.PersonIdentity{}, false
	}
	name := strings.TrimSpace(candidate.Entity)
	if name == "" {
		name = strings.TrimSpace(fallback.CanonicalName)
	}
	if name == "" {
		return entitycatalog.PersonIdentity{}, false
	}
	identity, err := entitycatalog.CanonicalizePersonIdentityWithAliases(name, suppliedID, graph)
	if err != nil || identity.Unresolved || identity.CanonicalEntityID == "" {
		return entitycatalog.PersonIdentity{}, false
	}
	if fallback.CanonicalEntityID != "" && identity.CanonicalEntityID != fallback.CanonicalEntityID {
		return entitycatalog.PersonIdentity{}, false
	}
	// An alias surface resolves to the established identity. Keep the identity's
	// canonical display name so the catalog entity does not take the alias text
	// ("Augias") as the canonical name of "Corrado Augias".
	if fallback.CanonicalEntityID != "" && strings.TrimSpace(fallback.CanonicalName) != "" &&
		identity.CanonicalEntityID == fallback.CanonicalEntityID {
		identity.CanonicalName = fallback.CanonicalName
	}
	return identity, true
}

// persistPersonImageVisualVerdicts writes the final pixel-level decision back
// to the same PERSON catalog row that owns provider identity and URL quality.
// This prevents an earlier metadata-only promotion from remaining usable after
// visual verification rejected the pixels.
//
// identity is the PERSON the caller established for this batch (the query class
// was decided upstream); it is REQUIRED. Without it a contextual query would
// reach CanonicalizePersonName and be persisted as a fake person pool — the
// exact contamination this boundary exists to stop.
func persistPersonImageVisualVerdicts(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, graph *entitycatalog.PersonAliasGraph, results []scriptpkg.SegmentAssetCandidate) error {
	if repo == nil || strings.TrimSpace(identity.CanonicalEntityID) == "" {
		return nil
	}
	for rank, result := range results {
		if !scriptpkg.IsInternetImageSourceProvider(result.Provider) ||
			strings.TrimSpace(result.Entity) == "" || strings.TrimSpace(result.SourceURL) == "" ||
			(result.SemanticStatus != entitycatalog.CandidateSemanticAccepted && result.SemanticStatus != entitycatalog.CandidateSemanticRejected) {
			continue
		}
		if result.SemanticStatus == entitycatalog.CandidateSemanticAccepted && !imagegate.ReusablePersonImageVerdict(result) {
			continue
		}
		identity, ok := personCatalogCandidateIdentity(result, identity, graph)
		if !ok {
			continue
		}
		if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
			CanonicalEntityID: identity.CanonicalEntityID,
			EntityType:        entitycatalog.EntityTypePerson,
			CanonicalName:     identity.CanonicalName,
		}); err != nil {
			return err
		}
		if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          entityImageCatalogProviderFor(result),
			Rank:              rank + 1,
			SourceURL:         result.SourceURL,
			SourcePageURL:     result.SourcePageURL,
			ThumbnailURL:      result.PreviewURL,
			Width:             result.Width,
			Height:            result.Height,
			Status:            entitycatalog.CandidateStatusFresh,
			SemanticStatus:    result.SemanticStatus,
			SemanticScore:     result.SemanticScore,
			ContentSHA256:     entityImageCatalogContentSHA256(result.ContentSHA256),
			VerifierVersion:   entitycatalog.CandidateVerifierVersion,
			TechnicalScore:    result.TechnicalQualityScore,
			QualityReason:     result.QualityReason,
		}); err != nil {
			return err
		}
	}
	return nil
}

// hydratePersonImageVisualVerdicts stamps durable pixel verdicts from the
// catalog onto discovered candidates that carry none. The segment/entity
// discovery caches store PRE-verification candidates, so a cache hit used to
// download the pixels and spend a VLM call again for URLs the catalog has
// already certified. Only a definitive, non-transient verdict is stamped:
// imagegate.ReusablePersonImageVerdict is the single decision surface for that, so an
// outage-shaped rejection stays retryable exactly like inside the scorer. Any
// catalog error degrades to no hydration (the candidate is simply re-verified
// later) — the catalog is a replay projection, never truth.
func hydratePersonImageVisualVerdicts(ctx context.Context, repo entitycatalog.Repository, graph *entitycatalog.PersonAliasGraph, candidates []scriptpkg.SegmentAssetCandidate) {
	if repo == nil || len(candidates) == 0 {
		return
	}
	type pendingIndex struct {
		index int
		url   string
	}
	byEntity := make(map[string][]pendingIndex)
	for i := range candidates {
		candidate := &candidates[i]
		if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) ||
			strings.TrimSpace(candidate.Entity) == "" || strings.TrimSpace(candidate.SourceURL) == "" {
			continue
		}
		if imagegate.ReusablePersonImageVerdict(*candidate) {
			continue
		}
		identity, ok := personCatalogCandidateIdentity(*candidate, entitycatalog.PersonIdentity{}, graph)
		if !ok {
			continue
		}
		byEntity[identity.CanonicalEntityID] = append(byEntity[identity.CanonicalEntityID], pendingIndex{
			index: i, url: strings.ToLower(strings.TrimSpace(candidate.SourceURL)),
		})
	}
	for entityID, pending := range byEntity {
		rows, err := repo.ListCandidates(ctx, entityID, 1000)
		if err != nil {
			continue
		}
		verdicts := make(map[string]entitycatalog.Candidate, len(rows))
		for _, row := range rows {
			verdicts[strings.ToLower(strings.TrimSpace(row.SourceURL))] = row
		}
		for _, p := range pending {
			row, ok := verdicts[p.url]
			if !ok || !entitycatalog.IsCurrentCandidateVerifierVersion(row.VerifierVersion) {
				continue
			}
			verdict := scriptpkg.SegmentAssetCandidate{
				SemanticStatus: row.SemanticStatus,
				SemanticScore:  row.SemanticScore,
				QualityReason:  row.QualityReason,
			}
			if !imagegate.ReusablePersonImageVerdict(verdict) {
				continue
			}
			candidate := &candidates[p.index]
			candidate.SemanticStatus = row.SemanticStatus
			candidate.SemanticScore = row.SemanticScore
			if strings.TrimSpace(row.QualityReason) != "" {
				candidate.QualityReason = row.QualityReason
			}
		}
	}
}

// nowUTC is a small seam for catalog refresh timestamps while keeping the
// persistence adapter responsible for the actual SQL representation.
func nowUTC() time.Time { return time.Now().UTC() }

func entityImageCatalogCandidateID(ctx context.Context, repo entitycatalog.Repository, candidate scriptpkg.SegmentAssetCandidate) (int64, error) {
	return entityImageCatalogCandidateIDWithAliases(ctx, repo, nil, candidate)
}

// entityImageCatalogCandidateIDWithAliases is the alias-aware catalog row
// lookup. A segment's PersonAliasGraph must be applied here for the same reason
// it is applied on write: an alias ("Augias") and its canonical name
// ("Corrado Augias") must resolve to the SAME candidate row.
func entityImageCatalogCandidateIDWithAliases(ctx context.Context, repo entitycatalog.Repository, graph *entitycatalog.PersonAliasGraph, candidate scriptpkg.SegmentAssetCandidate) (int64, error) {
	if repo == nil || !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) {
		return 0, nil
	}
	if rawID := strings.TrimPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-"); rawID != strings.TrimSpace(candidate.AssetID) {
		if candidateID, err := strconv.ParseInt(rawID, 10, 64); err == nil && candidateID > 0 {
			return candidateID, nil
		}
	}
	if strings.TrimSpace(candidate.Entity) == "" || strings.TrimSpace(candidate.SourceURL) == "" {
		return 0, nil
	}
	identity, ok := personCatalogCandidateIdentity(candidate, entitycatalog.PersonIdentity{}, graph)
	if !ok {
		return 0, nil
	}
	rows, err := repo.ListCandidates(ctx, identity.CanonicalEntityID, 100)
	if err != nil {
		if errors.Is(err, entitycatalog.ErrEntityNotFound) {
			return 0, nil
		}
		return 0, err
	}
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.SourceURL), strings.TrimSpace(candidate.SourceURL)) {
			return row.ID, nil
		}
	}
	return 0, nil
}

func setEntityImageCatalogCandidateStatus(ctx context.Context, repo entitycatalog.Repository, candidate scriptpkg.SegmentAssetCandidate, status string) error {
	candidateID, err := entityImageCatalogCandidateID(ctx, repo, candidate)
	if err != nil || candidateID == 0 {
		return err
	}
	return repo.SetCandidateStatus(ctx, candidateID, status)
}

func applyEntityImageCatalogMaterialization(candidate scriptpkg.SegmentAssetCandidate, materialization *entitycatalog.Materialization, contentStore EntityImageContentStore) (scriptpkg.SegmentAssetCandidate, bool) {
	if materialization == nil || materialization.Status != entitycatalog.MaterializationStatusMaterialized ||
		strings.TrimSpace(materialization.AssetID) == "" || strings.TrimSpace(materialization.DriveLink) == "" ||
		strings.TrimSpace(materialization.ContentSHA256) == "" {
		return candidate, false
	}
	candidate.AssetID = materialization.AssetID
	candidate.DriveLink = materialization.DriveLink
	candidate.ContentSHA256 = materialization.ContentSHA256
	// The catalog row deliberately carries no local path: a path is runtime
	// state, and injecting a stale one here is what let a media row disagree
	// with its own content address. The bytes are located from the content
	// address instead: when the CAS on THIS host holds the object, the
	// candidate carries its canonical path, so reuse does not have to
	// re-download a copy that is already local. A miss leaves the path empty
	// and the normal materializer falls back to the Drive copy.
	candidate.LocalPath = resolveMaterializedImageLocalPath(contentStore, materialization.ContentSHA256)
	candidate.RightsStatus = "unknown_allowed"
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = "indexed"
	return candidate, true
}

// EntityImageContentStore is the READ side of the content-addressed store: it
// answers "where do the bytes with this address live on this host?". It is the
// shape of *cas.Store.LocalPath; the adapters package depends on the port, not
// on the storage implementation, so a composition without a data dir simply
// supplies nothing and every caller keeps working.
type EntityImageContentStore interface {
	LocalPath(contentSHA256 string) (string, error)
}

// resolveMaterializedImageLocalPath returns the CAS path of an already
// published image, or "" when the store is absent, the address is not a real
// content address, or the object is not on this host.
//
// A catalog row carries a content address, not a path: a path is runtime state
// and a remembered one can point at another machine's disk or at bytes that
// were deleted. The address is the durable fact, so the path is DERIVED from it
// here and only returned when the object is actually present and non-empty. A
// miss is absence, never an error: the caller falls back to the Drive copy.
func resolveMaterializedImageLocalPath(store EntityImageContentStore, contentSHA256 string) string {
	sha := entityImageCatalogContentSHA256(contentSHA256)
	if store == nil || sha == "" {
		return ""
	}
	path, err := store.LocalPath(sha)
	if err != nil {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	return path
}

const (
	minimumPersonImageLongSide = entitycatalog.CatalogImageMinLongSide
	minimumPersonImagePixels   = entitycatalog.CatalogImageMinPixels
	minimumQualityScore        = 0.50
)

// filterPersonEntityImageCandidates is the semantic/technical gate for
// durable PERSON catalog promotion. A technically valid image is not enough:

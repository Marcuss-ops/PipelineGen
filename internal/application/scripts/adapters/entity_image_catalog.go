package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const entityImageCatalogProvider = "duckduckgo"

// entityImageCatalogPool is the durable candidate pool plus its usable count.
// Broken and retired URLs are never returned as fallback candidates.
type entityImageCatalogPool struct {
	Candidates  []scriptpkg.SegmentAssetCandidate
	UsableCount int
	KnownCount  int
	Sufficient  bool
}

func entityImageCatalogPoolTarget(limit int) int {
	if limit <= 1 {
		return 1
	}
	// Keep a small reserve below the provider query limit. This matches the
	// intended policy: eight healthy URLs out of a ten-entry pool are enough,
	// while three healthy URLs trigger a refresh but remain usable as fallback.
	if target := limit - 2; target > 0 {
		return target
	}
	return 1
}

// entityImageCatalogCandidates converts durable catalog rows into the normal
// VidRush candidate shape. A catalog hit remains a discovery result unless it
// also has a successful materialization, in which case the common materializer
// sees the existing Drive/hash state and does not download or upload again.
func entityImageCatalogCandidates(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, limit int) (entityImageCatalogPool, error) {
	if repo == nil || identity.CanonicalEntityID == "" {
		return entityImageCatalogPool{}, nil
	}
	rows, err := repo.ListCandidates(ctx, identity.CanonicalEntityID, 100)
	if err != nil {
		if errors.Is(err, entitycatalog.ErrEntityNotFound) {
			return entityImageCatalogPool{}, nil
		}
		return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: list %s: %w", identity.CanonicalEntityID, err)
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(rows))
	known := 0
	for _, row := range rows {
		known++
		assessment := entitycatalog.AssessCandidateState(nowUTC(), row)
		if assessment.State != row.Status && !row.LastSeenAt.IsZero() &&
			row.Status != entitycatalog.CandidateStatusBroken && row.Status != entitycatalog.CandidateStatusRetired {
			if err := repo.SetCandidateStatus(ctx, row.ID, assessment.State); err != nil {
				return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: persist state %d: %w", row.ID, err)
			}
			row.Status = assessment.State
		}
		if !entitycatalog.IsUsableCandidateStatus(assessment.State) || !entitycatalog.IsUsableCandidateQuality(row) {
			continue
		}
		if strings.TrimSpace(row.SourceURL) == "" {
			continue
		}
		candidate := scriptpkg.SegmentAssetCandidate{
			AssetID:               fmt.Sprintf("entity-image-%d", row.ID),
			Provider:              scriptpkg.VidRushProviderInternetImages,
			Query:                 identity.CanonicalName,
			Entity:                identity.CanonicalName,
			Score:                 1.0 / float64(maxInt(1, row.Rank)),
			SourceURL:             row.SourceURL,
			PreviewURL:            row.ThumbnailURL,
			Width:                 row.Width,
			Height:                row.Height,
			SemanticStatus:        row.SemanticStatus,
			SemanticScore:         row.SemanticScore,
			TechnicalQualityScore: row.TechnicalScore,
			QualityReason:         row.QualityReason,
			RightsStatus:          "unknown",
			SelectionReason:       "persistent PERSON entity image catalog candidate",
		}
		materialization, matErr := repo.GetMaterialization(ctx, row.ID)
		if matErr != nil {
			return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: get materialization %d: %w", row.ID, matErr)
		}
		if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization); ok {
			candidate = hydrated
		}
		out = append(out, candidate)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	target := entityImageCatalogPoolTarget(limit)
	if known > 0 && known < target {
		target = known
	}
	return entityImageCatalogPool{Candidates: out, UsableCount: len(out), KnownCount: known, Sufficient: known > 0 && len(out) >= target}, nil
}

// persistEntityImageCatalogCandidates promotes a successful PERSON provider
// search into the durable catalog. It stores URLs only; acquisition remains
// owned by VidRushMaterializationProcessor and its common finalizer.
func persistEntityImageCatalogCandidates(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate) error {
	if repo == nil || identity.CanonicalEntityID == "" {
		return nil
	}
	results = filterPersonEntityImageCandidates(identity, results)
	if err := repo.UpsertEntity(ctx, entitycatalog.Entity{
		CanonicalEntityID: identity.CanonicalEntityID,
		EntityType:        entitycatalog.EntityTypePerson,
		CanonicalName:     identity.CanonicalName,
	}); err != nil {
		return err
	}
	rank := 0
	for _, result := range results {
		if !strings.EqualFold(strings.TrimSpace(result.Provider), scriptpkg.VidRushProviderInternetImages) || strings.TrimSpace(result.SourceURL) == "" {
			continue
		}
		rank++
		if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
			CanonicalEntityID: identity.CanonicalEntityID,
			Provider:          entityImageCatalogProvider,
			Rank:              rank,
			SourceURL:         result.SourceURL,
			ThumbnailURL:      result.PreviewURL,
			Width:             result.Width,
			Height:            result.Height,
			Status:            entitycatalog.CandidateStatusFresh,
			SemanticStatus:    result.SemanticStatus,
			SemanticScore:     result.SemanticScore,
			TechnicalScore:    result.TechnicalQualityScore,
			QualityReason:     result.QualityReason,
		}); err != nil {
			return err
		}
	}
	return repo.SetRefreshState(ctx, identity.CanonicalEntityID, entitycatalog.RefreshStatusSucceeded, nowUTC(), "")
}

// nowUTC is a small seam for catalog refresh timestamps while keeping the
// persistence adapter responsible for the actual SQL representation.
func nowUTC() time.Time { return time.Now().UTC() }

func entityImageCatalogCandidateID(ctx context.Context, repo entitycatalog.Repository, candidate scriptpkg.SegmentAssetCandidate) (int64, error) {
	if repo == nil || !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
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
	identity, err := entitycatalog.CanonicalizePersonName(candidate.Entity)
	if err != nil {
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

func applyEntityImageCatalogMaterialization(candidate scriptpkg.SegmentAssetCandidate, materialization *entitycatalog.Materialization) (scriptpkg.SegmentAssetCandidate, bool) {
	if materialization == nil || materialization.Status != entitycatalog.MaterializationStatusMaterialized ||
		strings.TrimSpace(materialization.AssetID) == "" || strings.TrimSpace(materialization.DriveLink) == "" ||
		strings.TrimSpace(materialization.LegacyFileMD5) == "" {
		return candidate, false
	}
	candidate.AssetID = materialization.AssetID
	candidate.DriveLink = materialization.DriveLink
	candidate.LegacyFileMD5 = materialization.LegacyFileMD5
	candidate.LocalPath = materialization.LocalPath
	candidate.RightsStatus = "unknown_allowed"
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = "indexed"
	return candidate, true
}

const (
	minimumPersonImageLongSide = 800
	minimumPersonImagePixels   = 400000
	minimumQualityScore        = 0.50
)

// filterPersonEntityImageCandidates is the semantic/technical gate for
// durable PERSON catalog promotion. A technically valid image is not enough:
// if the provider supplies an explicit entity, it must resolve to the exact
// canonical PERSON identity (so Michael B. Jordan cannot enter Michael
// Jordan's pool). When dimensions or quality scores are present, they must
// also meet the minimum retrieval quality.
func filterPersonEntityImageCandidates(identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	for _, result := range results {
		accepted, semanticScore, technicalScore, reason := evaluatePersonImageCandidate(identity, result)
		if !accepted {
			continue
		}
		result.SemanticStatus = entitycatalog.CandidateSemanticAccepted
		result.SemanticScore = semanticScore
		result.TechnicalQualityScore = technicalScore
		result.QualityReason = reason
		out = append(out, result)
	}
	return out
}

func evaluatePersonImageCandidate(identity entitycatalog.PersonIdentity, candidate scriptpkg.SegmentAssetCandidate) (bool, float64, float64, string) {
	expectedID := identity.CanonicalEntityID
	semanticScore := 0.5
	reason := "PERSON identity inferred from query; technical dimensions not supplied"

	if entity := strings.TrimSpace(candidate.Entity); entity != "" {
		cleanEntity := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(entity, "'s"), "’s"))
		actual, err := entitycatalog.CanonicalizePersonName(cleanEntity)
		if err != nil || actual.CanonicalEntityID != expectedID {
			return false, 0, 0, "candidate entity does not match requested PERSON identity"
		}
		semanticScore = 1
		reason = "explicit PERSON entity matches canonical identity"
	} else if query := strings.TrimSpace(candidate.Query); query != "" {
		if !personQueryContainsCanonicalName(query, identity.CanonicalName) {
			return false, 0, 0, "candidate query does not contain requested PERSON identity"
		}
		semanticScore = 0.85
		reason = "candidate query contains canonical PERSON identity"
	}

	technicalScore := 0.0
	if candidate.TechnicalQualityScore > 0 {
		if candidate.TechnicalQualityScore < minimumQualityScore {
			return false, semanticScore, candidate.TechnicalQualityScore, "technical quality score below minimum"
		}
		technicalScore = candidate.TechnicalQualityScore
	}
	if candidate.Width > 0 && candidate.Height > 0 {
		longSide := candidate.Width
		if candidate.Height > longSide {
			longSide = candidate.Height
		}
		if longSide < minimumPersonImageLongSide || candidate.Width*candidate.Height < minimumPersonImagePixels {
			return false, semanticScore, technicalScore, "image dimensions below PERSON catalog minimum"
		}
		if technicalScore == 0 {
			technicalScore = 1
		}
		reason += "; dimensions meet minimum"
	}
	return true, semanticScore, technicalScore, reason
}

func personQueryContainsCanonicalName(query, canonicalName string) bool {
	want := strings.Fields(normalizeEntityMatch(canonicalName))
	got := strings.Fields(normalizeEntityMatch(query))
	if len(want) == 0 || len(got) < len(want) {
		return false
	}
	for start := 0; start <= len(got)-len(want); start++ {
		match := true
		for i := range want {
			if strings.Trim(got[start+i], ".,;:!?\"'") != strings.Trim(want[i], ".,;:!?\"'") {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func normalizeInternetImageCatalogResults(results []scriptpkg.SegmentAssetCandidate, query string) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Provider) == "" {
			result.Provider = scriptpkg.VidRushProviderInternetImages
		}
		if strings.TrimSpace(result.Query) == "" {
			result.Query = query
		}
		out = append(out, result)
	}
	return out
}

func personCatalogIdentityForSegmentQuery(segment scriptpkg.VidRushSegmentResult, query string) (entitycatalog.PersonIdentity, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return entitycatalog.PersonIdentity{}, false, nil
	}
	if len(segment.Insights.ImageEntityCanonicalIDs) > 0 {
		for name, id := range segment.Insights.ImageEntityCanonicalIDs {
			if normalizeEntityMatch(name) != normalizeEntityMatch(query) || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "person:") {
				continue
			}
			identity, err := entitycatalog.CanonicalizePersonIdentity(query, id)
			return identity, err == nil, err
		}
	}
	for _, entity := range segment.Insights.Entities {
		if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson || normalizeEntityMatch(entity.Value) != normalizeEntityMatch(query) {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonName(entity.Value)
		return identity, err == nil, err
	}
	return entitycatalog.PersonIdentity{}, false, nil
}

func personCatalogIdentityForQuery(spec scriptpkg.SpecSceneOutput, idx sceneIdentityIndex, segment scriptpkg.VidRushSegmentResult, query string) (entitycatalog.PersonIdentity, bool, error) {
	best := idx.sceneFor(segment)
	if best == -1 || best >= len(spec.Scenes) || spec.Scenes[best].Annotations == nil {
		return entitycatalog.PersonIdentity{}, false, nil
	}
	query = strings.TrimSpace(query)
	for _, entity := range spec.Scenes[best].Annotations.PrimaryEntities {
		if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson {
			continue
		}
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			name = strings.TrimSpace(entity.Text)
		}
		if name == "" || (normalizeEntityMatch(name) != normalizeEntityMatch(query) && normalizeEntityMatch(entity.Text) != normalizeEntityMatch(query)) {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonIdentity(name, entity.CanonicalEntityID)
		if err != nil {
			return entitycatalog.PersonIdentity{}, false, err
		}
		return identity, true, nil
	}
	return entitycatalog.PersonIdentity{}, false, nil
}

package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const entityImageCatalogProvider = "duckduckgo"

const (
	entityImageCatalogPoolSize              = 10
	entityImageCatalogMaterializationCopies = 5
)

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
	// Keep a small reserve below the provider query limit. Eight healthy URLs
	// out of a ten-entry pool are enough; shorter but fully usable existing
	// pools remain valid fallback while the caller decides whether to refresh.
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
		if reason := personImageSourceIdentityFailure(identity.CanonicalName, row.SourceURL, row.SourcePageURL); reason != "" {
			if row.SemanticStatus != entitycatalog.CandidateSemanticRejected || row.QualityReason != reason {
				if _, err := repo.UpsertCandidate(ctx, entitycatalog.Candidate{
					CanonicalEntityID: row.CanonicalEntityID,
					Provider:          row.Provider,
					Rank:              row.Rank,
					SourceURL:         row.SourceURL,
					SourcePageURL:     row.SourcePageURL,
					ThumbnailURL:      row.ThumbnailURL,
					Width:             row.Width,
					Height:            row.Height,
					Status:            row.Status,
					SemanticStatus:    entitycatalog.CandidateSemanticRejected,
					TechnicalScore:    row.TechnicalScore,
					QualityReason:     reason,
				}); err != nil {
					return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: reject unverified source identity for candidate %d: %w", row.ID, err)
				}
			}
			continue
		}
		assessment := entitycatalog.AssessCandidateState(nowUTC(), row)
		if assessment.State != row.Status && !row.LastSeenAt.IsZero() &&
			row.Status != entitycatalog.CandidateStatusBroken && row.Status != entitycatalog.CandidateStatusRetired {
			if err := repo.SetCandidateStatus(ctx, row.ID, assessment.State); err != nil {
				return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: persist state %d: %w", row.ID, err)
			}
			row.Status = assessment.State
		}
		recertifyLayout := personImageLayoutNeedsRecertification(row.QualityReason)
		if !entitycatalog.IsUsableCandidateStatus(assessment.State) || (!entitycatalog.IsUsableCandidateQuality(row) && !recertifyLayout) {
			continue
		}
		if strings.TrimSpace(row.SourceURL) == "" {
			continue
		}
		semanticStatus, semanticScore, qualityReason := row.SemanticStatus, row.SemanticScore, row.QualityReason
		if recertifyLayout {
			semanticStatus = entitycatalog.CandidateSemanticUnknown
			semanticScore = 0
			qualityReason = "pending visual recheck under dominant-subject policy"
		}
		candidate := scriptpkg.SegmentAssetCandidate{
			AssetID:               fmt.Sprintf("entity-image-%d", row.ID),
			Provider:              scriptpkg.VidRushProviderInternetImages,
			Query:                 identity.CanonicalName,
			Entity:                identity.CanonicalName,
			Score:                 1.0 / float64(max(1, row.Rank)),
			SourceURL:             row.SourceURL,
			SourcePageURL:         row.SourcePageURL,
			PreviewURL:            row.ThumbnailURL,
			Width:                 row.Width,
			Height:                row.Height,
			SemanticStatus:        semanticStatus,
			SemanticScore:         semanticScore,
			TechnicalQualityScore: row.TechnicalScore,
			QualityReason:         qualityReason,
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
		// Every usable row above costs one materialization lookup. Once the
		// requested pool size is satisfied there is nothing left to hydrate:
		// stopping here keeps a warm catalog replay at `limit` lookups instead
		// of one per row in the 100-row scan window.
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	usableCount := len(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	target := entityImageCatalogPoolTarget(limit)
	if known > 0 && known < target {
		target = known
	}
	return entityImageCatalogPool{Candidates: out, UsableCount: usableCount, KnownCount: known, Sufficient: known > 0 && usableCount >= target}, nil
}

// durableEntityImagePoolForSegment loads the durable per-person catalog pool
// for every PERSON identity this segment queries, so the rows go through
// Acquire → Verify → Finalize in-run. Without this the pool is only appended
// after materialization, when un-acquired rows can no longer be downloaded:
// a person with one surviving Drive asset and dozens of verified-but-never-
// fetched editorial URLs then rebinds the same photo on every scene and
// per-scene variety is impossible. Callers append the result with
// AppendProviderCandidatesUnique, which collapses same-URL duplicates.
func durableEntityImagePoolForSegment(ctx context.Context, repo entitycatalog.Repository, segment scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	if repo == nil {
		return nil
	}
	seenQueries := make(map[string]struct{})
	seenIdentities := make(map[string]struct{})
	var out []scriptpkg.SegmentAssetCandidate
	for _, candidate := range segment.Assets.Candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		query := strings.TrimSpace(candidate.Query)
		if query == "" {
			query = strings.TrimSpace(candidate.Entity)
		}
		if query == "" {
			continue
		}
		lowered := strings.ToLower(query)
		if _, dup := seenQueries[lowered]; dup {
			continue
		}
		seenQueries[lowered] = struct{}{}
		identity, ok, err := personCatalogIdentityForSegmentQuery(segment, query)
		if err != nil || !ok {
			continue
		}
		if _, dup := seenIdentities[identity.CanonicalEntityID]; dup {
			continue
		}
		seenIdentities[identity.CanonicalEntityID] = struct{}{}
		pool, err := entityImageCatalogCandidates(ctx, repo, identity, entityImageCatalogPoolSize)
		if err != nil || len(pool.Candidates) == 0 {
			continue
		}
		out = append(out, pool.Candidates...)
	}
	return out
}

// personImageLayoutNeedsRecertification identifies the prior visual-layout
// verdict that incorrectly required a tight headshot and rejected contextual
// editorial photos. It does not reopen collage, identity, or technical
// rejections; the current scorer must still approve the pixels.
func personImageLayoutNeedsRecertification(reason string) bool {
	return strings.TrimSpace(reason) == "Ollama visual verification did not find one clear person"
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
			SourcePageURL:     result.SourcePageURL,
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

// persistPersonImageVisualVerdicts writes the final pixel-level decision back
// to the same PERSON catalog row that owns provider identity and URL quality.
// This prevents an earlier metadata-only promotion from remaining usable after
// visual verification rejected the pixels.
func persistPersonImageVisualVerdicts(ctx context.Context, repo entitycatalog.Repository, results []scriptpkg.SegmentAssetCandidate) error {
	if repo == nil {
		return nil
	}
	for rank, result := range results {
		if !strings.EqualFold(strings.TrimSpace(result.Provider), scriptpkg.VidRushProviderInternetImages) ||
			strings.TrimSpace(result.Entity) == "" || strings.TrimSpace(result.SourceURL) == "" ||
			(result.SemanticStatus != entitycatalog.CandidateSemanticAccepted && result.SemanticStatus != entitycatalog.CandidateSemanticRejected) {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonName(result.Entity)
		if err != nil {
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
			Provider:          entityImageCatalogProvider,
			Rank:              rank + 1,
			SourceURL:         result.SourceURL,
			SourcePageURL:     result.SourcePageURL,
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
	return nil
}

// hydratePersonImageVisualVerdicts stamps durable pixel verdicts from the
// catalog onto discovered candidates that carry none. The segment/entity
// discovery caches store PRE-verification candidates, so a cache hit used to
// download the pixels and spend a VLM call again for URLs the catalog has
// already certified. Only a definitive, non-transient verdict is stamped:
// ReusablePersonImageVerdict is the single decision surface for that, so an
// outage-shaped rejection stays retryable exactly like inside the scorer. Any
// catalog error degrades to no hydration (the candidate is simply re-verified
// later) — the catalog is a replay projection, never truth.
func hydratePersonImageVisualVerdicts(ctx context.Context, repo entitycatalog.Repository, candidates []scriptpkg.SegmentAssetCandidate) {
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
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) ||
			strings.TrimSpace(candidate.Entity) == "" || strings.TrimSpace(candidate.SourceURL) == "" {
			continue
		}
		if ReusablePersonImageVerdict(*candidate) {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonName(candidate.Entity)
		if err != nil {
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
			if !ok {
				continue
			}
			verdict := scriptpkg.SegmentAssetCandidate{
				SemanticStatus: row.SemanticStatus,
				SemanticScore:  row.SemanticScore,
				QualityReason:  row.QualityReason,
			}
			if !ReusablePersonImageVerdict(verdict) {
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
		strings.TrimSpace(materialization.ContentSHA256) == "" {
		return candidate, false
	}
	candidate.AssetID = materialization.AssetID
	candidate.DriveLink = materialization.DriveLink
	candidate.ContentSHA256 = materialization.ContentSHA256
	// The catalog row deliberately carries no local path: a path is runtime
	// state, and injecting a stale one here is what let a media row disagree
	// with its own content address. Bytes are located by the canonical
	// materializer from the content address, not remembered in a database.
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

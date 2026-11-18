package adapters

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// entityImageCatalogProviderFor is the one provider-identity rule for the
// durable catalog. The concrete retrieval backend is the provenance; the
// lifecycle bucket (internet_images) is only a fallback when no backend was
// recorded. A single helper prevents discovery and visual-verdict writes from
// stamping different providers for the same candidate.
func entityImageCatalogProviderFor(candidate scriptpkg.SegmentAssetCandidate) string {
	provider := strings.ToLower(strings.TrimSpace(candidate.RetrievalProvider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(candidate.Provider))
	}
	if provider == "" {
		return scriptpkg.VidRushProviderInternetImages
	}
	return provider
}

// entityImageCatalogContentSHA256 keeps only a real 64-hex content address.
// Test doubles and legacy call sites sometimes carry short placeholder hashes;
// those are absence of content identity, not a digest, and must not reach the
// catalog's verdict boundary.
func entityImageCatalogContentSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

const (
	entityImageCatalogPoolSize         = 10
	entityImageCatalogMinimumReusePool = 4
	// Ask the provider for more rows than the durable pool exposes. The gate
	// rejects most web results before vision, so a ten-row request left fewer
	// than ten usable portraits; twenty rows keep the same bounded pool while
	// giving the identity/quality filter enough candidates to survive.
	entityImageCatalogDiscoveryLimit = 20
	// Keep one alternate beyond the current scene's image demand. The durable
	// discovery pool remains larger; downloading five copies per requested
	// image on every video run made catalog warming dominate the hot path.
	entityImageCatalogMaterializationCopies = 2
)

// entityImageCatalogPool is the durable candidate pool plus its usable count.
// Broken and retired URLs are never returned as fallback candidates.
// EntityImageCatalogDecision is one row-level trace. Aggregate metrics say
// how many candidates were rejected; this ledger says which query produced a
// candidate, which gate decided it, and whether it reached the pool.
type EntityImageCatalogDecision struct {
	Query           string
	CandidateID     int64
	SourceURL       string
	Gate            string
	Verdict         string
	Selection       string
	Materialization string
}

type entityImageCatalogPool struct {
	Candidates  []scriptpkg.SegmentAssetCandidate
	Decisions   []EntityImageCatalogDecision
	UsableCount int
	KnownCount  int
	Sufficient  bool
}

func entityImageCatalogPoolTarget(limit int) int {
	if limit <= 1 {
		return 1
	}
	// Keep the reusable-pool target below the provider query limit, but do not
	// force a fresh provider/VLM pass every run once a modest verified pool can
	// already serve normal per-scene demand. Larger pools still add choice when
	// available.
	if target := limit - 2; target > 0 {
		return min(target, entityImageCatalogMinimumReusePool)
	}
	return 1
}

// entityImageCatalogPoolMeetsDemand avoids another provider call when the
// catalog already has enough visually verified candidates for this scene's
// configured image count. The larger reusable-pool target remains useful for
// jobs that do not declare a per-scene demand.
func entityImageCatalogPoolMeetsDemand(pool entityImageCatalogPool, imagesPerScene int) bool {
	return pool.Sufficient || (imagesPerScene > 0 && pool.UsableCount >= imagesPerScene)
}

// entityImageCatalogCandidates converts durable catalog rows into the normal
// VidRush candidate shape. A catalog hit remains a discovery result unless it
// also has a successful materialization, in which case the common materializer
// sees the existing Drive/hash state and does not download or upload again.
func entityImageCatalogCandidates(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, limit int) (entityImageCatalogPool, error) {
	if repo == nil || identity.CanonicalEntityID == "" {
		return entityImageCatalogPool{}, nil
	}
	var rows []entitycatalog.Candidate
	if pager, ok := repo.(entitycatalog.CandidatePageReader); ok {
		const pageSize = 100
		for offset := 0; ; offset += pageSize {
			page, err := pager.ListCandidatesPage(ctx, identity.CanonicalEntityID, offset, pageSize)
			if err != nil {
				if errors.Is(err, entitycatalog.ErrEntityNotFound) {
					return entityImageCatalogPool{}, nil
				}
				return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: page %s at %d: %w", identity.CanonicalEntityID, offset, err)
			}
			rows = append(rows, page...)
			if len(page) < pageSize {
				break
			}
		}
	} else {
		var err error
		rows, err = repo.ListCandidates(ctx, identity.CanonicalEntityID, 100)
		if err != nil {
			if errors.Is(err, entitycatalog.ErrEntityNotFound) {
				return entityImageCatalogPool{}, nil
			}
			return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: list %s: %w", identity.CanonicalEntityID, err)
		}
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(rows))
	decisions := make([]EntityImageCatalogDecision, 0, len(rows))
	known := 0
	materializations := make(map[int64]*entitycatalog.Materialization, len(rows))
	canBatchMaterials := false
	if batch, ok := repo.(entitycatalog.MaterializationBatchReader); ok {
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		batchMaterials, batchErr := batch.GetMaterializations(ctx, ids)
		if batchErr != nil {
			return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: batch materializations: %w", batchErr)
		}
		materializations = batchMaterials
		canBatchMaterials = true
	}
	for _, row := range rows {
		known++
		decision := EntityImageCatalogDecision{Query: identity.CanonicalName, CandidateID: row.ID, SourceURL: row.SourceURL, Verdict: row.SemanticStatus, Materialization: "missing"}
		if reason := personImageSourceIdentityFailure(identity.CanonicalName, row.SourceURL, row.SourcePageURL); reason != "" {
			// Catalog reads are pure. Rejection persistence belongs to the
			// refresh/recertification command, never to a lookup.
			decision.Gate, decision.Selection = "identity", "rejected"
			decisions = append(decisions, decision)
			continue
		}
		// A broken source URL does not invalidate the durable image already
		// copied to Drive. Reuse a fully materialized, visually accepted asset
		// without asking the provider to serve that URL again.
		materialization, batched := materializations[row.ID]
		if !batched && !canBatchMaterials {
			var matErr error
			materialization, matErr = repo.GetMaterialization(ctx, row.ID)
			if matErr != nil {
				return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: get materialization %d: %w", row.ID, matErr)
			}
		}
		_, durableImage := applyEntityImageCatalogMaterialization(scriptpkg.SegmentAssetCandidate{}, materialization)
		durableImage = durableImage && row.SemanticStatus == entitycatalog.CandidateSemanticAccepted &&
			entitycatalog.IsUsableCandidateQuality(row)
		if durableImage {
			decision.Materialization = "materialized"
		}

		assessment := entitycatalog.AssessCandidateState(nowUTC(), row)
		if assessment.State != row.Status && assessment.State != entitycatalog.CandidateStatusUnknown &&
			!row.LastSeenAt.IsZero() &&
			row.Status != entitycatalog.CandidateStatusBroken && row.Status != entitycatalog.CandidateStatusRetired {
			if err := repo.SetCandidateStatus(ctx, row.ID, assessment.State); err != nil {
				return entityImageCatalogPool{}, fmt.Errorf("entity image catalog: persist state %d: %w", row.ID, err)
			}
			row.Status = assessment.State
		}
		recertifyLayout := personImageLayoutNeedsRecertification(row.QualityReason)
		// A verified materialization may keep a broken remote URL renderable.
		// It never resurrects a retired or unrecognized row: those are policy
		// decisions that an existing copy must not override.
		durableBypass := durableImage && assessment.State == entitycatalog.CandidateStatusBroken
		if (!entitycatalog.IsUsableCandidateStatus(assessment.State) && !durableBypass) ||
			(!entitycatalog.IsUsableCandidateQuality(row) && !recertifyLayout && !durableBypass) {
			decision.Gate, decision.Selection = "lifecycle_quality", "rejected"
			decisions = append(decisions, decision)
			continue
		}
		if strings.TrimSpace(row.SourceURL) == "" {
			decision.Gate, decision.Selection = "source_url", "rejected"
			decisions = append(decisions, decision)
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
			RetrievalProvider:     row.Provider,
			Query:                 identity.CanonicalName,
			Entity:                identity.CanonicalName,
			Score:                 1.0 / float64(max(1, row.Rank)),
			SourceURL:             row.SourceURL,
			SourcePageURL:         row.SourcePageURL,
			PreviewURL:            row.ThumbnailURL,
			Width:                 row.Width,
			Height:                row.Height,
			ContentSHA256:         row.ContentSHA256,
			VerifierVersion:       row.VerifierVersion,
			SemanticStatus:        semanticStatus,
			SemanticScore:         semanticScore,
			TechnicalQualityScore: row.TechnicalScore,
			QualityReason:         qualityReason,
			RightsStatus:          "unknown",
			SelectionReason:       "persistent PERSON entity image catalog candidate",
		}
		if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization); ok {
			candidate = hydrated
		}
		out = append(out, candidate)
		decision.Gate, decision.Selection = "quality", "pool"
		decisions = append(decisions, decision)
		// Every usable row above costs one materialization lookup. Once the
		// requested pool size is satisfied there is nothing left to hydrate:
		// stopping here keeps a warm catalog replay at `limit` lookups instead
		// of one per row in the 100-row scan window.
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	// `out` also carries rows the selector cannot use yet (for example a
	// layout-recertification row whose verdict is deliberately reset to
	// unknown). Counting those as usable made poolMeetsDemand skip the refresh
	// and then selection dropped them, leaving the scene with no image at all.
	// Supply means an accepted verdict; unknown remains a retry state.
	usableCount := 0
	for _, candidate := range out {
		if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticAccepted) {
			usableCount++
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	target := entityImageCatalogPoolTarget(limit)
	if known > 0 && known < target {
		target = known
	}
	return entityImageCatalogPool{Candidates: out, Decisions: decisions, UsableCount: usableCount, KnownCount: known, Sufficient: known > 0 && usableCount >= target}, nil
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
	aliasGraph := segmentPersonAliasGraph(segment)
	seenQueries := make(map[string]struct{})
	seenIdentities := make(map[string]struct{})
	var out []scriptpkg.SegmentAssetCandidate
	appendIdentityPool := func(identity entitycatalog.PersonIdentity) {
		if identity.CanonicalEntityID == "" {
			return
		}
		if _, dup := seenIdentities[identity.CanonicalEntityID]; dup {
			return
		}
		seenIdentities[identity.CanonicalEntityID] = struct{}{}
		pool, err := entityImageCatalogCandidates(ctx, repo, identity, entityImageCatalogPoolSize)
		if err == nil && len(pool.Candidates) > 0 {
			out = append(out, pool.Candidates...)
		}
	}
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
		appendIdentityPool(identity)
	}
	// Incremental materialization can receive only a contextual query result
	// (or no provider candidate at all). Seed the durable pool from the scene's
	// typed PERSON entities too, so a stock-bound scene can reuse its accepted
	// catalog images without depending on the batch-only post-materialization
	// projection.
	for _, entity := range segment.Insights.Entities {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonNameWithAliases(entity.Value, aliasGraph)
		if err == nil {
			appendIdentityPool(identity)
		}
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

// persistPersonImageVisualVerdicts writes the final pixel-level decision back
// to the same PERSON catalog row that owns provider identity and URL quality.
// This prevents an earlier metadata-only promotion from remaining usable after
// visual verification rejected the pixels.
func persistPersonImageVisualVerdicts(ctx context.Context, repo entitycatalog.Repository, results []scriptpkg.SegmentAssetCandidate) error {
	if repo == nil {
		return nil
	}
	for rank, result := range results {
		if !scriptpkg.IsInternetImageSourceProvider(result.Provider) ||
			strings.TrimSpace(result.Entity) == "" || strings.TrimSpace(result.SourceURL) == "" ||
			(result.SemanticStatus != entitycatalog.CandidateSemanticAccepted && result.SemanticStatus != entitycatalog.CandidateSemanticRejected) {
			continue
		}
		if result.SemanticStatus == entitycatalog.CandidateSemanticAccepted && !ReusablePersonImageVerdict(result) {
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
		if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) ||
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
			if !ok || !entitycatalog.IsCurrentCandidateVerifierVersion(row.VerifierVersion) {
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
	minimumPersonImageLongSide = entitycatalog.CatalogImageMinLongSide
	minimumPersonImagePixels   = entitycatalog.CatalogImageMinPixels
	minimumQualityScore        = 0.50
)

// filterPersonEntityImageCandidates is the semantic/technical gate for
// durable PERSON catalog promotion. A technically valid image is not enough:

package vidrush

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"

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
// entityImageCatalogCandidates loads and projects the durable catalog pool.
// Row loading and pool construction remain in the phase helper.
func entityImageCatalogCandidates(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, limit int, contentStore EntityImageContentStore) (entityImageCatalogPool, error) {
	return entityImageCatalogCandidatesPhase(ctx, repo, identity, limit, contentStore)
}

func entityImageCatalogCandidatesPhase(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, limit int, contentStore EntityImageContentStore) (entityImageCatalogPool, error) {
	if repo == nil || identity.CanonicalEntityID == "" {
		return entityImageCatalogPool{}, nil
	}
	rows, err := loadEntityImageCatalogRows(ctx, repo, identity.CanonicalEntityID)
	if err != nil || rows == nil {
		return entityImageCatalogPool{}, err
	}
	// Repository order is rank-first, but a highly ranked row that cannot be
	// reused as-is must not consume the bounded pool ahead of one that can.
	// Sorting on the accepted bit alone was not enough: an accepted row queued
	// for layout recertification is reset to UNKNOWN below and still holds a
	// slot, so a rank-first dirty head could leave usableCount at 0 while
	// verified materialized portraits waited further down the ranking. Sort on
	// the same predicate the loop uses to admit a row.
	sort.SliceStable(rows, func(i, j int) bool {
		iUsable := entitycatalog.IsUsableCandidateQuality(rows[i])
		jUsable := entitycatalog.IsUsableCandidateQuality(rows[j])
		return iUsable && !jUsable
	})
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(rows))
	decisions := make([]EntityImageCatalogDecision, 0, len(rows))
	known := len(rows)
	materializations, canBatchMaterials, err := loadEntityImageMaterializations(ctx, repo, rows)
	if err != nil {
		return entityImageCatalogPool{}, err
	}
	for _, row := range rows {
		candidate, decision, include, rowErr := projectEntityImageCatalogCandidate(ctx, repo, identity, row, materializations, canBatchMaterials, contentStore)
		if rowErr != nil {
			return entityImageCatalogPool{}, rowErr
		}
		if decision.Selection != "" {
			decisions = append(decisions, decision)
		}
		if include {
			out = append(out, candidate)
		}
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
		if imagegate.CandidateEligibleForSlot(candidate, false) {
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

func loadEntityImageCatalogRows(ctx context.Context, repo entitycatalog.Repository, entityID string) ([]entitycatalog.Candidate, error) {
	var rows []entitycatalog.Candidate
	if pager, ok := repo.(entitycatalog.CandidatePageReader); ok {
		const pageSize = 100
		for offset := 0; ; offset += pageSize {
			page, err := pager.ListCandidatesPage(ctx, entityID, offset, pageSize)
			if errors.Is(err, entitycatalog.ErrEntityNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, fmt.Errorf("entity image catalog: page %s at %d: %w", entityID, offset, err)
			}
			rows = append(rows, page...)
			if len(page) < pageSize {
				return rows, nil
			}
		}
	}
	rows, err := repo.ListCandidates(ctx, entityID, 100)
	if errors.Is(err, entitycatalog.ErrEntityNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("entity image catalog: list %s: %w", entityID, err)
	}
	return rows, nil
}

func loadEntityImageMaterializations(ctx context.Context, repo entitycatalog.Repository, rows []entitycatalog.Candidate) (map[int64]*entitycatalog.Materialization, bool, error) {
	materializations := make(map[int64]*entitycatalog.Materialization, len(rows))
	batch, ok := repo.(entitycatalog.MaterializationBatchReader)
	if !ok {
		return materializations, false, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	materializations, err := batch.GetMaterializations(ctx, ids)
	if err != nil {
		return nil, true, fmt.Errorf("entity image catalog: batch materializations: %w", err)
	}
	return materializations, true, nil
}

func projectEntityImageCatalogCandidate(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, row entitycatalog.Candidate, materializations map[int64]*entitycatalog.Materialization, canBatch bool, contentStore EntityImageContentStore) (scriptpkg.SegmentAssetCandidate, EntityImageCatalogDecision, bool, error) {
	decision := EntityImageCatalogDecision{Query: identity.CanonicalName, CandidateID: row.ID, SourceURL: row.SourceURL, Verdict: row.SemanticStatus, Materialization: "missing"}
	if personImageSourceIdentityFailure(identity.CanonicalName, row.SourceURL, row.SourcePageURL) != "" {
		decision.Gate, decision.Selection = "identity", "rejected"
		return scriptpkg.SegmentAssetCandidate{}, decision, false, nil
	}
	materialization, batched := materializations[row.ID]
	if !batched && !canBatch {
		var err error
		materialization, err = repo.GetMaterialization(ctx, row.ID)
		if err != nil {
			return scriptpkg.SegmentAssetCandidate{}, decision, false, fmt.Errorf("entity image catalog: get materialization %d: %w", row.ID, err)
		}
	}
	_, durableImage := applyEntityImageCatalogMaterialization(scriptpkg.SegmentAssetCandidate{}, materialization, nil)
	durableImage = durableImage && row.SemanticStatus == entitycatalog.CandidateSemanticAccepted && entitycatalog.IsUsableCandidateQuality(row)
	if durableImage {
		decision.Materialization = "materialized"
	}
	assessment := entitycatalog.AssessCandidateState(nowUTC(), row)
	if err := persistEntityImageCandidateAssessment(ctx, repo, &row, assessment); err != nil {
		return scriptpkg.SegmentAssetCandidate{}, decision, false, err
	}
	recertifyLayout := personImageLayoutNeedsRecertification(row.QualityReason)
	durableBypass := durableImage && assessment.State == entitycatalog.CandidateStatusBroken
	if (!entitycatalog.IsUsableCandidateStatus(assessment.State) && !durableBypass) ||
		(!entitycatalog.IsUsableCandidateQuality(row) && !recertifyLayout && !durableBypass) {
		decision.Gate, decision.Selection = "lifecycle_quality", "rejected"
		return scriptpkg.SegmentAssetCandidate{}, decision, false, nil
	}
	if strings.TrimSpace(row.SourceURL) == "" {
		decision.Gate, decision.Selection = "source_url", "rejected"
		return scriptpkg.SegmentAssetCandidate{}, decision, false, nil
	}
	candidate := catalogRowCandidate(identity, row, recertifyLayout)
	if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization, contentStore); ok {
		candidate = hydrated
	}
	decision.Gate, decision.Selection = "quality", "pool"
	return candidate, decision, true, nil
}

func persistEntityImageCandidateAssessment(ctx context.Context, repo entitycatalog.Repository, row *entitycatalog.Candidate, assessment entitycatalog.CandidateStateAssessment) error {
	if assessment.State == row.Status || assessment.State == entitycatalog.CandidateStatusUnknown || row.LastSeenAt.IsZero() ||
		row.Status == entitycatalog.CandidateStatusBroken || row.Status == entitycatalog.CandidateStatusRetired {
		return nil
	}
	if err := repo.SetCandidateStatus(ctx, row.ID, assessment.State); err != nil {
		return fmt.Errorf("entity image catalog: persist state %d: %w", row.ID, err)
	}
	row.Status = assessment.State
	return nil
}

func catalogRowCandidate(identity entitycatalog.PersonIdentity, row entitycatalog.Candidate, recertifyLayout bool) scriptpkg.SegmentAssetCandidate {
	semanticStatus, semanticScore, qualityReason := row.SemanticStatus, row.SemanticScore, row.QualityReason
	if recertifyLayout {
		semanticStatus, semanticScore = entitycatalog.CandidateSemanticUnknown, 0
		qualityReason = "pending visual recheck under dominant-subject policy"
	}
	return scriptpkg.SegmentAssetCandidate{
		AssetID: fmt.Sprintf("entity-image-%d", row.ID), Provider: scriptpkg.VidRushProviderInternetImages,
		RetrievalProvider: row.Provider, Query: identity.CanonicalName, Entity: identity.CanonicalName,
		EntityID: identity.CanonicalEntityID, Score: 1.0 / float64(max(1, row.Rank)),
		SourceURL: row.SourceURL, SourcePageURL: row.SourcePageURL, PreviewURL: row.ThumbnailURL,
		Width: row.Width, Height: row.Height, ContentSHA256: row.ContentSHA256,
		VerifierVersion: row.VerifierVersion, SemanticStatus: semanticStatus, SemanticScore: semanticScore,
		TechnicalQualityScore: row.TechnicalScore, QualityReason: qualityReason,
		RightsStatus: "unknown", SelectionReason: "persistent PERSON entity image catalog candidate",
	}
}

// durableEntityImagePoolForSegment loads the durable per-person catalog pool
// for every PERSON identity this segment queries, so the rows go through
// Acquire → Verify → Finalize in-run. Without this the pool is only appended
// after materialization, when un-acquired rows can no longer be downloaded:
// a person with one surviving Drive asset and dozens of verified-but-never-
// fetched editorial URLs then rebinds the same photo on every scene and
// per-scene variety is impossible. Callers append the result with
// AppendProviderCandidatesUnique, which collapses same-URL duplicates.
func durableEntityImagePoolForSegment(ctx context.Context, repo entitycatalog.Repository, segment scriptpkg.VidRushSegmentResult, contentStore EntityImageContentStore) []scriptpkg.SegmentAssetCandidate {
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
		pool, err := entityImageCatalogCandidates(ctx, repo, identity, entityImageCatalogPoolSize, contentStore)
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

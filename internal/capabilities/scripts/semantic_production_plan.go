package scriptgeneration

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// SemanticProductionPlanSchema versions the planner-owned execution view.
const SemanticProductionPlanSchema = "semantic_production_plan.v1"

// Deterministic unit statuses. The plan never invents a composition: a unit is
// either admitted, admitted by reusing an already retrieved asset, or dropped
// with an explicit reason.
const (
	ProductionUnitPlanned            = "planned"
	ProductionUnitReuse              = "reuse"
	ProductionUnitDroppedBudget      = "dropped_budget"
	ProductionUnitDroppedSceneBudget = "dropped_scene_budget"
	ProductionUnitSkipped            = "skipped"
)

// Search statuses owned by the planner.
const (
	ProductionSearchPending   = "pending"
	ProductionSearchSatisfied = "satisfied"
	ProductionSearchFailed    = "failed"
	ProductionSearchReuse     = "reuse"
)

// SemanticProductionPlanConfig bounds the compositions a run may produce.
// Zero values fall back to the certified defaults; the overlay planner still
// enforces its own independent image ceiling.
type SemanticProductionPlanConfig struct {
	// MaxMediaUnits caps external asset searches per run.
	MaxMediaUnits int
	// MaxMediaUnitsPerScene caps the searched compositions anchored in one
	// scene, mirroring the certified two-images-per-scene budget.
	MaxMediaUnitsPerScene int
}

// DefaultSemanticProductionPlanConfig returns the certified run budget: twenty
// materialized images per run and two per scene, the same ceilings the overlay
// compiler enforces.
func DefaultSemanticProductionPlanConfig() SemanticProductionPlanConfig {
	return SemanticProductionPlanConfig{MaxMediaUnits: 20, MaxMediaUnitsPerScene: 2}
}

func (c SemanticProductionPlanConfig) normalized() SemanticProductionPlanConfig {
	defaults := DefaultSemanticProductionPlanConfig()
	if c.MaxMediaUnits <= 0 {
		c.MaxMediaUnits = defaults.MaxMediaUnits
	}
	if c.MaxMediaUnitsPerScene <= 0 {
		c.MaxMediaUnitsPerScene = defaults.MaxMediaUnitsPerScene
	}
	return c
}

// SemanticProductionPlan is the executable projection of the consolidated
// view: which compositions this run will produce, which asset is searched once
// and reused, what remains uncovered, and the planner-owned search outcomes.
type SemanticProductionPlan struct {
	Schema   string
	Units    []SemanticProductionUnit
	Identity []SemanticPlannedIdentity
	Coverage SemanticProductionCoverage
	Metrics  SemanticProductionMetrics

	// factsByCandidate and coveredFacts make coverage revocable when a
	// retrieval settles as skipped. They are planner state, never wire data.
	factsByCandidate map[string][]string
	coveredFacts     map[string]bool
	// identityByID is the canonical identity registry the plan was built
	// against. The batch scheduler reads it to classify a composition's query
	// and to key the plan cache on the normalized entity surface.
	identityByID map[string]SemanticIdentity
}

// SemanticPlannedIdentity is one canonical identity the plan knows about.
// Media reports whether the identity was admitted for an image; the overlay
// planner admits an identity image only when it is true.
type SemanticPlannedIdentity struct {
	ID, Surface, Kind string
	Media             bool
	AssetKey          string
	CandidateIDs      []string
}

// SemanticProductionUnit is one composition the planner may produce.
type SemanticProductionUnit struct {
	CandidateID, Recipe string
	Rank                int
	Priority            float64
	SceneID             string
	EntityIDs           []string
	FactIDs             []string
	Title, Evidence     string
	Start, End          int
	AssetKey            string
	RequiresMedia       bool
	Status              string
	DroppedReason       string
	// ReusedFrom is the candidate id whose retrieval serves this asset key.
	ReusedFrom string
	// Queries is the current widening ladder. It is empty for text/data
	// compositions and for asset reuse.
	Queries []SemanticSearchQuery
	// EntityLadders keeps one exact-name ladder per identity the composition
	// mentions but its main ladder never searched, so a failed multi-name
	// search can drop a name instead of the whole composition.
	EntityLadders   []SemanticNameLadder
	SearchStatus    string
	Rung            int
	DroppedEntities int
	ResolvedAssetID string
	Retry           *SemanticSearchRetry
	// ResolvedImage is the first hit a satisfied search produced, carried
	// with its content address and locations so the overlay binder can use
	// it without a second provider call. Nil until a search hits.
	ResolvedImage *SemanticResolvedImage
}

// SemanticResolvedImage is the durable projection of a planner hit. SHA256 is
// the admission key: without verified bytes a hit stays a reference and never
// becomes a rendered overlay asset.
type SemanticResolvedImage struct {
	AssetID, URL, DriveLink, SHA256, MediaType, Provider, Query, Entity string
	QueryOrigin                                                         string
	TemplateID                                                          string
	Illustrative                                                        bool
	Width, Height                                                       int
}

// SemanticNameLadder is the untried exact-name fallback of one identity.
type SemanticNameLadder struct {
	EntityID string
	Queries  []SemanticSearchQuery
}

// SemanticProductionCoverage accounts for every canonical fact after the run
// budget has been applied.
type SemanticProductionCoverage struct {
	CanonicalFacts int
	CoveredFacts   int
	DeferredFacts  int
	UncoveredFacts int
	Deferred       []SemanticDeferredFact
	Uncovered      []SemanticDeferredFact
}

// SemanticDeferredFact is one canonical fact without an admitted visual and
// the explicit reason why.
type SemanticDeferredFact struct {
	FactID, Reason string
}

// SemanticProductionMetrics are measured planner-level counts, never
// provider measurements.
type SemanticProductionMetrics struct {
	RawCandidates        int
	AdmittedUnits        int
	MediaUnits           int
	ReusedAssets         int
	DroppedBudget        int
	DroppedSceneBudget   int
	PlannedQueries       int
	CanonicalFacts       int
	CoveredFacts         int
	DeferredFacts        int
	UncoveredFacts       int
	SearchAttempts       int
	SearchesSatisfied    int
	SearchesFailed       int
	SearchesSkipped      int
	SearchBroadens       int
	EntityDrops          int
	DuplicateQueriesDrop int
}

// ScriptConsolidationFromBundles returns the shared script-level consolidation
// projection carried by every scene bundle, or nil when the worker predates it.
func ScriptConsolidationFromBundles(bundles map[string]*SemanticCandidateBundle) *SemanticVisualConsolidation {
	for _, bundle := range bundles {
		if bundle != nil && bundle.Consolidation != nil {
			return bundle.Consolidation
		}
	}
	return nil
}

// ScriptSearchFeedbackFromBundles returns the shared engine handshake.
func ScriptSearchFeedbackFromBundles(bundles map[string]*SemanticCandidateBundle) *SemanticSearchFeedback {
	for _, bundle := range bundles {
		if bundle != nil && bundle.SearchFeedback != nil {
			return bundle.SearchFeedback
		}
	}
	return nil
}

// PlanScriptProduction turns one analysed batch into the planner-owned view:
// the consolidated projection, its executable plan and the search feedback the
// plan currently reports. A worker without the consolidation projection yields
// nils, so older deployments keep their existing planning unchanged.
func PlanScriptProduction(bundles map[string]*SemanticCandidateBundle) (*SemanticVisualConsolidation, *SemanticProductionPlan, *SemanticSearchFeedback, error) {
	consolidation := ScriptConsolidationFromBundles(bundles)
	if consolidation == nil {
		return nil, nil, nil, nil
	}
	plan, err := BuildSemanticProductionPlan(consolidation, SemanticIdentitiesFromBundles(bundles), DefaultSemanticProductionPlanConfig())
	if err != nil {
		// The consolidation itself stays visible for audit; only the executable
		// plan is withheld, because a broken plan must never drive a render.
		return consolidation, nil, nil, err
	}
	return consolidation, plan, plan.SearchFeedback(), nil
}

// SemanticIdentitiesFromBundles merges the cross-scene identity registry. Every
// scene bundle carries the registry as known at that scene, so the merge is
// idempotent and the last occurrence wins.
func SemanticIdentitiesFromBundles(bundles map[string]*SemanticCandidateBundle) []SemanticIdentity {
	byID := make(map[string]SemanticIdentity)
	for _, bundle := range bundles {
		if bundle == nil {
			continue
		}
		for _, identity := range bundle.Identities {
			if strings.TrimSpace(identity.ID) == "" {
				continue
			}
			byID[identity.ID] = identity
		}
	}
	out := make([]SemanticIdentity, 0, len(byID))
	for _, identity := range byID {
		out = append(out, identity)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuildSemanticProductionPlan applies the run budget to the consolidated view.
// It fails closed on a broken projection: an unknown represented candidate, a
// fact that is neither represented nor explained, a duplicated candidate id or
// an unreachable identity reference is an error, not a silent omission.
func BuildSemanticProductionPlan(consolidation *SemanticVisualConsolidation, identities []SemanticIdentity, config SemanticProductionPlanConfig) (*SemanticProductionPlan, error) {
	if consolidation == nil {
		return nil, fmt.Errorf("semantic production plan: consolidation is required")
	}
	if consolidation.Schema != "visual_consolidation.v1" {
		return nil, fmt.Errorf("semantic production plan: unsupported consolidation schema %q", consolidation.Schema)
	}
	config = config.normalized()
	knownIdentities := make(map[string]SemanticIdentity, len(identities))
	for _, identity := range identities {
		knownIdentities[identity.ID] = identity
	}
	surfaceOf := func(entityID string) string {
		return strings.TrimSpace(knownIdentities[entityID].CanonicalSurface)
	}

	candidates := append([]SemanticProductionCandidate(nil), consolidation.ProductionCandidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		leftScore := semanticPlannerPriority(candidates[i], knownIdentities)
		rightScore := semanticPlannerPriority(candidates[j], knownIdentities)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if candidates[i].Rank != candidates[j].Rank {
			return candidates[i].Rank < candidates[j].Rank
		}
		if candidates[i].SceneID != candidates[j].SceneID {
			return candidates[i].SceneID < candidates[j].SceneID
		}
		return candidates[i].ID < candidates[j].ID
	})

	plan := &SemanticProductionPlan{
		Schema:           SemanticProductionPlanSchema,
		Units:            make([]SemanticProductionUnit, 0, len(candidates)),
		Coverage:         SemanticProductionCoverage{CanonicalFacts: len(consolidation.Facts)},
		factsByCandidate: make(map[string][]string, len(candidates)),
		coveredFacts:     make(map[string]bool, len(consolidation.Facts)),
		identityByID:     knownIdentities,
	}
	plan.Metrics.RawCandidates = len(candidates)
	plan.Metrics.CanonicalFacts = len(consolidation.Facts)
	admitted := make(map[string]struct{}, len(candidates))
	candidateFacts := make(map[string][]string, len(candidates))
	assetOwner := make(map[string]string)
	sceneMedia := make(map[string]int)
	mediaUnits := 0
	seenIDs := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Recipe) == "" {
			return nil, fmt.Errorf("semantic production plan: candidate without id or recipe")
		}
		if candidate.Rank <= 0 {
			return nil, fmt.Errorf("semantic production plan: candidate %q has invalid rank %d", candidate.ID, candidate.Rank)
		}
		if _, duplicate := seenIDs[candidate.ID]; duplicate {
			return nil, fmt.Errorf("semantic production plan: duplicate candidate id %q", candidate.ID)
		}
		seenIDs[candidate.ID] = struct{}{}
		for _, entityID := range candidate.EntityIDs {
			if _, ok := knownIdentities[entityID]; !ok {
				return nil, fmt.Errorf("semantic production plan: candidate %q references unknown identity %q", candidate.ID, entityID)
			}
		}
		unit := SemanticProductionUnit{
			CandidateID: candidate.ID, Recipe: candidate.Recipe, Rank: candidate.Rank,
			Priority:  semanticPlannerPriority(candidate, knownIdentities),
			SceneID:   candidate.SceneID,
			EntityIDs: append([]string(nil), candidate.EntityIDs...),
			FactIDs:   append([]string(nil), candidate.FactIDs...),
			Title:     candidate.Title, Evidence: candidate.Evidence, Start: candidate.Start, End: candidate.End,
			AssetKey:      candidate.AssetKey,
			RequiresMedia: candidate.RequiresMedia,
			Queries:       append([]SemanticSearchQuery(nil), candidate.Queries...),
			Status:        ProductionUnitPlanned,
			SearchStatus:  ProductionSearchPending,
		}
		if candidate.RequiresMedia && strings.TrimSpace(candidate.AssetKey) == "" {
			return nil, fmt.Errorf("semantic production plan: media candidate %q has no asset key", candidate.ID)
		}
		if candidate.RequiresMedia {
			if owner, reused := assetOwner[candidate.AssetKey]; reused {
				// One retrieval per asset key: the later composition reuses the
				// bytes and must not issue a second external search.
				unit.Status = ProductionUnitReuse
				unit.ReusedFrom = owner
				unit.SearchStatus = ProductionSearchReuse
				if len(unit.Queries) > 0 {
					plan.Metrics.DuplicateQueriesDrop += len(unit.Queries)
					unit.Queries = nil
				}
				unit.EntityLadders = nil
				plan.Metrics.ReusedAssets++
			} else if sceneMedia[candidate.SceneID] >= config.MaxMediaUnitsPerScene {
				unit.Status = ProductionUnitDroppedSceneBudget
				unit.SearchStatus = ProductionSearchFailed
				unit.DroppedReason = fmt.Sprintf("scene %q already holds %d searched compositions", candidate.SceneID, config.MaxMediaUnitsPerScene)
				unit.Queries = nil
				unit.EntityLadders = nil
				plan.Metrics.DroppedSceneBudget++
			} else if mediaUnits >= config.MaxMediaUnits {
				unit.Status = ProductionUnitDroppedBudget
				unit.SearchStatus = ProductionSearchFailed
				unit.DroppedReason = fmt.Sprintf("run budget of %d searched compositions exhausted", config.MaxMediaUnits)
				unit.Queries = nil
				unit.EntityLadders = nil
				plan.Metrics.DroppedBudget++
			} else {
				if len(unit.Queries) == 0 {
					return nil, fmt.Errorf("semantic production plan: media candidate %q has no query ladder", candidate.ID)
				}
				assetOwner[candidate.AssetKey] = candidate.ID
				sceneMedia[candidate.SceneID]++
				mediaUnits++
				plan.Metrics.MediaUnits++
				plan.Metrics.PlannedQueries += len(unit.Queries)
				unit.EntityLadders = exactNameLadders(unit.Queries, unit.EntityIDs, surfaceOf)
			}
		} else {
			unit.SearchStatus = ProductionSearchReuse
		}
		if unit.Status == ProductionUnitPlanned || unit.Status == ProductionUnitReuse {
			plan.Metrics.AdmittedUnits++
			admitted[unit.CandidateID] = struct{}{}
		}
		candidateFacts[unit.CandidateID] = unit.FactIDs
		plan.Units = append(plan.Units, unit)
	}

	// Coverage: a canonical fact is covered only when its representing
	// composition survived the budget. A represented-but-dropped fact is
	// deferred with the exact reason instead of being reported as missing data.
	for _, fact := range consolidation.Facts {
		if fact.Representation == nil {
			if strings.TrimSpace(fact.UncoveredReason) == "" {
				return nil, fmt.Errorf("semantic production plan: fact %q is neither represented nor explained", fact.ID)
			}
			plan.Coverage.UncoveredFacts++
			plan.Coverage.Uncovered = append(plan.Coverage.Uncovered, SemanticDeferredFact{FactID: fact.ID, Reason: fact.UncoveredReason})
			continue
		}
		if !slices.Contains(candidateFacts[fact.Representation.CandidateID], fact.ID) {
			return nil, fmt.Errorf("semantic production plan: candidate %q does not declare fact %q", fact.Representation.CandidateID, fact.ID)
		}
		_, ok := admitted[fact.Representation.CandidateID]
		if ok {
			plan.Coverage.CoveredFacts++
			plan.coveredFacts[fact.ID] = true
			continue
		}
		reason := fmt.Sprintf("representation %q was dropped by the run budget", fact.Representation.CandidateID)
		plan.Coverage.DeferredFacts++
		plan.Coverage.Deferred = append(plan.Coverage.Deferred, SemanticDeferredFact{FactID: fact.ID, Reason: reason})
	}
	plan.Metrics.CoveredFacts = plan.Coverage.CoveredFacts
	plan.Metrics.DeferredFacts = plan.Coverage.DeferredFacts
	plan.Metrics.UncoveredFacts = plan.Coverage.UncoveredFacts

	// Identity admission: an identity image is produced only when one admitted
	// composition searches or reuses its asset.
	byIdentity := make(map[string]*SemanticPlannedIdentity, len(knownIdentities))
	for _, unit := range plan.Units {
		if unit.Status != ProductionUnitPlanned && unit.Status != ProductionUnitReuse {
			continue
		}
		for _, entityID := range unit.EntityIDs {
			identity, ok := knownIdentities[entityID]
			if !ok {
				continue
			}
			planned, exists := byIdentity[entityID]
			if !exists {
				planned = &SemanticPlannedIdentity{ID: identity.ID, Surface: identity.CanonicalSurface, Kind: identity.Kind}
				byIdentity[entityID] = planned
			}
			planned.CandidateIDs = append(planned.CandidateIDs, unit.CandidateID)
			if unit.RequiresMedia {
				planned.Media = true
				if planned.AssetKey == "" {
					planned.AssetKey = unit.AssetKey
				}
			}
		}
	}
	plan.Identity = make([]SemanticPlannedIdentity, 0, len(byIdentity))
	for _, identity := range byIdentity {
		plan.Identity = append(plan.Identity, *identity)
	}
	sort.Slice(plan.Identity, func(i, j int) bool { return plan.Identity[i].ID < plan.Identity[j].ID })
	plan.factsByCandidate = candidateFacts
	return plan, nil
}

// semanticPlannerPriority gives a source-grounded composed query a chance to
// compete as the visual event it describes. Rust assigns a base priority to
// the owning identity; this final planner pass considers the actual query rung
// before spending a scarce media slot. An isolated one-word identity remains
// available as a fallback, but does not beat a well-grounded composition just
// because its identity score was slightly higher.
func semanticPlannerPriority(candidate SemanticProductionCandidate, identities map[string]SemanticIdentity) float64 {
	priority := candidate.Priority
	if slices.Contains(candidate.Rationale, "global_document_anchor") {
		return priority
	}
	if candidate.Recipe != "entity_search" || len(candidate.Queries) == 0 {
		return priority
	}
	query := candidate.Queries[0]
	if query.Origin == "derived_from_source_context" && strings.TrimSpace(query.Evidence) != "" && len(query.Components) >= 2 {
		owner := normalizeIdentitySurface(candidate.Title)
		for _, entityID := range candidate.EntityIDs {
			if identity, ok := identities[entityID]; ok && normalizeIdentitySurface(identity.CanonicalSurface) != "" {
				owner = normalizeIdentitySurface(identity.CanonicalSurface)
				break
			}
		}
		for _, component := range query.Components {
			if normalizeIdentitySurface(component.Text) == owner {
				return min(priority+0.2, 0.97)
			}
		}
	}
	if len(strings.Fields(strings.TrimSpace(query.Text))) == 1 {
		return priority * 0.82
	}
	return priority
}

// revokeSkippedUnit keeps coverage and identity admission honest after a
// retrieval ladder exhausted: the facts the composition represented become
// deferred with that reason, and an identity loses its image admission unless
// another admitted media composition still searches it.
func (p *SemanticProductionPlan) revokeSkippedUnit(unit *SemanticProductionUnit, reason string) {
	if unit == nil {
		return
	}
	for _, factID := range p.factsByCandidate[unit.CandidateID] {
		if !p.coveredFacts[factID] {
			continue
		}
		delete(p.coveredFacts, factID)
		p.Coverage.CoveredFacts--
		p.Coverage.DeferredFacts++
		p.Coverage.Deferred = append(p.Coverage.Deferred, SemanticDeferredFact{FactID: factID, Reason: reason})
	}
	p.Metrics.CoveredFacts = p.Coverage.CoveredFacts
	p.Metrics.DeferredFacts = p.Coverage.DeferredFacts
	for _, entityID := range unit.EntityIDs {
		shared := false
		for index := range p.Units {
			other := &p.Units[index]
			if other.CandidateID == unit.CandidateID || !other.RequiresMedia {
				continue
			}
			if other.Status != ProductionUnitPlanned && other.Status != ProductionUnitReuse {
				continue
			}
			if !slices.Contains(other.EntityIDs, entityID) {
				continue
			}
			shared = true
			break
		}
		if shared {
			continue
		}
		for index := range p.Identity {
			if p.Identity[index].ID == entityID {
				p.Identity[index].Media = false
				p.Identity[index].AssetKey = ""
			}
		}
	}
}

// exactNameLadders lists the identities a composition mentions but its main
// ladder never searched, one exact-name fallback each. Only a composition
// carrying several names can be narrowed by dropping one.
func exactNameLadders(queries []SemanticSearchQuery, entityIDs []string, surfaceOf func(string) string) []SemanticNameLadder {
	if len(entityIDs) < 2 {
		return nil
	}
	tried := make(map[string]struct{}, len(queries))
	for _, query := range queries {
		if query.Kind == "exact_mention" {
			tried[normalizeIdentitySurface(query.Text)] = struct{}{}
		}
	}
	var ladders []SemanticNameLadder
	for _, entityID := range entityIDs {
		surface := surfaceOf(entityID)
		if surface == "" {
			continue
		}
		if _, ok := tried[normalizeIdentitySurface(surface)]; ok {
			continue
		}
		ladders = append(ladders, SemanticNameLadder{
			EntityID: entityID,
			Queries:  []SemanticSearchQuery{{Kind: "exact_mention", Text: surface}},
		})
	}
	return ladders
}

// IdentitiesAdmittedForMedia returns the canonical surfaces the plan admitted
// for an identity image. The overlay planner reads exactly this set.
func (p *SemanticProductionPlan) IdentitiesAdmittedForMedia() map[string]struct{} {
	admitted := make(map[string]struct{})
	if p == nil {
		return admitted
	}
	for _, identity := range p.Identity {
		if !identity.Media {
			continue
		}
		if surface := normalizeIdentitySurface(identity.Surface); surface != "" {
			admitted[surface] = struct{}{}
		}
	}
	return admitted
}

// AdmitsIdentity reports whether an identity surface may become an image.
func (p *SemanticProductionPlan) AdmitsIdentity(surface string) bool {
	if p == nil {
		return true
	}
	_, ok := p.IdentitiesAdmittedForMedia()[normalizeIdentitySurface(surface)]
	return ok
}

func normalizeIdentitySurface(surface string) string {
	return strings.ToLower(strings.Join(strings.Fields(surface), " "))
}

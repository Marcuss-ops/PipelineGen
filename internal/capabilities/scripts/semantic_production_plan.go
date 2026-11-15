package scriptgeneration

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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
	SceneIDs            []string
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
	Queries         []SemanticSearchQuery
	SearchStatus    string
	Rung            int
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
	VerifierVersion                                                     string
	QueryOrigin                                                         string
	TemplateID                                                          string
	Illustrative                                                        bool
	Width, Height                                                       int
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
	SearchesTooLate      int
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
func PlanScriptProduction(bundles map[string]*SemanticCandidateBundle, configs ...SemanticProductionPlanConfig) (*SemanticVisualConsolidation, *SemanticProductionPlan, *SemanticSearchFeedback, error) {
	consolidation := ScriptConsolidationFromBundles(bundles)
	if consolidation == nil {
		return nil, nil, nil, nil
	}
	config := DefaultSemanticProductionPlanConfig()
	if len(configs) > 0 {
		config = configs[0]
	}
	plan, err := BuildSemanticProductionPlan(consolidation, SemanticIdentitiesFromBundles(bundles), config)
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
			SceneIDs:  append([]string(nil), candidate.SceneIDs...),
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
			if reason := weakVisualEntityQuery(candidate, knownIdentities); reason != "" {
				unit.Status = ProductionUnitSkipped
				unit.SearchStatus = ProductionSearchFailed
				unit.DroppedReason = reason
				unit.Queries = nil
				// No retrieval was attempted, so this is not a skipped search.
			} else if owner, reused := assetOwner[candidate.AssetKey]; reused {
				// One retrieval per asset key: the later composition reuses the
				// bytes and must not issue a second external search.
				unit.Status = ProductionUnitReuse
				unit.ReusedFrom = owner
				unit.SearchStatus = ProductionSearchReuse
				if len(unit.Queries) > 0 {
					plan.Metrics.DuplicateQueriesDrop += len(unit.Queries)
					unit.Queries = nil
				}
				plan.Metrics.ReusedAssets++
			} else if sceneMedia[candidate.SceneID] >= config.MaxMediaUnitsPerScene {
				unit.Status = ProductionUnitDroppedSceneBudget
				unit.SearchStatus = ProductionSearchFailed
				unit.DroppedReason = fmt.Sprintf("scene %q already holds %d searched compositions", candidate.SceneID, config.MaxMediaUnitsPerScene)
				unit.Queries = nil
				plan.Metrics.DroppedSceneBudget++
			} else if mediaUnits >= config.MaxMediaUnits {
				unit.Status = ProductionUnitDroppedBudget
				unit.SearchStatus = ProductionSearchFailed
				unit.DroppedReason = fmt.Sprintf("dropped by the run budget: %d searched compositions exhausted", config.MaxMediaUnits)
				unit.Queries = nil
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
				// Query intents are emitted by BrainRust. Go preserves their order
				// and never invents a per-identity fallback query from display names.
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
		for _, unit := range plan.Units {
			if unit.CandidateID == fact.Representation.CandidateID && strings.TrimSpace(unit.DroppedReason) != "" {
				reason = unit.DroppedReason
				break
			}
		}
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

// weakVisualEntityQuery suppresses an image search for an abstract visual
// concept or a bare place name unless the source provides a contextual query
// with multiple grounded components. Those bare terms routinely return
// generic biographies, maps, or unrelated venue pages. Contextual scene
// searches and person identities remain eligible.
func weakVisualEntityQuery(candidate SemanticProductionCandidate, identities map[string]SemanticIdentity) string {
	if candidate.Recipe != "entity_search" || len(candidate.EntityIDs) != 1 {
		return ""
	}
	kind := strings.ToUpper(strings.TrimSpace(identities[candidate.EntityIDs[0]].Kind))
	if kind != "VISUAL_CONCEPT" && kind != "LOCATION" && kind != "PLACE" && kind != "GPE" {
		return ""
	}
	surface := strings.ToLower(strings.TrimSpace(identities[candidate.EntityIDs[0]].CanonicalSurface))
	for _, query := range candidate.Queries {
		if query.Kind == "context_phrase" && strings.TrimSpace(query.Evidence) != "" && len(query.Components) >= 2 {
			return ""
		}
		// A full source sentence naming the identity is source-grounded
		// context, not a bare place query. The scheduler keeps that sentence as
		// its own rung; skipping the unit before the ladder runs discarded a
		// concrete place because only context_phrase was recognized.
		if query.Kind == "source_sentence" && surface != "" &&
			strings.Contains(strings.ToLower(query.Text), surface) {
			return ""
		}
	}
	return fmt.Sprintf("weak_visual_query: %s identity has no source-grounded contextual query", strings.ToLower(kind))
}

// ApplyCallerImageSearches adds explicit, caller-authored image searches to
// the matching semantic scene ladder. These searches are kept in the same
// planner/report path as inferred searches, so an operator can see the exact
// query and its result. A manual query is attached to at most one production
// unit per scene to avoid multiplying provider calls across co-mentioned
// entities.
func ApplyCallerImageSearches(plan *SemanticProductionPlan, searches []mediadomain.SegmentMediaSearch, assignments []mediadomain.SegmentMediaAssignment) int {
	if plan == nil || len(searches) == 0 {
		return 0
	}
	added := 0
	for _, search := range searches {
		if !callerImageSearchAllowed(search) || callerImageSearchLocked(search, assignments) {
			continue
		}
		query := strings.TrimSpace(search.Query)
		if query == "" {
			continue
		}
		matched := make([]int, 0)
		duplicate := false
		late := false
		for index := range plan.Units {
			unit := &plan.Units[index]
			if !unit.RequiresMedia || unit.Status != ProductionUnitPlanned || !unitMatchesScene(unit, search.SegmentID) {
				continue
			}
			if unit.SearchStatus != ProductionSearchPending {
				late = true
				continue
			}
			if semanticQueryPresent(unit.Queries, query) {
				duplicate = true
				break
			}
			matched = append(matched, index)
		}
		if duplicate || len(matched) == 0 {
			if late && !duplicate {
				plan.Metrics.SearchesTooLate++
			}
			continue
		}
		// Give different authored queries independent composition units in
		// the same scene before turning them into fallback rungs on one unit.
		selected := matched[0]
		for _, index := range matched {
			if !unitHasCallerQuery(&plan.Units[index]) {
				selected = index
				break
			}
		}
		unit := &plan.Units[selected]
		unit.Queries = append([]SemanticSearchQuery{{Kind: "context_phrase", Text: query, Origin: "caller_media_plan"}}, unit.Queries...)
		plan.Metrics.PlannedQueries++
		added++
	}
	return added
}

func callerImageSearchAllowed(search mediadomain.SegmentMediaSearch) bool {
	switch search.Slot {
	case mediadomain.SlotSecondaryImage, mediadomain.SlotPortrait, mediadomain.SlotEvidenceOverlay, mediadomain.SlotBackground:
	default:
		return false
	}
	if len(search.Providers) > 0 {
		allowed := false
		for _, provider := range search.Providers {
			if strings.EqualFold(strings.TrimSpace(provider), "internet_images") || strings.EqualFold(strings.TrimSpace(provider), "semantic_production_plan") {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	if len(search.MediaTypes) > 0 {
		allowed := false
		for _, mediaType := range search.MediaTypes {
			if strings.EqualFold(strings.TrimSpace(mediaType), "image") || strings.EqualFold(strings.TrimSpace(mediaType), "image/*") {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return strings.TrimSpace(search.SegmentID) != ""
}

func callerImageSearchLocked(search mediadomain.SegmentMediaSearch, assignments []mediadomain.SegmentMediaAssignment) bool {
	for _, assignment := range assignments {
		if assignment.Locked && assignment.SegmentID == search.SegmentID && assignment.Slot == search.Slot {
			return true
		}
	}
	return false
}

func unitMatchesScene(unit *SemanticProductionUnit, sceneID string) bool {
	if unit == nil || strings.TrimSpace(sceneID) == "" {
		return false
	}
	if unit.SceneID == sceneID || slices.Contains(unit.SceneIDs, sceneID) {
		return true
	}
	return false
}

func semanticQueryPresent(queries []SemanticSearchQuery, query string) bool {
	want := normalizeIdentitySurface(query)
	for _, existing := range queries {
		if normalizeIdentitySurface(existing.Text) == want {
			return true
		}
	}
	return false
}

func unitHasCallerQuery(unit *SemanticProductionUnit) bool {
	if unit == nil {
		return false
	}
	for _, query := range unit.Queries {
		if query.Origin == "caller_media_plan" {
			return true
		}
	}
	return false
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

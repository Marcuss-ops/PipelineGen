package contract

import (
	"slices"
	"strings"

	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

// Production unit statuses. The plan never invents a composition: a unit is
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

// SemanticProductionPlan is the executable projection of the consolidated
// view: which compositions this run will produce, which asset is searched once
// and reused, what remains uncovered, and the planner-owned search outcomes.
type SemanticProductionPlan struct {
	Schema   string
	Units    []generationcontract.SemanticProductionUnit
	Identity []generationcontract.SemanticPlannedIdentity
	Coverage generationcontract.SemanticProductionCoverage
	Metrics  generationcontract.SemanticProductionMetrics

	// FactsByCandidate and CoveredFacts make coverage revocable when a
	// retrieval settles as skipped. They are planner state, never wire data.
	FactsByCandidate map[string][]string `json:"-"`
	CoveredFacts     map[string]bool     `json:"-"`
	// IdentityByID is the canonical identity registry the plan was built
	// against. The batch scheduler reads it to classify a composition's query
	// and to key the plan cache on the normalized entity surface.
	IdentityByID map[string]SemanticIdentity `json:"-"`
}

// revokeSkippedUnit keeps coverage and identity admission honest after a
// retrieval ladder exhausted: the facts the composition represented become
// deferred with that reason, and an identity loses its image admission unless
// another admitted media composition still searches it.
func (p *SemanticProductionPlan) RevokeSkippedUnit(unit *generationcontract.SemanticProductionUnit, reason string) {
	if unit == nil {
		return
	}
	for _, factID := range p.FactsByCandidate[unit.CandidateID] {
		if !p.CoveredFacts[factID] {
			continue
		}
		delete(p.CoveredFacts, factID)
		p.Coverage.CoveredFacts--
		p.Coverage.DeferredFacts++
		p.Coverage.Deferred = append(p.Coverage.Deferred, generationcontract.SemanticDeferredFact{FactID: factID, Reason: reason})
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
		if surface := NormalizeIdentitySurface(identity.Surface); surface != "" {
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
	_, ok := p.IdentitiesAdmittedForMedia()[NormalizeIdentitySurface(surface)]
	return ok
}

// NormalizeIdentitySurface is the single identity-key normalization used by
// identity admission and image binding: case-folded, whitespace-collapsed.
func NormalizeIdentitySurface(surface string) string {
	return strings.ToLower(strings.Join(strings.Fields(surface), " "))
}

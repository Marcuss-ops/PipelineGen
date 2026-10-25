package scriptgeneration

import (
	"fmt"
	"strings"
)

// SemanticSearchOutcome is one retrieval result reported by the planner's
// asset provider. The planner owns retrieval; the engine only proposed the
// exact-source ladder.
type SemanticSearchOutcome struct {
	CandidateID string
	Rung        int
	Found       bool
	// AssetID is the certified content-addressed asset on success.
	AssetID string
	// Error is the provider's failure description on failure.
	Error string
}

// SemanticSearchAttempt is the next query the planner must execute.
type SemanticSearchAttempt struct {
	CandidateID, AssetKey string
	SceneID               string
	Query, QueryKind      string
	Rung                  int
	// Rank is the plan rank of the composition. The concurrent scheduler
	// uses it to keep the measured report deterministic even when attempts
	// complete out of order.
	Rank int
	// Entity is the canonical surface of the composition's single identity,
	// empty for multi-identity compositions. It is the plan-cache key.
	Entity string
	// Class is the routing class derived from the identity kind. It selects
	// the provider lane and the cache namespace.
	Class SemanticSearchClass
}

func (p *SemanticProductionPlan) mediaUnit(candidateID string) *SemanticProductionUnit {
	if p == nil {
		return nil
	}
	for index := range p.Units {
		unit := &p.Units[index]
		if unit.CandidateID == candidateID && unit.RequiresMedia && unit.SearchStatus == ProductionSearchPending && len(unit.Queries) > 0 {
			return unit
		}
	}
	return nil
}

// NextSearchAttempt returns the highest-priority pending media query, in plan
// rank order. It returns false when every media composition is settled.
func (p *SemanticProductionPlan) NextSearchAttempt() (SemanticSearchAttempt, bool) {
	if p == nil {
		return SemanticSearchAttempt{}, false
	}
	for index := range p.Units {
		unit := &p.Units[index]
		if attempt, ok := p.attemptForUnit(unit); ok {
			return attempt, true
		}
	}
	return SemanticSearchAttempt{}, false
}

// PendingSearchAttempts returns one attempt per pending media composition, in
// plan order. The concurrent scheduler uses it to start every independent
// ladder at once while the plan keeps each ladder's own rung ordering.
func (p *SemanticProductionPlan) PendingSearchAttempts() []SemanticSearchAttempt {
	if p == nil {
		return nil
	}
	out := make([]SemanticSearchAttempt, 0, len(p.Units))
	for index := range p.Units {
		if attempt, ok := p.attemptForUnit(&p.Units[index]); ok {
			out = append(out, attempt)
		}
	}
	return out
}

// NextAttemptForCandidate returns the next rung of one composition after an
// outcome was recorded. Unlike NextSearchAttempt it never crosses into another
// candidate, so a worker can own a single ladder without touching its peers.
func (p *SemanticProductionPlan) NextAttemptForCandidate(candidateID string) (SemanticSearchAttempt, bool) {
	if p == nil {
		return SemanticSearchAttempt{}, false
	}
	for index := range p.Units {
		unit := &p.Units[index]
		if unit.CandidateID != candidateID {
			continue
		}
		return p.attemptForUnit(unit)
	}
	return SemanticSearchAttempt{}, false
}

// attemptForUnit projects one pending unit into the next executable attempt.
// It is the single owner of the rung/query/class/entity projection, so the
// sequential and concurrent paths can never disagree about what comes next.
func (p *SemanticProductionPlan) attemptForUnit(unit *SemanticProductionUnit) (SemanticSearchAttempt, bool) {
	if p == nil || unit == nil {
		return SemanticSearchAttempt{}, false
	}
	if !unit.RequiresMedia || unit.SearchStatus != ProductionSearchPending || len(unit.Queries) == 0 {
		return SemanticSearchAttempt{}, false
	}
	if unit.Rung < 0 || unit.Rung >= len(unit.Queries) {
		return SemanticSearchAttempt{}, false
	}
	query := unit.Queries[unit.Rung]
	attempt := SemanticSearchAttempt{
		CandidateID: unit.CandidateID, AssetKey: unit.AssetKey, SceneID: unit.SceneID,
		Query: query.Text, QueryKind: query.Kind, Rung: unit.Rung,
		Rank: unit.Rank, Class: SearchClassGeneric,
	}
	if len(unit.EntityIDs) == 1 {
		identity := p.identityByID[unit.EntityIDs[0]]
		attempt.Entity = strings.TrimSpace(identity.CanonicalSurface)
		attempt.Class = SearchClassForIdentityKind(identity.Kind)
		if attempt.Class == SearchClassGeneric && strings.TrimSpace(identity.Kind) == "" {
			// An untyped identity is the only case where the query text can
			// still recover an institutional route without guessing a person.
			attempt.Class = SearchClassFromQuery(query.Text)
		}
	}
	return attempt, true
}

// SkipUnsupportedUnit settles a composition whose query class has no provider
// on the wired port. It is not a provider miss: the visual is not produced and
// the plan records the routing reason, so coverage cannot stay green on an
// image that was never searched.
func (p *SemanticProductionPlan) SkipUnsupportedUnit(candidateID, reason string) error {
	if p == nil {
		return fmt.Errorf("semantic production plan: nil plan")
	}
	unit := p.mediaUnit(strings.TrimSpace(candidateID))
	if unit == nil {
		return fmt.Errorf("semantic production plan: %q is not a pending media composition", candidateID)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "no retrieval provider is routed for this query class"
	}
	unit.SearchStatus = ProductionSearchFailed
	unit.Status = ProductionUnitSkipped
	unit.Retry = &SemanticSearchRetry{Action: "skip_visual", Reason: reason}
	p.Metrics.SearchesSkipped++
	p.Metrics.AdmittedUnits--
	p.revokeSkippedUnit(unit, reason)
	return nil
}

// RecordSearchOutcome applies one retrieval outcome and returns the next
// attempt, if any. The policy is deterministic:
//
//   - success settles the composition (`satisfied`) and the asset is reused by
//     every later composition sharing its asset key;
//   - a failure before the last rung widens the query (`broaden`);
//   - an exhausted ladder drops one name when the composition still holds
//     another identity (`drop_entity`);
//   - an exhausted single-name ladder leaves the composition unproduced
//     (`skip_visual`) instead of retrying forever.
func (p *SemanticProductionPlan) RecordSearchOutcome(outcome SemanticSearchOutcome) (SemanticSearchAttempt, bool, error) {
	if p == nil {
		return SemanticSearchAttempt{}, false, fmt.Errorf("semantic production plan: nil plan")
	}
	unit := p.mediaUnit(strings.TrimSpace(outcome.CandidateID))
	if unit == nil {
		return SemanticSearchAttempt{}, false, fmt.Errorf("semantic production plan: %q is not a pending media composition", outcome.CandidateID)
	}
	if outcome.Rung != unit.Rung {
		return SemanticSearchAttempt{}, false, fmt.Errorf("semantic production plan: %q reported rung %d, pending rung is %d", outcome.CandidateID, outcome.Rung, unit.Rung)
	}
	if unit.Retry != nil {
		unit.Retry = nil
	}
	switch {
	case outcome.Found:
		unit.SearchStatus = ProductionSearchSatisfied
		unit.ResolvedAssetID = strings.TrimSpace(outcome.AssetID)
		unit.DroppedEntities = 0
		p.Metrics.SearchAttempts++
		p.Metrics.SearchesSatisfied++
	case unit.Rung+1 < len(unit.Queries):
		unit.Rung++
		p.Metrics.SearchAttempts++
		p.Metrics.SearchBroadens++
		unit.Retry = &SemanticSearchRetry{
			Action: "broaden",
			Reason: fmt.Sprintf("rung %d (%s) did not resolve; widening to %s", unit.Rung-1, unit.Queries[unit.Rung-1].Kind, unit.Queries[unit.Rung].Kind),
		}
	case unit.DroppedEntities < len(unit.EntityLadders):
		// The name is not searchable: keep the composition and search the
		// remaining identity on its own exact mention.
		remaining := unit.EntityLadders[unit.DroppedEntities]
		unit.DroppedEntities++
		unit.Queries = append([]SemanticSearchQuery(nil), remaining.Queries...)
		unit.Rung = 0
		unit.EntityIDs = []string{remaining.EntityID}
		p.Metrics.SearchAttempts++
		p.Metrics.EntityDrops++
		unit.Retry = &SemanticSearchRetry{
			Action: "drop_entity",
			Reason: "the name did not resolve; searching the remaining identity alone",
		}
	default:
		unit.SearchStatus = ProductionSearchFailed
		unit.Status = ProductionUnitSkipped
		p.Metrics.SearchAttempts++
		p.Metrics.SearchesFailed++
		p.Metrics.SearchesSkipped++
		p.Metrics.AdmittedUnits--
		reason := "every exact-source query failed"
		if trimmed := strings.TrimSpace(outcome.Error); trimmed != "" {
			reason = "every exact-source query failed: " + trimmed
		}
		unit.Retry = &SemanticSearchRetry{Action: "skip_visual", Reason: reason}
		p.revokeSkippedUnit(unit, reason)
	}
	if next, ok := p.NextSearchAttempt(); ok {
		return next, true, nil
	}
	return SemanticSearchAttempt{}, false, nil
}

// SearchFeedback projects the planner-owned state back into the same
// `search_feedback` contract the engine emitted, so a failed search is
// reported instead of disappearing into planner logs. One entry is emitted per
// media composition, keyed by its stable candidate id.
func (p *SemanticProductionPlan) SearchFeedback() *SemanticSearchFeedback {
	if p == nil {
		return nil
	}
	feedback := &SemanticSearchFeedback{Entries: make([]SemanticSearchFeedbackEntry, 0, len(p.Units))}
	for index := range p.Units {
		unit := &p.Units[index]
		if !unit.RequiresMedia || unit.SearchStatus == ProductionSearchReuse {
			continue
		}
		if unit.Status == ProductionUnitDroppedBudget || unit.Status == ProductionUnitDroppedSceneBudget {
			// Budget-dropped compositions never searched: there is no outcome
			// to report in the retrieval handshake.
			continue
		}
		status := unit.SearchStatus
		if status == "" {
			status = ProductionSearchPending
		}
		entry := SemanticSearchFeedbackEntry{
			ProposalID: unit.CandidateID, SceneID: unit.SceneID,
			Queries: append([]SemanticSearchQuery(nil), unit.Queries...),
			Status:  status,
		}
		if unit.Retry != nil {
			retry := *unit.Retry
			entry.Retry = &retry
		}
		feedback.Entries = append(feedback.Entries, entry)
	}
	return feedback
}

// SearchableUnits returns how many media compositions still need a query; the
// planner uses it to decide whether a run has any external work left.
func (p *SemanticProductionPlan) SearchableUnits() int {
	if p == nil {
		return 0
	}
	pending := 0
	for index := range p.Units {
		unit := &p.Units[index]
		if unit.RequiresMedia && unit.SearchStatus == ProductionSearchPending && len(unit.Queries) > 0 {
			pending++
		}
	}
	return pending
}

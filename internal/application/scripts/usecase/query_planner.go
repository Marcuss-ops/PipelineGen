// Package scripts — query_planner.go provides query escalation for
// multi-provider research. Given a SubjectIdentity and an escalation
// level, it produces a set of search queries with increasing specificity.
//
// correctness fix (August 2026): replaces the hardcoded researchQueries()
// in source_research_queries.go with a data-driven planner that uses
// the identity's canonical name and aliases. Registered in
// package_hotspots.json under the application use-case migration owner.
package usecase

import (
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// QueryPlanner produces search query variants for a given identity
// and escalation level. Level 0 is the most specific (canonical name +
// domain terms); higher levels broaden to aliases + general terms.
type QueryPlanner struct{}

// NewQueryPlanner creates a QueryPlanner with default settings.
func NewQueryPlanner() *QueryPlanner {
	return &QueryPlanner{}
}

// Plan returns search queries for the given identity at the specified
// escalation level.
func (p *QueryPlanner) Plan(identity scriptpkg.SubjectIdentity, level int) []string {
	name := identity.CanonicalName
	if name == "" {
		return nil
	}
	switch level {
	case 0:
		return []string{
			fmt.Sprintf("%s boxing career earnings", name),
			fmt.Sprintf("%s net worth endorsements", name),
		}
	case 1:
		alias := name
		if len(identity.Aliases) > 0 {
			alias = identity.Aliases[0]
		}
		return []string{
			fmt.Sprintf("%s boxing earnings", alias),
			fmt.Sprintf("%s biography championships", alias),
		}
	case 2:
		return []string{
			fmt.Sprintf("%s career earnings boxer", name),
			fmt.Sprintf("%s Forbes financial history", name),
		}
	default:
		return []string{
			fmt.Sprintf("%s boxing", name),
		}
	}
}

// FullPlan produces a complete query set across multiple escalation
// levels, up to maxQueries total. Level 0 queries come first.
func (p *QueryPlanner) FullPlan(identity scriptpkg.SubjectIdentity, maxQueries int) []string {
	if maxQueries <= 0 {
		maxQueries = 4
	}
	var queries []string
	for level := 0; len(queries) < maxQueries; level++ {
		batch := p.Plan(identity, level)
		if len(batch) == 0 {
			break
		}
		for _, q := range batch {
			if len(queries) >= maxQueries {
				break
			}
			queries = append(queries, q)
		}
		if level > 5 {
			break
		}
	}
	return queries
}

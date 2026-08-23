// Package scripts — query_planner.go provides query escalation for
// multi-provider research. Given a SubjectIdentity and an escalation
// level, it produces a set of search queries with increasing specificity.
//
// correctness fix (August 2026): replaces the hardcoded researchQueries()
// in source_research_queries.go with a data-driven planner that uses
// the identity's canonical name and aliases. Registered in
// package_hotspots.json under the application use-case migration owner.
//
// Metric-aware (August 2026): when a ranking metric is resolved
// (estimated_net_worth, career_earnings, annual_earnings, fight_purse,
// sports_achievement), the planner emits query templates oriented to that
// metric — e.g. "Canelo Álvarez estimated net worth" instead of the generic
// "Canelo Álvarez boxing career earnings" — so the evidence gathered is
// actually comparable against the requested ranking criterion.
package usecase

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// QueryPlanner produces search query variants for a given identity
// and escalation level. Level 0 is the most specific (canonical name +
// domain terms); higher levels broaden to aliases + general terms.
type QueryPlanner struct {
	metric scriptpkg.RankingMetric
}

// NewQueryPlanner creates a QueryPlanner with default settings
// (metric-agnostic templates). Callers that know their ranking metric
// should use NewQueryPlannerForMetric instead.
func NewQueryPlanner() *QueryPlanner {
	return &QueryPlanner{}
}

// NewQueryPlannerForMetric creates a QueryPlanner whose query templates
// are oriented to the given ranking metric.
func NewQueryPlannerForMetric(metric scriptpkg.RankingMetric) *QueryPlanner {
	return &QueryPlanner{metric: metric}
}

// WithMetric returns a planner configured for the given metric.
func (p *QueryPlanner) WithMetric(metric scriptpkg.RankingMetric) *QueryPlanner {
	if p != nil {
		p.metric = metric
	}
	return p
}

// Plan returns search queries for the given identity at the specified
// escalation level. Templates use {name} (canonical) and {alias} (first
// alias) placeholders.
func (p *QueryPlanner) Plan(identity scriptpkg.SubjectIdentity, level int) []string {
	name := identity.CanonicalName
	if name == "" {
		return nil
	}
	metric := p.metric
	if metric == "" {
		metric = scriptpkg.RankingMetricGeneric
	}
	alias := name
	if len(identity.Aliases) > 0 {
		alias = identity.Aliases[0]
	}
	replacer := strings.NewReplacer("{name}", name, "{alias}", alias)
	templates := metricQueryTemplates(metric)
	if level < len(templates) {
		queries := make([]string, 0, len(templates[level]))
		for _, template := range templates[level] {
			queries = append(queries, replacer.Replace(template))
		}
		return queries
	}
	return []string{replacer.Replace(metricFallbackQuery(metric))}
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

// metricQueryTemplates returns escalation-level query templates per
// ranking metric. Levels 0..2 hold two templates each. Templates are
// subject-agnostic: they never hardcode a domain word (e.g. "boxing"),
// so research works for athletes, tennis players, countries, actors,
// etc. Known boxers still research cleanly because their identities
// carry domain terms in the SubjectIdentity registry.
func metricQueryTemplates(metric scriptpkg.RankingMetric) [][]string {
	switch metric {
	case scriptpkg.RankingMetricEstimatedNetWorth:
		return [][]string{
			{"{name} estimated net worth", "{name} net worth"},
			{"{name} net worth Forbes", "{name} wealth assets businesses"},
			{"{name} estimated fortune", "{name} how much is {name} worth"},
		}
	case scriptpkg.RankingMetricCareerEarnings:
		return [][]string{
			{"{name} career earnings", "{name} total earnings"},
			{"{name} earnings", "{name} career income"},
			{"{name} earnings history", "{name} career earnings record"},
		}
	case scriptpkg.RankingMetricAnnualEarnings:
		return [][]string{
			{"{name} highest paid", "{name} annual earnings"},
			{"{name} earnings per year", "{name} annual salary"},
			{"{name} single year earnings", "{name} highest paid athlete"},
		}
	case scriptpkg.RankingMetricFightPurse:
		return [][]string{
			{"{name} fight purse", "{name} payday"},
			{"{name} guaranteed purse", "{name} purse history"},
			{"{name} purse per fight", "{name} fight purse history"},
		}
	case scriptpkg.RankingMetricSportsAchievement:
		return [][]string{
			{"{name} career achievements", "{name} championships"},
			{"{name} hall of fame", "{name} legacy"},
			{"{name} greatest", "{name} career history"},
		}
	case scriptpkg.RankingMetricMostInfluential:
		return [][]string{
			{"{name} influence", "{name} cultural impact"},
			{"{name} legacy beyond sport", "{name} icon"},
			{"{name} impact", "{name} influence on the sport"},
		}
	case scriptpkg.RankingMetricMostControversial:
		return [][]string{
			{"{name} controversy", "{name} scandal"},
			{"{name} controversial moments", "{name} polarizing"},
			{"{name} banned", "{name} infamous"},
		}
	default:
		return [][]string{
			{"{name} biography", "{name} career"},
			{"{alias} biography", "{alias} career achievements"},
			{"{name} Wikipedia", "{name} profile"},
		}
	}
}

// metricFallbackQuery is the single query used for escalation levels
// beyond the template table (levels 3+).
func metricFallbackQuery(metric scriptpkg.RankingMetric) string {
	switch metric {
	case scriptpkg.RankingMetricEstimatedNetWorth:
		return "{name} net worth"
	case scriptpkg.RankingMetricCareerEarnings:
		return "{name} earnings"
	case scriptpkg.RankingMetricAnnualEarnings:
		return "{name} annual earnings"
	case scriptpkg.RankingMetricFightPurse:
		return "{name} purse"
	case scriptpkg.RankingMetricSportsAchievement:
		return "{name} achievements"
	case scriptpkg.RankingMetricMostInfluential:
		return "{name} influence"
	case scriptpkg.RankingMetricMostControversial:
		return "{name} controversy"
	default:
		return "{name}"
	}
}

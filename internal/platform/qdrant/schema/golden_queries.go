// Package schema — golden_queries.go: the canonical golden query set and the
// reproducibility certification for the media_assets_v4 rebuild
// (PR-HASH-SEMANTICS item 14, August 2026).
//
// Before the alias switch promotes a rebuilt collection, the same query must
// return the same ordered top-k across repeated executions against the same
// collection. Relevance classes (not hard-coded IDs) are stored alongside
// each query so the certification can be re-scored as content evolves.
package schema

import (
	"fmt"
	"strings"
)

// GoldenQueryRunCount is the canonical number of repeated executions used by
// the reproducibility certification (item 14: "10 volte la stessa query").
const GoldenQueryRunCount = 10

// GoldenQueryTopK is the canonical result width asserted by the
// certification (item 14: "top10 IDs devono essere identici").
const GoldenQueryTopK = 10

// GoldenQuery is one canonical relevance probe. ExpectedClasses is advisory
// (relevance classes, not hard-coded IDs) so the certification stays valid as
// the catalog evolves.
type GoldenQuery struct {
	Text string

	// ExpectedClasses are the relevance classes the top results should
	// belong to (advisory, for operator scoring — not part of the
	// deterministic-top-k check).
	ExpectedClasses []string
}

// CanonicalGoldenQueries returns the canonical golden query set used to
// certify a media_assets rebuild (item 14). Order is stable.
func CanonicalGoldenQueries() []GoldenQuery {
	return []GoldenQuery{
		{Text: "Jackie Chan interview", ExpectedClasses: []string{"interview", "celebrity"}},
		{Text: "Tom Holland interview", ExpectedClasses: []string{"interview", "celebrity"}},
		{Text: "Adam Sandler interview", ExpectedClasses: []string{"interview", "celebrity"}},
		{Text: "love comedy", ExpectedClasses: []string{"romance", "comedy"}},
		{Text: "boxing interview", ExpectedClasses: []string{"sports", "interview"}},
	}
}

// CertifyGoldenDeterminism checks that every golden query returned the same
// ordered top-k IDs across every run. results[queryIndex][runIndex] is the
// ordered list of IDs for that query/run. It fails closed on the first
// non-deterministic query and requires at least two runs per query.
func CertifyGoldenDeterminism(results [][][]string) error {
	if len(results) == 0 {
		return fmt.Errorf("golden certification: no query results")
	}
	for qi, queryRuns := range results {
		if len(queryRuns) < 2 {
			return fmt.Errorf("golden certification: query %d needs at least 2 runs, got %d", qi, len(queryRuns))
		}
		first := strings.Join(queryRuns[0], "\x00")
		for ri := 1; ri < len(queryRuns); ri++ {
			if strings.Join(queryRuns[ri], "\x00") != first {
				return fmt.Errorf("golden certification: query %d run %d differs from run 0", qi, ri)
			}
		}
	}
	return nil
}

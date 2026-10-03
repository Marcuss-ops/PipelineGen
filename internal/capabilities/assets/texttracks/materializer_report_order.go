package texttracks

import "sort"

// normalizeReportOrder reorders the report's language slices into the
// canonical candidate order after the parallel fan-out.
//
// WHY IT EXISTS (A2, October 2026): the parallel per-language fan-out
// appends to CreatedLanguages / SkippedLanguages / RetranslatedLanguages in
// goroutine-COMPLETION order, which varies with translator latency run to
// run. These lists flow into job results and aggregated reports, so the
// same input must always produce byte-identical report content — the
// historical sequential path already appended in candidate order, and this
// helper makes the parallel path match it exactly.
//
// Languages not present in the candidate list (defensive: none should
// exist) keep a stable relative order at the end, so the function never
// silently drops anything. FailedLanguages is a map — key order is
// irrelevant by construction.
//
// This lives in its own file because materializer.go sits near the
// godlike/08 strict line-count cap (the index seam was split out for the
// same reason).
func normalizeReportOrder(candidates []string, report *MaterializationReport) {
	if report == nil {
		return
	}
	rank := make(map[string]int, len(candidates))
	for i, lang := range candidates {
		rank[lang] = i
	}
	// unknownRank sorts non-candidate languages after every candidate while
	// keeping their relative (append) order via sort.SliceStable.
	unknownRank := len(candidates)
	stable := func(list []string) {
		sort.SliceStable(list, func(i, j int) bool {
			ri, iok := rank[list[i]]
			if !iok {
				ri = unknownRank
			}
			rj, jok := rank[list[j]]
			if !jok {
				rj = unknownRank
			}
			return ri < rj
		})
	}
	stable(report.CreatedLanguages)
	stable(report.SkippedLanguages)
	stable(report.RetranslatedLanguages)
}

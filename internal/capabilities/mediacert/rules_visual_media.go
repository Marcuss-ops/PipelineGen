package mediacert

import "fmt"

// ruleImageFanout certifies the image target policy. Query counts are
// observations, not gates: providers return different result counts and one
// entity may legitimately use several queries. The gate is the explicit
// image target: when a plan asks for at least one image per segment, a
// non-stock segment with no candidate fails, and a job with zero images can
// never be certified. Stock-bound scenes remain governed by the separate
// STOCK ISOLATION rule.
func ruleImageFanout(spec Spec, result MediaResult) CheckResult {
	expected := segmentByID(spec)
	total, pass := 0, 0
	var violations []Violation
	for _, seg := range result.Segments {
		if exp, ok := expected[seg.SegmentID]; ok && exp.StockBound {
			continue
		}
		total++
		n := len(seg.Assets.SecondaryImages) + len(seg.Assets.GeneratedImages) + len(seg.Assets.Candidates)
		if spec.ImagesPerSegment > 0 && n == 0 {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("images available = 0, target at least %d", spec.ImagesPerSegment),
			})
			continue
		}
		pass++
	}
	if total == 0 {
		return passBool(CheckImageFanout, spec.ImagesPerSegment <= 0)
	}
	return passCount(CheckImageFanout, pass, total, violations...)
}

// ruleStockIsolation certifies the clip/stock separation positively: a
// segment declared stock_bound in the spec (a caller stock folder binding)
// must carry NO provider video winner and NO provider image candidates.
// Its media comes from the binding, never from discovery. Segments not
// declared stock_bound are not constrained by this rule.
func ruleStockIsolation(spec Spec, result MediaResult) CheckResult {
	expected := segmentByID(spec)
	total, pass := 0, 0
	var violations []Violation
	for _, seg := range result.Segments {
		exp, ok := expected[seg.SegmentID]
		if !ok || !exp.StockBound {
			continue
		}
		total++
		if winner := winnerOf(seg.Assets); winner != nil {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckStockIsolation),
				Detail:    fmt.Sprintf("stock-bound segment carries provider winner asset %q (provider %q)", winner.AssetID, winner.Provider),
			})
		}
		if n := len(seg.Assets.SecondaryImages) + len(seg.Assets.GeneratedImages) + len(seg.Assets.Candidates); n > 0 {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckStockIsolation),
				Detail:    fmt.Sprintf("stock-bound segment carries %d provider image candidate(s)", n),
			})
		}
		if allForSegment(violations, seg.SegmentID) == 0 {
			pass++
		}
	}
	if total == 0 {
		return passBool(CheckStockIsolation, true)
	}
	return passCount(CheckStockIsolation, pass, total, violations...)
}

// allForSegment counts how many violations already belong to a segment.
func allForSegment(violations []Violation, segID string) int {
	n := 0
	for _, v := range violations {
		if v.SegmentID == segID {
			n++
		}
	}
	return n
}

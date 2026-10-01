package mediacert

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ruleImageFanout verifies one image query per entity and the configured
// maximum image count per segment. It never requires fabricated queries for
// entities that the extractor did not find.
func ruleImageFanout(spec Spec, result MediaResult) CheckResult {
	expected := segmentByID(spec)
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		nQueries := len(seg.Insights.ImageQueries)
		nEnts := 0
		for _, entity := range seg.Insights.Entities {
			if script.IsAnnotationEntityKind(script.NormalizeAnnotationType(entity.Type)) {
				nEnts++
			}
		}
		if spec.EntitiesPerSegment > 0 && nQueries > spec.EntitiesPerSegment {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("image queries = %d, maximum one per entity (%d)", nQueries, spec.EntitiesPerSegment),
			})
		}
		if nEnts > 0 && nQueries != nEnts {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("image queries = %d but entities = %d (fanout mismatch)", nQueries, nEnts),
			})
		}
		// A stock-bound scene takes its visuals from the caller's direct
		// stock binding, so the image budget certifies nothing there: the
		// STOCK ISOLATION rule owns that scene's zero-provider contract.
		if expected[seg.SegmentID].StockBound {
			if allForSegment(violations, seg.SegmentID) == 0 {
				pass++
			}
			continue
		}
		if nImgs := len(seg.Assets.SecondaryImages) + len(seg.Assets.GeneratedImages); spec.ImagesPerSegment > 0 && nImgs < spec.ImagesPerSegment {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("images available = %d, expected at least %d", nImgs, spec.ImagesPerSegment),
			})
		}
		if allForSegment(violations, seg.SegmentID) == 0 {
			pass++
		}
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
		if n := len(seg.Assets.SecondaryImages) + len(seg.Assets.GeneratedImages); n > 0 {
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

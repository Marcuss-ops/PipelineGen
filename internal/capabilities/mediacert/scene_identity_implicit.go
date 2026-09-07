package mediacert

import (
	"fmt"
	"strings"
)

// ruleImplicitSceneIdentity certifies scene IDs when the caller did not
// declare an authored SegmentsExpected contract. Generated IDs are allowed to
// use any stable spelling, but they must be non-empty, unique, and preserve
// the canonical result order through Position.
func ruleImplicitSceneIdentity(result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	seen := make(map[string]struct{}, total)
	var violations []Violation
	for i, seg := range result.Segments {
		id := strings.TrimSpace(seg.SegmentID)
		if id == "" {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckSceneIdentity),
				Detail:    "generated segment_id is empty",
			})
			continue
		}
		if _, exists := seen[id]; exists {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckSceneIdentity),
				Detail:    fmt.Sprintf("generated segment_id %q is duplicated", id),
			})
			continue
		}
		seen[id] = struct{}{}
		if seg.Position != i {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckSceneIdentity),
				Detail:    fmt.Sprintf("generated segment_id %q has position %d, expected %d", id, seg.Position, i),
			})
			continue
		}
		pass++
	}
	return passCount(CheckSceneIdentity, pass, total, violations...)
}

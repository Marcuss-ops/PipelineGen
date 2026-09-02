// Package mediacert — rules_cross_scene.go: asset-reuse, provider-policy and
// cross-contamination rules. Split out of rules.go (2026-09-02) to keep the
// rule surface under the strict 600-LOC cap.
package mediacert

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
)

// ruleCrossSceneReuse is the canonical rule for cross-scene asset reuse.
func ruleCrossSceneReuse(spec Spec, result MediaResult) CheckResult {
	if spec.AllowCrossSceneAssetReuse {
		return passBool(CheckCrossSceneReuse, true)
	}
	seen := make(map[string]string)
	var violations []Violation
	reuseCount := 0
	totalSegments := len(result.Segments)
	for _, seg := range result.Segments {
		reused := false
		for _, c := range candidatesOf(seg.Assets) {
			id := strings.TrimSpace(c.AssetID)
			if id == "" {
				continue
			}
			if first, ok := seen[id]; ok && first != seg.SegmentID {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckCrossSceneReuse),
					Detail:    fmt.Sprintf("asset %q already bound to segment %q (reuse forbidden)", id, first),
				})
				reused = true
			} else {
				seen[id] = seg.SegmentID
			}
		}
		if !reused {
			reuseCount++
		}
	}
	return passCount(CheckCrossSceneReuse, reuseCount, totalSegments, violations...)
}

// ruleProviderPolicy verifies only spec-allowed VIDEO providers are used.
func ruleProviderPolicy(spec Spec, result MediaResult) CheckResult {
	allowed := strings.ToLower(strings.TrimSpace(spec.VideoProvider))
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		bad := false
		if seg.Assets.PrimaryVideo != nil {
			p := strings.ToLower(strings.TrimSpace(seg.Assets.PrimaryVideo.Provider))
			if allowed != "" && p != "" && p != allowed {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckProviderPolicy),
					Detail:    fmt.Sprintf("primary video %q provider %q not allowed (expected %q)", seg.Assets.PrimaryVideo.AssetID, seg.Assets.PrimaryVideo.Provider, allowed),
				})
				bad = true
			}
		}
		if !bad {
			pass++
		}
	}
	return passCount(CheckProviderPolicy, pass, total, violations...)
}

// ruleCrossContamination is the umbrella check that summarizes the
// query-ownership + asset-ownership + cross-scene-reuse results.
func ruleCrossContamination(spec Spec, result MediaResult) CheckResult {
	q := ruleQueryOwnership(spec, result)
	a := ruleAssetOwnership(spec, result)
	r := ruleCrossSceneReuse(spec, result)
	total := len(q.Violations) + len(a.Violations) + len(r.Violations)
	passed := total == 0
	return passBool(CheckCrossContamination, passed, append(append(append([]Violation{}, q.Violations...), a.Violations...), r.Violations...)...)
}

// sceneirUnused keeps the sceneir import alive for the ResultSegment.SceneIR
// field accessors even when no rule directly calls a sceneir function (the
// field type is *sceneir.SceneIR).
var _ = sceneir.SceneIR{}

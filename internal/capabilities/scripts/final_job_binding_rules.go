package scriptgeneration

import (
	"fmt"
	"strings"
)

// validateFinalJobPlanBindingRules mirrors the admission surface the remote
// assembler applies to a compiled V2 plan, so a plan the worker would reject is
// rejected in this process instead. The rules were confirmed against the worker
// contract on 2026-11-01:
//
//   - assets[] must carry unique ids, and two assets may not share one
//     asset_key: the resolver rejects a second id for the same content;
//   - two assets may not certify the same bytes, because the plan refuses two
//     assets with one canonical identity;
//   - several segments MAY reference one asset. The placement lives on the
//     segment — unique segment_id, own timeline_start_frame and frame_count —
//     so a repeated window is one artifact referenced twice.
//
// The last rule is why identical work is canonicalised by sha256 before this
// check runs: the plan is only correct when every distinct byte-set has exactly
// one asset, whatever number of placements point at it.
func validateFinalJobPlanBindingRules(plan finalJobCompiledPlan) error {
	byID := make(map[string]finalJobPlanAsset, len(plan.Assets))
	byKey := make(map[string]string, len(plan.Assets))
	bySHA := make(map[string]string, len(plan.Assets))
	for _, asset := range plan.Assets {
		id := strings.TrimSpace(asset.AssetID)
		if id == "" {
			return fmt.Errorf("final_job plan asset without an id")
		}
		if _, exists := byID[id]; exists {
			return fmt.Errorf("final_job plan assets repeat id %q", id)
		}
		byID[id] = asset
		key := strings.TrimSpace(asset.AssetKey)
		if key == "" {
			key = "sha256:" + strings.ToLower(asset.SHA256)
		}
		if owner, exists := byKey[key]; exists {
			return fmt.Errorf("final_job plan assets %q and %q share one asset key %q", owner, id, key)
		}
		byKey[key] = id
		if sha := strings.ToLower(strings.TrimSpace(asset.SHA256)); sha != "" {
			if owner, exists := bySHA[sha]; exists {
				return fmt.Errorf("final_job plan assets %q and %q certify the same bytes %s", owner, id, sha)
			}
			bySHA[sha] = id
		}
	}
	seenSegment := make(map[string]struct{}, len(plan.Assets))
	for _, track := range plan.VideoTracks {
		for _, segment := range track.Segments {
			id := strings.TrimSpace(segment.SegmentID)
			if id == "" {
				return fmt.Errorf("final_job plan segment without an id")
			}
			if _, exists := seenSegment[id]; exists {
				return fmt.Errorf("final_job plan segments repeat id %q", id)
			}
			seenSegment[id] = struct{}{}
			asset, ok := byID[segment.AssetID]
			if !ok {
				return fmt.Errorf("final_job plan segment %q references unknown asset %q", id, segment.AssetID)
			}
			if !strings.EqualFold(strings.TrimSpace(asset.SHA256), strings.TrimSpace(segment.SHA256)) {
				return fmt.Errorf("final_job plan segment %q does not certify its asset %q", id, asset.AssetID)
			}
			if segment.FrameCount <= 0 {
				return fmt.Errorf("final_job plan segment %q has no frame span", id)
			}
		}
	}
	return nil
}

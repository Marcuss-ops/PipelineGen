package publish

import (
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

func buildPublishPlan(groupPlans []core.ClipPlan, result core.CutBatchResult, batchID, sourceID string, rootFolderName, resolvedFolderID, timestampGroupName string, in *core.RunInput, segmentCounts map[string]int) (publishPlan, []string, error) {
	plan := publishPlan{}
	paths := make([]string, 0, len(groupPlans))
	for clipIdx, clipPlan := range groupPlans {
		item := result.Items[clipIdx]
		if item.Status == core.CutItemStatusFailed || item.OutputPath == "" || item.SHA256Hex == "" {
			continue
		}
		leafName := timestampGroupName
		if in != nil && len(in.Clips) > 0 {
			leafName = stockClipFolderName(in, clipPlan, timestampGroupName)
		}
		segmentCounts[leafName]++
		filename := fmt.Sprintf("clip_%03d.mp4", segmentCounts[leafName])
		plan.Cuts = append(plan.Cuts, publishCutPlan{ClipIndex: clipIdx, Plan: clipPlan, Item: item, Artifact: finalization.VerifiedArtifact{
			ArtifactID: clipPlan.OutputLogicalID, Kind: finalization.KindVideo, Filename: filename, MIMEType: "video/mp4",
			LocalPath: item.OutputPath, SizeBytes: item.SizeBytes, SHA256: item.SHA256Hex,
			Requirement: finalization.ArtifactRequirementRequired, IdempotencyKey: clipPlan.OutputLogicalID + ":" + item.SHA256Hex,
			Description: clipPlan.Description, RootFolderName: rootFolderName, ResolvedFolderID: resolvedFolderID,
			RootFolderResolved: in != nil && in.DriveFolderResolved, PathLeafName: leafName,
		}})
		paths = append(paths, item.OutputPath)
	}
	sort.SliceStable(plan.Cuts, func(i, j int) bool { return plan.Cuts[i].ClipIndex < plan.Cuts[j].ClipIndex })
	return plan, paths, nil
}

package stockpipeline

import "testing"

func TestBuildPublishPlanOrdersCutsDeterministically(t *testing.T) {
	plans, paths, err := buildPublishPlan(
		[]ClipPlan{{OutputLogicalID: "b"}, {OutputLogicalID: "a"}},
		CutBatchResult{Items: []CutItemResult{{Status: CutItemStatusSucceeded, OutputPath: "/tmp/b.mp4", SHA256Hex: "b"}, {Status: CutItemStatusSucceeded, OutputPath: "/tmp/a.mp4", SHA256Hex: "a"}}},
		"batch", "source", "root", "folder", "group", nil, map[string]int{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans.Cuts) != 2 || plans.Cuts[0].ClipIndex != 0 || plans.Cuts[1].ClipIndex != 1 {
		t.Fatalf("cuts = %+v", plans.Cuts)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
}

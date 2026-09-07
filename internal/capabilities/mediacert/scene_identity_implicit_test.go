package mediacert

import "testing"

func TestCertifyImplicitSceneIdentityAcceptsGeneratedSceneIDs(t *testing.T) {
	result := MediaResult{Segments: []ResultSegment{
		{SegmentID: "scene-0", Position: 0},
		{SegmentID: "scene-1", Position: 1},
	}}
	report := Certify(Spec{}, result)
	for _, check := range report.Checks {
		if check.Name == CheckSceneIdentity {
			if !check.Passed {
				t.Fatalf("implicit scene identity failed: %+v", check.Violations)
			}
			return
		}
	}
	t.Fatal("scene identity check missing")
}

func TestImplicitSceneIdentityRejectsDuplicateIDs(t *testing.T) {
	result := MediaResult{Segments: []ResultSegment{
		{SegmentID: "scene-0", Position: 0},
		{SegmentID: "scene-0", Position: 1},
	}}
	check := ruleImplicitSceneIdentity(result)
	if check.Passed {
		t.Fatal("duplicate generated segment IDs must fail")
	}
}

func TestExplicitSceneIdentityRemainsStrict(t *testing.T) {
	spec := Spec{SegmentsExpected: []SpecSegment{{ID: "authored-a"}, {ID: "authored-b"}}}
	result := MediaResult{Segments: []ResultSegment{
		{SegmentID: "scene-0", Position: 0},
		{SegmentID: "scene-1", Position: 1},
	}}
	check := ruleSceneIdentity(spec, result)
	if check.Passed {
		t.Fatal("authored segment IDs must not be rewritten to generated scene IDs")
	}
}

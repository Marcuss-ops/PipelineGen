package model

import (
	"strings"
	"testing"
)

func validFamilyPackageRef() *PreparedRenderPackageRef {
	return &PreparedRenderPackageRef{
		PackageID: strings.Repeat("a", 64), PlanSHA256: strings.Repeat("b", 64),
		AssetManifestSHA: strings.Repeat("c", 64), TotalFrames: 200, CompilerVersion: "chronon-3.4.1",
		ProfileID: "velox-h264-1080p30-v1",
	}
}

func TestValidateJobBatchRequiresOnePreparedPackageForEveryChunk(t *testing.T) {
	ref := validFamilyPackageRef()
	base := []Job{
		{ID: "anchor"},
		{ID: "chunk-1", ParentJobID: "anchor", ChunkIndex: 0, FrameRange: &FrameRange{Start: 0, End: 100}, PreparedRenderPackage: ref},
		{ID: "chunk-2", ParentJobID: "anchor", ChunkIndex: 1, FrameRange: &FrameRange{Start: 100, End: 200}, PreparedRenderPackage: ref},
	}
	if err := ValidateJobBatch(base); err != nil {
		t.Fatalf("same package family rejected: %v", err)
	}

	missing := []Job{base[0], base[1], {ID: "chunk-2", ParentJobID: "anchor"}}
	if err := ValidateJobBatch(missing); err == nil {
		t.Fatal("family with a missing prepared package reference must be rejected")
	}

	other := *ref
	other.PackageID = strings.Repeat("d", 64)
	conflicting := []Job{base[0], base[1], {ID: "chunk-2", ParentJobID: "anchor", PreparedRenderPackage: &other}}
	if err := ValidateJobBatch(conflicting); err == nil {
		t.Fatal("family with conflicting prepared package references must be rejected")
	}
}

func TestValidateJobBatchPreparedFamilyRequiresOrderedContiguousRanges(t *testing.T) {
	ref := validFamilyPackageRef()
	base := []Job{
		{ID: "anchor"},
		{ID: "chunk-1", ParentJobID: "anchor", ChunkIndex: 0, FrameRange: &FrameRange{Start: 0, End: 100}, PreparedRenderPackage: ref},
		{ID: "chunk-2", ParentJobID: "anchor", ChunkIndex: 1, FrameRange: &FrameRange{Start: 100, End: 200}, PreparedRenderPackage: ref},
	}
	cases := []struct {
		name   string
		mutate func([]Job) []Job
	}{
		{"duplicate chunk index", func(jobs []Job) []Job { jobs[2].ChunkIndex = 0; return jobs }},
		{"missing chunk index", func(jobs []Job) []Job { jobs[2].ChunkIndex = 2; return jobs }},
		{"different parent", func(jobs []Job) []Job { jobs[2].ParentJobID = "other"; return jobs }},
		{"frame gap", func(jobs []Job) []Job { jobs[2].FrameRange.Start++; return jobs }},
		{"overlap", func(jobs []Job) []Job { jobs[2].FrameRange.Start--; return jobs }},
		{"nonzero family start", func(jobs []Job) []Job { jobs[1].FrameRange.Start = 1; return jobs }},
		{"missing range", func(jobs []Job) []Job { jobs[2].FrameRange = nil; return jobs }},
		{"incomplete family tail", func(jobs []Job) []Job { jobs[2].FrameRange.End = 199; return jobs }},
		{"range beyond plan", func(jobs []Job) []Job { jobs[2].FrameRange.End = 201; return jobs }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobs := append([]Job(nil), base...)
			for i := range jobs {
				if jobs[i].FrameRange != nil {
					rangeCopy := *jobs[i].FrameRange
					jobs[i].FrameRange = &rangeCopy
				}
			}
			if err := ValidateJobBatch(tc.mutate(jobs)); err == nil {
				t.Fatal("malformed prepared render family was accepted")
			}
		})
	}
	// Transport order is irrelevant when each chunk carries its immutable
	// index and range. Replays still identify the same ordered family.
	reordered := []Job{base[0], base[2], base[1]}
	if err := ValidateJobBatch(reordered); err != nil {
		t.Fatalf("transport-reordered but identically indexed family rejected: %v", err)
	}
}

func TestValidateJobBatchRejectsPreparedPackageOnAssemblyAnchor(t *testing.T) {
	if err := ValidateJobBatch([]Job{{ID: "anchor", PreparedRenderPackage: validFamilyPackageRef()}}); err == nil {
		t.Fatal("assembly anchor must not reference a package compiled from a different child plan")
	}
}

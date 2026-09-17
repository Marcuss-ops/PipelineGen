package cliprender

import (
	"strings"
	"testing"
)

// chunkTestPlan is a minimal SEALED plan: 10s at 24fps = 240 frames, the
// canonical certification clip's geometry. It is sealed through the production
// Seal(), never with a hand-written digest: Validate() recomputes the plan hash,
// so a fixture that invented one would only test the fixture.
func chunkTestPlan(t *testing.T) ClipRenderPlanV1 {
	t.Helper()
	plan := ClipRenderPlanV1{
		Version:    PlanVersion,
		RunID:      "job_test_chunk",
		DurationMS: 10_000,
		Source: PlanSource{
			AssetID: "yt_test",
			Path:    "/tmp/source.mp4",
			SHA256:  strings.Repeat("a", 64),
		},
		Output: PlanOutput{
			ContractID:  "VELOX_ASSEMBLY_READY_V1",
			Container:   "mp4",
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       1920,
			Height:      1080,
			FPSNum:      24,
			FPSDen:      1,
		},
		Audio:      PlanAudio{Mode: AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: "/tmp/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatalf("seal fixture plan: %v", err)
	}
	return plan
}

// resealed applies a mutation and re-seals, so a case exercises the property it
// names instead of tripping over the digest guard that Validate() applies to any
// plan whose content changed after sealing.
func resealed(t *testing.T, plan ClipRenderPlanV1, mutate func(*ClipRenderPlanV1)) ClipRenderPlanV1 {
	t.Helper()
	mutate(&plan)
	if err := plan.Seal(); err != nil {
		t.Fatalf("reseal: %v", err)
	}
	return plan
}

// TestBuildChunkSetIsAnExactPartition is the core acceptance criterion: whatever
// the request, the windows must cover the timeline exactly once.
func TestBuildChunkSetIsAnExactPartition(t *testing.T) {
	plan := chunkTestPlan(t)
	for _, requested := range []int{1, 2, 3, 5, 7, 64} {
		set, err := BuildChunkSet(plan, requested, 48)
		if err != nil {
			t.Fatalf("requested %d: %v", requested, err)
		}
		var covered int64
		for i, c := range set.Chunks {
			if c.StartFrame%48 != 0 {
				t.Fatalf("requested %d: chunk %d starts at %d, off the keyframe alignment", requested, i, c.StartFrame)
			}
			if i > 0 && c.StartFrame != set.Chunks[i-1].EndFrame {
				t.Fatalf("requested %d: chunk %d starts at %d after %d (gap or overlap)",
					requested, i, c.StartFrame, set.Chunks[i-1].EndFrame)
			}
			covered += c.FrameCount()
		}
		if covered != set.TotalFrames {
			t.Fatalf("requested %d: windows cover %d of %d frames", requested, covered, set.TotalFrames)
		}
		if last := set.Chunks[len(set.Chunks)-1]; last.EndFrame != 240 {
			t.Fatalf("requested %d: last window ends at %d, want 240", requested, last.EndFrame)
		}
		if set.WindowCount() > requested {
			t.Fatalf("requested %d: produced %d windows", requested, set.WindowCount())
		}
	}
}

// TestBuildChunkSetIsDeterministic pins the identity property the retry rule
// depends on: the same plan and request must produce the same windows AND the
// same job ids, because a retry that mints a new id renders the same frames
// twice.
func TestBuildChunkSetIsDeterministic(t *testing.T) {
	plan := chunkTestPlan(t)
	first, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Chunks) != len(second.Chunks) {
		t.Fatalf("window counts differ: %d vs %d", len(first.Chunks), len(second.Chunks))
	}
	for i := range first.Chunks {
		if first.Chunks[i] != second.Chunks[i] {
			t.Fatalf("chunk %d differs between two identical builds: %+v vs %+v",
				i, first.Chunks[i], second.Chunks[i])
		}
	}
	if first.AnchorJobID() != second.AnchorJobID() {
		t.Fatalf("anchor id is not stable: %q vs %q", first.AnchorJobID(), second.AnchorJobID())
	}
}

// TestChunkJobIDsAreContentAddressed pins that a window's id is a function of
// the PLAN, not of the run: two runs of the same plan revision must address the
// same windows, and an edited plan must address different ones.
func TestChunkJobIDsAreContentAddressed(t *testing.T) {
	plan := chunkTestPlan(t)
	set, err := BuildChunkSet(plan, 2, 48)
	if err != nil {
		t.Fatal(err)
	}
	// The identity is the plan's CONTENT, not its run: the run id appears
	// nowhere in a window id...
	if strings.Contains(set.Chunks[0].JobID, plan.RunID) {
		t.Fatalf("chunk id %q carries the run id: a retry of the same window must not be a new job", set.Chunks[0].JobID)
	}
	// ... and because RunID is sealed plan content, a different run IS a
	// different plan revision, so it must not reuse a window id (that would
	// make two different renders share one identity).
	renamed := resealed(t, plan, func(p *ClipRenderPlanV1) { p.RunID = "job_other_run" })
	other, err := BuildChunkSet(renamed, 2, 48)
	if err != nil {
		t.Fatal(err)
	}
	if other.Chunks[0].JobID == set.Chunks[0].JobID {
		t.Fatalf("two different plan revisions share the window id %q", set.Chunks[0].JobID)
	}

	// A different plan content address: different ids. (Sealed through the
	// production path, so the digest really is that of edited content.)
	edited := resealed(t, plan, func(p *ClipRenderPlanV1) { p.DurationMS = 5_000 })
	editedSet, err := BuildChunkSet(edited, 2, 48)
	if err != nil {
		t.Fatal(err)
	}
	if editedSet.Chunks[0].JobID == set.Chunks[0].JobID {
		t.Fatal("an edited plan reused a chunk id: a retry would collide with different content")
	}
	if editedSet.AnchorJobID() == set.AnchorJobID() {
		t.Fatal("an edited plan reused the anchor id")
	}

	// The version is part of the identity: a chunk contract change mints new
	// ids instead of re-pointing old ones at a new meaning.
	if !strings.Contains(set.Chunks[0].JobID, ChunkContractVersion) {
		t.Fatalf("chunk id %q does not carry the contract version", set.Chunks[0].JobID)
	}
	if !strings.HasPrefix(set.AnchorJobID(), "anchor-") || !strings.HasPrefix(set.Chunks[0].JobID, "chunk-") {
		t.Fatalf("ids must make their role visible: %q / %q", set.AnchorJobID(), set.Chunks[0].JobID)
	}
}

// TestBuildChunkSetReportsWhatItCouldNotDo pins the honest failure of a request
// the timeline cannot satisfy: a 10-frame clip cannot be cut into five aligned
// windows, and the set says so instead of inventing unaligned boundaries.
func TestBuildChunkSetReportsWhatItCouldNotDo(t *testing.T) {
	plan := resealed(t, chunkTestPlan(t), func(p *ClipRenderPlanV1) { p.DurationMS = 417 }) // 10 frames at 24fps
	set, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	if set.TotalFrames != 10 {
		t.Fatalf("frame count %d, want 10", set.TotalFrames)
	}
	if set.WindowCount() != 1 {
		t.Fatalf("window count %d, want 1: the clip is shorter than one keyframe interval", set.WindowCount())
	}
	if set.MaximumParallelWindows() != 1 {
		t.Fatalf("maximum parallel windows %d, want 1", set.MaximumParallelWindows())
	}

	// A tail shorter than the alignment becomes its own last window rather than
	// being folded into a neighbour: 250 frames at 48 is 5 full windows + 10.
	plan = resealed(t, plan, func(p *ClipRenderPlanV1) { p.DurationMS = 250 * 1000 / 24 }) // 250 frames
	set, err = BuildChunkSet(plan, 6, 48)
	if err != nil {
		t.Fatal(err)
	}
	if set.TotalFrames != 250 {
		t.Fatalf("frame count %d, want 250", set.TotalFrames)
	}
	if set.WindowCount() != 6 {
		t.Fatalf("window count %d, want 6", set.WindowCount())
	}
	last := set.Chunks[len(set.Chunks)-1]
	if last.StartFrame != 240 || last.EndFrame != 250 {
		t.Fatalf("tail window = [%d,%d), want [240,250)", last.StartFrame, last.EndFrame)
	}
}

// TestBuildChunkSetRefusesUnknownInputs pins the fail-closed inputs: a chunker
// that guesses the alignment or the timeline produces windows nothing can
// assemble.
func TestBuildChunkSetRefusesUnknownInputs(t *testing.T) {
	plan := chunkTestPlan(t)

	cases := []struct {
		name      string
		plan      ClipRenderPlanV1
		requested int
		alignment int64
	}{
		{"no alignment", plan, 2, 0},
		{"negative alignment", plan, 2, -48},
		{"zero chunks", plan, 0, 48},
		{"negative chunks", plan, -1, 48},
		{"no duration", resealed(t, plan, func(p *ClipRenderPlanV1) { p.DurationMS = 0 }), 2, 48},
		{"no frame rate", resealed(t, plan, func(p *ClipRenderPlanV1) { p.Output.FPSNum = 0 }), 2, 48},
		// A digest that is present but not the content's: the tamper case, which
		// must NOT be re-sealed (sealing would make it consistent again).
		{"forged plan digest", func() ClipRenderPlanV1 {
			p := plan
			p.PlanSHA256 = strings.Repeat("d", 64)
			return p
		}(), 2, 48},
		{"no plan digest", func() ClipRenderPlanV1 { p := plan; p.PlanSHA256 = ""; return p }(), 2, 48},
		{"no run id", func() ClipRenderPlanV1 { p := plan; p.RunID = ""; return p }(), 2, 48},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildChunkSet(tc.plan, tc.requested, tc.alignment); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

// TestChunkSetValidateIsTheAssemblyGate pins that a hand-built (or persisted)
// family cannot pass the gate: the gate is what stands between a plausible list
// of ranges and a concatenated artifact.
func TestChunkSetValidateIsTheAssemblyGate(t *testing.T) {
	plan := chunkTestPlan(t)
	set, err := BuildChunkSet(plan, 3, 48)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("a built set must validate: %v", err)
	}

	broken := []struct {
		name   string
		mutate func(*ChunkSet)
	}{
		{"gap", func(s *ChunkSet) { s.Chunks[1].StartFrame += 48 }},
		{"overlap", func(s *ChunkSet) { s.Chunks[1].StartFrame -= 48 }},
		{"unaligned start", func(s *ChunkSet) { s.Chunks[1].StartFrame += 1; s.Chunks[1].EndFrame += 1 }},
		{"missing tail", func(s *ChunkSet) { s.Chunks = s.Chunks[:len(s.Chunks)-1] }},
		{"short last window", func(s *ChunkSet) { s.Chunks[len(s.Chunks)-1].EndFrame -= 1 }},
		{"empty window", func(s *ChunkSet) { s.Chunks[0].EndFrame = s.Chunks[0].StartFrame }},
		{"reordered", func(s *ChunkSet) { s.Chunks[0], s.Chunks[1] = s.Chunks[1], s.Chunks[0] }},
		{"forged id", func(s *ChunkSet) { s.Chunks[0].JobID = "chunk-forged" }},
		{"no digest", func(s *ChunkSet) { s.PlanSHA256 = "" }},
		{"too many windows", func(s *ChunkSet) { s.RequestedChunks = 2 }},
		{"wrong contract", func(s *ChunkSet) { s.ContractVersion = "clip-chunk.v2" }},
		{"no frames", func(s *ChunkSet) { s.TotalFrames = 0 }},
	}
	for _, tc := range broken {
		t.Run(tc.name, func(t *testing.T) {
			clone := set
			clone.Chunks = append([]Chunk(nil), set.Chunks...)
			tc.mutate(&clone)
			if err := clone.Validate(); err == nil {
				t.Fatal("expected the gate to refuse this family")
			}
		})
	}
}

// TestChunkPlanIsNotEnabledByDefault pins the opt-in property: nothing in the
// library partitions a plan implicitly. The chunker is a pure function a
// producer must call, and the render cache stays whole-clip until the same
// change that submits windows also keys the cache by window.
func TestChunkPlanIsNotEnabledByDefault(t *testing.T) {
	plan := chunkTestPlan(t)
	set, err := BuildChunkSet(plan, 1, 48)
	if err != nil {
		t.Fatal(err)
	}
	if set.WindowCount() != 1 {
		t.Fatalf("a single requested window must stay whole-clip, got %d", set.WindowCount())
	}
	if set.Chunks[0].StartFrame != 0 || set.Chunks[0].EndFrame != set.TotalFrames {
		t.Fatalf("the single window must cover the whole plan, got [%d,%d)",
			set.Chunks[0].StartFrame, set.Chunks[0].EndFrame)
	}
}

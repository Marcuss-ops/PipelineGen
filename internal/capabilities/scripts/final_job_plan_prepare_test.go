package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type preparedSegmentStub struct {
	request FinalJobVideoSegmentRequest
	called  int
	// identical makes every window certify the SAME bytes: two placements of one
	// object, which is what a cache hit on identical work also produces.
	identical bool
}

func (s *preparedSegmentStub) PrepareFinalJobVideoSegment(_ context.Context, source map[string]any, request FinalJobVideoSegmentRequest) (map[string]any, error) {
	s.request = request
	s.called++
	if !s.identical && source["asset_id"] != "stock-raw" {
		return nil, context.Canceled
	}
	return map[string]any{
		"asset_id": "fragment-" + request.SegmentID, "source_uri": "https://objects.example/fragment",
		"role":   "stock",
		"sha256": strings.Repeat("b", 64), "size_bytes": int64(1024),
		"duration_us": int64(2_000_000), "frame_count": int64(48),
		"profile_id": "VELOX_ASSEMBLY_READY_V1", "copy_eligible": true,
		"first_frame_keyframe": true, "closed_gop": true,
		"timeline_revision": request.TimelineRevision, "timeline_sha256": request.TimelineSHA256,
		"timeline_start_frame": request.TimelineStartFrame,
	}, nil
}

// TestPrepareFinalJobVideoSegmentsSharesOneAssetAcrossIdenticalPlacements pins
// the rule the assembler actually enforces: a placement is certified by its
// SEGMENT (unique id, own timeline position) and an artifact by its BYTES. Two
// placements of the same pictures are therefore one asset referenced twice — not
// two objects forced to differ in the container, which is what used to make the
// worker refuse the film (`segment-0017 still certifies the bytes of another
// placement`).
func TestPrepareFinalJobVideoSegmentsSharesOneAssetAcrossIdenticalPlacements(t *testing.T) {
	timelineSHA := strings.Repeat("a", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 4_000_000,
		FinalAudio: finalJobPlanAudio{Mode: "FINAL_AUDIO_COPY", AssetID: "audio-1", SHA256: strings.Repeat("c", 64), SizeBytes: 32, DurationUS: 4_000_000},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{
			{SegmentID: "segment-0000", AssetID: "first-raw", SHA256: strings.Repeat("d", 64), TimelineStartFrame: 0, FrameCount: 48, SourceInUS: 0, SourceDurationUS: 2_000_000},
			{SegmentID: "segment-0001", AssetID: "second-raw", SHA256: strings.Repeat("e", 64), TimelineStartFrame: 48, FrameCount: 48, SourceInUS: 0, SourceDurationUS: 2_000_000},
		}}},
		Assets: []finalJobPlanAsset{
			{AssetID: "audio-1", SHA256: strings.Repeat("c", 64), SizeBytes: 32, Kind: "final_audio", DurationUS: 4_000_000},
			{AssetID: "first-raw", SHA256: strings.Repeat("d", 64), SizeBytes: 2048, Kind: "video", DurationUS: 2_000_000},
			{AssetID: "second-raw", SHA256: strings.Repeat("e", 64), SizeBytes: 2048, Kind: "video", DurationUS: 2_000_000},
		},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	refs := []any{
		map[string]any{"asset_id": "first-raw", "sha256": strings.Repeat("d", 64), "size_bytes": int64(2048), "source_uri": "https://objects.example/first", "role": "stock"},
		map[string]any{"asset_id": "second-raw", "sha256": strings.Repeat("e", 64), "size_bytes": int64(2048), "source_uri": "https://objects.example/second", "role": "stock"},
		map[string]any{"asset_id": "audio-1", "sha256": strings.Repeat("c", 64), "size_bytes": int64(32), "source_uri": "https://objects.example/audio", "role": "final_audio"},
	}
	preparer := &preparedSegmentStub{identical: true}
	prepared, runtimeAssets, err := prepareFinalJobVideoSegments(context.Background(), string(raw), refs, preparer)
	if err != nil {
		t.Fatalf("prepareFinalJobVideoSegments: %v", err)
	}
	if preparer.called != 2 {
		t.Fatalf("preparer calls = %d, want one preparation per placement and no re-encode", preparer.called)
	}
	var got finalJobCompiledPlan
	if err := json.Unmarshal([]byte(prepared), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.VideoTracks[0].Segments) != 2 {
		t.Fatalf("segments = %d, want both placements kept", len(got.VideoTracks[0].Segments))
	}
	first, second := got.VideoTracks[0].Segments[0], got.VideoTracks[0].Segments[1]
	if first.AssetID != second.AssetID {
		t.Fatalf("identical pictures produced two assets %q and %q", first.AssetID, second.AssetID)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("the same pictures must certify the same bytes: %q vs %q", first.SHA256, second.SHA256)
	}
	// The placement lives on the segment: unique id, own timeline position.
	if first.SegmentID == second.SegmentID {
		t.Fatalf("two placements share one segment id %q", first.SegmentID)
	}
	if second.TimelineStartFrame != 48 || second.FrameCount != 48 {
		t.Fatalf("second placement lost its timeline position: %+v", second)
	}
	// Each binding describes its own placement: in-point zero because the
	// fragment is already the window, and the duration of the object it names.
	for _, segment := range got.VideoTracks[0].Segments {
		if segment.SourceInUS != 0 || segment.SourceDurationUS != 2_000_000 {
			t.Fatalf("segment %q has an inexact binding: %+v", segment.SegmentID, segment)
		}
	}
	videos := 0
	for _, asset := range got.Assets {
		if asset.Kind != "video" {
			continue
		}
		videos++
		// A shared asset has no single timeline position to certify.
		if asset.TimelineStartFrame != 0 {
			t.Fatalf("shared asset %q still carries placement %d", asset.AssetID, asset.TimelineStartFrame)
		}
	}
	if videos != 1 {
		t.Fatalf("compiled assets carry %d video objects for one byte-set", videos)
	}
	if len(runtimeAssets) != 2 {
		t.Fatalf("runtime assets = %d, want the shared fragment plus the final audio", len(runtimeAssets))
	}
	// The plan that leaves this process must pass the same admission rules the
	// worker applies, or the film would fail on the far end instead.
	if err := validateFinalJobPlanBindingRules(got); err != nil {
		t.Fatalf("binding rules: %v", err)
	}
}

// TestValidateFinalJobPlanBindingRulesRejectsOneByteSetOnTwoAssets pins the
// class of failure that burned five live runs: two assets certifying the same
// bytes (or one asset key under two ids) is refused by the worker, so it must be
// refused here first.
func TestValidateFinalJobPlanBindingRulesRejectsOneByteSetOnTwoAssets(t *testing.T) {
	sha := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		Assets: []finalJobPlanAsset{
			{AssetID: "fragment-a", AssetKey: "sha256:" + sha, SHA256: sha, SizeBytes: 1024, Kind: "video", FrameCount: 48},
			{AssetID: "fragment-b", AssetKey: "sha256:" + sha, SHA256: sha, SizeBytes: 1024, Kind: "video", FrameCount: 48},
		},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{
			{SegmentID: "segment-0000", AssetID: "fragment-a", SHA256: sha, FrameCount: 48},
			{SegmentID: "segment-0001", AssetID: "fragment-b", SHA256: sha, FrameCount: 48},
		}}},
	}
	if err := validateFinalJobPlanBindingRules(plan); err == nil {
		t.Fatal("two assets with one byte-set were accepted")
	}
	// The supported representation of the same film: one asset, two placements.
	plan.Assets = plan.Assets[:1]
	plan.VideoTracks[0].Segments[1].AssetID = "fragment-a"
	if err := validateFinalJobPlanBindingRules(plan); err != nil {
		t.Fatalf("one asset referenced twice must be admitted: %v", err)
	}
}

func TestPrepareFinalJobVideoSegmentsReplacesRawSourceAndKeepsFinalAudio(t *testing.T) {
	timelineSHA := strings.Repeat("a", 64)
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: timelineSHA, DurationUS: 2_000_000,
		FinalAudio: finalJobPlanAudio{Mode: "FINAL_AUDIO_COPY", AssetID: "audio-1", SHA256: strings.Repeat("c", 64), SizeBytes: 32, DurationUS: 2_000_000},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: []finalJobPlanSegment{{
			SegmentID: "segment-0000", AssetID: "stock-raw", SHA256: strings.Repeat("d", 64),
			TimelineStartFrame: 24, FrameCount: 48, SourceInUS: 500_000, SourceDurationUS: 2_000_000,
		}}}},
		Assets: []finalJobPlanAsset{
			{AssetID: "audio-1", SHA256: strings.Repeat("c", 64), SizeBytes: 32, Kind: "final_audio", DurationUS: 2_000_000},
			{AssetID: "stock-raw", SHA256: strings.Repeat("d", 64), SizeBytes: 2048, Kind: "video", DurationUS: 4_000_000},
		},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	refs := []any{
		map[string]any{"asset_id": "stock-raw", "sha256": strings.Repeat("d", 64), "size_bytes": int64(2048), "source_uri": "https://objects.example/stock", "role": "stock"},
		map[string]any{"asset_id": "audio-1", "sha256": strings.Repeat("c", 64), "size_bytes": int64(32), "source_uri": "https://objects.example/audio", "role": "final_audio"},
	}
	preparer := &preparedSegmentStub{}
	prepared, runtimeAssets, err := prepareFinalJobVideoSegments(context.Background(), string(raw), refs, preparer)
	if err != nil {
		t.Fatalf("prepareFinalJobVideoSegments: %v", err)
	}
	if preparer.called != 1 || preparer.request.SourceInUS != 500_000 || preparer.request.TimelineStartFrame != 24 {
		t.Fatalf("preparer calls/request = %d/%+v", preparer.called, preparer.request)
	}
	var got finalJobCompiledPlan
	if err := json.Unmarshal([]byte(prepared), &got); err != nil {
		t.Fatal(err)
	}
	segment := got.VideoTracks[0].Segments[0]
	if segment.AssetID != "fragment-segment-0000" || segment.SourceInUS != 0 || segment.SourceDurationUS != 2_000_000 || segment.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("prepared segment = %+v", segment)
	}
	if len(got.Assets) != 2 || got.Assets[0].AssetID != "audio-1" && got.Assets[1].AssetID != "audio-1" {
		t.Fatalf("compiled assets = %+v; want prepared fragment plus final audio", got.Assets)
	}
	if len(runtimeAssets) != 2 {
		t.Fatalf("runtime assets = %#v; want one prepared fragment and final audio", runtimeAssets)
	}
	for _, rawAsset := range runtimeAssets {
		asset := rawAsset.(map[string]any)
		if asset["asset_id"] == "stock-raw" {
			t.Fatal("raw stock source crossed the FINALIZE boundary")
		}
	}
}

// concurrentSegmentStub records how many preparations ran at once and returns a
// distinct fragment per plan position, so ordering can be asserted.
type concurrentSegmentStub struct {
	lanes     int
	inFlight  int32
	peak      int32
	failIndex int
	block     chan struct{}
}

func (s *concurrentSegmentStub) Parallelism() int { return s.lanes }

func (s *concurrentSegmentStub) PrepareFinalJobVideoSegment(_ context.Context, _ map[string]any, request FinalJobVideoSegmentRequest) (map[string]any, error) {
	if s.block != nil {
		<-s.block
	}
	current := atomic.AddInt32(&s.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&s.peak)
		if current <= peak || atomic.CompareAndSwapInt32(&s.peak, peak, current) {
			break
		}
	}
	defer atomic.AddInt32(&s.inFlight, -1)
	time.Sleep(5 * time.Millisecond)
	if s.failIndex >= 0 && strings.HasSuffix(request.SegmentID, fmt.Sprintf("-%04d", s.failIndex)) {
		return nil, fmt.Errorf("stub rejected %s", request.SegmentID)
	}
	return map[string]any{
		"asset_id":             "fragment-" + request.SegmentID,
		"source_uri":           "https://objects.example/" + request.SegmentID,
		"sha256":               strings.Repeat("c", 64),
		"size_bytes":           int64(2048),
		"duration_us":          finalJobFrameDurationUS(request.FrameCount, 24, 1),
		"frame_count":          request.FrameCount,
		"profile_id":           "VELOX_ASSEMBLY_READY_V1",
		"copy_eligible":        true,
		"first_frame_keyframe": true,
		"closed_gop":           true,
		"timeline_revision":    request.TimelineRevision,
		"timeline_sha256":      request.TimelineSHA256,
		"timeline_start_frame": request.TimelineStartFrame,
	}, nil
}

func TestPrepareFinalJobSegmentsRunsLanesAndKeepsPlanOrder(t *testing.T) {
	timelineSHA := strings.Repeat("a", 64)
	work := make([]finalJobSegmentWork, 0, 6)
	for i := 0; i < 6; i++ {
		work = append(work, finalJobSegmentWork{
			index:  i,
			source: map[string]any{"asset_id": "stock-raw", "sha256": strings.Repeat("d", 64)},
			request: FinalJobVideoSegmentRequest{
				SegmentID: fmt.Sprintf("segment-%04d", i), SourceSHA256: strings.Repeat("d", 64),
				SourceDurationUS: 1_000_000, FrameCount: 24,
				TimelineRevision: 1, TimelineSHA256: timelineSHA,
			},
		})
	}
	stub := &concurrentSegmentStub{lanes: 3, failIndex: -1}
	prepared, err := finalJobPrepareSegments(context.Background(), stub, work)
	if err != nil {
		t.Fatalf("finalJobPrepareSegments: %v", err)
	}
	if len(prepared) != len(work) {
		t.Fatalf("prepared %d segments, want %d", len(prepared), len(work))
	}
	for i, raw := range prepared {
		if got, want := raw["asset_id"], fmt.Sprintf("fragment-segment-%04d", i); got != want {
			t.Fatalf("prepared[%d] = %v, want %v (results must stay in plan order)", i, got, want)
		}
	}
	if peak := atomic.LoadInt32(&stub.peak); peak < 2 {
		t.Fatalf("peak concurrency = %d, want the lanes to actually overlap", peak)
	}
	if peak := atomic.LoadInt32(&stub.peak); peak > int32(stub.lanes) {
		t.Fatalf("peak concurrency = %d, want it bounded by %d lanes", peak, stub.lanes)
	}
}

func TestPrepareFinalJobSegmentsReportsTheFirstPlanFailure(t *testing.T) {
	timelineSHA := strings.Repeat("a", 64)
	work := make([]finalJobSegmentWork, 0, 4)
	for i := 0; i < 4; i++ {
		work = append(work, finalJobSegmentWork{
			index:  i,
			source: map[string]any{"asset_id": "stock-raw", "sha256": strings.Repeat("d", 64)},
			request: FinalJobVideoSegmentRequest{
				SegmentID: fmt.Sprintf("segment-%04d", i), SourceSHA256: strings.Repeat("d", 64),
				SourceDurationUS: 1_000_000, FrameCount: 24,
				TimelineRevision: 1, TimelineSHA256: timelineSHA,
			},
		})
	}
	stub := &concurrentSegmentStub{lanes: 2, failIndex: 1}
	_, err := finalJobPrepareSegments(context.Background(), stub, work)
	if err == nil {
		t.Fatal("finalJobPrepareSegments = nil error, want the rejected segment")
	}
	if !strings.Contains(err.Error(), "segment-0001") {
		t.Fatalf("error = %v, want it to name the first failing plan position", err)
	}
	if strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want the real cause, not the cancellation", err)
	}
}

func TestMergeContiguousSegmentsGluesOnlyProvableWindows(t *testing.T) {
	segments := []finalJobPlanSegment{
		{SegmentID: "a", AssetID: "asset-1", SHA256: "s1", TimelineStartFrame: 0, FrameCount: 48, SourceInUS: 0, SourceDurationUS: 2_000_000},
		{SegmentID: "b", AssetID: "asset-1", SHA256: "s1", TimelineStartFrame: 48, FrameCount: 24, SourceInUS: 2_000_000, SourceDurationUS: 1_000_000},
		{SegmentID: "c", AssetID: "asset-2", SHA256: "s2", TimelineStartFrame: 72, FrameCount: 24, SourceInUS: 0, SourceDurationUS: 1_000_000},
		{SegmentID: "d", AssetID: "asset-2", SHA256: "s2", TimelineStartFrame: 96, FrameCount: 24, SourceInUS: 5_000_000, SourceDurationUS: 1_000_000},
	}
	merged := finalJobMergeContiguousSegments(segments)
	if len(merged) != 3 {
		t.Fatalf("merged %d segments, want 3: %#v", len(merged), merged)
	}
	if merged[0].FrameCount != 72 || merged[0].SourceDurationUS != 3_000_000 {
		t.Fatalf("first merged segment = %#v, want the two contiguous windows glued", merged[0])
	}
	if merged[0].SegmentID != "segment-0000" || merged[2].SegmentID != "segment-0002" {
		t.Fatalf("segment ids = %q..%q, want them renumbered densely", merged[0].SegmentID, merged[2].SegmentID)
	}
	// A source discontinuity is not glue-able: same asset, contiguous timeline,
	// but a jump inside the source.
	if merged[1].AssetID != "asset-2" || merged[2].SourceInUS != 5_000_000 {
		t.Fatalf("merged = %#v, want the source jump kept split", merged)
	}
}

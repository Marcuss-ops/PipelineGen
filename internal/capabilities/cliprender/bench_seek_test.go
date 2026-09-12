package cliprender

// bench_seek_test.go owns scenario 7 of the canonical clip.render benchmark:
// Chronon source seek.
//
// HONEST SCOPE. Decode cost lives INSIDE Chronon. The clip.render plan is
// deliberately source-relative: it names the certified source bytes and the
// output window, and it carries NO trim/seek field, because segment selection
// is owned upstream (the clip asset already IS the segment) and frame-accurate
// seeking is Chronon's decision (GOP/keyframe positioning). There is therefore
// nothing in this repository that can be measured as "decode_ms for a segment
// at minute 25" without the real RenderingGen + Chronon stack.
//
// What IS provable in-repo — and what a Chronon-side change must not break —
// is the contract a seek has to preserve:
//
//   - the source identity (path + certified SHA-256) and the exact duration
//     travel intact, so a decoder can validate what it seeks into;
//   - a segment window near the END of a long source is expressed exactly
//     (µs → ms) and validated against the duration;
//   - the source block exposes exactly asset_id/path/sha256 — no hidden offset
//     that would make the PlanSHA256 disagree with what Chronon receives.
//
// The live measurement is documented in the skipped test at the bottom; it is
// the procedure to run on a host with a warm Chronon and a 30-minute source.

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// benchLongSource builds the sealed plan for a segment of a 30-minute source.
func benchLongSource(t *testing.T, segmentStartUS, segmentEndUS int64) ClipRenderPlanV1 {
	t.Helper()
	const durationMS = 30 * 60 * 1000 // 30 minutes

	req := baseRenderRequest()
	req.Overlay = &OverlayRefSpec{
		RenderJobID:        "overlay-render-001",
		PlanFingerprint:    "fp-001",
		RenderKey:          "rk-001",
		SourceVideoAssetID: "long-source",
		StartUS:            segmentStartUS,
		EndUS:              segmentEndUS,
	}
	req.Normalize()
	contract, err := NewContractResolver().Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("resolve contract: %v", err)
	}
	sourceSHA := digest.SHA256Bytes([]byte("long-source-30min"))
	segmentSHA := digest.SHA256Bytes([]byte("overlay-segment"))
	plan, err := Compile(CompileInput{
		RunID:          "seek-bench",
		Source:         &MaterializedAsset{AssetID: "long-source", LocalPath: "/scratch/long-30min.mp4", SHA256: sourceSHA, SizeBytes: 2_000_000_000, DurationMS: durationMS},
		DurationMS:     durationMS,
		Contract:       contract,
		AudioMode:      AudioModeCopyIfCompatible,
		BackgroundMode: BackgroundModeNone,
		OutputPath:     "/scratch/out/segment.mp4",
		Overlay: &PlanOverlayInput{
			Segment: &OverlaySegment{RenderJobID: "overlay-render-001", RenderKey: "rk-001", LocalPath: "/scratch/overlay.mp4", SHA256: segmentSHA, SizeBytes: 4096},
			StartMS: (segmentStartUS + 500) / 1000,
			EndMS:   (segmentEndUS + 500) / 1000,
		},
	})
	if err != nil {
		t.Fatalf("compile long-source plan: %v", err)
	}
	return plan
}

// TestScenario7_SourceSeekPlanEvidence certifies the seek contract that a
// Chronon-side optimisation must preserve: the source is source-relative, the
// certified digest and duration travel intact, and a window at the end of a
// 30-minute source is expressed and validated exactly.
func TestScenario7_SourceSeekPlanEvidence(t *testing.T) {
	const (
		minute = int64(60 * 1000 * 1000) // µs
	)
	positions := []struct {
		name  string
		start int64
	}{
		{"start-minute-1", 1 * minute},
		{"middle-minute-15", 15 * minute},
		{"end-minute-25", 25 * minute},
	}

	for _, pos := range positions {
		end := pos.start + 20*int64(1_000_000) // 20 s segment
		plan := benchLongSource(t, pos.start, end)

		if plan.Source.Path == "" || plan.Source.SHA256 == "" {
			t.Fatalf("%s: source identity missing: %+v", pos.name, plan.Source)
		}
		if plan.DurationMS != 30*60*1000 {
			t.Errorf("%s: duration_ms = %d, want the full 30-minute source duration", pos.name, plan.DurationMS)
		}
		wantStartMS := (pos.start + 500) / 1000
		if plan.Overlay == nil || plan.Overlay.StartMS != wantStartMS {
			t.Fatalf("%s: window start = %v, want %d ms", pos.name, plan.Overlay, wantStartMS)
		}
		if plan.Overlay.EndMS != wantStartMS+20_000 {
			t.Errorf("%s: window end = %d ms, want %d ms", pos.name, plan.Overlay.EndMS, wantStartMS+20_000)
		}

		// The source block must be exactly {asset_id, path, sha256}: no hidden
		// offset, so the sealed digest describes precisely the source bytes
		// Chronon is handed and any seek stays inside Chronon.
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		var source map[string]json.RawMessage
		if err := json.Unmarshal(envelope["source"], &source); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(source))
		for k := range source {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if got := strings.Join(keys, ","); got != "asset_id,path,sha256" {
			t.Errorf("%s: source block keys = %q, want asset_id,path,sha256 (a trim/offset field would move the seek decision out of Chronon)", pos.name, got)
		}

		// The digest must be the plan's own content hash, so a drifted window
		// cannot reach a decoder unvalidated.
		if err := plan.Validate(); err != nil {
			t.Errorf("%s: sealed plan failed validation: %v", pos.name, err)
		}
	}

	// A window that runs past the source must fail closed: a seek beyond EOF
	// is never silently accepted.
	if _, err := Compile(CompileInput{
		RunID:          "seek-oob",
		Source:         &MaterializedAsset{AssetID: "long-source", LocalPath: "/scratch/long-30min.mp4", SHA256: digest.SHA256Bytes([]byte("long-source-30min")), SizeBytes: 2_000_000_000, DurationMS: 30 * 60 * 1000},
		DurationMS:     30 * 60 * 1000,
		Contract:       mustContract(t),
		AudioMode:      AudioModeCopyIfCompatible,
		BackgroundMode: BackgroundModeNone,
		OutputPath:     "/scratch/out/oob.mp4",
		Overlay: &PlanOverlayInput{
			Segment: &OverlaySegment{RenderJobID: "r", RenderKey: "k", LocalPath: "/scratch/o.mp4", SHA256: digest.SHA256Bytes([]byte("seg")), SizeBytes: 1},
			StartMS: 29 * 60 * 1000,
			EndMS:   31 * 60 * 1000,
		},
	}); err == nil {
		t.Error("a segment window past the source duration must fail closed")
	}
}

func mustContract(t *testing.T) *ResolvedContract {
	t.Helper()
	req := baseRenderRequest()
	req.Normalize()
	contract, err := NewContractResolver().Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("resolve contract: %v", err)
	}
	return contract
}

// TestScenario7_ChrononSegmentSeekLive is the LIVE half of scenario 7. It is
// intentionally skipped without the real stack because decode cost is a
// Chronon property, not a PipelineGen one.
//
// Procedure on a host with warm Chronon and a 30-minute source at SOURCE:
//
//  1. Submit three clip.render jobs over the SAME source with overlay windows
//     at minute 1, minute 15 and minute 25 (benchLongSource builds exactly
//     those plans; point the harness at a live executor instead of the lane
//     simulator).
//  2. Read chronon.decode_ms, frames, render_wall_ms and bytes_read from each
//     job's RenderMetricsV2 (metrics.go) — the canonical report already
//     carries them.
//  3. PASS when decode_ms(minute 25) ≈ decode_ms(minute 1) and all three
//     report ≈ 20 s × fps frames. A segment at minute 25 costing materially
//     more than one at minute 1 means the decoder walked from the start
//     instead of seeking to the nearest GOP/keyframe.
//
// Set VELOX_BENCH_REAL_STACK=1 once a live executor is wired into
// benchPipeline; until then this test reports the procedure rather than
// pretending to measure it.
func TestScenario7_ChrononSegmentSeekLive(t *testing.T) {
	if os.Getenv("VELOX_BENCH_REAL_STACK") != "1" {
		t.Skip("live-only: needs RenderingGen + a warm Chronon and a 30-minute source; see the procedure in this file")
	}
	t.Skip("VELOX_BENCH_REAL_STACK=1 is set, but this repository's harness drives a lane SIMULATOR, not a live RenderingGen endpoint; wire a live executor into benchPipeline before claiming a Chronon seek measurement")
}

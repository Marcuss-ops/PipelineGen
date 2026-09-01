package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// TestStockRustPerformanceRTF drives a realistic 10-clip × 7s (70s) canonical
// render through the real Go adapter + real Rust binary and reports the
// three-wall breakdown the certification plan asks for:
//
//	stock.render wall   — RenderCanonicalPlan total (Go validation + transport)
//	rust process wall   — the persistent Rust dispatcher round-trip (timed runner)
//	ffmpeg wall         — the native ffmpeg_ms the binary reports in its response
//	RTF                 — stock.render wall / rendered media duration
//
// ffmpeg wall is read natively from the render_stock response metadata (no
// external timing shim), which is what the Rust stage timing was added for.
// The ordering ffmpeg <= rust <= stock.render is asserted, proving the Go-side
// validation/transport overhead is measurable and attributed, not hidden.
func TestStockRustPerformanceRTF(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	realFFmpeg := resolveFFmpegBinary()
	if realFFmpeg == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(realFFmpeg)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	const (
		width      = 1280
		height     = 720
		fps        = 30
		clipCount  = 10
		clipSec    = 7
		frameCount = clipSec * fps // 210 per clip
	)

	// 1. Generate 10 synthetic clips (moving testsrc pattern — representative
	//    encode load, unlike a flat solid colour).
	assetsDir := t.TempDir()
	clipPaths := make([]string, clipCount)
	planSpec := make([]stockrustClip, clipCount)
	var inputBytes int64
	for i := range clipCount {
		assetID := fmt.Sprintf("clip-%02d", i)
		clipPaths[i] = filepath.Join(assetsDir, assetID+".mp4")
		generateTestsrcClip(t, realFFmpeg, clipPaths[i], frameCount, width, height, fps)
		info, err := os.Stat(clipPaths[i])
		if err != nil {
			t.Fatal(err)
		}
		inputBytes += info.Size()
		planSpec[i] = stockrustClip{assetID: assetID, durationUS: clipSec * 1_000_000, frameCount: frameCount}
	}
	inputDurationUS := int64(clipCount * clipSec * 1_000_000)

	plan := compileTenClipPlan(t, planSpec, clipPaths, inputDurationUS, fps)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatalf("validate canonical render plan: %v", err)
	}

	// 2. Instrumented executor: time each Rust dispatcher round-trip and read
	//    the native ffmpeg_ms the binary reports in its response metadata.
	executor := NewExecutor(musclesPath, realFFmpeg, nil)
	inner := &persistentRustProcessRunner{}
	timing := &timingRunner{inner: inner}
	executor.runner = timing
	// The timing runner is the diagnostic seam for this test; bypass pooled
	// runners or the measured wall/ffmpeg metadata would remain zero.
	executor.runnerPool = nil
	t.Cleanup(inner.reset)

	stock := stockrustRenderer(executor, width, height, fps)

	// 3. Measure stock.render wall (Go adapter boundary).
	start := time.Now()
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatalf("stockrust render failed: %v", err)
	}
	stockRenderWall := time.Since(start)

	// 4. Native ffmpeg wall from the render_stock response metadata.
	ffmpegWall := time.Duration(timing.ffmpegMS) * time.Millisecond

	// 5. Output identity for RTF and the bytes report.
	info, err := os.Stat(plan.OutputPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}
	outputBytes := info.Size()
	outputDurationUS := probeDurationUS(t, ffprobePath, plan.OutputPath)

	inputDurSec := float64(inputDurationUS) / 1_000_000
	outputDurSec := float64(outputDurationUS) / 1_000_000
	rtf := stockRenderWall.Seconds() / outputDurSec

	// 6. Breakdown derived from the three measured walls.
	goOverhead := stockRenderWall - timing.wall // Go validation + marshal + transport
	rustInternal := timing.wall - ffmpegWall    // Rust dispatcher/process overhead, excl. ffmpeg

	t.Logf("stock.render wall : %s (%.3fs)", stockRenderWall, stockRenderWall.Seconds())
	t.Logf("rust process wall : %s (%.3fs)", timing.wall, timing.wall.Seconds())
	t.Logf("ffmpeg wall       : %s (%.3fs) [native ffmpeg_ms]", ffmpegWall, ffmpegWall.Seconds())
	t.Logf("go overhead       : %s (%.3fs)", goOverhead, goOverhead.Seconds())
	t.Logf("rust internal     : %s (%.3fs)", rustInternal, rustInternal.Seconds())
	t.Logf("input duration    : %.3fs (media)", inputDurSec)
	t.Logf("output duration   : %.3fs (media)", outputDurSec)
	t.Logf("input bytes       : %d", inputBytes)
	t.Logf("output bytes      : %d", outputBytes)
	t.Logf("RTF (stock.render wall / media) : %.3f", rtf)

	// 7. Sanity: the three walls must nest ffmpeg <= rust <= stock.render, and
	//    RTF must be positive and non-pathological.
	const tolerance = 100 * time.Millisecond
	if ffmpegWall <= 0 {
		t.Fatalf("native ffmpeg_ms was not reported (%s)", ffmpegWall)
	}
	if ffmpegWall > timing.wall+tolerance {
		t.Fatalf("ffmpeg wall %s exceeds rust process wall %s", ffmpegWall, timing.wall)
	}
	if timing.wall > stockRenderWall+tolerance {
		t.Fatalf("rust process wall %s exceeds stock.render wall %s", timing.wall, stockRenderWall)
	}
	if rtf <= 0 || rtf > 30 {
		t.Fatalf("RTF = %.3f out of sane range (0, 30]", rtf)
	}

	// 8. Persist the measured run into performance_runs for historical
	//    comparison (env-gated; unset STOCKRUST_PERF_DB_PATH keeps it
	//    record-only in the test log).
	run := buildStockrustRun(
		fmt.Sprintf("stockrust-render-%d", time.Now().UnixNano()),
		stockRenderWall,
		stockrustRunMetadata{
			RTF:             rtf,
			StockRenderMS:   stockRenderWall.Milliseconds(),
			RustProcessMS:   timing.wall.Milliseconds(),
			FFmpegMS:        timing.ffmpegMS,
			GoOverheadMS:    goOverhead.Milliseconds(),
			RustInternalMS:  rustInternal.Milliseconds(),
			MediaDurationMS: outputDurationUS / 1000,
			InputBytes:      inputBytes,
			OutputBytes:     outputBytes,
		},
		start,
		start.Add(stockRenderWall),
	)
	persistStockrustRun(t, run)

	// 9. The rendered media must still decode fully.
	if decodeErrors := fullDecode(t, realFFmpeg, plan.OutputPath); decodeErrors != "" {
		t.Fatalf("full decode produced errors:\n%s", decodeErrors)
	}
}

// timingRunner times each Rust dispatcher round-trip while delegating to the
// real persistent runner, and accumulates the native ffmpeg_ms reported by the
// response metadata.
type timingRunner struct {
	inner    RustProcessRunner
	wall     time.Duration
	ffmpegMS int64
}

func (r *timingRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	start := time.Now()
	stdout, stderr, err := r.inner.Run(ctx, binary, input, outputLimit)
	r.wall += time.Since(start)
	if err == nil {
		var resp response
		if json.Unmarshal(bytes.TrimSpace(stdout), &resp) == nil && resp.Metadata != nil {
			r.ffmpegMS += resp.Metadata.FFmpegMS
		}
	}
	return stdout, stderr, err
}

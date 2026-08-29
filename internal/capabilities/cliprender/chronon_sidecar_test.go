package cliprender

import (
	"testing"
)

// chrononSidecarFixture mirrors the canonical chronon3d.frame-timing.v1
// shape written by the video pipe exporter: the exclusive wall timeline with
// measured phases (including legitimately measured zeros), the GPU backend
// context, the asset-cache counters and the render summary.
const chrononSidecarFixture = `{
  "cache": {
    "gpu_asset_cache_hits": 1,
    "gpu_asset_cache_misses": 0
  },
  "exclusive_wall_timeline": {
    "accounted_percent": 104.27911889519139,
    "encoder_drain_finalize_ms": 554.31399,
    "ffprobe_ms": 374.566563,
    "input_open_ms": 0.0,
    "process_wall_ms": 32222.128358,
    "prepare_ms": 2620.100047,
    "render_loop_ms": 24971.442112,
    "sha256_ms": 0.0,
    "startup_ms": 5077.674858
  },
  "job": {
    "gpu": {
      "effective_backend": "direct_yuv_cuda",
      "decoder_backend": "nvdec",
      "encoder_backend": "nvenc",
      "encoder_staging_copy_bytes": 2048,
      "gpu_readback_bytes": 1048576,
      "gpu_upload_bytes": 4194304
    }
  },
  "summary": {
    "end_to_end_fps": 29.97,
    "graph_reused_frames": 12,
    "fast_path_reused_frames": 0,
    "realtime_factor": 0.0042,
    "render_loop_fps": 30.1
  }
}`

func TestParseChrononSidecarProjectsExclusiveWallTimeline(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	requireFloatMS(t, "startup", sc.StartupMS, 5077.674858)
	// A measured zero is a real measurement: input_open and sha256 ran and
	// cost nothing, so they must be non-nil pointers to 0 — never absent.
	requireFloatMS(t, "input_open", sc.InputOpenMS, 0)
	requireFloatMS(t, "prepare", sc.PrepareMS, 2620.100047)
	requireFloatMS(t, "render_loop", sc.RenderLoopMS, 24971.442112)
	requireFloatMS(t, "encoder_drain", sc.EncoderDrainFinalizeMS, 554.31399)
	requireFloatMS(t, "ffprobe", sc.FFprobeMS, 374.566563)
	requireFloatMS(t, "sha256", sc.SHA256MS, 0)
	requireFloatMS(t, "process_wall", sc.ProcessWallMS, 32222.128358)
	if sc.AccountedPercent == nil || *sc.AccountedPercent != 104.27911889519139 {
		t.Fatalf("accounted_percent = %v, want 104.27911889519139", sc.AccountedPercent)
	}
}

func TestParseChrononSidecarProjectsGpuContext(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Backend != "direct_yuv_cuda" {
		t.Fatalf("backend = %q, want direct_yuv_cuda", sc.Backend)
	}
	if sc.Decoder != "nvdec" {
		t.Fatalf("decoder = %q, want nvdec", sc.Decoder)
	}
	if sc.Encoder != "nvenc" {
		t.Fatalf("encoder = %q, want nvenc", sc.Encoder)
	}
	requireUint64(t, "cuda upload bytes", sc.GPUUploadBytes, 4194304)
	requireUint64(t, "cuda readback bytes", sc.GPUReadbackBytes, 1048576)
	requireUint64(t, "encoder staging copy bytes", sc.EncoderStagingCopyBytes, 2048)
	requireUint64(t, "gpu asset cache hits", sc.GPUAssetCacheHits, 1)
	requireUint64(t, "gpu asset cache misses", sc.GPUAssetCacheMisses, 0)
}

func TestParseChrononSidecarProjectsSummary(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	if sc.EndToEndFPS == nil || *sc.EndToEndFPS != 29.97 {
		t.Fatalf("end_to_end_fps = %v, want 29.97", sc.EndToEndFPS)
	}
	if sc.RenderLoopFPS == nil || *sc.RenderLoopFPS != 30.1 {
		t.Fatalf("render_loop_fps = %v, want 30.1", sc.RenderLoopFPS)
	}
	if sc.RealtimeFactor == nil || *sc.RealtimeFactor != 0.0042 {
		t.Fatalf("realtime_factor = %v, want 0.0042", sc.RealtimeFactor)
	}
	requireUint64(t, "graph reused frames", sc.GraphReusedFrames, 12)
	requireUint64(t, "fast path reused frames", sc.FastPathReusedFrames, 0)
}

// TestParseChrononSidecarToleratesOlderShapes pins the schema-evolution
// tolerance: a document predating exclusive_wall_timeline / job.gpu /
// summary (or one that emits null for a field) parses with the affected
// fields nil — never an error and never a fabricated zero.
func TestParseChrononSidecarToleratesOlderShapes(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(`{
	  "encode_close_ms": 5,
	  "frame_times_ms": [{"conversion_copy_ms": 2.5, "encoder_ms": 3.5}],
	  "job": {"gpu": {"effective_backend": "unknown", "gpu_upload_bytes": null}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if sc.StartupMS != nil || sc.PrepareMS != nil || sc.RenderLoopMS != nil {
		t.Fatalf("absent exclusive-wall phases must stay nil, got %+v", sc)
	}
	if sc.Backend != "unknown" {
		t.Fatalf("backend = %q, want the exporter's unknown sentinel preserved", sc.Backend)
	}
	if sc.GPUUploadBytes != nil {
		t.Fatalf("null gpu_upload_bytes must stay nil, got %d", *sc.GPUUploadBytes)
	}
	if sc.GPUReadbackBytes != nil || sc.GPUAssetCacheHits != nil || sc.EndToEndFPS != nil {
		t.Fatalf("absent sections must stay nil: %+v", sc)
	}
}

func TestParseChrononSidecarRejectsInvalidDocuments(t *testing.T) {
	if _, err := ParseChrononSidecar(nil); err == nil {
		t.Fatal("empty document must error")
	}
	if _, err := ParseChrononSidecar([]byte("not json")); err == nil {
		t.Fatal("malformed document must error")
	}
}

func requireFloatMS(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func requireUint64(t *testing.T, name string, got *uint64, want uint64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %d, want %d", name, *got, want)
	}
}

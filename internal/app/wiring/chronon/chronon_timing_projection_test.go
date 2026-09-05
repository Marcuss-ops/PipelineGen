package chronon

import (
	"os"
	"path/filepath"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

func writeChrononTimingFixture(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chronon.mp4.timing.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const chrononTimingFixture = `{
  "encode_close_ms": 5,
  "frame_times_ms": [
    {"conversion_copy_ms": 2.5, "encoder_ms": 3.5},
    {"conversion_copy_ms": 1.5, "encoder_ms": 4.5}
  ],
  "job": {
    "engine_init_ms": 10,
    "backend_init_ms": 20,
    "plan_read_ms": 1,
    "plan_parse_ms": 2,
    "plan_validate_ms": 3,
    "plan_compile_ms": 4,
    "graph_compile_ms": 5,
    "prepare_ms": 6,
    "output_finalize_ms": 7,
    "gpu": {
      "video_decode_wall_ms": 42,
      "cuda_composite_wall_us": 8000
    },
    "text": {
      "raster_ms": 11,
      "atlas_upload_ms": 2,
      "draw_ms": 3
    },
    "image": {
      "resolve_ms": 1,
      "decode_ms": 2,
      "convert_ms": 3,
      "upload_ms": 4,
      "draw_ms": 5
    },
    "cpu_breakdown": {
      "compositenode_blend_ms": 4,
      "effect_stack_total_ms": 2
    }
  }
}`

func requireMS(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %d, want %d", name, *got, want)
	}
}

func TestReadChrononMeasuredPhasesProjectsEngineBreakdown(t *testing.T) {
	path := writeChrononTimingFixture(t, chrononTimingFixture)
	got, err := ReadChrononMeasuredPhases(path, cliprender.ClipRenderPlanV1{})
	if err != nil {
		t.Fatal(err)
	}

	requireMS(t, "startup", got.StartupMS, 51)
	requireMS(t, "decode", got.DecodeMS, 42)
	requireMS(t, "composite", got.CompositeMS, 14)
	requireMS(t, "frame conversion", got.FrameConversionMS, 4)
	requireMS(t, "encode", got.EncodeMS, 13)
	requireMS(t, "finalize", got.FinalizeMS, 7)
	if got.SubtitleRasterMS != nil || got.WatermarkRasterMS != nil {
		t.Fatalf("overlay attribution without overlay intent must stay unknown: %+v", got)
	}
}

func TestReadChrononMeasuredPhasesAttributesSingleTextOverlay(t *testing.T) {
	path := writeChrononTimingFixture(t, chrononTimingFixture)

	subtitlePlan := cliprender.ClipRenderPlanV1{
		Subtitles: &cliprender.PlanSubtitles{Path: "/tmp/subtitles.ass"},
	}
	subtitle, err := ReadChrononMeasuredPhases(path, subtitlePlan)
	if err != nil {
		t.Fatal(err)
	}
	requireMS(t, "subtitle raster", subtitle.SubtitleRasterMS, 16)
	if subtitle.WatermarkRasterMS != nil {
		t.Fatalf("watermark metric must stay nil on subtitle-only plan, got %d", *subtitle.WatermarkRasterMS)
	}

	watermarkPlan := cliprender.ClipRenderPlanV1{
		Watermark: &cliprender.PlanWatermark{Text: "TEST"},
	}
	watermark, err := ReadChrononMeasuredPhases(path, watermarkPlan)
	if err != nil {
		t.Fatal(err)
	}
	requireMS(t, "watermark raster", watermark.WatermarkRasterMS, 16)
	if watermark.SubtitleRasterMS != nil {
		t.Fatalf("subtitle metric must stay nil on watermark-only plan, got %d", *watermark.SubtitleRasterMS)
	}
}

func TestReadChrononMeasuredPhasesDoesNotGuessSharedTextCost(t *testing.T) {
	path := writeChrononTimingFixture(t, chrononTimingFixture)
	plan := cliprender.ClipRenderPlanV1{
		Subtitles: &cliprender.PlanSubtitles{Path: "/tmp/subtitles.ass"},
		Watermark: &cliprender.PlanWatermark{Text: "TEST"},
	}
	got, err := ReadChrononMeasuredPhases(path, plan)
	if err != nil {
		t.Fatal(err)
	}
	if got.SubtitleRasterMS != nil || got.WatermarkRasterMS != nil {
		t.Fatalf("shared job.text aggregate must not be split by guesswork: %+v", got)
	}
}

func TestReadChrononMeasuredPhasesAttributesUnambiguousImageWatermark(t *testing.T) {
	path := writeChrononTimingFixture(t, chrononTimingFixture)
	plan := cliprender.ClipRenderPlanV1{
		Watermark: &cliprender.PlanWatermark{Path: "/tmp/watermark.png"},
	}
	got, err := ReadChrononMeasuredPhases(path, plan)
	if err != nil {
		t.Fatal(err)
	}
	requireMS(t, "image watermark", got.WatermarkRasterMS, 15)

	plan.Background = &cliprender.PlanBackground{Mode: cliprender.BackgroundModeAsset, Path: "/tmp/background.png"}
	ambiguous, err := ReadChrononMeasuredPhases(path, plan)
	if err != nil {
		t.Fatal(err)
	}
	if ambiguous.WatermarkRasterMS != nil {
		t.Fatalf("shared job.image aggregate must stay unknown when background asset is present, got %d", *ambiguous.WatermarkRasterMS)
	}
}

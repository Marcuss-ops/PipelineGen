package wiring

// chronon_timing_sidecar.go is the timing-sidecar projection boundary.
// Chronon remains the owner of these measurements; PipelineGen only
// transports them and never re-times or fabricates missing phases. The
// narrow projection of Chronon's canonical *.timing.json sidecar feeds
// RenderMetricsV2 from the executor in chronon_clip_renderer.go.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"go.uber.org/zap"
)

// wireChrononMetricsAdapter builds the Chronon Metrics Adapter over the
// primary SQLite DB (performance_operations lives in the primary database).
// Best-effort wiring: a nil DB or a store construction error logs a Warn and
// returns nil — the render path then simply skips the performance projection
// instead of aborting boot.
func wireChrononMetricsAdapter(db *sql.DB, log *zap.Logger) *cliprender.ChrononMetricsAdapter {
	if db == nil {
		return nil
	}
	ops, err := perfstore.NewOperationStore(db)
	if err != nil {
		log.Warn("chronon metrics adapter NOT wired (performance operation store unavailable)", zap.Error(err))
		return nil
	}
	return cliprender.NewChrononMetricsAdapter(ops, log)
}

// chrononTimingReport is the narrow projection of Chronon's canonical
// *.timing.json sidecar needed by RenderMetricsV2. Chronon remains the owner
// of these measurements; PipelineGen only transports them and never re-times
// or fabricates missing phases.
type chrononTimingReport struct {
	EncodeCloseMS float64 `json:"encode_close_ms"`
	FrameTimes    []struct {
		ConversionCopyMS float64 `json:"conversion_copy_ms"`
		EncoderMS        float64 `json:"encoder_ms"`
	} `json:"frame_times_ms"`
	Job struct {
		EngineInitMS     *float64 `json:"engine_init_ms"`
		BackendInitMS    *float64 `json:"backend_init_ms"`
		PlanReadMS       *float64 `json:"plan_read_ms"`
		PlanParseMS      *float64 `json:"plan_parse_ms"`
		PlanValidateMS   *float64 `json:"plan_validate_ms"`
		PlanCompileMS    *float64 `json:"plan_compile_ms"`
		GraphCompileMS   *float64 `json:"graph_compile_ms"`
		PrepareMS        *float64 `json:"prepare_ms"`
		OutputFinalizeMS *float64 `json:"output_finalize_ms"`
		GPU              struct {
			CUDACompositeWallUS *uint64 `json:"cuda_composite_wall_us"`
			VideoDecodeWallMS   *uint64 `json:"video_decode_wall_ms"`
		} `json:"gpu"`
		Text struct {
			RasterMS      *float64 `json:"raster_ms"`
			AtlasUploadMS *float64 `json:"atlas_upload_ms"`
			DrawMS        *float64 `json:"draw_ms"`
		} `json:"text"`
		Image struct {
			ResolveMS *float64 `json:"resolve_ms"`
			DecodeMS  *float64 `json:"decode_ms"`
			ConvertMS *float64 `json:"convert_ms"`
			UploadMS  *float64 `json:"upload_ms"`
			DrawMS    *float64 `json:"draw_ms"`
		} `json:"image"`
		CPUBreakdown struct {
			CompositeNodeBlendMS *float64 `json:"compositenode_blend_ms"`
			EffectStackTotalMS   *float64 `json:"effect_stack_total_ms"`
		} `json:"cpu_breakdown"`
	} `json:"job"`
}

type chrononMeasuredPhases struct {
	StartupMS         *int64
	DecodeMS          *int64
	CompositeMS       *int64
	SubtitleRasterMS  *int64
	WatermarkRasterMS *int64
	FrameConversionMS *int64
	EncodeMS          *int64
	FinalizeMS        *int64
}

func sumMeasuredMS(values ...*float64) *int64 {
	var sum float64
	seen := false
	for _, value := range values {
		if value == nil {
			continue
		}
		seen = true
		sum += *value
	}
	if !seen {
		return nil
	}
	ms := int64(sum + 0.5)
	return &ms
}

func measuredUintMS(value *uint64) *int64 {
	if value == nil {
		return nil
	}
	ms := int64(*value)
	return &ms
}

func readChrononMeasuredPhases(path string, plan cliprender.ClipRenderPlanV1) (chrononMeasuredPhases, error) {
	var report chrononTimingReport
	raw, err := os.ReadFile(path)
	if err != nil {
		return chrononMeasuredPhases{}, err
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return chrononMeasuredPhases{}, fmt.Errorf("decode chronon timing sidecar: %w", err)
	}

	phases := chrononMeasuredPhases{}
	phases.StartupMS = sumMeasuredMS(
		report.Job.EngineInitMS,
		report.Job.BackendInitMS,
		report.Job.PlanReadMS,
		report.Job.PlanParseMS,
		report.Job.PlanValidateMS,
		report.Job.PlanCompileMS,
		report.Job.GraphCompileMS,
		report.Job.PrepareMS,
	)
	phases.DecodeMS = measuredUintMS(report.Job.GPU.VideoDecodeWallMS)

	var compositeValues []*float64
	if report.Job.CPUBreakdown.CompositeNodeBlendMS != nil {
		compositeValues = append(compositeValues, report.Job.CPUBreakdown.CompositeNodeBlendMS)
	}
	if report.Job.CPUBreakdown.EffectStackTotalMS != nil {
		compositeValues = append(compositeValues, report.Job.CPUBreakdown.EffectStackTotalMS)
	}
	if report.Job.GPU.CUDACompositeWallUS != nil {
		cudaMS := float64(*report.Job.GPU.CUDACompositeWallUS) / 1000.0
		compositeValues = append(compositeValues, &cudaMS)
	}
	phases.CompositeMS = sumMeasuredMS(compositeValues...)

	textOverlayMS := sumMeasuredMS(
		report.Job.Text.RasterMS,
		report.Job.Text.AtlasUploadMS,
		report.Job.Text.DrawMS,
	)
	hasSubtitles := plan.Subtitles != nil && strings.TrimSpace(plan.Subtitles.Path) != ""
	hasTextWatermark := plan.Watermark != nil && strings.TrimSpace(plan.Watermark.Text) != ""
	hasImageWatermark := plan.Watermark != nil && strings.TrimSpace(plan.Watermark.Text) == "" && strings.TrimSpace(plan.Watermark.Path) != ""
	hasBackgroundAsset := plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset && strings.TrimSpace(plan.Background.Path) != ""
	// Chronon's job.text aggregate covers all text nodes. Attribute it only
	// when exactly one text-overlay class is present; when subtitles and a
	// text watermark coexist we deliberately leave both fields unknown rather
	// than splitting one measured total by guesswork.
	if hasSubtitles && !hasTextWatermark {
		phases.SubtitleRasterMS = textOverlayMS
	}
	if hasTextWatermark && !hasSubtitles {
		phases.WatermarkRasterMS = textOverlayMS
	}
	// The job.image aggregate is watermark-specific only when no background
	// image is also present. In that safe case expose the complete measured
	// image watermark pipeline (resolve/decode/convert/upload/draw).
	if hasImageWatermark && !hasBackgroundAsset {
		phases.WatermarkRasterMS = sumMeasuredMS(
			report.Job.Image.ResolveMS,
			report.Job.Image.DecodeMS,
			report.Job.Image.ConvertMS,
			report.Job.Image.UploadMS,
			report.Job.Image.DrawMS,
		)
	}

	if len(report.FrameTimes) > 0 {
		var conversionMS float64
		var encodeMS float64
		for _, frame := range report.FrameTimes {
			conversionMS += frame.ConversionCopyMS
			encodeMS += frame.EncoderMS
		}
		conversion := int64(conversionMS + 0.5)
		encode := int64(encodeMS + report.EncodeCloseMS + 0.5)
		phases.FrameConversionMS = &conversion
		phases.EncodeMS = &encode
	}
	phases.FinalizeMS = sumMeasuredMS(report.Job.OutputFinalizeMS)
	return phases, nil
}

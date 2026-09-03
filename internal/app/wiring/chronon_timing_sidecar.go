package wiring

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

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

type chrononMeasuredPhases struct{ StartupMS, DecodeMS, CompositeMS, SubtitleRasterMS, WatermarkRasterMS, FrameConversionMS, EncodeMS, FinalizeMS *int64 }

func sumMeasuredMS(values ...*float64) *int64 {
	var total float64
	seen := false
	for _, value := range values {
		if value != nil {
			total += *value
			seen = true
		}
	}
	if !seen {
		return nil
	}
	result := int64(total + 0.5)
	return &result
}

func measuredUintMS(value *uint64) *int64 {
	if value == nil {
		return nil
	}
	result := int64(*value)
	return &result
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
	phases.StartupMS = sumMeasuredMS(report.Job.EngineInitMS, report.Job.BackendInitMS, report.Job.PlanReadMS, report.Job.PlanParseMS, report.Job.PlanValidateMS, report.Job.PlanCompileMS, report.Job.GraphCompileMS, report.Job.PrepareMS)
	phases.DecodeMS = measuredUintMS(report.Job.GPU.VideoDecodeWallMS)
	var composite []*float64
	composite = append(composite, report.Job.CPUBreakdown.CompositeNodeBlendMS, report.Job.CPUBreakdown.EffectStackTotalMS)
	if report.Job.GPU.CUDACompositeWallUS != nil {
		value := float64(*report.Job.GPU.CUDACompositeWallUS) / 1000
		composite = append(composite, &value)
	}
	phases.CompositeMS = sumMeasuredMS(composite...)
	textMS := sumMeasuredMS(report.Job.Text.RasterMS, report.Job.Text.AtlasUploadMS, report.Job.Text.DrawMS)
	hasSubtitles := plan.Subtitles != nil && strings.TrimSpace(plan.Subtitles.Path) != ""
	hasTextWatermark := plan.Watermark != nil && strings.TrimSpace(plan.Watermark.Text) != ""
	hasImageWatermark := plan.Watermark != nil && strings.TrimSpace(plan.Watermark.Text) == "" && strings.TrimSpace(plan.Watermark.Path) != ""
	hasBackgroundAsset := plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset && strings.TrimSpace(plan.Background.Path) != ""
	if hasSubtitles && !hasTextWatermark {
		phases.SubtitleRasterMS = textMS
	}
	if hasTextWatermark && !hasSubtitles {
		phases.WatermarkRasterMS = textMS
	}
	if hasImageWatermark && !hasBackgroundAsset {
		phases.WatermarkRasterMS = sumMeasuredMS(report.Job.Image.ResolveMS, report.Job.Image.DecodeMS, report.Job.Image.ConvertMS, report.Job.Image.UploadMS, report.Job.Image.DrawMS)
	}
	if len(report.FrameTimes) > 0 {
		var conversion, encode float64
		for _, frame := range report.FrameTimes {
			conversion += frame.ConversionCopyMS
			encode += frame.EncoderMS
		}
		conversionResult, encodeResult := int64(conversion+0.5), int64(encode+report.EncodeCloseMS+0.5)
		phases.FrameConversionMS, phases.EncodeMS = &conversionResult, &encodeResult
	}
	phases.FinalizeMS = sumMeasuredMS(report.Job.OutputFinalizeMS)
	return phases, nil
}

package rustexec

// video_processor_cut.go contains the Cut operation and its helper functions
// extracted from video_processor.go to keep the main file under the
// 600-LOC strict gate.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// Cut implements the Stock VideoCutter port through the same client and
// protocol used by every other Rust capability.
func (p *VideoProcessor) Cut(ctx context.Context, req stockpipeline.CutRequest) (stockpipeline.CutBatchResult, error) {
	result := stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: make([]stockpipeline.CutItemResult, len(req.Jobs))}
	codec, preset, crf, err := p.policyFor(req.Codec, req.Preset, req.CRF)
	if err != nil {
		return result, err
	}
	// A stock clip is a whole-file packet-copy input of the remote assembler.
	// Only the canonical libx264 lane produces the certified SPS/PPS; NVENC
	// emits a different parameter-set family under the same nominal profile,
	// so it must fail BEFORE any encode instead of being rejected downstream.
	if !strings.EqualFold(codec, "libx264") {
		return result, fmt.Errorf("STOCK_CANONICAL_ENCODER_REQUIRED: stock clips are packet-copy assembly inputs and require libx264, got %q", codec)
	}
	profileInput := p.profile
	if profileInput == (mediaexec.VideoProfile{}) {
		profileInput = mediaexec.VideoProfile{Width: req.Width, Height: req.Height, FPSNum: req.FPSNum, FPSDen: req.FPSDen, KeyframeInterval: req.KeyframeInterval}
	}
	profile, err := p.resolvedProfile(profileInput)
	if err != nil {
		return result, err
	}
	wire := request{
		Operation: "cut_batch", SourcePath: req.SourcePath, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPSNum: uint32(profile.FPSNum), FPSDen: uint32(profile.FPSDen),
		KeyframeInterval: uint32(profile.KeyframeInterval), AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels), NoAudio: req.NoAudio,
		// Capacity policy travels only when the composition root supplied it
		// (>0); omitempty keeps the historical Rust default otherwise.
		MaxParallelCuts: uint32(p.maxParallelCuts),
	}
	wireJobs := make([]cutRequestJob, len(req.Jobs))
	for i, job := range req.Jobs {
		result.Items[i] = stockpipeline.CutItemResult{JobID: job.OutputPath, OutputPath: job.OutputPath, Status: stockpipeline.CutItemStatusUnknown}
		wireJobs[i] = cutRequestJob{JobID: job.OutputPath, StartSec: job.StartSec, EndSec: job.EndSec, OutputPath: job.OutputPath}
	}
	wire.Jobs = wireJobs
	response, err := p.client.call(ctx, wire)
	if err != nil {
		return result, err
	}
	byJob := make(map[string]cutItem, len(response.Items))
	for _, item := range response.Items {
		byJob[item.JobID] = item
	}
	for i, job := range req.Jobs {
		item, ok := byJob[job.OutputPath]
		if !ok {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("rust muscles omitted job %q", job.OutputPath)
			continue
		}
		result.Items[i].OutputPath = item.OutputPath
		result.Items[i].SizeBytes = item.SizeBytes
		if (item.Status != "succeeded" && item.Status != "validated") || item.OutputPath == "" {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("rust cut failed: %s", item.Error)
			continue
		}
		result.Items[i].DurationSec = item.DurationSec
		result.Items[i].Status = stockpipeline.CutItemStatusValidated
		size, hash, hashErr := hashOutput(item.OutputPath)
		if hashErr != nil {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("validate rust cut output: %w", hashErr)
			continue
		}
		// Bitstream identity gate: same codec name, profile and geometry are
		// not enough — the exact SPS/PPS must be the canonical record, or
		// `concat -c copy` applies one segment's parameter sets to another
		// segment's slices. A failed gate makes the clip unpublishable.
		if gateErr := kernelmedia.VerifyCanonicalVideoExtradata(item.OutputPath); gateErr != nil {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("validate rust cut bitstream: %w", gateErr)
			continue
		}
		result.Items[i].SizeBytes, result.Items[i].SHA256Hex = size, hash
	}
	return result, nil
}

func hashOutput(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, "", err
	}
	hash, err := digest.SHA256Reader(file)
	if err != nil {
		return 0, "", err
	}
	return info.Size(), hash, nil
}

func normalizeProfile(opts mediaexec.NormalizeOptions) mediaexec.VideoProfile {
	profile := opts.Profile
	if opts.Width > 0 {
		profile.Width = opts.Width
	}
	if opts.Height > 0 {
		profile.Height = opts.Height
	}
	if opts.FPSNum > 0 && opts.FPSDen > 0 {
		profile.FPSNum = opts.FPSNum
		profile.FPSDen = opts.FPSDen
	}
	if opts.KeyframeInterval > 0 {
		profile.KeyframeInterval = opts.KeyframeInterval
	}
	return profile
}

func normalizeCodec(opts mediaexec.NormalizeOptions) string {
	if opts.Codec != "" {
		return opts.Codec
	}
	return opts.Policy.Codec
}

func normalizePreset(opts mediaexec.NormalizeOptions) string {
	if opts.Preset != "" {
		return opts.Preset
	}
	return opts.Policy.Preset
}

func normalizeCRF(opts mediaexec.NormalizeOptions) int {
	if opts.CRF > 0 {
		return opts.CRF
	}
	return opts.Policy.CRF
}

func cutCodec(opts mediaexec.CutAndNormalizeOptions) string {
	if opts.Codec != "" {
		return opts.Codec
	}
	return opts.Policy.Codec
}

func cutPreset(opts mediaexec.CutAndNormalizeOptions) string {
	if opts.Preset != "" {
		return opts.Preset
	}
	return opts.Policy.Preset
}

func cutCRF(opts mediaexec.CutAndNormalizeOptions) int {
	if opts.CRF > 0 {
		return opts.CRF
	}
	return opts.Policy.CRF
}

func cutProfile(opts mediaexec.CutAndNormalizeOptions) mediaexec.VideoProfile {
	profile := opts.Profile
	if opts.Width > 0 {
		profile.Width = opts.Width
	}
	if opts.Height > 0 {
		profile.Height = opts.Height
	}
	if opts.FPSNum > 0 && opts.FPSDen > 0 {
		profile.FPSNum = opts.FPSNum
		profile.FPSDen = opts.FPSDen
	}
	return profile
}

func (p *VideoProcessor) resolvedProfile(requested mediaexec.VideoProfile) (mediaexec.VideoProfile, error) {
	profile := p.profile
	if requested.Width > 0 {
		profile.Width = requested.Width
	}
	if requested.Height > 0 {
		profile.Height = requested.Height
	}
	if requested.FPSNum > 0 && requested.FPSDen > 0 {
		profile.FPSNum = requested.FPSNum
		profile.FPSDen = requested.FPSDen
	}
	if requested.KeyframeInterval > 0 {
		profile.KeyframeInterval = requested.KeyframeInterval
	}
	if requested.AudioCodec != "" {
		profile.AudioCodec = requested.AudioCodec
	}
	if requested.AudioBitrate != "" {
		profile.AudioBitrate = requested.AudioBitrate
	}
	if requested.SampleRate > 0 {
		profile.SampleRate = requested.SampleRate
	}
	if requested.Channels > 0 {
		profile.Channels = requested.Channels
	}
	if err := validateResolvedProfile(profile); err != nil {
		return mediaexec.VideoProfile{}, err
	}
	return profile, nil
}

func validateResolvedProfile(profile mediaexec.VideoProfile) error {
	if profile.Width <= 0 || profile.Height <= 0 || profile.FPSNum <= 0 || profile.FPSDen <= 0 || profile.KeyframeInterval <= 0 || profile.AudioCodec == "" || profile.AudioBitrate == "" || profile.SampleRate <= 0 || profile.Channels <= 0 {
		return fmt.Errorf("PROFILE_REQUIRED: complete resolved video profile is required")
	}
	return nil
}

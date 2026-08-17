package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

type VideoProcessor struct {
	client  *Client
	policy  mediaexec.EncoderPolicy
	profile mediaexec.VideoProfile
}

// CombinedAudioRenderer is the production bridge from the durable script
// workflow to the Rust audio execution plane. Timing decisions are completed
// before this adapter is called.
type CombinedAudioRenderer struct{ processor *VideoProcessor }

func NewCombinedAudioRenderer(processor *VideoProcessor) (*CombinedAudioRenderer, error) {
	if processor == nil {
		return nil, fmt.Errorf("combined audio renderer requires a video processor")
	}
	return &CombinedAudioRenderer{processor: processor}, nil
}

func (r *CombinedAudioRenderer) Render(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets) (scripts.FinalAudioReference, scripts.AudioPipelineMetrics, error) {
	if r == nil || r.processor == nil {
		return scripts.FinalAudioReference{}, scripts.AudioPipelineMetrics{}, fmt.Errorf("combined audio renderer is not configured")
	}
	if plan.PlanSHA256 == "" {
		return scripts.FinalAudioReference{}, scripts.AudioPipelineMetrics{}, fmt.Errorf("combined audio plan hash is missing")
	}
	output := filepath.Join(os.TempDir(), "pipelinegen-final-audio-"+plan.PlanSHA256+".m4a")
	started := time.Now()
	asset, stageMetrics, err := r.processor.RenderAudioPlanWithMetrics(ctx, plan, assets, output)
	if err != nil {
		return scripts.FinalAudioReference{}, scripts.AudioPipelineMetrics{}, err
	}
	metrics := stageMetrics
	metrics.TotalMS = time.Since(started).Milliseconds()
	metrics.AudioDurationMS = asset.DurationMS
	metrics.AudioEncodePasses = 1
	return scripts.FinalAudioReference{
		AssetID: asset.AssetID, Path: output, Container: strings.TrimPrefix(filepath.Ext(output), "."),
		AudioContractVersion: asset.AudioContractVersion,
		AudioPlanVersion:     asset.AudioPlanVersion, PlanSHA256: asset.AudioPlanSHA256,
		FinalAudioSHA256: asset.FinalAudioSHA256, Codec: asset.Codec, Profile: asset.Profile,
		SampleRate: asset.SampleRate, Channels: asset.Channels, ChannelLayout: asset.ChannelLayout,
		Bitrate: asset.Bitrate, DurationUS: asset.DurationMS * 1000, DurationMS: asset.DurationMS,
		StartPTS: asset.StartPTS, SizeBytes: asset.SizeBytes, FinalMix: asset.FinalMix, CopyEligible: asset.CopyEligible,
	}, metrics, nil
}

var _ scripts.CombinedAudioRenderer = (*CombinedAudioRenderer)(nil)

func NewVideoProcessor(binaryPath, ffmpegPath string, log *zap.Logger) *VideoProcessor {
	return NewVideoProcessorWithExecutor(NewExecutor(binaryPath, ffmpegPath, log), log)
}

func NewVideoProcessorWithExecutor(executor *Executor, log *zap.Logger) *VideoProcessor {
	return &VideoProcessor{client: NewClientWithExecutor(executor, log)}
}

// NewConfiguredVideoProcessor binds the single Go-owned encoder policy to all
// encoding capabilities exposed by this adapter. Probe and copy operations do
// not use it; encoded operations fail closed when it is absent.
func NewConfiguredVideoProcessor(binaryPath, ffmpegPath string, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *VideoProcessor {
	return NewConfiguredVideoProcessorWithExecutor(NewExecutor(binaryPath, ffmpegPath, log), policy, profile, log)
}

func NewConfiguredVideoProcessorWithExecutor(executor *Executor, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *VideoProcessor {
	return &VideoProcessor{client: NewClientWithExecutor(executor, log), policy: policy, profile: profile}
}

func (p *VideoProcessor) policyFor(codec, preset string, crf int) (string, string, int, error) {
	if codec == "" {
		codec = p.policy.Codec
	}
	if preset == "" {
		preset = p.policy.Preset
	}
	if crf <= 0 {
		crf = p.policy.CRF
	}
	if codec == "" || preset == "" || crf <= 0 {
		return "", "", 0, fmt.Errorf("ENCODER_POLICY_REQUIRED: Go did not provide a complete video encoder policy")
	}
	return codec, preset, crf, nil
}

func (p *VideoProcessor) Normalize(ctx context.Context, input, output string, opts mediaexec.NormalizeOptions) error {
	codec, preset, crf, err := p.policyFor(normalizeCodec(opts), normalizePreset(opts), normalizeCRF(opts))
	if err != nil {
		return err
	}
	profile, err := p.resolvedProfile(normalizeProfile(opts))
	if err != nil {
		return err
	}
	return p.run(ctx, request{
		Operation: "normalize", SourcePath: input, OutputPath: output,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels),
		Codec: codec, Preset: preset, CRF: crf,
		DurationSec: durationSeconds(opts), KeepAudio: opts.KeepAudio,
	})
}

func durationSeconds(opts mediaexec.NormalizeOptions) float64 {
	if opts.DisableDuration {
		return 0
	}
	return float64(opts.Duration)
}

// parseTimeSeconds converts a media timestamp to seconds. It accepts BOTH
// the plain float-seconds form used by stock/audio callers ("0", "15.5")
// and the HH:MM:SS.mmm form produced by youtube_pipeline.formatTime
// ("00:00:15.000"). Previously only strconv.ParseFloat was used, so the
// formatted timestamps failed to parse and degraded to endSec=0, which made
// the Rust cut run `-t 0` and publish an empty 262-byte MP4 stub while
// still returning ok=true (the root cause of the silent stub clips).
func parseTimeSeconds(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("invalid timestamp %q", s)
		}
		return v, nil
	}
	// HH:MM:SS.mmm (or HH:MM:SS) — split on ':' so the seconds field keeps
	// its optional fractional part ("15.000" → 15.0).
	parts := strings.Split(s, ":")
	if len(parts) == 2 || len(parts) == 3 {
		var secs float64
		for i, part := range parts {
			v, err := strconv.ParseFloat(part, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid timestamp %q: %w", s, err)
			}
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || (i > 0 && v >= 60) {
				return 0, fmt.Errorf("invalid timestamp %q", s)
			}
			secs = secs*60 + v
		}
		return secs, nil
	}
	return 0, fmt.Errorf("invalid timestamp %q", s)
}

func parseCutRange(start, end string, allowEmpty bool) (float64, float64, error) {
	if allowEmpty && strings.TrimSpace(start) == "" && strings.TrimSpace(end) == "" {
		return 0, 0, nil
	}
	startSec, err := parseTimeSeconds(start)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start timestamp: %w", err)
	}
	endSec, err := parseTimeSeconds(end)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end timestamp: %w", err)
	}
	if endSec <= startSec {
		return 0, 0, fmt.Errorf("invalid timestamp range: end must be after start")
	}
	return startSec, endSec, nil
}

func (p *VideoProcessor) CutCopy(ctx context.Context, input, output, start, end string, noAudio bool) error {
	startSec, endSec, err := parseCutRange(start, end, true)
	if err != nil {
		return err
	}
	return p.run(ctx, request{Operation: "cut_copy", SourcePath: input, OutputPath: output, StartSec: startSec, EndSec: endSec, NoAudio: noAudio})
}

func (p *VideoProcessor) CutAndNormalize(ctx context.Context, input, output, start, end string, opts mediaexec.CutAndNormalizeOptions) error {
	startSec, endSec, err := parseCutRange(start, end, false)
	if err != nil {
		return err
	}
	profile, err := p.resolvedProfile(cutProfile(opts))
	if err != nil {
		return err
	}
	codec, preset, crf, err := p.policyFor(cutCodec(opts), cutPreset(opts), cutCRF(opts))
	if err != nil {
		return err
	}
	return p.run(ctx, request{
		Operation: "cut_and_normalize", SourcePath: input, OutputPath: output,
		StartSec: startSec, EndSec: endSec, NoAudio: opts.NoAudio,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels),
		Codec: codec, Preset: preset, CRF: crf,
	})
}

func (p *VideoProcessor) ApplyWatermark(ctx context.Context, input, output string, opts mediaexec.WatermarkOptions) error {
	codec, preset, crf, err := p.policyFor("", "", 0)
	if err != nil {
		return err
	}
	profile, err := p.resolvedProfile(mediaexec.VideoProfile{})
	if err != nil {
		return err
	}
	return p.run(ctx, request{Operation: "watermark", SourcePath: input, OutputPath: output, OverlayPath: opts.ImagePath, Opacity: opts.Opacity, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS), KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate, SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels)})
}

func (p *VideoProcessor) RemuxHLS(ctx context.Context, sourceURL, output string) error {
	return p.run(ctx, request{Operation: "remux_hls", SourcePath: sourceURL, OutputPath: output})
}

func (p *VideoProcessor) Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error) {
	result, err := p.client.call(ctx, request{Operation: "probe", SourcePath: path})
	if err != nil {
		return nil, err
	}
	if result.Metadata == nil {
		return nil, fmt.Errorf("rust media probe returned no metadata")
	}
	m := result.Metadata
	return &mediaexec.MediaInfo{
		Duration: durationFromSeconds(m.DurationSec),
		Width:    int(m.Width), Height: int(m.Height), FPS: m.FPS,
		VideoCodec: m.VideoCodec, AudioCodec: m.AudioCodec,
		SampleRate: int(m.SampleRate), Channels: int(m.Channels),
		HasVideo: m.HasVideo, HasAudio: m.HasAudio,
		PixelFormat:      m.PixelFormat,
		FormatName:       m.FormatName,
		VideoStreamCount: int(m.VideoStreamCount),
		StreamCount:      int(m.StreamCount),
		AudioStreamCount: int(m.AudioStreamCount),
		FPSNum:           int(m.FPSNum),
		FPSDen:           int(m.FPSDen),
	}, nil
}

func (p *VideoProcessor) ExtractFrame(ctx context.Context, input, output string, timestamp float64) error {
	return p.run(ctx, request{Operation: "extract_frame", SourcePath: input, OutputPath: output, TimestampSec: timestamp})
}

func (p *VideoProcessor) GenerateProxy(ctx context.Context, input, output string) error {
	codec, preset, crf, err := p.policyFor("", "", 0)
	if err != nil {
		return err
	}
	profile, err := p.resolvedProfile(mediaexec.VideoProfile{})
	if err != nil {
		return err
	}
	return p.run(ctx, request{Operation: "generate_proxy", SourcePath: input, OutputPath: output, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS), KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate, SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels)})
}

func (p *VideoProcessor) GenerateStoryboard(ctx context.Context, input, output string, intervalFrames, cols, rows int) error {
	return p.run(ctx, request{Operation: "generate_storyboard", SourcePath: input, OutputPath: output, IntervalFrames: uint32(intervalFrames), Columns: uint32(cols), Rows: uint32(rows)})
}

func (p *VideoProcessor) MergeInputs(ctx context.Context, inputs []string, output string) error {
	return p.run(ctx, request{Operation: OperationMergeInputs, InputPaths: inputs, OutputPath: output})
}

// MuxFinalAudioCopy is the assembler-only path. It accepts only a previously
// certified canonical final audio asset and delegates a mux operation whose
// Rust command uses -c:a copy. There is deliberately no encode fallback.
func (p *VideoProcessor) MuxFinalAudioCopy(ctx context.Context, video, finalAudio, output string, asset audio.FinalAudioAsset) error {
	if !asset.CopyEligible || !asset.FinalMix || asset.FinalAudioSHA256 == "" || asset.Codec != "aac" || !strings.EqualFold(asset.Profile, "LC") || asset.SampleRate != 48000 || asset.Channels != 2 || asset.ChannelLayout != "stereo" {
		return fmt.Errorf("%w: final audio is not canonical copy-eligible media", audio.ErrAudioMediaIncompatible)
	}
	if strings.TrimSpace(video) == "" || strings.TrimSpace(finalAudio) == "" || strings.TrimSpace(output) == "" {
		return fmt.Errorf("%w: video, final audio, and output paths are required", audio.ErrAudioMediaIncompatible)
	}
	info, err := os.Stat(finalAudio)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || (asset.SizeBytes > 0 && info.Size() != asset.SizeBytes) {
		return fmt.Errorf("%w: final audio file is unavailable", audio.ErrAudioMediaIncompatible)
	}
	file, err := os.Open(finalAudio)
	if err != nil {
		return fmt.Errorf("%w: final audio cannot be opened", audio.ErrAudioMediaIncompatible)
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || fmt.Sprintf("%x", hasher.Sum(nil)) != asset.FinalAudioSHA256 {
		return fmt.Errorf("%w: final audio hash mismatch", audio.ErrAudioMediaIncompatible)
	}
	return p.run(ctx, request{Operation: OperationMuxAudioCopy, InputPaths: []string{video, finalAudio}, OutputPath: output})
}

func (p *VideoProcessor) RemoveSilence(ctx context.Context, input, output string) error {
	return p.run(ctx, request{Operation: OperationRemoveSilence, SourcePath: input, OutputPath: output})
}

func (p *VideoProcessor) RenderAudioPlan(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets, output string) (audio.FinalAudioAsset, error) {
	asset, _, err := p.RenderAudioPlanWithMetrics(ctx, plan, assets, output)
	return asset, err
}

// RenderAudioPlanWithMetrics is the same combined-audio render as
// RenderAudioPlan but also returns the per-stage timing metrics (mix, AAC
// encode, probe, hash) so the script runner can persist them durably.
func (p *VideoProcessor) RenderAudioPlanWithMetrics(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets, output string) (audio.FinalAudioAsset, scripts.AudioPipelineMetrics, error) {
	if err := plan.Validate(); err != nil {
		return audio.FinalAudioAsset{}, scripts.AudioPipelineMetrics{}, err
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return audio.FinalAudioAsset{}, scripts.AudioPipelineMetrics{}, fmt.Errorf("marshal audio plan: %w", err)
	}
	wireAssets := make([]audioAsset, len(assets))
	for i, asset := range assets {
		wireAssets[i] = audioAsset{AssetID: asset.AssetID, Path: asset.Path}
	}
	result, err := p.client.call(ctx, request{Operation: OperationRenderAudioPlan, OutputPath: output, AudioPlan: planJSON, AudioAssets: wireAssets})
	if err != nil {
		return audio.FinalAudioAsset{}, scripts.AudioPipelineMetrics{}, err
	}
	if result.Metadata == nil {
		return audio.FinalAudioAsset{}, scripts.AudioPipelineMetrics{}, fmt.Errorf("render_audio_plan returned no probe metadata")
	}
	var metrics scripts.AudioPipelineMetrics
	metrics.MixMS = result.Metadata.MixMS
	metrics.AACEncodeMS = result.Metadata.AACEncodeMS
	metrics.ProbeMS = result.Metadata.ProbeMS
	metrics.HashMS = result.Metadata.HashMS

	stat, err := os.Stat(output)
	if err != nil {
		return audio.FinalAudioAsset{}, metrics, fmt.Errorf("stat rendered audio: %w", err)
	}
	asset := audio.FinalAudioAsset{AssetID: output, AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: plan.Version, AudioPlanSHA256: plan.PlanSHA256, FinalAudioSHA256: result.Metadata.FinalAudioSHA256, Codec: result.Metadata.AudioCodec, Profile: result.Metadata.AudioProfile, SampleRate: int(result.Metadata.SampleRate), Channels: int(result.Metadata.Channels), ChannelLayout: plan.Output.ChannelLayout, Bitrate: result.Metadata.Bitrate, DurationMS: int64(result.Metadata.DurationSec*1000 + 0.5), StartPTS: result.Metadata.StartPTS, SizeBytes: stat.Size(), FinalMix: true, CopyEligible: true}
	if err := audio.ValidateFinalAudio(asset, plan); err != nil {
		return audio.FinalAudioAsset{}, metrics, fmt.Errorf("rendered audio certification failed: %w", err)
	}
	return asset, metrics, nil
}

var _ mediaexec.AudioProcessor = (*VideoProcessor)(nil)

// Cut implements the Stock VideoCutter port through the same client and
// protocol used by every other Rust capability.
func (p *VideoProcessor) Cut(ctx context.Context, req stockpipeline.CutRequest) (stockpipeline.CutBatchResult, error) {
	result := stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: make([]stockpipeline.CutItemResult, len(req.Jobs))}
	codec, preset, crf, err := p.policyFor(req.Codec, req.Preset, req.CRF)
	if err != nil {
		return result, err
	}
	profileInput := p.profile
	if profileInput == (mediaexec.VideoProfile{}) {
		profileInput = mediaexec.VideoProfile{Width: req.Width, Height: req.Height, FPS: req.FPS, KeyframeInterval: req.KeyframeInterval}
	}
	profile, err := p.resolvedProfile(profileInput)
	if err != nil {
		return result, err
	}
	wire := request{
		Operation: "cut_batch", SourcePath: req.SourcePath, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels), NoAudio: req.NoAudio,
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
		result.Items[i].SizeBytes, result.Items[i].SHA256Hex = size, hash
	}
	return result, nil
}

func hashOutput(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, "", err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return 0, "", err
	}
	return info.Size(), fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func normalizeProfile(opts mediaexec.NormalizeOptions) mediaexec.VideoProfile {
	profile := opts.Profile
	if opts.Width > 0 {
		profile.Width = opts.Width
	}
	if opts.Height > 0 {
		profile.Height = opts.Height
	}
	if opts.FPS > 0 {
		profile.FPS = opts.FPS
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
	if opts.FPS > 0 {
		profile.FPS = opts.FPS
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
	if requested.FPS > 0 {
		profile.FPS = requested.FPS
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
	if profile.Width <= 0 || profile.Height <= 0 || profile.FPS <= 0 || profile.KeyframeInterval <= 0 || profile.AudioCodec == "" || profile.AudioBitrate == "" || profile.SampleRate <= 0 || profile.Channels <= 0 {
		return fmt.Errorf("PROFILE_REQUIRED: complete resolved video profile is required")
	}
	return nil
}

func (p *VideoProcessor) run(ctx context.Context, req request) error {
	_, err := p.client.call(ctx, req)
	return err
}

// AdminMediaProcessor adapts the Rust capabilities to the operator media
// ports. Drive traversal and publication remain in Go.

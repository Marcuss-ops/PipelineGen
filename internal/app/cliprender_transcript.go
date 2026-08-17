package app

// cliprender_transcript.go wires the streaming transcription path for the
// clip.render preparation phase:
//
//   - clipRenderStreamingTranscriber — FFmpeg decodes the source STRAIGHT to a
//     raw s16le 16kHz mono PCM pipe into the Whisper bridge stdin: no WAV (or
//     any audio intermediate) ever touches disk (feature spec §4).
//   - clipRenderTranscriptResolver — reuses the canonical READY text track
//     when it exists; generates (streaming preferred, WAV chain fallback) and
//     persists otherwise.
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Streaming transcriber (zero temp WAV) ────────────────────────────

// clipRenderStreamingTranscriber decodes the source audio with FFmpeg STRAIGHT
// to a raw s16le 16kHz mono PCM pipe and streams it into the Whisper bridge's
// stdin (feature spec §4): MP4 → FFmpeg decode → PCM pipe → Whisper numpy
// array. No WAV (or any audio intermediate) ever touches disk.
//
// The bridge (scripts/bridges/whisper_transcriber.py --pcm-stdin) delegates
// to scripts/tools/transcribe_detect_lang.py --pcm-stdin, which feeds the PCM
// to faster-whisper as an in-memory float32 array.
type clipRenderStreamingTranscriber struct {
	pythonBin  string
	scriptPath string
	ffmpegPath string
	timeout    time.Duration
	log        *zap.Logger
}

// newClipRenderStreamingTranscriber constructs the streaming transcriber.
// Fail-closed at construction: missing python/ffmpeg/bridge script is a typed
// error (the caller decides whether to fall back to the WAV-based chain).
func newClipRenderStreamingTranscriber(cfg *config.Config, log *zap.Logger) (*clipRenderStreamingTranscriber, error) {
	pythonBin := "python3"
	scriptPath := "scripts/bridges/whisper_transcriber.py"
	ffmpegPath := "ffmpeg"
	if cfg != nil && cfg.External.FfmpegPath != "" {
		ffmpegPath = cfg.External.FfmpegPath
	}
	if _, err := exec.LookPath(pythonBin); err != nil {
		return nil, fmt.Errorf("streaming transcriber: python3 not on PATH: %w", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("streaming transcriber: bridge script %s not accessible: %w", scriptPath, err)
	}
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("streaming transcriber: ffmpeg %q not on PATH: %w", ffmpegPath, err)
	}
	return &clipRenderStreamingTranscriber{
		pythonBin:  pythonBin,
		scriptPath: scriptPath,
		ffmpegPath: ffmpegPath,
		timeout:    5 * time.Minute,
		log:        log,
	}, nil
}

// TranscribeStream decodes source → PCM pipe → Whisper bridge and returns the
// typed transcript. Fail-closed: any subprocess failure, parse error, or
// script error is a typed error — never a placeholder transcript.
func (s *clipRenderStreamingTranscriber) TranscribeStream(ctx context.Context, source *cliprender.MaterializedAsset, language string) (*cliprender.TranscriptResult, error) {
	if source == nil || source.LocalPath == "" {
		return nil, errors.New("clip.render: streaming transcribe requires a materialized source")
	}
	execCtx := ctx
	if s.timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	// FFmpeg decode → raw s16le 16kHz mono PCM on stdout (never a WAV file).
	ffmpeg := exec.CommandContext(execCtx, s.ffmpegPath,
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", source.LocalPath,
		"-vn", "-c:a", "pcm_s16le", "-ar", "16000", "-ac", "1",
		"-f", "s16le", "pipe:1",
	)
	pcmPipe, err := ffmpeg.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("streaming transcribe: ffmpeg stdout pipe: %w", err)
	}

	// Whisper bridge reads the PCM from stdin.
	bridgeArgs := []string{s.scriptPath, "--pcm-stdin"}
	if language != "" && language != "und" {
		bridgeArgs = append(bridgeArgs, "--language", language)
	}
	bridge := exec.CommandContext(execCtx, s.pythonBin, bridgeArgs...)
	bridge.Stdin = pcmPipe
	var bridgeOut, bridgeErr, ffmpegErr bytes.Buffer
	bridge.Stdout = &bridgeOut
	bridge.Stderr = &bridgeErr
	ffmpeg.Stderr = &ffmpegErr

	if err := ffmpeg.Start(); err != nil {
		return nil, fmt.Errorf("streaming transcribe: start ffmpeg: %w", err)
	}
	if err := bridge.Start(); err != nil {
		_ = ffmpeg.Wait()
		return nil, fmt.Errorf("streaming transcribe: start bridge: %w", err)
	}
	// The bridge consumes the PCM pipe to EOF; when it exits the pipe closes
	// and ffmpeg terminates (SIGPIPE on a crashed bridge). Collect both.
	bridgeWaitErr := bridge.Wait()
	ffmpegWaitErr := ffmpeg.Wait()

	if bridgeWaitErr != nil {
		s.log.Warn("streaming transcribe: bridge failed",
			zap.String("source", source.LocalPath),
			zap.String("stderr", bridgeErr.String()),
			zap.String("ffmpeg_stderr", ffmpegErr.String()),
			zap.Error(bridgeWaitErr))
		return nil, fmt.Errorf("whisper bridge subprocess: %w (stderr: %s)", bridgeWaitErr, bridgeErr.String())
	}
	if ffmpegWaitErr != nil {
		s.log.Warn("streaming transcribe: ffmpeg decode failed",
			zap.String("source", source.LocalPath),
			zap.String("stderr", ffmpegErr.String()),
			zap.Error(ffmpegWaitErr))
		return nil, fmt.Errorf("ffmpeg PCM decode: %w (stderr: %s)", ffmpegWaitErr, ffmpegErr.String())
	}

	var res struct {
		Text             string  `json:"text"`
		DetectedLanguage string  `json:"detected_language"`
		Confidence       float64 `json:"confidence"`
		DurationMs       int64   `json:"duration_ms"`
		Cues             []struct {
			StartMs int64  `json:"start_ms"`
			EndMs   int64  `json:"end_ms"`
			Text    string `json:"text"`
		} `json:"cues"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(bridgeOut.Bytes(), &res); err != nil {
		return nil, fmt.Errorf("streaming transcribe: parse bridge JSON: %w (raw: %s)", err, bridgeOut.String())
	}
	if res.Error != "" {
		return nil, fmt.Errorf("streaming transcribe: bridge error: %s", res.Error)
	}
	if res.Text == "" && len(res.Cues) == 0 {
		return nil, fmt.Errorf("%w: empty transcript for %q", cliprender.ErrTranscriptGenerationUnavailable, source.AssetID)
	}

	lang, nErr := asset.Normalize(res.DetectedLanguage)
	if nErr != nil || lang == "und" {
		lang = "und"
	}
	var confPtr *float64
	if res.Confidence > 0 {
		c := res.Confidence
		confPtr = &c
	}
	cues := make([]cliprender.Cue, 0, len(res.Cues))
	for _, c := range res.Cues {
		cues = append(cues, cliprender.Cue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
	}
	return &cliprender.TranscriptResult{
		Language:         lang,
		Text:             res.Text,
		Cues:             cues,
		Confidence:       confPtr,
		DurationMS:       res.DurationMs,
		StreamSourceType: string(asset.TextSourceWhisper),
	}, nil
}

// ── TranscriptResolver ───────────────────────────────────────────────

// clipRenderTranscriptResolver reuses the canonical READY text track when it
// exists and generates (streaming PCM preferred, Whisper chain fallback) +
// optionally persists when it does not.
type clipRenderTranscriptResolver struct {
	repo      asset.TextTrackRepository
	acquire   *texttracks.AcquireService
	streaming *clipRenderStreamingTranscriber
	cueWriter texttracks.TimedCueWriter
	log       *zap.Logger
}

func (r *clipRenderTranscriptResolver) Lookup(ctx context.Context, in cliprender.TranscriptInput) (*cliprender.TranscriptResult, bool, error) {
	if r.repo == nil {
		return nil, false, errors.New("clip.render: text track repository not wired")
	}
	track, cues, err := r.repo.FindReady(ctx, in.AssetID, in.Language, asset.TextTrackTranscript)
	if err != nil {
		return nil, false, err
	}
	if track == nil {
		return nil, false, nil
	}
	hash := track.TextHash
	if hash == "" {
		hash = asset.TextHash(track.TextContent, in.Language, asset.TextTrackTranscript)
	}
	return &cliprender.TranscriptResult{
		AssetID:           in.AssetID,
		Language:          in.Language,
		Text:              track.TextContent,
		Cues:              mapTimedCues(cues),
		TextSHA256:        hash,
		Reused:            true,
		SourceAudioSHA256: in.SourceSHA256,
	}, true, nil
}

func (r *clipRenderTranscriptResolver) Generate(ctx context.Context, in cliprender.TranscriptInput, source *cliprender.MaterializedAsset) (*cliprender.TranscriptResult, error) {
	if source == nil || source.LocalPath == "" {
		return nil, fmt.Errorf("%w: no materialized source audio", cliprender.ErrTranscriptGenerationUnavailable)
	}
	// Preferred path (spec §4): streaming PCM decode → Whisper bridge stdin.
	// Zero audio intermediates on disk; the typed transcript + cues are
	// persisted, never a temp WAV.
	if r.streaming != nil {
		result, err := r.streaming.TranscribeStream(ctx, source, in.Language)
		if err != nil {
			r.log.Warn("clip.render.transcript.streaming_failed",
				zap.String("asset_id", in.AssetID),
				zap.String("fallback", "acquire_chain"),
				zap.Error(err))
		} else {
			return r.finalizeGenerated(ctx, in, source, result), nil
		}
	}
	// Fallback: canonical Whisper chain (WAV-based helper).
	if r.acquire == nil {
		return nil, fmt.Errorf("%w: no Whisper acquisition service wired", cliprender.ErrTranscriptGenerationUnavailable)
	}
	acquired, err := r.acquire.Acquire(ctx, texttracks.AcquireCommand{
		AssetID:   in.AssetID,
		LocalPath: source.LocalPath,
		Language:  in.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("acquire transcript for %q: %w", in.AssetID, err)
	}
	if acquired == nil || (acquired.PlainText == "" && len(acquired.Cues) == 0) {
		return nil, fmt.Errorf("%w: empty transcript for %q", cliprender.ErrTranscriptGenerationUnavailable, in.AssetID)
	}
	result := &cliprender.TranscriptResult{
		Language:         acquired.LanguageCode,
		Text:             acquired.PlainText,
		Cues:             mapTimedCues(acquired.Cues),
		Confidence:       acquired.Confidence,
		DurationMS:       acquired.DurationMs,
		StreamSourceType: string(acquired.SourceType),
	}
	if result.Language == "" {
		result.Language = in.Language
	}
	return r.finalizeGenerated(ctx, in, source, result), nil
}

// finalizeGenerated computes the canonical text hash, persists the READY
// text track + cues when the request asks for it, and returns the typed
// capability result.
func (r *clipRenderTranscriptResolver) finalizeGenerated(
	ctx context.Context,
	in cliprender.TranscriptInput,
	source *cliprender.MaterializedAsset,
	result *cliprender.TranscriptResult,
) *cliprender.TranscriptResult {
	if result.Language == "" {
		result.Language = in.Language
	}
	result.AssetID = in.AssetID
	result.SourceAudioSHA256 = in.SourceSHA256
	result.TextSHA256 = asset.TextHash(result.Text, result.Language, asset.TextTrackTranscript)

	if in.Persist {
		if err := r.persistResult(ctx, in.AssetID, source, result); err != nil {
			r.log.Error("clip.render.transcript.persist_failed",
				zap.String("asset_id", in.AssetID),
				zap.Error(err))
		}
		if r.log != nil {
			r.log.Info("clip.render.transcript.persisted",
				zap.String("asset_id", in.AssetID),
				zap.String("language", result.Language),
				zap.Int("cues", len(result.Cues)),
			)
		}
	}
	return result
}

// persistResult writes the generated transcript as a READY canonical text
// track (idempotent upsert on UNIQUE(asset_id, language_code, text_kind))
// and the timed cues when present. Streaming transcripts are whisper
// provider; the WAV-chain marks its source type from the acquisition result.
func (r *clipRenderTranscriptResolver) persistResult(
	ctx context.Context,
	assetID string,
	source *cliprender.MaterializedAsset,
	result *cliprender.TranscriptResult,
) error {
	if r.repo == nil {
		return errors.New("text track repository not wired for persistence")
	}
	lang := result.Language
	if lang == "" {
		lang = "und"
	}
	srcType := asset.TextSourceWhisper
	if result.StreamSourceType != "" {
		srcType = asset.TextTrackSource(result.StreamSourceType)
	}
	if srcType == "local_file" {
		srcType = asset.TextSourceProvided
	}
	track := asset.TextTrack{
		AssetID:            assetID,
		LanguageCode:       lang,
		TextKind:           asset.TextTrackTranscript,
		TextContent:        result.Text,
		SourceType:         srcType,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           clipRenderProviderFor(srcType),
		TextHash:           result.TextSHA256,
		Confidence:         result.Confidence,
		Status:             asset.TextTrackReady,
	}
	if err := r.repo.UpsertBatch(ctx, []asset.TextTrack{track}); err != nil {
		return err
	}
	if len(result.Cues) > 0 && r.cueWriter != nil {
		cues := make([]asset.TimedCue, 0, len(result.Cues))
		for _, c := range result.Cues {
			cues = append(cues, asset.TimedCue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
		}
		return r.cueWriter.ReplaceTranscriptCues(ctx, assetID, map[string][]asset.TimedCue{lang: cues})
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────

func mapTimedCues(in []asset.TimedCue) []cliprender.Cue {
	if len(in) == 0 {
		return nil
	}
	out := make([]cliprender.Cue, 0, len(in))
	for _, c := range in {
		out = append(out, cliprender.Cue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
	}
	return out
}

func mapClipRenderCues(in []cliprender.Cue) []asset.TimedCue {
	if len(in) == 0 {
		return nil
	}
	out := make([]asset.TimedCue, 0, len(in))
	for _, c := range in {
		out = append(out, asset.TimedCue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
	}
	return out
}

// clipRenderProviderFor maps the acquisition source_type to the canonical
// provider label persisted on the text track (whisper/youtube, else empty).
func clipRenderProviderFor(st asset.TextTrackSource) string {
	switch st {
	case asset.TextSourceYouTubeSubtitle:
		return "youtube"
	case asset.TextSourceWhisper:
		return "whisper"
	default:
		return ""
	}
}

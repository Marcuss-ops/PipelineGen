package app

// cliprender_adapters.go wires the concrete adapters for the clip.render
// parallel preparation phase. The capability (internal/capabilities/cliprender)
// owns the ports; THIS file (composition root) owns the mechanics:
//
//   - AssetResolver     → canonical asset registry (asset.Service)
//   - AssetMaterializer → local copy reuse + Drive download to scratch
//   - TranscriptResolver → canonical text-track repo (reuse) + AcquireService
//     (Whisper chain generation) + cue writer (persist)
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// hashFile and firstNonEmpty are shared with vidrush_materialization_wiring.go
// (same package) — do not redeclare them here.

// ── AssetResolver ────────────────────────────────────────────────────

// clipRenderAssetResolver maps a canonical asset_id to the capability's
// AssetRef via the canonical asset registry.
type clipRenderAssetResolver struct {
	assets *asset.Service
}

func (r *clipRenderAssetResolver) ResolveAsset(ctx context.Context, assetID string) (*cliprender.AssetRef, error) {
	if r.assets == nil {
		return nil, errors.New("clip.render: asset registry not wired")
	}
	details, err := r.assets.Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("load asset %q: %w", assetID, err)
	}
	if details == nil || details.Asset == nil {
		return nil, fmt.Errorf("asset %q not found", assetID)
	}
	a := details.Asset
	return &cliprender.AssetRef{
		AssetID:     a.ID,
		MediaType:   string(a.MediaType),
		LocalPath:   a.LocalPath(),
		DriveFileID: a.DriveFileID(),
		FileHash:    firstNonEmpty(a.Sha256(), a.FileHash(), a.ContentHash()),
		DurationMS:  a.Duration.Milliseconds(),
	}, nil
}

// ── AssetMaterializer ────────────────────────────────────────────────

// clipRenderMaterializer ensures the asset bytes are local. Precedence:
// (1) the registry's local_path when the file exists, (2) a content-addressed
// scratch copy already downloaded in a prior run, (3) a fresh Drive download
// into scratch. A missing local copy AND missing Drive source fails closed.
type clipRenderMaterializer struct {
	drive      drivepkg.Reader
	scratchDir string
}

func (m *clipRenderMaterializer) Materialize(ctx context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	// (1) Registered local copy.
	if ref.LocalPath != "" {
		if info, err := os.Stat(ref.LocalPath); err == nil && !info.IsDir() {
			sha, size, err := hashFile(ref.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("hash local source %q: %w", ref.LocalPath, err)
			}
			return &cliprender.MaterializedAsset{
				AssetID:    ref.AssetID,
				LocalPath:  ref.LocalPath,
				SHA256:     sha,
				SizeBytes:  size,
				DurationMS: ref.DurationMS,
				FromCache:  true,
			}, nil
		}
	}

	// (2/3) Drive materialization into scratch.
	if ref.DriveFileID == "" {
		return nil, fmt.Errorf("clip.render: asset %q has neither a local copy nor a Drive source", ref.AssetID)
	}
	if m.drive == nil {
		return nil, fmt.Errorf("clip.render: Drive reader not wired (asset %q requires Drive materialization)", ref.AssetID)
	}
	target := filepath.Join(m.scratchDir, "assets", ref.AssetID+".mp4")
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		sha, size, err := hashFile(target)
		if err != nil {
			return nil, fmt.Errorf("hash cached source %q: %w", target, err)
		}
		return &cliprender.MaterializedAsset{
			AssetID:    ref.AssetID,
			LocalPath:  target,
			SHA256:     sha,
			SizeBytes:  size,
			DurationMS: ref.DurationMS,
			FromCache:  true,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	rc, _, err := m.drive.DownloadFile(ctx, ref.DriveFileID)
	if err != nil {
		return nil, fmt.Errorf("download asset %q from Drive: %w", ref.AssetID, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return nil, fmt.Errorf("create scratch file: %w", err)
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hasher), rc)
	closeErr := out.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("write scratch file %q: %w", target, copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close scratch file %q: %w", target, closeErr)
	}
	return &cliprender.MaterializedAsset{
		AssetID:    ref.AssetID,
		LocalPath:  target,
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:  n,
		DurationMS: ref.DurationMS,
		FromCache:  false,
	}, nil
}

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

// ── SubtitleCompiler (deterministic ASS) ─────────────────────────────

// clipRenderSubtitleCompiler implements the capability's SubtitleCompiler
// port with the canonical ASS generator (texttracks.CompileASSContent — the
// single owner of ASS content generation). The artifact is written into the
// run's scratch dir (subtitles.ass) and validated before the plan is sealed.
//
// Determinism: identical cues + style ALWAYS produce identical bytes (the
// generator embeds no timestamps/randoms/paths). Mode burn|sidecar only
// tags the artifact — the ASS bytes are the same, the render pass decides
// whether to rasterize libass (burn) or ship the file (sidecar).
//
// Fail-closed: empty cues, an invalid mode, or an invalid generated ASS is a
// typed error — speech recognition is NEVER regenerated just to build
// subtitles (feature spec §5).
type clipRenderSubtitleCompiler struct{}

func (c *clipRenderSubtitleCompiler) Compile(ctx context.Context, in cliprender.SubtitleCompileInput) (*cliprender.SubtitleArtifact, error) {
	switch in.Mode {
	case cliprender.SubtitlesModeBurn, cliprender.SubtitlesModeSidecar:
	default:
		return nil, fmt.Errorf("%w: invalid subtitle mode %q", cliprender.ErrSubtitleCompileUnavailable, in.Mode)
	}
	if len(in.Cues) == 0 {
		return nil, fmt.Errorf("%w: zero cues for asset %q — subtitles cannot be compiled without transcript timing (speech recognition is never regenerated for subtitles)", cliprender.ErrSubtitleCompileUnavailable, in.AssetID)
	}
	content, err := texttracks.CompileASSContent(mapClipRenderCues(in.Cues), in.StyleID)
	if err != nil {
		return nil, fmt.Errorf("%w: compile ASS content: %v", cliprender.ErrSubtitleCompileUnavailable, err)
	}
	if err := os.MkdirAll(in.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create subtitle output dir %q: %w", in.OutputDir, err)
	}
	localPath := filepath.Join(in.OutputDir, "subtitles.ass")
	if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write ASS artifact %q: %w", localPath, err)
	}
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	if err := texttracks.ValidateASSFile(localPath, in.ClipDurationMS); err != nil {
		return nil, fmt.Errorf("%w: invalid generated ASS for asset %q: %v", cliprender.ErrSubtitleCompileUnavailable, in.AssetID, err)
	}
	return &cliprender.SubtitleArtifact{
		LocalPath: localPath,
		SHA256:    sha,
		Mode:      in.Mode,
		StyleID:   in.StyleID,
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

package adapters

// cliprender_transcript.go wires the streaming transcription path for the
// clip.render preparation phase:
//
//   - ClipRenderStreamingTranscriber — FFmpeg decodes the source STRAIGHT to a
//     raw s16le 16kHz mono PCM pipe into the Whisper bridge stdin: no WAV (or
//     any audio intermediate) ever touches disk (feature spec §4).
//   - ClipRenderTranscriptResolver — reuses the canonical READY text track
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
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Streaming transcriber (zero temp WAV) ────────────────────────────

// ClipRenderStreamingTranscriber decodes the source audio with FFmpeg STRAIGHT
// to a raw s16le 16kHz mono PCM pipe and streams it into the Whisper bridge's
// stdin (feature spec §4): MP4 → FFmpeg decode → PCM pipe → Whisper numpy
// array. No WAV (or any audio intermediate) ever touches disk.
//
// The bridge (scripts/bridges/whisper_transcriber.py --pcm-stdin) delegates
// to scripts/tools/transcribe_detect_lang.py --pcm-stdin, which feeds the PCM
// to faster-whisper as an in-memory float32 array.
type ClipRenderStreamingTranscriber struct {
	pythonBin  string
	scriptPath string
	ffmpegPath string
	timeout    time.Duration
	log        *zap.Logger
	// cache is the shared derived-artifact cache (SQLite mapping + local CAS
	// bytes). The streaming path used to bypass the cached Whisper decorator,
	// so a retry over identical source bytes paid a full ASR pass again.
	cache        capcache.Cache
	cacheVersion string
}

// NewClipRenderStreamingTranscriber constructs the streaming transcriber.
// Fail-closed at construction: missing python/ffmpeg/bridge script is a typed
// error (the caller decides whether to fall back to the WAV-based chain).
func NewClipRenderStreamingTranscriber(cfg *config.Config, log *zap.Logger) (*ClipRenderStreamingTranscriber, error) {
	// The bridge needs the .venv-whisper interpreter (faster-whisper +
	// ctranslate2 are not installed in the system python3). Hardcoding
	// "python3" only worked when the service PATH pointed at that venv; any
	// invocation outside the systemd unit silently degraded every clip to the
	// WAV fallback.
	pythonBin := "python3"
	scriptPath := "scripts/bridges/whisper_transcriber.py"
	ffmpegPath := "ffmpeg"
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.Paths.WhisperPythonBin); configured != "" {
			pythonBin = configured
		}
		if cfg.External.FfmpegPath != "" {
			ffmpegPath = cfg.External.FfmpegPath
		}
	}
	if _, err := exec.LookPath(pythonBin); err != nil {
		return nil, fmt.Errorf("streaming transcriber: interpreter %q not executable: %w", pythonBin, err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("streaming transcriber: bridge script %s not accessible: %w", scriptPath, err)
	}
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("streaming transcriber: ffmpeg %q not on PATH: %w", ffmpegPath, err)
	}
	return &ClipRenderStreamingTranscriber{
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
func (s *ClipRenderStreamingTranscriber) TranscribeStream(ctx context.Context, source *cliprender.MaterializedAsset, language string) (*cliprender.TranscriptResult, error) {
	if source == nil || source.LocalPath == "" {
		return nil, errors.New("clip.render: streaming transcribe requires a materialized source")
	}
	// The cache address is the SOURCE BYTES (never the local path, which may be
	// a fresh materialization each run) plus the requested language: a forced
	// decode for one language must never satisfy another language's request.
	cacheKey, cacheable := streamingWhisperCacheKey(source.SHA256, language, s.cacheVersion)
	if s.cache != nil && cacheable {
		if cached, hit := s.lookupCachedTranscript(ctx, cacheKey, source); hit {
			return cached, nil
		}
	}
	// Whisper shares the GPU with Chronon. Hold one process-wide slot around
	// the bridge subprocess (the cache hit above costs no slot). This is the
	// gate that prevents an ASR fan-out from competing with a Vulkan render for
	// VRAM (CR-15).
	releaseGPU, err := acquireWhisperGPU(ctx)
	if err != nil {
		return nil, fmt.Errorf("streaming transcribe: whisper GPU slot: %w", err)
	}
	defer releaseGPU()
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
		if ffmpeg.Process != nil {
			_ = ffmpeg.Process.Kill()
		}
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
		Text string `json:"text"`
		// Language is the language the text was decoded as; DetectedLanguage is
		// what the model heard with NO language forced. The bridge emits both, so
		// a forced decode's echo can never be read as a detection.
		Language         string  `json:"language"`
		DetectedLanguage string  `json:"detected_language"`
		LanguageForced   bool    `json:"language_forced"`
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
		// The bridge ran and completed: this is silence / non-speech audio,
		// not an ASR failure. Classify it as such so the failure projection
		// says NO_SPEECH instead of blaming transcript generation.
		return nil, fmt.Errorf("%w: empty transcript for %q", cliprender.ErrNoSpeech, source.AssetID)
	}

	// The language of the TEXT: what the decode used (the forced request, or the
	// detection when nothing was forced). An older bridge emits only
	// detected_language, so that field is the documented fallback.
	used := strings.TrimSpace(res.Language)
	if used == "" {
		used = res.DetectedLanguage
	}
	lang, nErr := asset.Normalize(used)
	if nErr != nil || lang == "und" {
		lang = "und"
	}
	// The language of the AUDIO, kept strictly separate from the decode
	// language. When the two disagree the text is a forced decode of audio in
	// another language, and persistence must not present it as the clip's own
	// transcript in that language.
	detected, dErr := asset.Normalize(res.DetectedLanguage)
	if dErr != nil || detected == "und" {
		detected = ""
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
	result := &cliprender.TranscriptResult{
		Language:         lang,
		DetectedLanguage: detected,
		LanguageForced:   res.LanguageForced,
		Text:             res.Text,
		Cues:             cues,
		Confidence:       confPtr,
		DurationMS:       res.DurationMs,
		StreamSourceType: string(detail.TextSourceWhisper),
	}
	if s.cache != nil && cacheable {
		s.storeCachedTranscript(ctx, cacheKey, result)
	}
	return result, nil
}

// ── TranscriptResolver ───────────────────────────────────────────────

// ClipRenderTranscriptResolver reuses the canonical READY text track when it
// exists and generates (streaming PCM preferred, Whisper chain fallback) +
// optionally persists when it does not.
type ClipRenderTranscriptResolver struct {
	repo      detail.TextTrackRepository
	acquire   *texttracks.AcquireService
	streaming *ClipRenderStreamingTranscriber
	cueWriter texttracks.TimedCueWriter
	log       *zap.Logger
}

func (r *ClipRenderTranscriptResolver) Lookup(ctx context.Context, in cliprender.TranscriptInput) (*cliprender.TranscriptResult, bool, error) {
	if r.repo == nil {
		return nil, false, errors.New("clip.render: text track repository not wired")
	}
	track, cues, err := r.repo.FindReady(ctx, in.AssetID, in.Language, detail.TextTrackTranscript)
	if err != nil {
		return nil, false, err
	}
	if track == nil {
		// A requested language is an exact render contract. Falling back to the
		// first available track silently burned English subtitles for Italian and
		// German requests, while the job still reported SUCCEEDED. The caller
		// must either materialize the requested READY translation first or choose
		// an explicit language; never substitute a different track here.
		return nil, false, nil
	}
	hash := track.TextHash
	if hash == "" {
		hash = detail.TextHash(track.TextContent, track.LanguageCode, detail.TextTrackTranscript)
	}
	if err := validateRequestedTranscriptLanguage(in.Language, track.LanguageCode); err != nil {
		return nil, false, err
	}
	return &cliprender.TranscriptResult{
		AssetID:           in.AssetID,
		Language:          track.LanguageCode,
		Text:              track.TextContent,
		Cues:              mapTimedCues(cues),
		TextSHA256:        hash,
		Reused:            true,
		SourceAudioSHA256: in.SourceSHA256,
	}, true, nil
}

func (r *ClipRenderTranscriptResolver) Generate(ctx context.Context, in cliprender.TranscriptInput, source *cliprender.MaterializedAsset) (*cliprender.TranscriptResult, error) {
	if source == nil || source.LocalPath == "" {
		return nil, fmt.Errorf("%w: no materialized source audio", cliprender.ErrTranscriptGenerationUnavailable)
	}
	// Preferred path (spec §4): streaming PCM decode → Whisper bridge stdin.
	// Zero audio intermediates on disk; the typed transcript + cues are
	// persisted, never a temp WAV.
	// noSpeech remembers a completed-but-empty streaming ASR pass. The chain
	// still falls back (YouTube subtitles may exist for the same clip), but if
	// every priority also fails the operator must see NO_SPEECH rather than a
	// generic acquisition error.
	noSpeech := false
	if r.streaming != nil {
		result, err := r.streaming.TranscribeStream(ctx, source, in.Language)
		if err != nil {
			noSpeech = errors.Is(err, cliprender.ErrNoSpeech)
			r.log.Warn("clip.render.transcript.streaming_failed",
				zap.String("asset_id", in.AssetID),
				zap.String("fallback", "acquire_chain"),
				zap.Bool("no_speech", noSpeech),
				zap.Error(err))
		} else {
			if err := validateRequestedTranscriptLanguage(in.Language, result.Language); err != nil {
				return nil, err
			}
			return r.finalizeGenerated(ctx, in, source, result)
		}
	}
	// Fallback: canonical Whisper chain (WAV-based helper).
	if r.acquire == nil {
		if noSpeech {
			return nil, fmt.Errorf("%w: the streaming ASR found no speech and no acquisition service is wired", cliprender.ErrNoSpeech)
		}
		return nil, fmt.Errorf("%w: no Whisper acquisition service wired", cliprender.ErrTranscriptGenerationUnavailable)
	}
	// The fallback chain may itself invoke Whisper. Hold the same process-wide
	// slot as the streaming path so the two routes cannot each start an ASR
	// process at the same time (CR-15).
	releaseGPU, gateErr := acquireWhisperGPU(ctx)
	if gateErr != nil {
		return nil, fmt.Errorf("acquire transcript for %q: whisper GPU slot: %w", in.AssetID, gateErr)
	}
	defer releaseGPU()
	// The chain needs the SOURCE identity, not just the temporary local file:
	// without VideoID/StartSec/EndSec priorities 3+4 (YouTube manual/auto
	// subtitles) cannot run at all, and without DriveFileID priority 2.5 cannot
	// repair a corrupt registered copy.
	acquired, err := r.acquire.Acquire(ctx, texttracks.AcquireCommand{
		AssetID:     in.AssetID,
		VideoID:     source.VideoID,
		LocalPath:   source.LocalPath,
		StartSec:    source.StartSec,
		EndSec:      source.EndSec,
		DriveFileID: source.DriveFileID,
		Language:    in.Language,
	})
	if err != nil {
		if noSpeech {
			return nil, fmt.Errorf("acquire transcript for %q after an empty streaming ASR result: %w (%w)", in.AssetID, err, cliprender.ErrNoSpeech)
		}
		return nil, fmt.Errorf("acquire transcript for %q: %w", in.AssetID, err)
	}
	if acquired == nil || (acquired.PlainText == "" && len(acquired.Cues) == 0) {
		// A silent acquire chain is NOT proof of silence on its own, but when
		// the streaming ASR already completed with no text it is: report the
		// no-speech condition instead of a bare "generation unavailable".
		if noSpeech {
			return nil, fmt.Errorf("%w: empty transcript for %q", cliprender.ErrNoSpeech, in.AssetID)
		}
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
	if err := validateRequestedTranscriptLanguage(in.Language, result.Language); err != nil {
		return nil, err
	}
	return r.finalizeGenerated(ctx, in, source, result)
}

// sameLanguage reports whether two BCP-47 tags name the same language. The
// comparison goes through the canonical normalizer so a format variant ("EN"
// vs "en") is not read as two different languages; a tag the normalizer cannot
// read is only compared case-insensitively, never assumed equal.
func sameLanguage(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return a == b
	}
	if strings.EqualFold(a, b) {
		return true
	}
	na, aErr := asset.Normalize(a)
	nb, bErr := asset.Normalize(b)
	return aErr == nil && bErr == nil && na == nb
}

func validateRequestedTranscriptLanguage(requested, actual string) error {
	requested = strings.TrimSpace(requested)
	actual = strings.TrimSpace(actual)
	if requested != "" && actual != "" && !strings.EqualFold(requested, actual) {
		return fmt.Errorf("clip.render: transcript language mismatch: requested %q, resolved %q", requested, actual)
	}
	return nil
}

// finalizeGenerated computes the canonical text hash, persists the READY
// text track + cues when the request asks for it, and returns the typed
// capability result.
func (r *ClipRenderTranscriptResolver) finalizeGenerated(
	ctx context.Context,
	in cliprender.TranscriptInput,
	source *cliprender.MaterializedAsset,
	result *cliprender.TranscriptResult,
) (*cliprender.TranscriptResult, error) {
	if result.Language == "" {
		result.Language = in.Language
	}
	result.AssetID = in.AssetID
	result.SourceAudioSHA256 = in.SourceSHA256
	result.TextSHA256 = detail.TextHash(result.Text, result.Language, detail.TextTrackTranscript)

	if in.Persist {
		if err := r.persistResult(ctx, in.AssetID, source, result); err != nil {
			r.log.Error("clip.render.transcript.persist_failed",
				zap.String("asset_id", in.AssetID),
				zap.Error(err))
			return nil, fmt.Errorf("clip.render: persist transcript %q/%q: %w", in.AssetID, result.Language, err)
		}
		if r.log != nil {
			r.log.Info("clip.render.transcript.persisted",
				zap.String("asset_id", in.AssetID),
				zap.String("language", result.Language),
				zap.Int("cues", len(result.Cues)),
			)
		}
	}
	return result, nil
}

// persistResult writes the generated transcript as a READY canonical text
// track (idempotent upsert on UNIQUE(asset_id, language_code, text_kind))
// and the timed cues when present. Streaming transcripts are whisper
// provider; the WAV-chain marks its source type from the acquisition result.
func (r *ClipRenderTranscriptResolver) persistResult(
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
	// Honest provenance (godlike/07): LanguageCode is the language of the TEXT;
	// source_language_code is the language the AUDIO is actually in; is_original
	// is true ONLY when those two are the same. A forced decode of audio in
	// another language yields text that is not the clip's own transcript, and
	// recording it as the original would make every later reader (translation
	// source selection, search indexing, subtitle QA) treat fabricated text as
	// the clip's ground truth.
	sourceLang := result.DetectedLanguage
	if sourceLang == "" {
		sourceLang = lang
	}
	isOriginal := sameLanguage(sourceLang, lang)
	if !isOriginal {
		r.log.Warn("clip.render.transcript.forced_language_mismatch",
			zap.String("asset_id", assetID),
			zap.String("decoded_language", lang),
			zap.String("detected_language", sourceLang),
			zap.Bool("language_forced", result.LanguageForced),
			zap.String("effect", "persisted as a non-original variant, not the clip's transcript"),
		)
	}
	srcType := detail.TextSourceWhisper
	if result.StreamSourceType != "" {
		srcType = detail.TextTrackSource(result.StreamSourceType)
	}
	if srcType == "local_file" {
		srcType = detail.TextSourceProvided
	}
	track := detail.TextTrack{
		AssetID:            assetID,
		LanguageCode:       lang,
		TextKind:           detail.TextTrackTranscript,
		TextContent:        result.Text,
		SourceType:         srcType,
		SourceLanguageCode: sourceLang,
		IsOriginal:         isOriginal,
		Provider:           clipRenderProviderFor(srcType),
		TextHash:           result.TextSHA256,
		Confidence:         result.Confidence,
		Status:             detail.TextTrackReady,
	}
	if err := r.repo.UpsertBatch(ctx, []detail.TextTrack{track}); err != nil {
		return err
	}
	if len(result.Cues) > 0 && r.cueWriter != nil {
		cues := make([]detail.TimedCue, 0, len(result.Cues))
		for _, c := range result.Cues {
			cues = append(cues, detail.TimedCue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
		}
		byLang, err := r.cuesForWholeAssetReplacement(ctx, assetID, lang, cues)
		if err != nil {
			return err
		}
		return r.cueWriter.ReplaceTranscriptCues(ctx, assetID, byLang)
	}
	return nil
}

// cuesForWholeAssetReplacement preserves the other READY languages before a
// transcript cue write. ReplaceTranscriptCues deletes EVERY READY transcript
// cue for the asset and then inserts only the languages present in the map;
// passing a single language would silently erase every other language's
// timing. The merge is the caller-side half of that whole-asset contract.
func (r *ClipRenderTranscriptResolver) cuesForWholeAssetReplacement(
	ctx context.Context,
	assetID string,
	lang string,
	cues []detail.TimedCue,
) (map[string][]detail.TimedCue, error) {
	byLang := map[string][]detail.TimedCue{lang: cues}
	if r.repo == nil {
		return nil, errors.New("clip.render: text track repository not wired for cue preservation")
	}
	tracks, err := r.repo.ListByAsset(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("clip.render: list transcript cues for %q: %w", assetID, err)
	}
	for _, track := range tracks {
		if track.LanguageCode == lang || track.TextKind != detail.TextTrackTranscript || track.Status != detail.TextTrackReady {
			continue
		}
		_, existing, err := r.repo.FindReady(ctx, assetID, track.LanguageCode, detail.TextTrackTranscript)
		if err != nil {
			return nil, fmt.Errorf("clip.render: load %q transcript cues for %q: %w", track.LanguageCode, assetID, err)
		}
		if existing != nil {
			byLang[track.LanguageCode] = existing
		}
	}
	return byLang, nil
}

// ── Helpers ──────────────────────────────────────────────────────────

func mapTimedCues(in []detail.TimedCue) []cliprender.Cue {
	if len(in) == 0 {
		return nil
	}
	out := make([]cliprender.Cue, 0, len(in))
	for _, c := range in {
		out = append(out, cliprender.Cue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
	}
	return out
}

func mapClipRenderCues(in []cliprender.Cue) []detail.TimedCue {
	if len(in) == 0 {
		return nil
	}
	out := make([]detail.TimedCue, 0, len(in))
	for _, c := range in {
		out = append(out, detail.TimedCue{StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text})
	}
	return out
}

// clipRenderProviderFor maps the acquisition source_type to the canonical
// provider label persisted on the text track (whisper/youtube, else empty).
func clipRenderProviderFor(st detail.TextTrackSource) string {
	switch st {
	case detail.TextSourceYouTubeSubtitle:
		return "youtube"
	case detail.TextSourceWhisper:
		return "whisper"
	default:
		return ""
	}
}

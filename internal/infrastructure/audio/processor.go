package audioasset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// Processor — PR-VO-B1 (June 2026): the previous direct Drive
// coupling has been removed. Processor writes ONLY to the local
// filesystem; the Drive upload belongs to Lifecycle (which already
// owns Step 2 of ProcessAsset in internal/application/assets/lifecycle/
// service.go). voiceover.go now calls NewProcessor with only
// (pythonScriptsDir, log) and the audioasset package no longer
// imports infrastructure/drive or domain/asset.
//
// VO-DECOMPOSITION P0 #1 (July 2026): persistent TTS worker.
// Processor now manages a long-lived tts_edge_server.py subprocess
// (aiohttp HTTP server) instead of spawning a new `python3 tts_edge.py`
// process per call. The persistent worker is lazily started on the
// first Generate() call. When the worker is unavailable (Python not
// installed, server script not deployed, startup failure), Generate
// falls back to the legacy spawn-per-call path for backward compat.
// // Fields added (VO-DECOMPOSITION P0 #1 lazy worker state):
//   - mu sync.Mutex — protects worker lifecycle only; HTTP calls are concurrent
//   - cmd *exec.Cmd — the running subprocess (nil when not started)
//   - baseURL string — "http://127.0.0.1:<port>" discovered from stdout
//   - httpClient *http.Client — shared HTTP client (5-min timeout)
//   - started bool — true after successful startup
type Processor struct {
	pythonScriptsDir string
	log              *zap.Logger

	// Persistent worker state (VO-DECOMPOSITION P0 #1).
	mu           sync.Mutex
	requestSlots chan struct{}
	cmd          *exec.Cmd
	baseURL      string
	httpClient   *http.Client
	started      bool
	media        mediaexec.AudioProcessor
	loudness     capabilityaudio.LoudnessProber
}

// processorShape mirrors the GENERATE-side surface of
// voiceover.TTSProvider. The canonical port lives in
// internal/application/voiceover/ports.go::TTSProvider (signature:
// Synthesize(ctx, TTSInput) (TTSOutput, error)); *Processor satisfies
// it via the useCaseTTSAdapter at
// internal/app/adapters_voiceover_use_case.go which adapts the
// voiceover.TTSInput → audioasset.AudioInput and AudioResult → voiceover.TTSOutput.
//
// godlike/06 SSOT (one canonical owner per fact): the local mirror
// exists because importing voiceover here would create an import cycle
// (voiceover/ports.go is un-containerised to infrastructure/audio).
// Any drift in the Generate signature surfaces as a compile error here,
// NOT at first panic runtime.
//
// Cross-reference: AGENTS.md Pattern 0 + godlike/06 SSOT (one
// canonical owner per fact); the canonical TTSProvider pin is at
// `var _ voiceover.TTSProvider = (*useCaseTTSAdapter)(nil)` in
// internal/app/adapters_voiceover_use_case.go.
type processorShape interface {
	Generate(ctx context.Context, input *AudioInput) (*AudioResult, error)
}

// Compile-time pin (PR-VO-TTS-PERSISTENT-WORKER, July 2026): *Processor
// must structurally satisfy processorShape. Drift between the canonical
// voiceover.TTSProvider port and *Processor.Generate surfaces as a build
// failure here (forward-prevention per godlike/07 + Pattern 0).
var _ processorShape = (*Processor)(nil)

// NewProcessor constructs a Processor. The previous driveUploader and
// assetDestResolver arguments are intentionally gone — Processor
// handles local FS only (TTS generation + optional FFmpeg silence
// removal + MD5 hash). Drive upload is owned by Lifecycle.
//
// VO-DECOMPOSITION P0 #1 (July 2026): the persistent worker is NOT
// started at construction time — it's lazily initialised on the first
// Generate() call.
func NewProcessor(
	pythonScriptsDir string,
	log *zap.Logger,
) *Processor {
	return &Processor{
		pythonScriptsDir: pythonScriptsDir,
		log:              log,
		requestSlots:     make(chan struct{}, DefaultTTSRequestConcurrency),
	}
}

// DefaultTTSRequestConcurrency is the measured saturation point for the
// persistent Edge TTS worker. The concurrency benchmark showed that 4 is
// already near the throughput plateau while keeping queueing low; callers
// may still override it through voiceover.max_concurrent_tts.
const DefaultTTSRequestConcurrency = 4

// SetRequestConcurrency bounds simultaneous requests sent to the persistent
// Edge TTS worker. It is deliberately separate from the application fan-out:
// the sidecar is async, but the remote service may throttle excessive sockets.
func (p *Processor) SetRequestConcurrency(n int) {
	if p == nil {
		return
	}
	if n <= 0 {
		n = DefaultTTSRequestConcurrency
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requestSlots = make(chan struct{}, n)
}

func (p *Processor) SetMediaExecutor(media mediaexec.AudioProcessor) {
	if p == nil {
		return
	}
	p.media = media
}

// SetLoudnessProber registers the minimum-loudness gate. When set, Generate
// measures the synthesized VO's peak level and fails closed with
// ErrSilentAudio (retried like empty audio) when it is inaudible. When unset
// (default), the gate is skipped — the capability is simply not registered.
func (p *Processor) SetLoudnessProber(l capabilityaudio.LoudnessProber) {
	if p == nil {
		return
	}
	p.loudness = l
}

func (p *Processor) mediaExecutor() (mediaexec.AudioProcessor, error) {
	if p == nil || p.media == nil {
		return nil, fmt.Errorf("audio media executor unavailable")
	}
	return p.media, nil
}

// Generate runs TTS over the persistent Python worker (preferred) or
// the legacy spawn-per-call path (fallback). Local FS only; no Drive
// interaction.
//
// VO-DECOMPOSITION P0 #1 (July 2026): the persistent worker path
// eliminates the per-call Python startup cost (~1-3s cold start).
// The legacy spawn-per-call path is retained as backward-compat
// fallback for environments where the server script is not deployed
// or Python is unavailable.
//
// godlike/07 typed-error contract (PR-VO-TTS-PERSISTENT-WORKER): the
// path-traversal guard fails-closed with the typed ErrInvalidFilename
// sentinel so a caller supplying "../foo" gets a probe-able error
// without parsing string fragments.
func (p *Processor) Generate(ctx context.Context, input *AudioInput) (*AudioResult, error) {
	// Defense-in-depth: validate filename against path traversal.
	safeName := filepath.Base(input.Filename)
	if safeName != input.Filename {
		return nil, fmt.Errorf("invalid filename: path traversal detected: %w", ErrInvalidFilename)
	}
	if filepath.Ext(safeName) == "" {
		safeName += ".mp3"
	}

	// The bridge occasionally returns an unusable audio file: empty
	// (edge-tts rate-limit / transient network glitch) or silent
	// (non-empty but inaudible). Retry the synthesis a few times before
	// surfacing the failure, so a one-off bad response never fails the
	// whole run immediately.
	const maxRetryableAttempts = 3
	const retryableBackoff = 300 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxRetryableAttempts; attempt++ {
		res, err := p.synthesizeOnce(ctx, input, safeName)
		if err != nil {
			if !errors.Is(err, ErrEmptyAudio) && !errors.Is(err, ErrSilentAudio) {
				return nil, err
			}
			lastErr = err
		} else if err := p.gateLoudness(ctx, res); err != nil {
			if !errors.Is(err, ErrSilentAudio) {
				return nil, err
			}
			lastErr = err
		} else {
			return res, nil
		}
		p.log.Warn("TTS bridge returned unusable audio — retrying synthesis",
			zap.Int("attempt", attempt+1),
			zap.Int("max_attempts", maxRetryableAttempts),
			zap.Error(lastErr))
		// Give edge-tts a moment to recover from rate limiting before the
		// next attempt; honour ctx cancellation so the worker shutdown path
		// is never delayed.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryableBackoff):
		}
	}
	return nil, fmt.Errorf("TTS generation failed after %d attempts: %w", maxRetryableAttempts, lastErr)
}

// gateLoudness enforces the minimum-loudness floor on a synthesized VO. It
// returns ErrSilentAudio when the audio is definitively inaudible so Generate
// can retry it. A measurement-tool failure is logged and skipped (mirrors the
// duration-probe fail-open behaviour): the empty-file check and the downstream
// timing/certification gates still catch gross corruption, so a transient
// ffmpeg error must not hard-fail an otherwise-valid synthesis.
func (p *Processor) gateLoudness(ctx context.Context, res *AudioResult) error {
	if p == nil || p.loudness == nil || res == nil || res.LocalPath == "" {
		return nil
	}
	l, err := p.loudness.MeasureLoudness(ctx, res.LocalPath)
	if err != nil {
		p.log.Warn("voiceover loudness gate: measurement unavailable, skipping",
			zap.String("path", res.LocalPath),
			zap.Error(err))
		return nil
	}
	if l.IsSilent() {
		return fmt.Errorf("TTS bridge produced silent audio (max_volume=%.2f dB): %w", l.MaxDB, ErrSilentAudio)
	}
	return nil
}

// synthesizeOnce runs a single synthesis attempt (persistent worker when
// available, legacy spawn-per-call otherwise). Kept separate from Generate
// so the empty-audio retry loop re-runs the WHOLE synthesis, not just the
// response parsing. The lifecycle mutex is held only while starting/checking
// the worker; the potentially-minute-long HTTP request is never serialized.
func (p *Processor) synthesizeOnce(ctx context.Context, input *AudioInput, safeName string) (*AudioResult, error) {
	// Try persistent worker first.
	// BUG-FIX (2026-07-10): 3 interacting bugs caused the voiceover
	// pipeline hang:
	//   1. Missing defer p.mu.Unlock() — panic in ensureStarted or
	//      sendSynthesizeRequest poisoned the mutex permanently
	//   2. ensureStarted() blocked indefinitely on scanner.Scan()
	//      when Python stdout was buffered (no PYTHONUNBUFFERED=1)
	//   3. No timeout on PORT line reading — Python crash or buffer
	//      stall left the goroutine hanging forever
	//
	// The fix splits the lock scope: ensureStarted runs under p.mu
	// (serialises startup), then the mutex is released before the
	// legacy fallback (which does NOT need p.mu since each legacy
	// call spawns its own subprocess). HTTP requests use requestSlots,
	// not the lifecycle mutex, because aiohttp is asynchronous.
	lockWaitStart := time.Now()
	p.mu.Lock()
	lockWait := time.Since(lockWaitStart)
	if err := p.ensureStarted(ctx); err != nil {
		p.mu.Unlock()
		p.log.Warn("persistent TTS worker unavailable, falling back to legacy spawn-per-call",
			zap.Error(err))
		return p.generateLegacy(ctx, input, safeName)
	}
	p.mu.Unlock()

	queueStart := time.Now()
	if p.requestSlots != nil {
		select {
		case p.requestSlots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-p.requestSlots }()
	}
	queueWait := time.Since(queueStart)
	res, err := p.sendSynthesizeRequest(ctx, &AudioInput{
		Text:          input.Text,
		Language:      input.Language,
		Voice:         input.Voice,
		Filename:      safeName,
		OutputDir:     input.OutputDir,
		RemoveSilence: input.RemoveSilence,
	})
	if res != nil {
		res.Metrics.LockWaitMS = lockWait.Milliseconds()
		res.Metrics.QueueMS = queueWait.Milliseconds()
		p.log.Info("TTS timing metrics",
			zap.Int64("tts_queue_ms", res.Metrics.QueueMS),
			zap.Int64("tts_lock_wait_ms", res.Metrics.LockWaitMS),
			zap.Int64("tts_voice_resolve_ms", res.Metrics.VoiceResolveMS),
			zap.Int64("tts_stream_ms", res.Metrics.StreamMS),
			zap.Int64("tts_postprocess_ms", res.Metrics.PostprocessMS),
			zap.Int64("tts_audio_duration_ms", res.Metrics.AudioDurationMS),
			zap.Float64("tts_rtf", res.Metrics.RTF))
	}
	return res, err
}

// generateLegacy is the pre-P0-#1 spawn-per-call path. Retained as
// fallback for backward compatibility.
//
// DEPRECATED (VO-DECOMPOSITION P0 #1, July 2026): this path will be
// removed in the CUTOVER phase once all deployments run the persistent
// server. Forward-pointer: architecture/current.yaml#VO-DECOMPOSITION-
// 2026-07-04.linked_issues[PR-VO-TTS-PERSISTENT-WORKER-CUTOVER].
func (p *Processor) generateLegacy(ctx context.Context, input *AudioInput, safeName string) (*AudioResult, error) {
	result := &AudioResult{}

	outputPath := filepath.Join(input.OutputDir, safeName)

	scriptPath := filepath.Join(p.pythonScriptsDir, "bridges", "tts_edge.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tts script not found: %s", scriptPath)
	}

	args := []string{
		scriptPath,
		"--lang", input.Language,
		"--out", outputPath,
	}

	if input.Voice != "" {
		args = append(args, "--voice", input.Voice)
	} else if input.AllowVoiceFallback {
		args = append(args, "--allow-voice-fallback")
	}

	useStdin := input.UseStdin || len(input.Text) > 32*1024
	if !useStdin {
		args = append(args, "--text", input.Text)
	}

	// ARCH-ALLOWLIST: legacy-tts-spawn-per-call — backward-compat fallback
	// for environments where tts_edge_server.py is not deployed. This is
	// the ONLY site in internal/infrastructure/audio/ that calls
	// exec.CommandContext("python3", ...). Superseded by the persistent
	// worker path above; will be removed in CUTOVER phase.
	// See: architecture/current.yaml#VO-DECOMPOSITION-2026-07-04.
	cmd := exec.CommandContext(ctx, "python3", args...)
	if useStdin {
		cmd.Stdin = bytes.NewReader([]byte(input.Text))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("TTS generation failed: %w, output: %s", err, string(output))
	}

	// Parse JSON response.
	type ttsResponse struct {
		OK    bool   `json:"ok"`
		Voice string `json:"voice"`
		Error string `json:"error"`
		Path  string `json:"path"`
		// MetadataPath is <out>.metadata.jsonl when the bridge captured
		// word boundaries in the same synthesis stream (empty otherwise).
		MetadataPath string `json:"metadata_path"`
		// BoundaryCount is the number of WordBoundary chunks captured.
		BoundaryCount int `json:"boundary_count"`
	}
	var ttsOut ttsResponse
	if jsonErr := json.Unmarshal(bytes.TrimSpace(output), &ttsOut); jsonErr != nil {
		p.log.Warn("TTS script returned non-JSON output",
			zap.String("output", string(bytes.TrimSpace(output))),
			zap.Error(jsonErr))
	} else {
		result.Voice = ttsOut.Voice
		result.MetadataPath = ttsOut.MetadataPath
		result.BoundaryCount = ttsOut.BoundaryCount
		if !ttsOut.OK {
			if isBridgeEmptyAudioError(ttsOut.Error) {
				return nil, fmt.Errorf("TTS generation failed: %s: %w", ttsOut.Error, ErrEmptyAudio)
			}
			return nil, fmt.Errorf("TTS generation failed: %s", ttsOut.Error)
		}
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("TTS output file not found: %s", outputPath)
	}

	result.LocalPath = outputPath
	result.Status = "generated"
	if media, mediaErr := p.mediaExecutor(); mediaErr == nil {
		if info, probeErr := media.Probe(ctx, result.LocalPath); probeErr == nil {
			result.Duration = info.Duration
		} else {
			p.log.Warn("failed to probe synthesized audio duration", zap.Error(probeErr))
		}
	} else {
		p.log.Warn("failed to probe synthesized audio duration", zap.Error(mediaErr))
	}

	p.log.Info("TTS generated (legacy spawn-per-call)", zap.String("path", outputPath))

	// Optional silence removal.
	if input.RemoveSilence {
		cleanedPath := filepath.Join(input.OutputDir, "cleaned_"+safeName)
		media, mediaErr := p.mediaExecutor()
		if mediaErr != nil {
			return nil, fmt.Errorf("remove silence: %w", mediaErr)
		}
		if err := media.RemoveSilence(ctx, outputPath, cleanedPath); err != nil {
			p.log.Warn("silence removal failed", zap.Error(err))
		} else {
			result.CleanedPath = cleanedPath
			result.LocalPath = cleanedPath
			result.Status = "cleaned"
		}
	}
	// The cleaned file is the asset returned to callers. Re-probe it after
	// silence removal so downstream timeline compilation never uses the
	// duration of the pre-cleaned source.
	if result.LocalPath != "" {
		if media, mediaErr := p.mediaExecutor(); mediaErr == nil {
			if info, probeErr := media.Probe(ctx, result.LocalPath); probeErr == nil {
				result.Duration = info.Duration
			} else {
				p.log.Warn("failed to probe final synthesized audio duration", zap.Error(probeErr))
			}
		}
	}

	// Compute hash.
	if result.LocalPath != "" {
		if hash, hashErr := hashutil.LegacyMD5File(result.LocalPath); hashErr != nil {
			p.log.Warn("hash computation failed", zap.Error(hashErr))
		} else {
			result.LegacyFileMD5 = hash
		}
	}

	return result, nil
}

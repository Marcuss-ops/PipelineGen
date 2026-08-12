package audioasset

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
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
//   - mu sync.Mutex — serialises HTTP calls to the single-threaded Python server
//   - cmd *exec.Cmd — the running subprocess (nil when not started)
//   - baseURL string — "http://127.0.0.1:<port>" discovered from stdout
//   - httpClient *http.Client — shared HTTP client (5-min timeout)
//   - started bool — true after successful startup
type Processor struct {
	pythonScriptsDir string
	log              *zap.Logger

	// Persistent worker state (VO-DECOMPOSITION P0 #1).
	mu         sync.Mutex
	cmd        *exec.Cmd
	baseURL    string
	httpClient *http.Client
	started    bool
	media      mediaexec.AudioProcessor
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
	}
}

func (p *Processor) SetMediaExecutor(media mediaexec.AudioProcessor) {
	if p == nil {
		return
	}
	p.media = media
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
	// call spawns its own subprocess). sendSynthesizeRequest runs
	// under p.mu because the Python HTTP server is single-threaded.
	p.mu.Lock()
	if err := p.ensureStarted(ctx); err != nil {
		p.mu.Unlock()
		p.log.Warn("persistent TTS worker unavailable, falling back to legacy spawn-per-call",
			zap.Error(err))
		return p.generateLegacy(ctx, input, safeName)
	}

	// NOTE: p.mu is held here. sendSynthesizeRequest accesses the
	// single-threaded Python HTTP server, so serialisation is needed.
	// defer ensures unlock even on panic (BUG-FIX #1).
	defer p.mu.Unlock()
	return p.sendSynthesizeRequest(ctx, &AudioInput{
		Text:          input.Text,
		Language:      input.Language,
		Voice:         input.Voice,
		Filename:      safeName,
		OutputDir:     input.OutputDir,
		RemoveSilence: input.RemoveSilence,
	})
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
	}
	var ttsOut ttsResponse
	if jsonErr := json.Unmarshal(bytes.TrimSpace(output), &ttsOut); jsonErr != nil {
		p.log.Warn("TTS script returned non-JSON output",
			zap.String("output", string(bytes.TrimSpace(output))),
			zap.Error(jsonErr))
	} else {
		result.Voice = ttsOut.Voice
		if !ttsOut.OK {
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
		hash, err := hashutil.HashFile(result.LocalPath, md5.New())
		if err != nil {
			p.log.Warn("hash computation failed", zap.Error(err))
		} else {
			result.FileHash = hash
		}
	}

	return result, nil
}

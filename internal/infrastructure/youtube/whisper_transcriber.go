// Package youtube — whisper_transcriber.go: concrete WhisperTranscriber
// adapter that spawns the Python bridge script
// (scripts/bridges/whisper_transcriber.py) via subprocess and
// returns the typed asset.TranscriptResult.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026).
//
// The bridge delegates to the repository's real faster-whisper
// helper (scripts/tools/transcribe_detect_lang.py), which runs
// Whisper on CUDA when a usable GPU is available (the helper
// re-execs with a CUDA 12 runtime exposed when ctranslate2 needs
// it) and falls back to CPU int8 otherwise. The adapter:
//  1. Spawns the Python script with the local path as argv[1].
//  2. Reads stdout (JSON) and parses it into
//     asset.TranscriptResult.
//  3. Stderr is captured for the error path (the chain falls
//     through to the next priority or surfaces a typed
//     error).
//
// godlike/06 SSOT: this is the SOLE canonical concrete
// adapter for the WhisperTranscriber interface
// (internal/infrastructure/youtube/ports.go) and the
// application-layer WhisperTranscriberPort
// (internal/application/youtube/ports/ports.go). The
// application-layer port is a STRUCTURAL subset of the
// infrastructure-layer interface (single method,
// TranscribeAudioWithDetection); the same concrete instance
// satisfies both.
//
// godlike/07 fail-closed: every error path returns a typed
// error. The bridge always returns a real transcript or a
// typed error — never a placeholder — so the AcquireService
// surfaces ErrNoSourceAcquired and the chain falls through
// cleanly. No placeholder data ever pollutes asset_text_tracks.
package youtube

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

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrWhisperBridgeUnavailable is returned at construction
// time when the Python interpreter or bridge script is not
// reachable on the host. godlike/07 fail-closed: surfaces
// at startup, not at first-clip time.
var ErrWhisperBridgeUnavailable = errors.New("whisper bridge unavailable")

// WhisperTranscriberAdapter is the concrete implementation of
// the WhisperTranscriber interface. Spawns the Python bridge
// script via subprocess.
type WhisperTranscriberAdapter struct {
	pythonBin      string
	scriptPath     string
	defaultTimeout time.Duration
	log            *zap.Logger
}

// WhisperTranscriberConfig configures the adapter.
type WhisperTranscriberConfig struct {
	PythonBin      string        // default: "python3"
	ScriptPath     string        // default: "scripts/bridges/whisper_transcriber.py"
	DefaultTimeout time.Duration // default: 5 * time.Minute
}

// NewWhisperTranscriberAdapter constructs the canonical adapter.
//
// godlike/07 fail-closed: returns ErrWhisperBridgeUnavailable
// (wrapped) if the Python interpreter is not on PATH or the
// bridge script is not readable. Operators see the typed error
// at startup, not at first-clip time.
func NewWhisperTranscriberAdapter(cfg WhisperTranscriberConfig, log *zap.Logger) (*WhisperTranscriberAdapter, error) {
	if log == nil {
		return nil, fmt.Errorf("youtube.NewWhisperTranscriberAdapter: log is nil")
	}
	if cfg.PythonBin == "" {
		cfg.PythonBin = "python3"
	}
	if cfg.ScriptPath == "" {
		cfg.ScriptPath = "scripts/bridges/whisper_transcriber.py"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 5 * time.Minute
	}
	// Fail-closed: verify the Python interpreter is on PATH
	// and the bridge script is readable. Without these checks,
	// the first per-clip invocation would fail with a
	// confusing "executable file not found" or "no such file"
	// error deep in the subprocess call stack.
	if _, lookErr := exec.LookPath(cfg.PythonBin); lookErr != nil {
		return nil, fmt.Errorf("%w: %s not on PATH: %v", ErrWhisperBridgeUnavailable, cfg.PythonBin, lookErr)
	}
	if _, statErr := os.Stat(cfg.ScriptPath); statErr != nil {
		return nil, fmt.Errorf("%w: bridge script %s not accessible: %v", ErrWhisperBridgeUnavailable, cfg.ScriptPath, statErr)
	}
	return &WhisperTranscriberAdapter{
		pythonBin:      cfg.PythonBin,
		scriptPath:     cfg.ScriptPath,
		defaultTimeout: cfg.DefaultTimeout,
		log:            log,
	}, nil
}

// TranscribeAudio is the legacy plain-string method. RETAINED
// for back-compat with callers that don't need the typed
// result. Returns the Text field of the typed result; an error
// is returned on any failure.
func (a *WhisperTranscriberAdapter) TranscribeAudio(ctx context.Context, localPath string) (string, error) {
	res, err := a.TranscribeAudioWithDetection(ctx, localPath)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// TranscribeAudioWithDetection is the typed-result sibling
// (Fase 1.b). Spawns the Python script, parses the JSON
// output, and returns the canonical asset.TranscriptResult.
//
// Returns:
//
//	(TranscriptResult{Text: ""}, nil) on empty transcript
//	  (non-error; caller falls through to the next priority).
//	(TranscriptResult{}, err) on subprocess failure, parse
//	  error, or script-level error.
//
// godlike/07 fail-closed: errors propagate verbatim. The
// caller (AcquireService priority 5) checks `det.Text != ""`
// before accepting the result.
func (a *WhisperTranscriberAdapter) TranscribeAudioWithDetection(ctx context.Context, localPath string) (asset.TranscriptResult, error) {
	if localPath == "" {
		return asset.TranscriptResult{}, fmt.Errorf("whisper.TranscribeAudioWithDetection: localPath is empty")
	}

	// Subprocess with a bounded timeout (godlike/07
	// fail-closed: the chain never hangs on a stuck Whisper
	// subprocess).
	execCtx, cancel := context.WithTimeout(ctx, a.defaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, a.pythonBin, a.scriptPath, localPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		a.log.Warn("whisper: subprocess failed",
			zap.String("local_path", localPath),
			zap.String("stderr", stderr.String()),
			zap.Error(err))
		return asset.TranscriptResult{}, fmt.Errorf("whisper subprocess: %w (stderr: %s)", err, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return asset.TranscriptResult{}, fmt.Errorf("whisper: parse JSON output: %w (raw: %s)", err, stdout.String())
	}
	if res.Error != "" {
		return asset.TranscriptResult{}, fmt.Errorf("whisper: script error: %s", res.Error)
	}

	// Normalize the detected language. Empty input collapses
	// to "und" via the canonical bcp47.Normalize helper
	// (godlike/07 no-fake-availability: never silently
	// substitute "en").
	lang, nErr := asset.Normalize(res.DetectedLanguage)
	if nErr != nil {
		lang = "und"
	}

	var confPtr *float64
	if res.Confidence > 0 {
		c := res.Confidence
		confPtr = &c
	}

	cues := make([]asset.TimedCue, len(res.Cues))
	for i, c := range res.Cues {
		cues[i] = asset.TimedCue{
			StartMs: c.StartMs,
			EndMs:   c.EndMs,
			Text:    c.Text,
		}
	}

	return asset.TranscriptResult{
		Text:             res.Text,
		DetectedLanguage: lang,
		Confidence:       confPtr,
		DurationMs:       res.DurationMs,
		Cues:             cues,
	}, nil
}

// Compile-time assertion: WhisperTranscriberAdapter satisfies
// both the infrastructure-layer WhisperTranscriber interface
// and the application-layer WhisperTranscriberPort (via
// structural typing — the port is a subset of the interface).
var _ WhisperTranscriber = (*WhisperTranscriberAdapter)(nil)

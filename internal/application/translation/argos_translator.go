// Package translation — argos_translator.go: Argos Translate offline
// TranslationPort adapter.
//
// Argos Translate is the deterministic, CPU-only OpenNMT translator
// (scripts/bridges/argos_translator.py). This adapter spawns the bridge
// via subprocess — mirroring the Whisper bridge contract in
// internal/infrastructure/youtube/whisper_transcriber.go — and projects
// its JSON output onto the canonical TranslationResult.
//
// godlike/07 fail-closed:
//   - construction returns ErrArgosBridgeUnavailable when python3 or the
//     bridge script is not reachable (the composition root treats this as
//     fail-SOFT and falls back to the next provider).
//   - every runtime error path returns a typed error; an empty translated
//     output is surfaced as an error so the FallbackTranslator can route
//     to the next provider instead of persisting a fake success.
package translation

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
)

// ErrArgosBridgeUnavailable is returned at construction time when the
// Python interpreter or the Argos bridge script is not reachable.
var ErrArgosBridgeUnavailable = errors.New("argos bridge unavailable")

// ArgosTranslator is the concrete TranslationPort adapter for Argos
// Translate. It spawns scripts/bridges/argos_translator.py and pipes the
// source text on stdin (no argv-length limits on long transcripts).
type ArgosTranslator struct {
	pythonBin      string
	scriptPath     string
	defaultTimeout time.Duration
	log            *zap.Logger
}

// ArgosTranslatorConfig configures the adapter.
type ArgosTranslatorConfig struct {
	PythonBin      string        // default: "python3"
	ScriptPath     string        // default: "scripts/bridges/argos_translator.py"
	DefaultTimeout time.Duration // default: 2 * time.Minute
}

// NewArgosTranslator constructs the adapter. godlike/07 fail-closed:
// returns ErrArgosBridgeUnavailable when the interpreter or script is
// missing so operators see the typed error at boot instead of a first-clip
// failure deep in the subprocess stack.
func NewArgosTranslator(cfg ArgosTranslatorConfig, log *zap.Logger) (*ArgosTranslator, error) {
	if cfg.PythonBin == "" {
		cfg.PythonBin = "python3"
	}
	if cfg.ScriptPath == "" {
		cfg.ScriptPath = "scripts/bridges/argos_translator.py"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 2 * time.Minute
	}
	if _, lookErr := exec.LookPath(cfg.PythonBin); lookErr != nil {
		return nil, fmt.Errorf("%w: %s not on PATH: %v", ErrArgosBridgeUnavailable, cfg.PythonBin, lookErr)
	}
	if _, statErr := os.Stat(cfg.ScriptPath); statErr != nil {
		return nil, fmt.Errorf("%w: bridge script %s not accessible: %v", ErrArgosBridgeUnavailable, cfg.ScriptPath, statErr)
	}
	return &ArgosTranslator{
		pythonBin:      cfg.PythonBin,
		scriptPath:     cfg.ScriptPath,
		defaultTimeout: cfg.DefaultTimeout,
		log:            log,
	}, nil
}

// Translate implements TranslationPort. It spawns the Argos bridge and
// parses its JSON result. Argos is deterministic and ignores ModelPolicy /
// ModelHints (no generation knobs); the command's SourceLang / TargetLang /
// Text are the only inputs.
func (a *ArgosTranslator) Translate(ctx context.Context, cmd TranslationCommand) (TranslationResult, error) {
	if cmd.Text == "" {
		return TranslationResult{}, fmt.Errorf("argos.Translate: Text is empty")
	}
	if cmd.TargetLang == "" {
		return TranslationResult{}, fmt.Errorf("argos.Translate: TargetLang is empty")
	}
	if cmd.SourceLang == "" || cmd.SourceLang == "und" {
		// Argos cannot auto-detect source language; the caller must supply
		// a real BCP-47 tag. Surface a typed error so the fallback chain
		// (Ollama) can handle undetermined source.
		return TranslationResult{}, fmt.Errorf("argos.Translate: SourceLang is empty/undetermined")
	}

	execCtx, cancel := context.WithTimeout(ctx, a.defaultTimeout)
	defer cancel()

	args := []string{a.scriptPath, "--source", cmd.SourceLang, "--target", cmd.TargetLang}
	command := exec.CommandContext(execCtx, a.pythonBin, args...)
	command.Stdin = bytes.NewBufferString(cmd.Text)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		a.log.Warn("argos: subprocess failed",
			zap.String("source", cmd.SourceLang),
			zap.String("target", cmd.TargetLang),
			zap.String("stderr", stderr.String()),
			zap.Error(err))
		return TranslationResult{}, fmt.Errorf("argos subprocess: %w (stderr: %s)", err, stderr.String())
	}

	var res struct {
		TranslatedText string `json:"translated_text"`
		Source         string `json:"source"`
		Target         string `json:"target"`
		Model          string `json:"model"`
		Via            string `json:"via"`
		Error          string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return TranslationResult{}, fmt.Errorf("argos: parse JSON output: %w (raw: %s)", err, stdout.String())
	}
	if res.Error != "" {
		return TranslationResult{}, fmt.Errorf("argos: bridge error: %s", res.Error)
	}
	if res.TranslatedText == "" {
		return TranslationResult{}, fmt.Errorf("argos: bridge returned empty translation for %s->%s", cmd.SourceLang, cmd.TargetLang)
	}

	return TranslationResult{
		TranslatedText: res.TranslatedText,
		UsedProvider:   "argos",
		UsedModel:      res.Model,
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
	}, nil
}

// Compile-time assertion: *ArgosTranslator satisfies TranslationPort.
var _ TranslationPort = (*ArgosTranslator)(nil)

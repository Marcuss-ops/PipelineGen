// Package translation — argos_server_translator.go: persistent Argos
// Translate TranslationPort adapter.
//
// PR-ARGOS-TRANSLATION (Aug 2026): replaces the spawn-per-call subprocess
// bridge with a long-lived sidecar (scripts/bridges/argos_server.py) so the
// OpenNMT models are loaded ONCE and shared across every per-language
// translation. The lifecycle mirrors the TTS worker precedent in
// internal/infrastructure/audio/worker_process.go:
//
//   - ensureStarted spawns the Python HTTP server, reads the PORT=<n> line
//     (with a bounded timeout), drains stdout, and validates /health.
//   - Translate POSTs to /translate and parses the canonical JSON envelope.
//   - Stop posts /quit, waits, then SIGKILLs if the worker lingers.
//
// godlike/07 fail-closed: construction returns ErrArgosBridgeUnavailable
// when python3 or the server script is missing; a nil/undetermined source
// language surfaces a typed error so the FallbackTranslator can route to
// Ollama instead of faking a success.
package translation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ArgosServerConfig configures the persistent Argos server adapter.
type ArgosServerConfig struct {
	PythonBin      string        // default: "python3"
	ScriptsDir     string        // default: "scripts"
	StartTimeout   time.Duration // default: 30 * time.Second
	RequestTimeout time.Duration // default: 60 * time.Second
}

// ArgosServerTranslator is the concrete TranslationPort adapter that talks
// to the persistent Argos Translate HTTP sidecar.
type ArgosServerTranslator struct {
	mu        sync.Mutex
	pythonBin string
	script    string
	log       *zap.Logger

	startTimeout   time.Duration
	requestTimeout time.Duration

	started    bool
	baseURL    string
	httpClient *http.Client
	cmd        *exec.Cmd
}

// NewArgosServerTranslator constructs the adapter. godlike/07 fail-closed:
// returns ErrArgosBridgeUnavailable when the interpreter or server script is
// missing so the composition root can fail-soft at boot.
func NewArgosServerTranslator(cfg ArgosServerConfig, log *zap.Logger) (*ArgosServerTranslator, error) {
	if cfg.PythonBin == "" {
		// The Argos runtime lives in its own venv (.venv-argos), separate
		// from the Whisper runtime and the system python3. Operators point
		// the sidecar at it via VELOX_ARGOS_PYTHON (or cfg.Paths.ArgosPythonBin
		// at the composition root); the default remains the PATH python3.
		if venv := strings.TrimSpace(os.Getenv("VELOX_ARGOS_PYTHON")); venv != "" {
			cfg.PythonBin = venv
		} else {
			cfg.PythonBin = "python3"
		}
	}
	if cfg.ScriptsDir == "" {
		cfg.ScriptsDir = "scripts"
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = 30 * time.Second
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 60 * time.Second
	}
	if _, lookErr := exec.LookPath(cfg.PythonBin); lookErr != nil {
		return nil, fmt.Errorf("%w: %s not on PATH: %v", ErrArgosBridgeUnavailable, cfg.PythonBin, lookErr)
	}
	script := filepath.Join(cfg.ScriptsDir, "bridges", "argos_server.py")
	if _, statErr := os.Stat(script); statErr != nil {
		return nil, fmt.Errorf("%w: server script %s not accessible: %v", ErrArgosBridgeUnavailable, script, statErr)
	}
	return &ArgosServerTranslator{
		pythonBin:      cfg.PythonBin,
		script:         script,
		log:            log,
		startTimeout:   cfg.StartTimeout,
		requestTimeout: cfg.RequestTimeout,
	}, nil
}

// ensureStarted launches the sidecar if not already running. Caller must
// hold a.mu.
func (a *ArgosServerTranslator) ensureStarted(ctx context.Context) error {
	if a.started {
		if err := a.healthCheck(ctx); err != nil {
			a.started = false
			a.baseURL = ""
			return fmt.Errorf("argos server health check failed: %w", err)
		}
		return nil
	}

	a.log.Info("argos: launching persistent server", zap.String("script", a.script))
	cmd := exec.Command(a.pythonBin, a.script, "--host", "127.0.0.1", "--port", "0")
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("argos server stdout pipe: %w", err)
	}
	stderrPath := filepath.Join(os.TempDir(), "argos_server_stderr.log")
	if stderrFile, ferr := os.Create(stderrPath); ferr == nil {
		cmd.Stderr = stderrFile
		defer stderrFile.Close()
	} else {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start argos server: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	var port int
	found := false
	deadline := time.AfterFunc(a.startTimeout, func() { _ = stdoutPipe.Close() })
	defer deadline.Stop()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "PORT=") {
			if p, perr := strconv.Atoi(strings.TrimPrefix(line, "PORT=")); perr == nil {
				port = p
				found = true
				break
			}
		}
	}
	if !found {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return fmt.Errorf("argos server did not print PORT within %s: %w", a.startTimeout, ErrArgosBridgeUnavailable)
	}

	// Drain remaining stdout so the pipe buffer never blocks the worker.
	go func() {
		for scanner.Scan() {
			a.log.Debug("argos_server stdout", zap.String("line", scanner.Text()))
		}
	}()

	a.cmd = cmd
	a.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	a.httpClient = &http.Client{Timeout: a.requestTimeout}
	a.started = true

	if err := a.healthCheck(ctx); err != nil {
		a.started = false
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return fmt.Errorf("argos server health check failed after startup: %w", err)
	}
	a.log.Info("argos: server started", zap.Int("pid", cmd.Process.Pid), zap.Int("port", port))
	return nil
}

func (a *ArgosServerTranslator) healthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("argos server /health returned %d", resp.StatusCode)
	}
	return nil
}

// Translate implements TranslationPort.
func (a *ArgosServerTranslator) Translate(ctx context.Context, cmd TranslationCommand) (TranslationResult, error) {
	if cmd.Text == "" {
		return TranslationResult{}, fmt.Errorf("argos.Translate: Text is empty")
	}
	if cmd.TargetLang == "" {
		return TranslationResult{}, fmt.Errorf("argos.Translate: TargetLang is empty")
	}
	if cmd.SourceLang == "" || cmd.SourceLang == "und" {
		return TranslationResult{}, fmt.Errorf("argos.Translate: SourceLang is empty/undetermined")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureStarted(ctx); err != nil {
		return TranslationResult{}, err
	}

	body, err := json.Marshal(map[string]string{
		"text":   cmd.Text,
		"source": cmd.SourceLang,
		"target": cmd.TargetLang,
	})
	if err != nil {
		return TranslationResult{}, fmt.Errorf("argos: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/translate", bytes.NewReader(body))
	if err != nil {
		return TranslationResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return TranslationResult{}, fmt.Errorf("argos: translate request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return TranslationResult{}, fmt.Errorf("argos: read response: %w", err)
	}

	var res struct {
		TranslatedText string `json:"translated_text"`
		Model          string `json:"model"`
		Via            string `json:"via"`
		Error          string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return TranslationResult{}, fmt.Errorf("argos: parse response: %w (raw: %s)", err, string(respBody))
	}
	if resp.StatusCode != http.StatusOK || res.Error != "" {
		msg := res.Error
		if msg == "" {
			msg = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return TranslationResult{}, fmt.Errorf("argos: bridge error: %s", msg)
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

// Stop gracefully shuts down the sidecar (POST /quit, wait, SIGKILL).
// Idempotent: safe to call when never started.
func (a *ArgosServerTranslator) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started {
		return nil
	}

	a.log.Info("argos: stopping server")
	if a.baseURL != "" && a.httpClient != nil {
		quitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(quitCtx, http.MethodPost, a.baseURL+"/quit", nil)
		if err == nil {
			resp, _ := a.httpClient.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
		}
		cancel()
	}

	if a.cmd != nil && a.cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- a.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			a.log.Warn("argos: server did not exit within 5s — killing")
			_ = a.cmd.Process.Kill()
			<-done
		}
	}

	a.started = false
	a.baseURL = ""
	return nil
}

// Compile-time assertion: *ArgosServerTranslator satisfies TranslationPort.
var _ TranslationPort = (*ArgosServerTranslator)(nil)

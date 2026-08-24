// Package workerruntime — preflight.go (P1-3, June 2026).
//
// Two responsibilities owned by this file:
//
//  1. Master URL resolution (ResolveMasterURL, NormalizeURL,
//     MasterURLSource) — the canonical $VELOX_MASTER_URL >
//     cfg.External.VeloxMasterURL > http://127.0.0.1:<port>
//     precedence chain originally from cmd/worker/main.go.
//
//  2. Master readiness pre-flight loops (PreflightMasterHealth and
//     PreflightMasterScriptGenerateReady) — tight-bounded polls that
//     wait for the master to become live and for the script_generate
//     readiness sub-check to pass before the worker starts claiming jobs.
//
// Deliberately no fall-back to /api/system/doctor: the /health
// endpoint is the canonical liveness signal, and a worker that
// pretends the master is healthy because /doctor (a heavier
// endpoint) happened to come up does NOT have the right semantics.
package workerruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// DetectRendererHardware performs the renderer-specific fail-closed probe.
// GPU is detected through nvidia-smi (Chronon remains the actual renderer);
// FFmpeg is required for frame/alpha validation and artifact probing.
func DetectRendererHardware() (gpu, ffmpeg bool) {
	_, gpuPathErr := exec.LookPath("nvidia-smi")
	gpuErr := gpuPathErr
	if gpuPathErr == nil {
		gpuErr = exec.Command("nvidia-smi", "-L").Run()
	}
	_, ffmpegErr := exec.LookPath("ffmpeg")
	return gpuErr == nil, ffmpegErr == nil
}

func ValidateProfileHardware(profile *WorkerProfile, caps appjobs.WorkerCapabilities) error {
	if profile == nil {
		return fmt.Errorf("worker profile is nil")
	}
	if profile.RequiresGPU && !caps.GPU {
		return fmt.Errorf("worker profile %q requires GPU capability", profile.Name)
	}
	if profile.RequiresFFmpeg && !caps.FFmpeg {
		return fmt.Errorf("worker profile %q requires FFmpeg capability", profile.Name)
	}
	return nil
}

// Pre-flight constants — 30s is long enough for a healthy master
// to come up (Compose `depends_on` waits, K8s probes, VM reboots)
// and short enough that an operator notices a misconfiguration
// fast. Moved verbatim from cmd/worker/main.go.
const (
	preflightTimeout    = 30 * time.Second
	preflightInterval   = 1 * time.Second
	preflightHTTPClient = 5 * time.Second
)

// ResolveMasterURL returns the canonical master URL with the
// precedence chain:
//
//	$VELOX_MASTER_URL > cfg.External.VeloxMasterURL > "http://127.0.0.1:8000"
//
// Compose users can set service-based URLs (http://velox-server:8000)
// so the worker on the same network reaches the master without
// depending on port-mapped IPs. Docker-host users on Linux set
// extra_hosts: ["host.docker.internal:host-gateway"] and use
// http://host.docker.internal:8000.
func ResolveMasterURL(cfg *config.Config) string {
	if v := Env("VELOX_MASTER_URL", ""); v != "" {
		return normalizeURL(v)
	}
	if cfg != nil && strings.TrimSpace(cfg.External.VeloxMasterURL) != "" {
		return normalizeURL(cfg.External.VeloxMasterURL)
	}
	if cfg != nil && cfg.Server.Port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port)
	}
	return "http://127.0.0.1:8000"
}

// normalizeURL strips trailing slash + ensures scheme prefix.
// Moved verbatim from cmd/worker/main.go::normalizeURL.
func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "http://127.0.0.1:8000"
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

// MasterURLSource is informational (log only) — reports which
// input layer won the precedence chain (env or cfg fallback).
func MasterURLSource(resolved string) string {
	if v := Env("VELOX_MASTER_URL", ""); v != "" && normalizeURL(v) == resolved {
		return "env:VELOX_MASTER_URL"
	}
	return "config-yaml-or-default"
}

// PreflightMasterHealth polls <masterURL>/health every preflightInterval
// up to preflightTimeout. Returns nil on the first 200; returns
// error on deadline with the last seen failure.
//
// We deliberately do NOT fall back to /api/system/doctor: a
// worker that pretends the master is healthy because /doctor
// (a heavier endpoint) happened to come up does NOT have the
// right semantics. /health is the canonical liveness signal.
//
// Implementation: uses pkg/retry.Do with a custom IsRetryable
// predicate that returns TRUE on ANY non-OK fn-returned error,
// preserving the original "wait until 200 OK or deadline" semantic
// from the prior tight-loop implementation. retry.Options configures
// constant 1s backoff (BackoffFactor=1.0 + InitialBackoff=MaxBackoff=
// preflightInterval) so the poll cadence matches the original
// time.Sleep(preflightInterval) exactly. The ctx deadline
// (context.WithDeadline) caps the total budget at preflightTimeout.
// MaxAttempts is sized at int(preflightTimeout/preflightInterval)+2
// so the retry loop keeps spinning until the ctx deadline fires
// (vs bailing out at the canonical 3 default); ctx cancellation
// short-circuits before we hit MaxAttempts.
func PreflightMasterHealth(ctx context.Context, masterURL string) error {
	healthURL := strings.TrimRight(masterURL, "/") + "/health"
	client := &http.Client{Timeout: preflightHTTPClient}

	probeCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	walk := func() error {
		resp, err := client.Get(healthURL)
		if err != nil {
			return err
		}
		defer closeBody(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("/health returned %d", resp.StatusCode)
		}
		return nil
	}

	err := retry.Do(probeCtx, walk, preflightRetryOptions())

	if err == nil {
		return nil
	}
	// Wrap to preserve the canonical "/health" error envelope so
	// observability contracts (log greps, dashboard queries, errors.Is
	// matchers) stay stable across the refactor.
	return fmt.Errorf("master /health did not return 200 within %s: %w (url=%s)",
		preflightTimeout, err, healthURL)
}

// PreflightMasterScriptGenerateReady polls <masterURL>/ready and verifies
// the script_generate readiness sub-check before the worker attempts to
// claim script.generate jobs. It reuses the same 30s / 1s retry budget as
// PreflightMasterHealth so a master that is still initialising has time
// to become ready.
//
// Semantics:
//   - If /ready reports ok=false, the preflight fails.
//   - If the script_generate check is absent or applicable=false, the
//     preflight passes (feature disabled or master predates the check).
//   - If script_generate is applicable and ok=true, the preflight passes.
//   - If script_generate is applicable and ok=false, the preflight retries
//     until the 30s budget expires.
func PreflightMasterScriptGenerateReady(ctx context.Context, masterURL string) error {
	readyURL := strings.TrimRight(masterURL, "/") + "/ready"
	client := &http.Client{Timeout: preflightHTTPClient}

	probeCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	var lastErr error
	walk := func() error {
		lastErr = nil
		resp, err := client.Get(readyURL)
		if err != nil {
			lastErr = err
			return err
		}
		defer closeBody(resp.Body)

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
			lastErr = fmt.Errorf("/ready returned %d", resp.StatusCode)
			return lastErr
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("read /ready body: %w", err)
			return lastErr
		}

		var r struct {
			OK     bool   `json:"ok"`
			Status string `json:"status"`
			Checks map[string]struct {
				OK         bool   `json:"ok"`
				Applicable bool   `json:"applicable"`
				Error      string `json:"error"`
				Note       string `json:"note"`
			} `json:"checks"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			lastErr = fmt.Errorf("parse /ready body: %w", err)
			return lastErr
		}

		if !r.OK {
			lastErr = fmt.Errorf("/ready reported unhealthy (status=%s)", r.Status)
			return lastErr
		}

		sg, ok := r.Checks["script_generate"]
		if !ok {
			// Master predates the script_generate readiness check or the
			// feature is not mounted; treat as not applicable.
			return nil
		}
		if !sg.Applicable {
			return nil
		}
		if !sg.OK {
			msg := "/ready script_generate check failed"
			if sg.Error != "" {
				msg += ": " + sg.Error
			} else if sg.Note != "" {
				msg += " (" + sg.Note + ")"
			}
			lastErr = fmt.Errorf("%s", msg)
			return lastErr
		}
		return nil
	}

	err := retry.Do(probeCtx, walk, preflightRetryOptions())
	if err == nil {
		return nil
	}
	if lastErr == nil {
		lastErr = err
	}
	return fmt.Errorf("master /ready script_generate did not become ready within %s: %w (url=%s)",
		preflightTimeout, lastErr, readyURL)
}

// preflightRetryOptions returns the retry configuration shared by the
// master preflight probes. It preserves the original 1s tight-poll
// cadence and 30s total budget.
func preflightRetryOptions() retry.Options {
	return retry.Options{
		IsRetryable: func(err error) bool {
			return err != nil
		},
		InitialBackoff: preflightInterval,
		MaxBackoff:     preflightInterval,
		BackoffFactor:  1.0,
		JitterFraction: 0,
		DisableJitter:  true,
		MaxAttempts:    int(preflightTimeout/preflightInterval) + 2,
	}
}

// closeBody is an interface-typed wrapper around resp.Body.Close().
// Moved verbatim from cmd/worker/main.go. Using `interface{ Close() error }`
// lets us avoid importing the net/http package's Body type at the
// function-level for callers that pass other body-shaped types.
func closeBody(body interface{ Close() error }) {
	if body == nil {
		return
	}
	_ = body.Close()
}

// Package workerruntime — preflight.go (P1-3, June 2026).
//
// Two responsibilities owned by this file:
//
//  1. Master URL resolution (ResolveMasterURL, NormalizeURL,
//     MasterURLSource) — the canonical $VELOX_MASTER_URL >
//     cfg.External.VeloxMasterURL > http://127.0.0.1:<port>
//     precedence chain from cmd/worker/main.go. Moved verbatim.
//
//  2. /health pre-flight loop (PreflightMasterHealth) — the
//     30s tight-bounded poll-so-master-is-up loop from
//     cmd/worker/main.go. Moved verbatim.
//
// Deliberately no fall-back to /api/system/doctor: the /health
// endpoint is the canonical liveness signal, and a worker that
// pretends the master is healthy because /doctor (a heavier
// endpoint) happened to come up does NOT have the right semantics.
package workerruntime

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

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
func PreflightMasterHealth(masterURL string) error {
	healthURL := strings.TrimRight(masterURL, "/") + "/health"
	client := &http.Client{Timeout: preflightHTTPClient}
	deadline := time.Now().Add(preflightTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			closeBody(resp.Body)
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("/health returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(preflightInterval)
	}
	if lastErr == nil {
		lastErr = errors.New("timed out without a single response")
	}
	return fmt.Errorf("master /health did not return 200 within %s: %w (url=%s)", preflightTimeout, lastErr, healthURL)
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

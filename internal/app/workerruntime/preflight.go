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
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
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
func PreflightMasterHealth(masterURL string) error {
	healthURL := strings.TrimRight(masterURL, "/") + "/health"
	client := &http.Client{Timeout: preflightHTTPClient}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(preflightTimeout))
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

	err := retry.Do(ctx, walk, retry.Options{
		IsRetryable: func(err error) bool {
			// Preserve the "wait until 200 OK or deadline" semantic:
			// any non-OK fn-returned error keeps the retry loop spinning;
			// ctx deadline (30s budget) is what actually stops the loop.
			return err != nil
		},
		// Constant 1s backoff to preserve the original time.Sleep(1s)
		// tight-poll cadence exactly. BackoffFactor=1.0 disables
		// exponential growth; InitialBackoff=MaxBackoff=preflightInterval
		// clamps the sleep envelope. JitterFraction=0 because preflight is
		// operator-visible (every attempt logged) and bounded to 30s;
		// thundering-herd concerns do not apply here.
		InitialBackoff: preflightInterval,
		MaxBackoff:     preflightInterval,
		BackoffFactor:  1.0,
		JitterFraction: 0,
		// Size MaxAttempts at the full preflight budget + 2 headroom so
		// the loop keeps spinning until ctx deadline fires (vs bailing
		// out at the canonical 3 default). At 1s per attempt in a 30s
		// budget this is 32 attempts; ctx cancellation short-circuits
		// before we hit it.
		MaxAttempts: int(preflightTimeout/preflightInterval) + 2,
	})

	if err == nil {
		return nil
	}
	// Wrap to preserve the canonical "/health" error envelope so
	// observability contracts (log greps, dashboard queries, errors.Is
	// matchers) stay stable across the refactor.
	return fmt.Errorf("master /health did not return 200 within %s: %w (url=%s)",
		preflightTimeout, err, healthURL)
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

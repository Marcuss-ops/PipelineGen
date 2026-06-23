// Package main is the cross-host PipelineGen worker binary.
//
// This binary registers against an external HTTP broker (the
// pipelinegen server) and executes the long-running background jobs
// (script generation, artlist, voiceover). It is OPTIONAL — the server
// in cmd/server spins up its own in-process worker when --mode all is
// used (the default for `make run`). The cross-host worker exists
// ONLY for deployments that want to scale the worker tier separately
// from the API tier.
//
// Per the Operational Readiness PR (June 2026) the canonical default
// port is 8000 (config.Server.Port default) and the worker reads
// VELOX_MASTER_URL (config.External.VeloxMasterURL default)
// to find the broker. Typical setups:
//
//	http://127.0.0.1:8000                  # local dev: server + worker on the same host
//	http://velox-server:8000               # docker compose service name
//	http://host.docker.internal:8000       # worker in container, master on host (Linux: extra_hosts host.docker.internal=host-gateway)
//
// Before running, the worker runs a tight 30-second /health pre-flight
// against $VELOX_MASTER_URL/health so it fails fast (log.Fatal) when
// the master is not yet ready — typical in `depends_on` Compose
// startups. There is intentionally NO --skip-master-health-check flag:
// silent retry-and-crash later is harder to debug than loud
// pre-flight failure.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	logging "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	assettransferclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/assettransferclient"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// pre-flight constants. 30s is long enough for a healthy master to
// come up (Compose `depends_on` waits, K8s probes, VM reboots) and
// short enough that an operator notices a misconfiguration fast.
const (
	preflightTimeout    = 30 * time.Second
	preflightInterval   = 1 * time.Second
	preflightHTTPClient = 5 * time.Second
)

func main() {
	var (
		cfgPth = flag.String("config", "config.yaml", "Path to configuration file")
	)
	flag.Parse()

	// Fail-fast on malformed config (per audit P0 #5). Use
	// config.GetFromPath (NOT config.Get) so a typo in config.yaml does
	// NOT silently fall back to defaults — that was the audit-bug.
	cfg, err := config.GetFromPath(*cfgPth)
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: failed to load config:", err)
		os.Exit(2)
	}
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "FATAL: nil config from config.GetFromPath")
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: config validation failed:", err)
		os.Exit(2)
	}

	logging.Init(cfg.Logging.Level, cfg.Logging.Format)
	log := logging.Get().Named("worker")
	defer func() { _ = logging.Sync() }()

	masterURL := resolveMasterURL(cfg)
	log.Info("worker master URL resolved",
		zap.String("master_url", masterURL),
		zap.String("source", masterURLSource(masterURL)),
	)

	// Pre-flight /health check. Tight loop bounded to 30s, then
	// log.Fatal so a process manager (systemd, Compose) restarts.
	if err := preflightMasterHealth(masterURL); err != nil {
		log.Fatal("master /health pre-flight failed",
			zap.String("master_url", masterURL),
			zap.Duration("timeout", preflightTimeout),
			zap.Error(err),
		)
	}
	log.Info("master /health pre-flight passed", zap.String("master_url", masterURL))

	// Build the local service graph so the worker can execute handlers.
	// The remote worker shares the same DB (via shared volume) and the
	// same service code; it only differs in how it claims jobs (HTTP
	// broker instead of in-process repo polling).
	root, cleanup, err := app.InitWorkerComposition(cfg, log)
	if err != nil {
		log.Fatal("failed to build worker composition", zap.Error(err))
	}
	defer cleanup()

	registry, caps, err := app.BuildWorkerRegistry(root)
	if err != nil {
		log.Fatal("failed to build worker registry", zap.Error(err))
	}
	if registry.Len() == 0 {
		log.Fatal("worker has no registered handlers — aborting startup")
	}
	log.Info("worker registry built",
		zap.Int("handlers", registry.Len()),
		zap.Strings("capabilities", caps),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	token := strings.TrimSpace(os.Getenv("VELOX_WORKER_TOKEN"))
	broker := jobbrokerclient.New(masterURL, token)
	assetClient := assettransferclient.New(masterURL, token)

	workerID := envOr("VELOX_WORKER_ID", hostnameFallback())
	workerName := envOr("VELOX_WORKER_NAME", workerID)
	version := envOr("VELOX_WORKER_VERSION", "dev")

	// Validate configured capabilities against registered types.
	// Empty env → use all registered types. Malformed/unknown → exit non-zero.
	workerCaps, err := parseAndValidateCaps(os.Getenv("VELOX_WORKER_CAPABILITIES"), caps)
	if err != nil {
		log.Fatal("invalid worker capabilities", zap.Error(err))
	}
	// Freeze the registry after capabilities are resolved — no more
	// registrations are possible from this point.
	registry.Freeze()

	workspaceRoot := filepath.Join(os.TempDir(), "pipelinegen", "jobs")
	_ = os.MkdirAll(workspaceRoot, 0o755)
	log.Info("worker workspace ready", zap.String("workspace_root", workspaceRoot))
	ws, err := worker.NewWorkspace(filepath.Join(os.TempDir(), "pipelinegen"))
	if err != nil {
		log.Fatal("workspace init failed", zap.Error(err))
	}

	session, err := broker.RegisterWorker(ctx, job.RegisterWorkerCommand{
		WorkerID:     workerID,
		Name:         workerName,
		Version:      version,
		Hostname:     hostnameFallback(),
		Capabilities: workerCaps,
		SessionTTL:   90 * time.Second,
	})
	if err != nil {
		log.Fatal("failed to register worker", zap.Error(err))
	}
	log.Info("worker registered",
		zap.String("worker_id", session.WorkerID),
		zap.String("session_id", session.SessionID),
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go heartbeatLoop(runCtx, broker, workerID, session.SessionID, log)

	runner := worker.NewRunner(broker, registry, ws, assetClient, log, workerID, session.SessionID, workerCaps.JobTypes)
	if err := runner.Run(runCtx); err != nil && runCtx.Err() == nil {
		log.Fatal("worker runner failed", zap.Error(err))
	}
}

// resolveMasterURL returns the canonical master URL in priority order:
//
//	$VELOX_MASTER_URL > $VELOX_BROKER_URL > cfg.External.VeloxMasterURL > "http://127.0.0.1:8000"
//
// Compose users can set service-based URLs (http://velox-server:8000)
// so the worker on the same network reaches the master without
// depending on port-mapped IPs. Docker-host users on Linux set
// extra_hosts: ["host.docker.internal:host-gateway"] and use
// http://host.docker.internal:8000.
func resolveMasterURL(cfg *config.Config) string {
	if v := strings.TrimSpace(os.Getenv("VELOX_MASTER_URL")); v != "" {
		return normalizeURL(v)
	}
	if v := strings.TrimSpace(os.Getenv("VELOX_BROKER_URL")); v != "" {
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

// masterURLSource is informational (log only).
func masterURLSource(resolved string) string {
	if v := strings.TrimSpace(os.Getenv("VELOX_MASTER_URL")); v != "" && normalizeURL(v) == resolved {
		return "env:VELOX_MASTER_URL"
	}
	if v := strings.TrimSpace(os.Getenv("VELOX_BROKER_URL")); v != "" && normalizeURL(v) == resolved {
		return "env:VELOX_BROKER_URL"
	}
	return "config-yaml-or-default"
}

// preflightMasterHealth polls <masterURL>/health every 1s up to 30s.
// Returns nil on the first 200; returns error on deadline.
//
// We deliberately do NOT fall back to /api/system/doctor: a worker
// that pretends the master is healthy because /doctor (a heavier
// endpoint) happened to come up does not have the right semantics.
// /health is the canonical liveness signal.
func preflightMasterHealth(masterURL string) error {
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

func closeBody(body interface{ Close() error }) {
	if body == nil {
		return
	}
	_ = body.Close()
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func hostnameFallback() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

// parseAndValidateCaps parses the VELOX_WORKER_CAPABILITIES env var and
// validates that every configured job type exists in the registered set.
// Returns an error (non-nil) for: malformed JSON, empty array, unknown type.
// If raw is empty, returns the full registered set (no narrowing).
func parseAndValidateCaps(raw string, registeredTypes []string) (job.WorkerCapabilities, error) {
	if strings.TrimSpace(raw) == "" {
		return job.WorkerCapabilities{JobTypes: registeredTypes}, nil
	}
	var caps job.WorkerCapabilities
	if err := json.Unmarshal([]byte(raw), &caps); err != nil {
		return job.WorkerCapabilities{}, fmt.Errorf("malformed VELOX_WORKER_CAPABILITIES JSON: %w", err)
	}
	if len(caps.JobTypes) == 0 {
		return job.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES has empty job_types array")
	}
	// Build lookup for registered types.
	registered := make(map[string]struct{}, len(registeredTypes))
	for _, t := range registeredTypes {
		registered[t] = struct{}{}
	}
	// Deduplicate and validate.
	seen := make(map[string]struct{}, len(caps.JobTypes))
	var validated []string
	for _, jt := range caps.JobTypes {
		jt = strings.TrimSpace(jt)
		if jt == "" {
			continue
		}
		if _, ok := seen[jt]; ok {
			continue
		}
		seen[jt] = struct{}{}
		if _, ok := registered[jt]; !ok {
			return job.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES contains unknown job type: %s", jt)
		}
		validated = append(validated, jt)
	}
	if len(validated) == 0 {
		return job.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES resolved to empty set")
	}
	caps.JobTypes = validated
	// W1 spec: "final set sorted and non-empty". Without this sort the
	// resulting slice would mirror input order, which would mask
	// regression in logging/agent-config tooling that assumes deterministic
	// order. Pin it here so a test that hands in ["c","a","b"] snaps to
	// the canonical ascending order.
	sort.Strings(caps.JobTypes)
	return caps, nil
}

func heartbeatLoop(ctx context.Context, broker appjobs.Broker, workerID, sessionID string, log *zap.Logger) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := broker.Heartbeat(ctx, job.HeartbeatCommand{
				WorkerID:        workerID,
				WorkerSessionID: sessionID,
				SessionTTL:      90 * time.Second,
			}); err != nil {
				log.Warn("heartbeat failed", zap.Error(err))
			}
		}
	}
}

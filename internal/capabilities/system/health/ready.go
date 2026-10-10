package system

import (
	"context"
	"sync"
	"time"
)

// ReadyChecker evaluates full-system readiness in one call.
//
// fix(health) close-out (June 2026, problem #2 final cleanup): the
// readiness policy used to live inline in the http handler
// (the health handler's Ready method, removed in Issue 10 June 2026), where the handler called
// svc.Check(ctx, []string{"db"}) AND independently decided the HTTP
// status code. That conflated transport with policy: any new check
// (e.g. broker liveness) required a handler edit. The ReadyChecker
// moves the policy to the application layer.
//
// Construction is pure (no setters, Pattern 0); ReadyChecker is
// constructed once at composition and held on UtilityBundle. The
// http handler becomes thin transport: c.JSON(status, ready.CheckReady(ctx)).
//
// Aggregation rule (inherited verbatim from Service.Check):
//   - DB + Jobs are mandatory; nil checker = misconfiguration surfaced loudly.
//   - Drive + Qdrant are optional; nil / typed-nil / applicable=false = opted out.
//   - applicable=false results do NOT flip allOK; ok=false results do.
//
// Step 8 YouTube Clips Deploy Readiness (July 2026): added severe checks
// for tools (yt-dlp/ffmpeg/ffprobe), clips path writability, Drive canary,
// and handler registration. These are OPTIONAL at construction — nil deps
// report {ok:true, applicable:false} so the /ready shape is stable across
// deploy profiles.
type ReadyChecker struct {
	svc *Service
	// storagePlanes is an independent storage-plane probe. Media and jobs are
	// readiness-critical; cache and observability are reported diagnostically
	// but do not make the runtime unavailable.
	storagePlanes func(context.Context) map[string]CheckResult

	// Step 8 severe checks (July 2026).
	tools        ToolsChecker
	clipsPath    string            // data/media/clips/
	canary       DriveCanaryPort   // publisher-backed canary upload
	canaryFolder string            // target folder for canary
	handlerCheck HandlerRegChecker // job handler registration probe

	// FASE 6 severe readiness checks (July 2026).
	tempPath         string              // writable temp folder path
	tempChecker      TempWritableChecker // temp folder writability
	ttsChecker       TTSChecker          // Python TTS availability
	driveRootChecker DriveRootChecker    // Drive root folder accessible
	driveRootFolder  string              // Drive root folder ID
	ollamaChecker    OllamaChecker       // Ollama reachability
	outboxChecker    OutboxChecker       // outbox worker pool active

	// Step 4 Drive-specific checks (July 2026).
	driveCreds     DriveCredentialsChecker // token.json + credentials.json
	driveFolder    DriveFolderChecker      // folder accessibility via Publisher
	driveFolderID  string                  // target folder for folder-access check
	publisherCheck PublisherChecker        // delivery.Publisher is non-nil
	destClipCheck  DestinationClipChecker  // DestinationYouTubeClip registered

	// Script-generation readiness check (July 2026).
	scriptGenerateCheck ScriptGenerateChecker
	scriptRouteMounted  func() bool

	// PR-YTDLP-HEALTH-GUARD: yt-dlp version staleness + PO Token provider
	// availability. WARN-ONLY — see runYTDLPHealthCheck.
	ytdlpHealth YTDLPHealthChecker
}

// NewReadyChecker wraps the canonical *Service with the readiness policy.
func NewReadyChecker(svc *Service) *ReadyChecker {
	return &ReadyChecker{svc: svc}
}

// WithStoragePlanes adds the independent media/jobs/cache/observability
// storage report to /ready. The callback is kept as a narrow function so the
// health capability does not depend on the concrete SQLite package.
func (r *ReadyChecker) WithStoragePlanes(check func(context.Context) map[string]CheckResult) *ReadyChecker {
	if r != nil {
		r.storagePlanes = check
	}
	return r
}

// CheckReady runs the deep health set (db + drive + qdrant + jobs) and
// returns the aggregated HealthResponse. Callers map the response.OK
// to HTTP 200 vs 503 — the status-mapping lives at the transport layer.
//
// Step 8 (July 2026): also runs severe checks (tools, clips path,
// Drive canary, handler registration) when wired. These are additive —
// nil deps report {ok:true, applicable:false}.
//
// codex/health-ready-contract (June 2026): nil svc is handled gracefully
// — returns ok=false with an explicit error rather than panicking.
func (r *ReadyChecker) CheckReady(ctx context.Context) HealthResponse {
	if r == nil || r.svc == nil {
		return HealthResponse{
			OK:     false,
			Status: "unhealthy",
			Checks: map[string]CheckResult{
				"db":     {"ok": false, "duration_ms": int64(0), "error": "health service not initialized"},
				"drive":  {"ok": false, "duration_ms": int64(0), "error": "health service not initialized"},
				"qdrant": {"ok": false, "duration_ms": int64(0), "error": "health service not initialized"},
				"jobs":   {"ok": false, "duration_ms": int64(0), "error": "health service not initialized"},
			},
		}
	}
	resp := HealthResponse{OK: true, Status: "ready", Checks: map[string]CheckResult{}}
	r.runReadyChecks(ctx, &resp)
	return resp
}

// readyCheckBudget bounds ONE readiness probe.
//
// The probes run CONCURRENTLY, so this is a per-probe budget and not a
// serialized sum. Before that, every probe shared the transport's single 15s
// deadline and ran in series: when the slow probes (Drive canary retry loop,
// Drive root probe) consumed the budget, every later probe saw an
// already-expired context and reported "context deadline exceeded" for healthy
// dependencies — Ollama answered /api/tags in 8ms and /ready still reported it
// unreachable, and tts, storage_* and script_generate.db were false-negative
// for the same reason.
//
// It is a variable so tests can shrink the budget.
var readyCheckBudget = 15 * time.Second

// runReadyChecks runs every readiness probe concurrently and merges the
// results into resp. Each probe gets its own bounded context
// (context.WithoutCancel + readyCheckBudget): the aggregate deadline describes
// the /ready SLA, never a probe's budget, so one slow dependency can only lose
// its own probe.
func (r *ReadyChecker) runReadyChecks(ctx context.Context, resp *HealthResponse) {
	checks := make([]func(context.Context, *HealthResponse), 0, 17)

	// Mandatory/optional component set (db, drive, qdrant, jobs).
	checks = append(checks, func(c context.Context, sub *HealthResponse) {
		got := r.svc.Check(c, []string{"db", "drive", "qdrant", "jobs"})
		mergeHealthResponse(sub, &got)
	})

	if r.storagePlanes != nil {
		checks = append(checks, func(c context.Context, sub *HealthResponse) {
			for name, result := range r.storagePlanes(c) {
				sub.Checks["storage_"+name] = result
				// A cache or observability outage is a degradation, not a reason
				// to take the media/execution service out of readiness.
				if name == "cache" || name == "observability" {
					continue
				}
				if applicable, ok := result["applicable"].(bool); !ok || applicable {
					if healthy, ok := result["ok"].(bool); !ok || !healthy {
						sub.OK = false
						sub.Status = "unhealthy"
					}
				}
			}
		})
	}

	// Step 8 severe checks (tools, clips path, Drive canary, handlers) — each
	// nil dep still reports applicable=false.
	checks = append(checks, r.runToolsCheck, r.ctxFreeCheck(r.runClipsPathCheck), r.runCanaryCheck, r.runHandlerCheck)

	// Step 4 Drive-specific severe checks (credentials, folder, Publisher
	// wiring, DestinationClip registration).
	checks = append(checks, r.runDriveCredentialsCheck, r.runDriveFolderCheck, r.ctxFreeCheck(r.runPublisherCheck), r.ctxFreeCheck(r.runDestinationClipCheck))

	// FASE 6 severe readiness checks (temp, tts, drive_root, ollama, outbox) and
	// the script-generation / yt-dlp health probes.
	checks = append(checks, r.ctxFreeCheck(r.runTempPathCheck), r.runTTSCheck, r.runDriveRootCheck, r.runOllamaCheck, r.runOutboxCheck, r.runScriptGenerateCheck, r.runYTDLPHealthCheck)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, check := range checks {
		wg.Add(1)
		go func(check func(context.Context, *HealthResponse)) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readyCheckBudget)
			defer cancel()
			// A probe writes only into its own response: the aggregate is
			// touched exclusively under mu, so the runners need no locking.
			sub := HealthResponse{OK: true, Status: "ready", Checks: map[string]CheckResult{}}
			check(checkCtx, &sub)
			mu.Lock()
			defer mu.Unlock()
			mergeHealthResponse(resp, &sub)
		}(check)
	}
	wg.Wait()
}

// ctxFreeCheck adapts a probe that takes no context to the check signature.
func (r *ReadyChecker) ctxFreeCheck(check func(*HealthResponse)) func(context.Context, *HealthResponse) {
	return func(_ context.Context, sub *HealthResponse) { check(sub) }
}

// mergeHealthResponse folds one probe's response into the aggregate: every
// check result is kept, and a probe that failed flips the aggregate verdict.
func mergeHealthResponse(dst, src *HealthResponse) {
	if dst == nil || src == nil {
		return
	}
	if dst.Checks == nil {
		dst.Checks = map[string]CheckResult{}
	}
	for name, result := range src.Checks {
		dst.Checks[name] = result
	}
	if !src.OK {
		dst.OK = false
		dst.Status = "unhealthy"
	}
}

package system

import "context"

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
	resp := r.svc.Check(ctx, []string{"db", "drive", "qdrant", "jobs"})
	if r.storagePlanes != nil {
		for name, result := range r.storagePlanes(ctx) {
			resp.Checks["storage_"+name] = result
			// A cache or observability outage is a degradation, not a reason
			// to take the media/execution service out of readiness.
			if name != "cache" && name != "observability" {
				if applicable, ok := result["applicable"].(bool); !ok || applicable {
					if healthy, ok := result["ok"].(bool); !ok || !healthy {
						resp.OK = false
						resp.Status = "unhealthy"
					}
				}
			}
		}
	}

	// Step 8: run severe checks (tools, clips path, Drive canary, handlers).
	// Each nil dep reports applicable=false, preserving the allOK logic.
	r.runToolsCheck(ctx, &resp)
	r.runClipsPathCheck(&resp)
	r.runCanaryCheck(ctx, &resp)
	r.runHandlerCheck(ctx, &resp)

	// Step 4: run Drive-specific severe checks (credentials, folder,
	// Publisher wiring, DestinationClip registration).
	r.runDriveCredentialsCheck(ctx, &resp)
	r.runDriveFolderCheck(ctx, &resp)
	r.runPublisherCheck(&resp)
	r.runDestinationClipCheck(&resp)

	// FASE 6: run severe readiness checks (temp, tts, drive_root, ollama, outbox,
	// script_generate).
	r.runTempPathCheck(&resp)
	r.runTTSCheck(ctx, &resp)
	r.runDriveRootCheck(ctx, &resp)
	r.runOllamaCheck(ctx, &resp)
	r.runOutboxCheck(ctx, &resp)

	// Script-generation readiness: database, job registry/worker, Ollama,
	// document service, Drive, and the /api/script route.
	r.runScriptGenerateCheck(ctx, &resp)

	return resp
}

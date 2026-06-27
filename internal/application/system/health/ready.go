package health

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
type ReadyChecker struct {
	svc *Service
}

// NewReadyChecker wraps the canonical *Service with the readiness policy.
func NewReadyChecker(svc *Service) *ReadyChecker {
	return &ReadyChecker{svc: svc}
}

// CheckReady runs the deep health set (db + drive + qdrant + jobs) and
// returns the aggregated HealthResponse. Callers map the response.OK
// to HTTP 200 vs 503 — the status-mapping lives at the transport layer.
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
	return r.svc.Check(ctx, []string{"db", "drive", "qdrant", "jobs"})
}

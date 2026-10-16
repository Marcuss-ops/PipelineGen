// Package server exposes the job queue over HTTP.
//
// Contract (matches the RenderingGen worker client):
//
//	POST /jobs                  submit a job (PipelineGen)
//	POST /jobs/claim            claim the next job (worker pulls)
//	POST /jobs/{id}/complete    report completion
//	POST /jobs/{id}/fail        report failure (requeue or fail permanently)
//	POST /jobs/{id}/renew       extend the lease during a long render
//	POST /jobs/{id}/progress    report render progress (frames done/total)
//	GET  /jobs/{id}/wait        long-poll until the job is terminal (producer)
//	GET  /jobs/depth            queue depth/stats (autoscaling)
//	GET  /health                health check
//
// File layout: server.go owns routing + submit/claim, transition_routes.go the
// job-transition handlers, read_routes.go the read-only handlers and
// workers.go the worker registry endpoints.
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

const maxClaimWait = 25 * time.Second

const maxSubmitBytes = 10 << 20 // 10 MiB: large semantic plans + asset refs; unbounded is a hardening gap

// Server wraps the job service with HTTP handlers. Wake-up signaling for
// long-poll claims lives in one place — the service's Notifier — so the
// submit/claim/complete transitions and both claim endpoints share a single
// broadcast primitive (see WaitAndClaim).
type Server struct {
	svc            *service.Service
	metricsHandler http.Handler
}

// New creates a server backed by the given job service.
func New(s *service.Service) *Server {
	return &Server{svc: s}
}

// SetMetricsHandler attaches an optional Prometheus exposition handler served
// at GET /metrics. It is a no-op when the handler is nil.
func (s *Server) SetMetricsHandler(h http.Handler) {
	s.metricsHandler = h
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", s.submit)
	mux.HandleFunc("POST /jobs/batch", s.submitBatch)
	mux.HandleFunc("POST /jobs/claim", s.claim)
	mux.HandleFunc("POST /jobs/claim/wait", s.claimWait)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/rendered", s.rendered)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/rendered", s.rendered)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{id}/fail-permanent", s.failPermanently)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/fail-permanent", s.failPermanently)
	mux.HandleFunc("POST /jobs/{id}/retry", s.retry)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/retry", s.retry)
	mux.HandleFunc("POST /jobs/{id}/cancel", s.cancel)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/cancel", s.cancel)
	mux.HandleFunc("POST /jobs/{id}/renew", s.renew)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/renew", s.renew)
	mux.HandleFunc("POST /jobs/{id}/progress", s.progress)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/progress", s.progress)
	mux.HandleFunc("POST /jobs/{id}/finalize/claim", s.claimFinalization)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/finalize/claim", s.claimFinalization)
	mux.HandleFunc("GET /jobs/finalization/recoverable", s.recoverableParents)
	mux.HandleFunc("GET /jobs/{id}/children", s.children)
	mux.HandleFunc("GET /jobs/{parent}/{lang}/children", s.children)
	mux.HandleFunc("GET /jobs/{id}/wait", s.waitJob)
	mux.HandleFunc("GET /jobs/{parent}/{lang}/wait", s.waitJob)
	mux.HandleFunc("GET /jobs/{id}", s.get)
	mux.HandleFunc("GET /jobs/{parent}/{lang}", s.get)
	// Run-scoped listing: every job submitted under one parent_job_id
	// ("what did this run enqueue?"). Registered before the {id} patterns so
	// the bare /jobs path is unambiguous.
	mux.HandleFunc("GET /jobs", s.listByParent)
	mux.HandleFunc("GET /jobs/depth", s.depth)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /workers/register", s.registerWorker)
	mux.HandleFunc("POST /workers/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /workers", s.listWorkers)
	mux.HandleFunc("GET /workers/health", s.workerHealth)
	if s.metricsHandler != nil {
		mux.Handle("GET /metrics", s.metricsHandler)
	}
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

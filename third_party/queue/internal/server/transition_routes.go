// transition_routes.go owns the job-transition HTTP handlers: complete, fail,
// retry, cancel, rendered, progress, renew and finalize-claim. Each transition
// mirrors the repository's ownership semantics (a stale worker is a conflict,
// never a corruption vector).
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// decodeRequestBody decodes a JSON request body and reports whether the caller
// may proceed. A malformed body must surface as a 400 with the real cause,
// never as a misleading 409 ("job X is not running or not owned by ”"), so
// the guard lives in one place instead of once per handler.
func decodeRequestBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return false
	}
	return true
}

// requireWorker rejects a transition that names no worker before it reaches the
// ownership check, where the missing worker would otherwise read as a conflict.
func requireWorker(w http.ResponseWriter, worker string) bool {
	if worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string         `json:"worker"`
		Data   model.Artifact `json:"data"`
	}
	if !decodeRequestBody(w, r, &req) || !requireWorker(w, req.Worker) {
		return
	}

	if err := s.svc.Complete(r.Context(), id, req.Worker, req.Data); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string `json:"worker"`
		Data   struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if !decodeRequestBody(w, r, &req) || !requireWorker(w, req.Worker) {
		return
	}

	if err := s.svc.Fail(r.Context(), id, req.Worker, req.Data.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) failPermanently(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string `json:"worker"`
		Data   struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if req.Worker == "" || req.Data.Reason == "" {
		http.Error(w, "worker and failure reason are required", http.StatusBadRequest)
		return
	}
	if err := s.svc.FailPermanently(r.Context(), id, req.Worker, req.Data.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeTransitionError keeps the not-found/conflict mapping identical across
// the transition handlers. A missing job is always 404; every other ownership
// or state failure uses the handler's declared conflict status.
func writeTransitionError(w http.ResponseWriter, err error, fallback int) {
	if errors.Is(err, repository.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), fallback)
}

func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	if err := s.svc.Retry(r.Context(), id); err != nil {
		writeTransitionError(w, err, http.StatusBadRequest)
		return
	}
	// service.Retry already notified claim waiters.
	w.WriteHeader(http.StatusNoContent)
}

// cancel moves a job that has not reached a terminal state to cancelled. It
// is the producer-side "stop this job" signal: once cancelled the job is
// never claimable again and lease expiry never requeues it, so a cancelled
// job can never trigger a new Chronon render across lease/claim cycles.
// Cancelling an already-cancelled job is a no-op (204); a job that already
// completed or failed cannot be cancelled (409).
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	if err := s.svc.Cancel(r.Context(), id); err != nil {
		writeTransitionError(w, err, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rendered(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string `json:"worker"`
		Data   struct {
			Reason   string         `json:"reason"`
			Artifact model.Artifact `json:"artifact"`
		} `json:"data"`
	}
	if !decodeRequestBody(w, r, &req) || !requireWorker(w, req.Worker) {
		return
	}

	if err := s.svc.Rendered(r.Context(), id, req.Worker, req.Data.Artifact, req.Data.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// progress records render progress reported by the lease-owning worker. It
// mirrors renew's conflict semantics: a stale worker's report is a 409, not a
// corruption vector.
func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string         `json:"worker"`
		Data   model.Progress `json:"data"`
	}
	if !decodeRequestBody(w, r, &req) || !requireWorker(w, req.Worker) {
		return
	}
	if err := s.svc.SetProgress(r.Context(), id, req.Worker, req.Data); err != nil {
		writeTransitionError(w, err, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string `json:"worker"`
	}
	if !decodeRequestBody(w, r, &req) || !requireWorker(w, req.Worker) {
		return
	}

	if err := s.svc.Renew(r.Context(), id, req.Worker); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) claimFinalization(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker string `json:"worker"`
	}
	if !decodeRequestBody(w, r, &req) {
		return
	}
	job, claimed, err := s.svc.ClaimFinalization(r.Context(), parseJobID(r), req.Worker)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

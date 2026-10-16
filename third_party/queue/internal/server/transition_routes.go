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

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string         `json:"worker"`
		Data   model.Artifact `json:"data"`
	}
	// Strict decode + explicit worker check: a malformed body or a missing
	// worker must surface as a 400 with the real cause, never as a misleading
	// 409 ("job X is not running or not owned by ''").
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
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

func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	if err := s.svc.Retry(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
		return
	}
	if err := s.svc.SetProgress(r.Context(), id, req.Worker, req.Data); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string `json:"worker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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

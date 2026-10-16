package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// validClaimState reports whether s is a known job state in the canonical
// wire vocabulary (client.AllStates). Unknown strings are producer typos and
// must be rejected at ingress instead of silently matching nothing.
func validClaimState(s string) bool {
	if s == "" {
		return true
	}
	for _, known := range client.AllStates() {
		if string(known) == s {
			return true
		}
	}
	return false
}

// claimStateErrorIsBadRequest reports whether a ClaimState/WaitAndClaim error
// is a producer input error (unknown state, missing worker) rather than a
// storage failure, so the server can return 400 instead of 500.
func claimStateErrorIsBadRequest(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unsupported claim state") ||
		strings.Contains(msg, "worker id is required") ||
		strings.Contains(msg, "max wait") ||
		strings.Contains(msg, "max_wait")
}

// claim is the thin polling endpoint. With wait_ms==0 it is a single atomic
// ClaimState; with wait_ms>0 it delegates to the single Notifier-backed
// WaitAndClaim so both claim endpoints share one wake-up path. The wait never
// assigns work — every wake just re-runs the atomic claim (SKIP LOCKED).
func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker string `json:"worker"`
		State  string `json:"state,omitempty"`
		WaitMS int64  `json:"wait_ms,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !validClaimState(req.State) {
		http.Error(w, "unknown state "+req.State, http.StatusBadRequest)
		return
	}
	if req.WaitMS < 0 {
		http.Error(w, "wait_ms must be >= 0", http.StatusBadRequest)
		return
	}

	job, lease, err := s.svc.ClaimState(r.Context(), req.Worker, model.State(req.State))
	if err != nil {
		if claimStateErrorIsBadRequest(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil && req.WaitMS > 0 {
		wait := time.Duration(req.WaitMS) * time.Millisecond
		if wait > maxClaimWait {
			wait = maxClaimWait
		}
		// Delegate the wait to the service: it re-runs the atomic claim on
		// every wake-up and bounded re-poll, on the single shared Notifier.
		waitCtx, cancel := context.WithTimeout(r.Context(), wait+5*time.Second)
		defer cancel()
		job, lease, err = s.svc.WaitAndClaim(waitCtx, req.Worker, model.State(req.State), wait)
		if err != nil {
			if r.Context().Err() != nil {
				return // client disconnected; nothing to write
			}
			if claimStateErrorIsBadRequest(err) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// The claim response is the canonical Job envelope plus the lease the
	// worker must renew. There is no separate claim-response type: the wire
	// contract exists once, in queue/client, and every layer aliases it.
	job.Lease = lease
	writeJSON(w, http.StatusOK, job)
}

// claimWait is the dedicated long-poll endpoint. It is a thin wrapper over
// the same service WaitAndClaim as claim's wait_ms path — both share the
// single Notifier broadcast (submit/complete/fail/requeue all Notify). The
// only difference is wire naming (max_wait_ms vs wait_ms) and default wait.
func (s *Server) claimWait(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker    string `json:"worker"`
		State     string `json:"state,omitempty"`
		MaxWaitMs int64  `json:"max_wait_ms,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !validClaimState(req.State) {
		http.Error(w, "unknown state "+req.State, http.StatusBadRequest)
		return
	}
	if req.MaxWaitMs < 0 {
		http.Error(w, "max_wait_ms must be >= 0", http.StatusBadRequest)
		return
	}
	maxWait := time.Duration(req.MaxWaitMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(r.Context(), maxWait+5*time.Second)
	defer cancel()
	job, lease, err := s.svc.WaitAndClaim(ctx, req.Worker, model.State(req.State), maxWait)
	if err != nil {
		if claimStateErrorIsBadRequest(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The claim response is the canonical Job envelope plus the lease the
	// worker must renew. There is no separate claim-response type: the wire
	// contract exists once, in queue/client, and every layer aliases it.
	job.Lease = lease
	writeJSON(w, http.StatusOK, job)
}

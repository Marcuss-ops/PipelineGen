// wait_routes.go owns the producer-side job-status long poll. It is a pure
// read: it never mutates state and never assigns work, it only parks the HTTP
// request until the job reaches a terminal state (or the bounded wait
// elapses) and returns the current job document, exactly like GET /jobs/{id}.
package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

const (
	// defaultJobWait is the wait window when the caller omits max_wait_ms.
	defaultJobWait = maxClaimWait
	// maxJobWait bounds a single wait request so a producer can never pin an
	// HTTP connection (and a server goroutine) indefinitely.
	maxJobWait = maxClaimWait
)

// waitJob serves GET /jobs/{id}/wait?max_wait_ms=N.
//
//   - 200 + the job document once it is terminal (completed/failed/cancelled),
//     or when the bounded wait elapses — the response body is the same shape
//     as GET /jobs/{id}, terminal or not.
//   - 404 when the job does not exist.
//
// Terminal transitions broadcast the service's single Notifier (submit,
// complete, fail, rendered, cancel, retry, requeue and the PostgreSQL LISTEN
// bridge), so on the replica that performed the transition the caller is woken
// immediately. On another replica the service's bounded re-poll covers it.
func (s *Server) waitJob(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	if id == "" {
		http.Error(w, "job id is required", http.StatusBadRequest)
		return
	}
	maxWait := defaultJobWait
	if raw := r.URL.Query().Get("max_wait_ms"); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ms < 0 {
			http.Error(w, "max_wait_ms must be a non-negative integer", http.StatusBadRequest)
			return
		}
		maxWait = time.Duration(ms) * time.Millisecond
		if maxWait <= 0 || maxWait > maxJobWait {
			maxWait = maxJobWait
		}
	}

	job, err := s.svc.WaitState(r.Context(), id, maxWait)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// The client disconnected or the request context ended: there is
			// no connection left to answer on.
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

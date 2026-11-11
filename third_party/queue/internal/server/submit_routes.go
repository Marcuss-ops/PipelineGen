package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// validateSubmittedJob runs the three fail-at-ingress checks in order, so a
// malformed producer payload never becomes a stored job.
func validateSubmittedJob(job model.Job) error {
	if err := client.ValidateJobID(job.ID); err != nil {
		return err
	}
	if err := client.ValidateJobMetadata(job); err != nil {
		return err
	}
	return client.ValidateJobEnvelope(job)
}

func (s *Server) submitBatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)
	var req struct {
		Jobs []model.Job `json:"jobs"`
	}
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if len(req.Jobs) == 0 {
		http.Error(w, "jobs is required", http.StatusBadRequest)
		return
	}
	keyCount := 0
	for _, job := range req.Jobs {
		if err := validateSubmittedJob(job); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if job.IdempotencyKey != "" {
			keyCount++
		}
	}
	if keyCount != 0 && keyCount != len(req.Jobs) {
		http.Error(w, "idempotency keys must be provided for every job in a batch", http.StatusBadRequest)
		return
	}
	created, err := s.svc.SubmitBatchIdempotent(r.Context(), req.Jobs)
	if err != nil {
		if errors.Is(err, repository.ErrJobExists) {
			http.Error(w, "job_id_conflict: "+err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			http.Error(w, "idempotency_conflict: "+err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}
func parseJobID(r *http.Request) string {
	if parent := r.PathValue("parent"); parent != "" {
		if lang := r.PathValue("lang"); lang != "" {
			return parent + "/" + lang
		}
	}
	return r.PathValue("id")
}
func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)
	var job model.Job
	if !decodeRequestBody(w, r, &job) {
		return
	}
	if job.ID == "" {
		id, err := newID()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job.ID = id
	}
	// Fail-at-ingress, before SubmitIdempotent: an invalid payload must be a
	// 400, never a stored job and never an idempotent replay.
	if err := validateSubmittedJob(job); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	canonical, created, err := s.svc.SubmitIdempotent(r.Context(), job)
	if err != nil {
		// Duplicate IDs and mismatched idempotency replays are conflicts. A
		// transient storage failure must never be reported as 409: producers
		// could otherwise mistake an outage for a completed replay.
		if errors.Is(err, repository.ErrJobExists) {
			http.Error(w, "job_id_conflict: "+err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			http.Error(w, "idempotency_conflict: "+err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// SubmitIdempotent already notified claim waiters on creation.
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]string{"id": canonical.ID})
}
func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Retry once: a weak fallback ID would break the idempotency and
		// uniqueness contract, so fail instead of returning weak bytes.
		if _, err := rand.Read(b); err != nil {
			return "", errors.New("generate job id: entropy source unavailable")
		}
	}
	return "job-" + hex.EncodeToString(b), nil
}

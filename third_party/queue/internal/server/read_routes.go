// read_routes.go owns the read-only HTTP handlers: listByParent, children,
// get, depth and health. None of them mutate queue state.
package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// listByParent implements GET /jobs?parent_job_id=<id>: the run-scoped read that
// answers "what did this run enqueue?" without knowing any job id in advance.
//
// It exists because a producer stamps parent_job_id with the MASTER run id on
// every job it submits, while the only pre-existing read into that column was
// GET /jobs/{id}/children (the assembly fan-in, ordered by chunk index). An
// operator correlating a run with the render jobs it produced therefore had to
// scrape the id out of a log first. A missing parent_job_id is a 400 (the query
// parameter IS the question); an unknown id is an empty 200, not a 404 — "this
// run enqueued nothing" is an answer, not a miss.
func (s *Server) listByParent(w http.ResponseWriter, r *http.Request) {
	parentJobID := strings.TrimSpace(r.URL.Query().Get("parent_job_id"))
	if parentJobID == "" {
		http.Error(w, "parent_job_id is required", http.StatusBadRequest)
		return
	}
	jobs, err := s.svc.ByParent(r.Context(), parentJobID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if jobs == nil {
		jobs = []*model.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) recoverableParents(w http.ResponseWriter, r *http.Request) {
	ids, err := s.svc.RecoverableParents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ids)
}

func (s *Server) children(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.svc.Children(r.Context(), parseJobID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	job, err := s.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) depth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Stats(r.Context()))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

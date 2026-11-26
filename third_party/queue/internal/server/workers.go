package server

import (
	"encoding/json"
	"net/http"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// registerWorker handles POST /workers/register.
func (s *Server) registerWorker(w http.ResponseWriter, r *http.Request) {
	var worker model.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.svc.RegisterWorker(r.Context(), worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// heartbeat handles POST /workers/heartbeat.
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker string `json:"worker"`
	}
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if err := s.svc.WorkerHeartbeat(r.Context(), req.Worker); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listWorkers handles GET /workers. The request context is threaded into the
// read so a client that disconnects stops the query instead of leaving it to
// run to completion against the registry.
func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.svc.ListWorkers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, workers)
}

// workerCapabilities handles GET /capabilities: the capability inventory the
// live workers advertise, sorted and de-duplicated. A producer validates the
// plan it is about to render against this answer instead of a hand-written
// allowlist, so the reply is exactly what the running worker proves.
func (s *Server) workerCapabilities(w http.ResponseWriter, r *http.Request) {
	capabilities, err := s.svc.WorkerCapabilities(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if capabilities == nil {
		capabilities = []string{}
	}
	writeJSON(w, http.StatusOK, capabilities)
}

// workerHealth handles GET /workers/health.
func (s *Server) workerHealth(w http.ResponseWriter, r *http.Request) {
	health, err := s.svc.WorkerHealth(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

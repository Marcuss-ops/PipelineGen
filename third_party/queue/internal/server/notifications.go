package server

import "github.com/Marcuss-ops/RenderingGen/queue/internal/model"

// NotifyState wakes long-poll claimers after an external queue notification
// (for example PostgreSQL LISTEN/NOTIFY). It never assigns a job: claimers
// still re-read the database and compete through SKIP LOCKED. Wake-ups are
// broadcast through the service's single Notifier; a claimer re-runs the
// atomic claim for its requested state, so a state-agnostic wake is safe.
func (s *Server) NotifyState(_ model.State) {
	s.svc.Notify().Notify()
}

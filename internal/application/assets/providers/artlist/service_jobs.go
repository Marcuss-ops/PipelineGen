package artlist

import (
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// RegisterHandler registers the Artlist job handler (HandleJob) with the
// canonical appjobs.Service dispatcher for TypeArtlistRun ("media.artlist").
// The composition root (WireArtlistJobBindings in build_bundles_artlist.go)
// calls this method after WireArtlist completes, mirroring the
// catalog/youtube RegisterHandler precedent in build_bundles_youtube.go.
//
// PR-P2-FAILCLOSED-JOB (July 2026): on success the receiver ALSO
// tracks hasConsumer=true so the new /api/artlist/job-consumer
// health endpoint (artlist_handlers.go::JobConsumer) can read the
// consumer state without coupling to the appjobs.Service surface
// shape. The handler-side check is the source of truth for "is
// media.artlist consumed?" — operator dashboards +
// `make verify-main` gate both rely on this bool. godlike/06 SSOT:
// the bool is the SINGLE canonical ownership surface for the
// consumer-alive question scoped to artlist.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	// PR-P2-FAILCLOSED-JOB (July 2026): reset hasConsumer=false BEFORE
	// the bind call so a panic mid-call, a failed re-bind, or any
	// side-channel that could leave stale TRUE is correctly cleared
	// regardless of prior state (defense-in-depth on top of the
	// post-bind HasHandler cross-check in WireArtlistJobBindings).
	s.hasConsumer = false
	if err := s.jobAdapter.RegisterHandler(jobsSvc); err != nil {
		return err
	}
	s.hasConsumer = true
	return nil
}

// HasConsumer reports whether the Artlist job handler is currently
// bound to a appjobs.Service dispatcher for TypeArtlistRun
// ("media.artlist"). godlike/06 SSOT — operator-facing surface for
// /api/artlist/job-consumer; composition root drives the bool via
// RegisterHandler (which is the only writer). Returns false when the
// receiver is nil (defensive godlike/06 SSOT — handler-side nil
// tolerance without a panic on dereference).
func (s *Service) HasConsumer() bool {
	if s == nil {
		return false
	}
	return s.hasConsumer
}

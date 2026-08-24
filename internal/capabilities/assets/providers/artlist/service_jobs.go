package assets

import (
	"context"
	"errors"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// ErrInvalidRunDedupInput is returned by Service.EnqueueRun when the
// run-dedup ActiveKey cannot be constructed from operator-supplied
// request fields (malformed term/folder/strategy shape surfaced by
// pkg/idempotency via the canonical RunDedupKey wrapper).
//
// godlike/07 typed-error contract: the HTTP handler maps this sentinel
// to 400 BadRequest because a malformed run-dedup input is an
// operator-input error, NOT a server-side failure. All other EnqueueRun
// failures (jobs service not wired, enqueue transport error) surface as
// plain wrapped errors → HTTP 500 at the transport.
var ErrInvalidRunDedupInput = errors.New("artlist: invalid run-dedup input")

// EnqueueRun is the single canonical enqueue path for a media.artlist
// run job. It builds the deterministic run-dedup ActiveKey and enqueues
// through the canonical jobs service, mirroring the orchestration
// pattern already used by SearchService.DiscoverAndQueueRun (godlike/06
// SSOT: both paths must produce byte-identical dedup keys + payloads).
//
// Error contract (godlike/07 typed errors):
//   - ErrInvalidRunDedupInput (wrap) → operator-input error → HTTP 400
//   - "jobs service is not configured" → composition gap → HTTP 500
//   - enqueue failure → transport/server error → HTTP 500
//
// Returns the enqueued *job.Job so the caller can render the canonical
// RunTagResponse via JobToRunTagResponse.
func (s *Service) EnqueueRun(ctx context.Context, req RunTagRequest) (*job.Job, error) {
	if s == nil || s.jobsSvc == nil {
		return nil, fmt.Errorf("artlist.Service.EnqueueRun: jobs service is not configured")
	}

	// Commit B (FASE 5 follow-up, July 2026): RunDedupKey now returns
	// (string, error). The previous shape silently produced a fmt.Sprintf
	// fallback when canonical json.Marshal failed; the new shape fails
	// closed per godlike/07 with a typed sentinel from pkg/idempotency.
	runActiveKey, err := RunDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRunDedupInput, err)
	}

	enqueued, err := s.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
		Type:       media.TypeArtlistRun,
		Payload:    (&JobCodec{}).PayloadFromRequest(&req),
		MaxRetries: 3,
		// Fase 5 / Commit 3 follow-up (July 2026): limit is part of
		// the canonical run identity. A replay with the same term +
		// folder + strategy + dryRun but a DIFFERENT limit is a
		// DIFFERENT run (the operator asked for a different number
		// of clips), and must produce a different ActiveKey so the
		// dedup does not silently merge distinct operator requests
		// (godlike/07 fail-closed).
		ActiveKey: runActiveKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue artlist job: %w", err)
	}
	return enqueued, nil
}

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

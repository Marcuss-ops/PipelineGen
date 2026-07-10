// job_registration.go — registers handlers for YouTube job types.
//
// (CPR-CC-6 + PR1.7, June 2026.) Split out of orchestrator.go in Step 4
// so each usecase/ file owns exactly one responsibility. The Service
// struct, ServiceDeps, and constructor remain in service.go.
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytjobs "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/jobs"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// RegisterHandler wires the orchestrator's two job-type handlers into the
// central jobtools.Service. Call once at composition time.
//
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps jobtools.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, jobtools.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs. The error-return signature (refactored in
// Audit P0 #2 cont. — PR-VALIDATOR-LITERAL-REGISTER, July 2026)
// closes the silent-success class of "if jobsSvc == nil { return }"
// that pre-P0 #2 swallowed wiring gaps.
func (s *Service) RegisterHandler(jobsSvc *jobtools.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("youtube.Service.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", jobtools.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(jobservice.TypeYouTubeClipExtract, jobtools.HandlerFunc(ytjobs.NewJobHandler(s, s.log).HandleJob)); err != nil {
		return fmt.Errorf("youtube.Service.RegisterHandler: bind %q to dispatcher: %w", jobservice.TypeYouTubeClipExtract, err)
	}
	s.log.Info("registered youtube_clip.extract job handler", zap.String("type", jobservice.TypeYouTubeClipExtract))

	// rebuild_search_text needs Clips to be wired so the rebuild can
	// locate the indexed-clip rows. Guard keeps a half-wired bundle from
	// registering a handler that would no-op on first invocation.
	if s.clips != nil {
		if err := jobsSvc.RegisterHandler(jobservice.TypeYouTubeRebuildST, jobtools.HandlerFunc(s.HandleRebuildSearchTextJob)); err != nil {
			return fmt.Errorf("youtube.Service.RegisterHandler: bind %q to dispatcher: %w", jobservice.TypeYouTubeRebuildST, err)
		}
		s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", jobservice.TypeYouTubeRebuildST))
	}
	return nil
}

// HandleRebuildSearchTextJob wraps the free function form of
// ytjobs.HandleRebuildSearchTextJob (which takes a RebuildDeps struct as
// its first arg) into the method-form signature expected by
// jobtools.Service.RegisterHandler: (ctx, j, tools) → (map, error).
//
// The dep surface (clip lister, log, indexer, metadata enricher closure)
// is reconstructed lazily from the orchestrator's runtime state at
// invocation time, so the composition root does not have to thread a
// fifth dep through ServiceDeps for this job type specifically.
//
// Interface alignment between orchestrator runtime
// fields and the ytjobs.RebuildDeps struct (specifically ClipIndexerPort
// vs jobs.ClipIndexer, plus the meta any closure-cast to
// *youtubeports.DownloaderMetadata) needs verification once a real
// rebuild_search_text job is exercised end-to-end.
func (s *Service) HandleRebuildSearchTextJob(ctx context.Context, j *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	deps := ytjobs.RebuildDeps{
		Log:     s.log,
		Indexer: s.indexer,
		Clips:   s.clips,
		Enricher: func(ctx context.Context, clipID string, meta any, force bool) {
			var m *youtubeports.DownloaderMetadata
			if meta != nil {
				m, _ = meta.(*youtubeports.DownloaderMetadata)
			}
			s.enrichClip(ctx, clipID, m, force)
		},
	}
	return ytjobs.HandleRebuildSearchTextJob(deps, ctx, j, tools)
}

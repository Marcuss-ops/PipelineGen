package localization

// service.go owns the canonical localization orchestration: the single seam
// that ties the render step, the parallel scheduler, and the Drive upload into
// one fan-out. It is the composition-root entry point a service/CLI calls to
// "localize this source into these languages".
//
// Pipeline (per source):
//
//	[]LocalizedClipPlan (fingerprinted, priority-ordered)
//	  → Scheduler (bounded concurrency)
//	     → LocalizedClipRenderer.Render   (RenderPlan → Rust → RENDERED)
//	     → DrivePublisher.Publish          (→ UPLOADED)
//
// godlike/06 SSOT (one canonical owner per fact): this is the SINGLE fan-out
// orchestrator. The renderer, scheduler, and uploader each own their step; the
// service owns only the ordering between them.
//

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// LocalizeInput is the fully-resolved input for one localization fan-out.
// Plans arrive already fingerprinted + priority-ordered (the PlanBuilder owns
// that step); the service renders, uploads, and assembles them.
type LocalizeInput struct {
	// Concurrency is the render fan-out parallelism. <1 is clamped to
	// DefaultRenderConcurrency.
	Concurrency int
	// UploadConcurrency bounds Drive publication independently from rendering.
	// A zero value uses the service default.
	UploadConcurrency int
	// OnRendered is called after local bytes are certified and before Drive
	// publication. It is optional and must not mutate the artifact.
	OnRendered func(LocalizedClipArtifact) error
	// FolderID is the Drive folder the rendered clips upload into.
	FolderID string
	// SubtitleFolderID is the per-clip Drive folder for the compiled ASS.
	SubtitleFolderID       string
	UploadSubtitleArtifact bool
	// SkipDrivePublication renders and certifies the local artifact without
	// uploading it. The result artifacts stay RENDERED; the caller must consume
	// the local/object-store bytes. FolderID/SubtitleFolderID are not read.
	SkipDrivePublication bool
	// Plans are the fingerprinted plans, in priority (editorial) order.
	Plans []LocalizedClipPlan
}

// LocalizeResult is the certified outcome of one fan-out: the certified
// artifacts (priority order; UPLOADED, or RENDERED when Drive publication was
// skipped) and the per-language failures (a failed language never aborts the
// others).
type LocalizeResult struct {
	Artifacts []LocalizedClipArtifact
	Failures  []TaskResult
}

// Service is the canonical localization orchestrator. It is immutable after
// construction and safe for concurrent Localize calls.
type Service struct {
	renderer   *LocalizedClipRenderer
	uploader   *DrivePublisher
	renderGate chan struct{}
	uploadGate chan struct{}
	// reusePublishedDrive records where a certified clip was published and
	// reuses that identity on an identical later run instead of re-uploading
	// unchanged bytes. OFF by default: the per-run folder contract wins unless
	// the deployment opts in.
	reusePublishedDrive bool
}

// WithPublishedDriveReuse opts the fan-out into content-addressed Drive reuse:
// a clip whose bytes are already published is certified UPLOADED with the
// existing Drive identity instead of being uploaded again. The flag reaches
// both halves of the seam — the publisher (skip) and the reuse cache (record) —
// so they can never disagree about whether reuse is allowed.
func (s *Service) WithPublishedDriveReuse(enabled bool) *Service {
	if s == nil {
		return s
	}
	s.reusePublishedDrive = enabled
	if s.uploader != nil {
		s.uploader.WithPublishedReuse(enabled)
	}
	return s
}

// recordDriveIdentity persists where certified bytes were published, so a later
// identical run can skip the upload. Best-effort by design: a Store failure
// after a successful upload must never fail a run that already delivered.
func (s *Service) recordDriveIdentity(ctx context.Context, artifact LocalizedClipArtifact) {
	if s == nil || !s.reusePublishedDrive || s.renderer == nil || s.renderer.reuseCache == nil {
		return
	}
	if strings.TrimSpace(artifact.DriveFileID) == "" || strings.TrimSpace(artifact.DriveLink) == "" {
		return
	}
	recorder, ok := s.renderer.reuseCache.(RenderDriveIdentityRecorder)
	if !ok {
		return
	}
	_ = recorder.RecordDriveIdentity(ctx, artifact.PlanFingerprint, RenderDriveIdentity{
		FileID:   artifact.DriveFileID,
		Link:     artifact.DriveLink,
		FolderID: artifact.DriveFolderID,
	})
}

// UploadRendered publishes an already certified local artifact without
// invoking the renderer. It is the recovery boundary for a crash or transient
// Drive failure after RENDERED and before UPLOADED.
func (s *Service) UploadRendered(ctx context.Context, artifact LocalizedClipArtifact, folderID string) (LocalizedClipArtifact, error) {
	if s == nil || s.uploader == nil {
		return artifact, fmt.Errorf("localization: upload rendered: service is not initialized")
	}
	release, err := kernobs.AcquireSlot(ctx, s.uploadGate, kernobs.ComponentDrive, kernobs.WaitSemaphore)
	if err != nil {
		return artifact, err
	}
	defer release()
	started := time.Now().UTC()
	out, err := s.uploader.Publish(ctx, artifact, folderID)
	finished := time.Now().UTC()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseDrive, started, finished, kernobs.StageStatusCompleted, err)
	return out, err
}

// defaultGlobalRenderConcurrency is the measured fan-out ceiling for the
// localization render lane: 4 concurrent renders amortize the per-language
// prepare/startup (see RenderingGen gpu_lanes=2 + DefaultRenderConcurrency=4
// + daemon_reused/graph_reused_frames — the win is admission + reuse, not
// overlapping Chronon execution, which stays serialized per daemon domain).
// Upload stays 4 so a slow Drive operation never blocks the next render
// (Localize releases the render gates at RENDERED, before Drive).
const (
	defaultGlobalRenderConcurrency = 4
	defaultUploadConcurrency       = 4
)

// RenderWidth resolves the effective render fan-out for ONE Localize call from
// the shared machine bound and the caller's requested width. Every call holds
// BOTH a per-call gate (the request) and the shared machine gate, so a request
// wider than the machine bound is throttled by the shared gate silently: an
// operator who sends render_concurrency=5 against a machine bound of 4 pays a
// full extra per-scene wait with no signal at all (2026-09-21 tail audit: the
// fifth of five scenes always waited for a slot). Resolving the width once, and
// reporting the clamp, turns that invisible throttle into an explicit one.
//
// machineBound <1 means "no shared bound" (unbounded); requested <1 falls back
// to DefaultRenderConcurrency. cramped is true exactly when the machine bound
// is the stricter of the two, i.e. when the caller must raise the deployment
// bound for the request to take effect.
func RenderWidth(machineBound, requested int) (effective int, cramped bool) {
	if requested < 1 {
		requested = DefaultRenderConcurrency
	}
	if machineBound > 0 && machineBound < requested {
		return machineBound, true
	}
	return requested, false
}

// NewService builds the orchestrator. Fail-closed: all three steps are
// mandatory — a service that cannot render, upload, or assemble can never
// complete a fan-out.

// NewServiceWithConcurrency builds the orchestrator with separate bounded
// resource pools. Render slots cover only the renderer; upload slots cover
// only Drive I/O.
func NewServiceWithConcurrency(renderer *LocalizedClipRenderer, uploader *DrivePublisher, renderConcurrency, uploadConcurrency int) (*Service, error) {
	if renderConcurrency < 1 {
		renderConcurrency = defaultGlobalRenderConcurrency
	}
	return NewServiceWithConcurrencyAndGate(renderer, uploader, make(chan struct{}, renderConcurrency), uploadConcurrency)
}

// NewServiceWithConcurrencyAndGate lets the composition root share the SAME
// admission channel with the render executor. The executor holds it around
// Submit/Settle only, so a 22–33 MB Materialize no longer occupies a GPU slot
// (CR-14).
func NewServiceWithConcurrencyAndGate(renderer *LocalizedClipRenderer, uploader *DrivePublisher, renderGate chan struct{}, uploadConcurrency int) (*Service, error) {
	if renderer == nil {
		return nil, fmt.Errorf("localization.NewService: renderer is required")
	}
	if uploader == nil {
		return nil, fmt.Errorf("localization.NewService: drive publisher is required")
	}
	if renderGate == nil || cap(renderGate) < 1 {
		renderGate = make(chan struct{}, defaultGlobalRenderConcurrency)
	}
	if uploadConcurrency < 1 {
		uploadConcurrency = defaultUploadConcurrency
	}
	return &Service{
		renderer: renderer, uploader: uploader,
		renderGate: renderGate,
		uploadGate: make(chan struct{}, uploadConcurrency),
	}, nil
}

// GPUGate exposes the shared admission channel for the render executor. It is
// a capability seam, not a second owner: the service still owns its width, the
// executor only acquires/releases around Submit/Settle.
func (s *Service) GPUGate() chan struct{} {
	if s == nil {
		return nil
	}
	return s.renderGate
}

// Localize runs the full fan-out: render + upload each plan with bounded
// concurrency. Per-language failures are recorded (never fatal to the
// others).
func (s *Service) Localize(ctx context.Context, in LocalizeInput) (*LocalizeResult, error) {
	if s == nil || s.renderer == nil || s.uploader == nil {
		return nil, fmt.Errorf("localization: service is not initialized")
	}
	if len(in.Plans) == 0 {
		return nil, fmt.Errorf("localization: localize: plans is required")
	}

	// The per-call render gate is the requested width capped by the shared
	// machine gate (see RenderWidth). Resolving it here keeps the two gates from
	// disagreeing about how wide this call may be, so the clamp is a single,
	// testable fact instead of an emergent property of two semaphores.
	concurrency, _ := RenderWidth(cap(s.renderGate), in.Concurrency)

	// Streaming producer-consumer pipeline. Each worker owns a plan, releases
	// the render slots at RENDERED, and immediately enters the independent
	// upload pool. Thus a slow Drive operation never blocks the next render.
	localRenderGate := make(chan struct{}, concurrency)
	results := make([]TaskResult, len(in.Plans))
	for i, plan := range in.Plans {
		results[i] = TaskResult{Priority: plan.Priority}
	}
	var pipelineWG sync.WaitGroup
	var resultsMu sync.Mutex
	// Launch longest-first (LPT): the plans' DurationMS is the render window,
	// so starting the longest renders first lets the short ones fill the tail
	// instead of waiting behind them. The 2026-09-21 chain-debug measured
	// chronon_queue_wait up to 17.2s inside a 5-scene fan-out whose similar-
	// length scenes landed on the GPU lanes in arbitrary order; LPT cannot
	// change the lanes, but it removes the avoidable component of the tail
	// wait when durations differ. Results stay keyed by the ORIGINAL index,
	// so the published manifest keeps the editorial priority order — only
	// the submission order changes.
	for _, idx := range longestFirstLaunchOrder(in.Plans) {
		plan := in.Plans[idx]
		// Per-plan submission time: the admission wait is measured from here to
		// the render slot, never from the call's first plan (which would charge
		// plan N with the work of plans 1..N-1).
		submittedAt := time.Now().UTC()
		pipelineWG.Add(1)
		go func() {
			defer pipelineWG.Done()
			recordFailure := func(artifact LocalizedClipArtifact, renderErr error) {
				resultsMu.Lock()
				results[idx] = TaskResult{Priority: plan.Priority, Artifact: artifact, Err: renderErr}
				resultsMu.Unlock()
			}
			releaseLocal, err := kernobs.AcquireSlot(ctx, localRenderGate, kernobs.ComponentRenderQueue, kernobs.WaitSemaphore)
			if err != nil {
				recordFailure(LocalizedClipArtifact{Status: LocalizedClipFailed, QueueWaitMS: time.Since(submittedAt).Milliseconds()}, err)
				return
			}
			admittedAt := time.Now().UTC()
			// GPU admission is held by the executor around Submit/Settle only;
			// renderer.Render still compiles, wires and hashes outside that slot.
			renderStart := time.Now().UTC()
			rendered, renderErr := s.renderer.Render(ctx, plan)
			renderEnd := time.Now().UTC()
			kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseFFmpeg, renderStart, renderEnd, kernobs.StageStatusCompleted, renderErr)
			releaseLocal()
			if renderErr != nil {
				recordFailure(rendered, renderErr)
				return
			}
			if in.UploadSubtitleArtifact {
				rendered.SubtitleFolderID = in.SubtitleFolderID
			} else {
				rendered.SubtitlePath = ""
				rendered.SubtitleSHA256 = ""
			}
			if in.OnRendered != nil {
				if readyErr := in.OnRendered(rendered); readyErr != nil {
					recordFailure(rendered, readyErr)
					return
				}
			}
			if in.SkipDrivePublication {
				// The bytes are consumed from the local/object-store path. Keep
				// the certified RENDERED artifact instead of inventing an
				// UPLOADED status with no Drive identity.
				rendered.AdmittedAt = admittedAt
				rendered.FinishedAt = time.Now().UTC()
				rendered.QueueWaitMS = admittedAt.Sub(submittedAt).Milliseconds()
				recordFailure(rendered, nil)
				return
			}
			published, publishErr := s.UploadRendered(ctx, rendered, in.FolderID)
			finishedAt := time.Now().UTC()
			if publishErr == nil {
				// A reused artifact lives in the folder that already holds it; only
				// a fresh publication belongs to this run's destination.
				if !published.Reused || published.DriveFolderID == "" {
					published.DriveFolderID = in.FolderID
				}
				s.recordDriveIdentity(ctx, published)
				// The unit's own span is admission → publication; the wait before
				// admission is a separate fact (see LocalizedClipArtifact).
				published.AdmittedAt = admittedAt
				published.FinishedAt = finishedAt
				published.QueueWaitMS = admittedAt.Sub(submittedAt).Milliseconds()
			}
			recordFailure(published, publishErr)
		}()
	}
	pipelineWG.Wait()

	artifacts := make([]LocalizedClipArtifact, 0, len(results))
	failures := make([]TaskResult, 0)
	for _, r := range results {
		if r.Err != nil {
			failures = append(failures, r)
			continue
		}
		artifacts = append(artifacts, r.Artifact)
	}
	return &LocalizeResult{Artifacts: artifacts, Failures: failures}, nil
}

// longestFirstLaunchOrder returns the plan indices ordered by estimated render
// weight (DurationMS, the render window) descending, with ties stable so the
// editorial priority order inside a duration band is preserved. It decides
// only the goroutine SUBMISSION order of the Localize fan-out: results are
// written by original index, so the manifest, artifacts and failures keep the
// editorial ordering the correctness contracts pin.
func longestFirstLaunchOrder(plans []LocalizedClipPlan) []int {
	order := make([]int, len(plans))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return plans[order[a]].DurationMS > plans[order[b]].DurationMS
	})
	return order
}

package localization

// service.go owns the canonical localization orchestration: the single seam
// that ties the render step, the parallel scheduler, the Drive upload, and the
// Docs assembly into one fan-out. It is the composition-root entry point a
// service/CLI calls to "localize this source into these languages".
//
// Pipeline (per source):
//
//	[]LocalizedClipPlan (fingerprinted, priority-ordered)
//	  → Scheduler (bounded concurrency)
//	     → LocalizedClipRenderer.Render   (RenderPlan → Rust → RENDERED)
//	     → DrivePublisher.Publish          (→ UPLOADED)
//	  → []LocalizedDocumentEntry (priority-ordered)
//	  → DocumentAssembler.Assemble (→ LocalizedDocumentRef)
//
// godlike/06 SSOT (one canonical owner per fact): this is the SINGLE fan-out
// orchestrator. The renderer, scheduler, uploader, and assembler each own their
// step; the service owns only the ordering between them and the projection
// from certified artifacts into document entries.

import (
	"context"
	"fmt"
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
	// DocTitle / DocFolderID / DocIdempotencyKey / DocForce configure the
	// localization manifest Google Doc.
	DocTitle          string
	DocFolderID       string
	DocIdempotencyKey string
	DocForce          bool
	// SkipDocument disables the per-localized-clip manifest. Script
	// generation owns the single final Google Doc; clip localization must
	// only render/upload the MP4 and never create a Doc as a side effect.
	SkipDocument bool

	// Plans are the fingerprinted plans, in priority (editorial) order.
	Plans []LocalizedClipPlan
}

// LocalizeResult is the certified outcome of one fan-out: the published doc,
// the UPLOADED artifacts (priority order), and the per-language failures (a
// failed language never aborts the others).
type LocalizeResult struct {
	Ref       *LocalizedDocumentRef
	Artifacts []LocalizedClipArtifact
	Failures  []TaskResult
}

// Service is the canonical localization orchestrator. It is immutable after
// construction and safe for concurrent Localize calls.
type Service struct {
	renderer   *LocalizedClipRenderer
	uploader   *DrivePublisher
	assembler  *DocumentAssembler
	renderGate chan struct{}
	uploadGate chan struct{}
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

const (
	defaultGlobalRenderConcurrency = 2
	defaultUploadConcurrency       = 4
)

// NewService builds the orchestrator. Fail-closed: all three steps are
// mandatory — a service that cannot render, upload, or assemble can never
// complete a fan-out.
func NewService(renderer *LocalizedClipRenderer, uploader *DrivePublisher, assembler *DocumentAssembler) (*Service, error) {
	return NewServiceWithConcurrency(renderer, uploader, assembler, defaultGlobalRenderConcurrency, defaultUploadConcurrency)
}

// NewServiceWithConcurrency builds the orchestrator with separate bounded
// resource pools. Render slots cover only the renderer; upload slots cover
// only Drive I/O.
func NewServiceWithConcurrency(renderer *LocalizedClipRenderer, uploader *DrivePublisher, assembler *DocumentAssembler, renderConcurrency, uploadConcurrency int) (*Service, error) {
	if renderer == nil {
		return nil, fmt.Errorf("localization.NewService: renderer is required")
	}
	if uploader == nil {
		return nil, fmt.Errorf("localization.NewService: drive publisher is required")
	}
	if assembler == nil {
		return nil, fmt.Errorf("localization.NewService: document assembler is required")
	}
	if renderConcurrency < 1 {
		renderConcurrency = defaultGlobalRenderConcurrency
	}
	if uploadConcurrency < 1 {
		uploadConcurrency = defaultUploadConcurrency
	}
	return &Service{
		renderer: renderer, uploader: uploader, assembler: assembler,
		renderGate: make(chan struct{}, renderConcurrency),
		uploadGate: make(chan struct{}, uploadConcurrency),
	}, nil
}

// Localize runs the full fan-out: render + upload each plan with bounded
// concurrency, project the UPLOADED artifacts into document entries, and
// assemble the manifest doc. Per-language failures are recorded (never fatal
// to the others); the doc assembly failure is returned as an error while the
// ordered entries survive on the ref.
func (s *Service) Localize(ctx context.Context, in LocalizeInput) (*LocalizeResult, error) {
	if s == nil || s.renderer == nil || s.uploader == nil || s.assembler == nil {
		return nil, fmt.Errorf("localization: service is not initialized")
	}
	if len(in.Plans) == 0 {
		return nil, fmt.Errorf("localization: localize: plans is required")
	}

	concurrency := in.Concurrency
	if concurrency < 1 {
		concurrency = DefaultRenderConcurrency
	}

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
	for i, plan := range in.Plans {
		idx := i
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
				recordFailure(LocalizedClipArtifact{Status: LocalizedClipFailed}, err)
				return
			}
			releaseGlobal, err := kernobs.AcquireSlot(ctx, s.renderGate, kernobs.ComponentRenderQueue, kernobs.WaitSemaphore)
			if err != nil {
				releaseLocal()
				recordFailure(LocalizedClipArtifact{Status: LocalizedClipFailed}, err)
				return
			}
			renderStart := time.Now().UTC()
			rendered, renderErr := s.renderer.Render(ctx, plan)
			renderEnd := time.Now().UTC()
			kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseFFmpeg, renderStart, renderEnd, kernobs.StageStatusCompleted, renderErr)
			releaseGlobal()
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
			published, publishErr := s.UploadRendered(ctx, rendered, in.FolderID)
			recordFailure(published, publishErr)
		}()
	}
	pipelineWG.Wait()

	artifacts := make([]LocalizedClipArtifact, 0, len(results))
	entries := make([]LocalizedDocumentEntry, 0, len(results))
	failures := make([]TaskResult, 0)
	for i, r := range results {
		if r.Err != nil {
			failures = append(failures, r)
			continue
		}
		artifacts = append(artifacts, r.Artifact)
		// TextTrackID is the plan's subtitle-track reference — the artifact
		// carries the rendered bytes, not the text-track identity.
		entries = append(entries, LocalizedDocumentEntry{
			SceneID:      r.Artifact.SceneID,
			ClipID:       r.Artifact.ClipID,
			Language:     r.Artifact.Language,
			Priority:     r.Priority,
			TextTrackID:  in.Plans[i].SubtitleTrackID,
			VideoAssetID: r.Artifact.AssetID,
			DriveFileID:  r.Artifact.DriveFileID,
			DriveLink:    r.Artifact.DriveLink,
			DurationMS:   r.Artifact.DurationMS,
			SHA256:       r.Artifact.SHA256,
		})
	}

	if in.SkipDocument {
		return &LocalizeResult{Artifacts: artifacts, Failures: failures}, nil
	}

	ref, err := s.assembler.Assemble(ctx, AssembleInput{
		Title:          in.DocTitle,
		FolderID:       in.DocFolderID,
		IdempotencyKey: in.DocIdempotencyKey,
		Force:          in.DocForce,
		Entries:        entries,
	})
	if err != nil {
		return &LocalizeResult{Ref: ref, Artifacts: artifacts, Failures: failures}, fmt.Errorf("localization: localize: assemble doc: %w", err)
	}
	return &LocalizeResult{Ref: ref, Artifacts: artifacts, Failures: failures}, nil
}

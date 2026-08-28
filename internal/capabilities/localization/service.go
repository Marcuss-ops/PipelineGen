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
	renderer  *LocalizedClipRenderer
	uploader  *DrivePublisher
	assembler *DocumentAssembler
}

// NewService builds the orchestrator. Fail-closed: all three steps are
// mandatory — a service that cannot render, upload, or assemble can never
// complete a fan-out.
func NewService(renderer *LocalizedClipRenderer, uploader *DrivePublisher, assembler *DocumentAssembler) (*Service, error) {
	if renderer == nil {
		return nil, fmt.Errorf("localization.NewService: renderer is required")
	}
	if uploader == nil {
		return nil, fmt.Errorf("localization.NewService: drive publisher is required")
	}
	if assembler == nil {
		return nil, fmt.Errorf("localization.NewService: document assembler is required")
	}
	return &Service{renderer: renderer, uploader: uploader, assembler: assembler}, nil
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

	// One render seam: render → upload. The scheduler never learns the
	// per-plan mechanics, only that each plan yields one certified artifact.
	folderID := in.FolderID
	renderFunc := func(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		renderStart := time.Now().UTC()
		rendered, err := s.renderer.Render(ctx, plan)
		renderEnd := time.Now().UTC()
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseFFmpeg, renderStart, renderEnd, kernobs.StageStatusCompleted, err)
		if err != nil {
			return rendered, err
		}
		if in.UploadSubtitleArtifact {
			rendered.SubtitleFolderID = in.SubtitleFolderID
		} else {
			rendered.SubtitlePath = ""
			rendered.SubtitleSHA256 = ""
		}
		publishStart := time.Now().UTC()
		published, err := s.uploader.Publish(ctx, rendered, folderID)
		publishEnd := time.Now().UTC()
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseDrive, publishStart, publishEnd, kernobs.StageStatusCompleted, err)
		return published, err
	}

	scheduler, err := NewScheduler(ctx, renderFunc, concurrency)
	if err != nil {
		return nil, fmt.Errorf("localization: localize: %w", err)
	}
	for _, plan := range in.Plans {
		scheduler.Submit(LocalizedClipTask{Priority: plan.Priority, Plan: plan})
	}
	results := scheduler.Wait()

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

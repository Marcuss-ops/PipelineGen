// Package app — wire_script_adapters.go.
//
// FASE 2.A PR3 (June 2026) split: the infrastructure-bridging
// adapter types + the composition-time wiring validators moved out of
// wire_script.go. The previous PR3 file (wire_script.go at pre-PR3
// 698 LOC) interleaved five responsibilities: source resolvers
// (now in wire_script_sources.go), curation adapters (now in
// wire_script_curation.go), post-processor registration (now in
// wire_script_postprocess.go), and the two responsibilities
// collected here — concrete port adapters + composition invariants.
//
//  1. driveFolderAdapterImpl + docCreatorImpl — these are the ONLY
//     adapter structs that bridge *drive.Uploader and drive.DocClient
//     into scriptapi.DriveFolderClient / scriptapi.DocumentCreator
//     ports used by the script handler. Source-resolver adapters live
//     in wire_script_sources.go (Qdrant + ClipsRepository bridges);
//     curation adapters live in wire_script_curation.go
//     (imgservice → ImageGenService bridge). Promoting *drive usage
//     to its own composition-root-local file keeps the
//     `package app` cleanly sliced: wire_script.go itself no longer
//     imports `internal/infrastructure/drive` directly (the
//     drive.DocClient usage stays here, where it has natural ownership).
//
//  2. validateScriptGenerateWiring + validateRequiredProcessors +
//     requiredProcessorNames — these are composition-time invariants
//     that gate fail-closed on missing components. Issue 7 / P1
//     (June 2026) replaced the pre-Issue-7 log.Warn with explicit
//     composition-time errors; PR 2 (June 2026) closed the
//     "partial registration" gap with the post-freeze required-names
//     check. Grouping them with the adapter types is intentional:
//     both are infrastructure-bridging concerns (adapters bridge
//     concrete services into typed ports; validators bridge
//     composition-time state into fail-closed startup semantics).
//
// Package boundary: same `package app` as wire_script.go. Promoting
// either cluster to a sub-package would force wire_script.go to
// import a new symbol while preserving the same constructor
// call-site; staying in `package app` matches the
// clips_adapters_*.go + adapters_infra.go convention already in
// use across the composition root.
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow uses
//     driveFolderAdapterImpl + docCreatorImpl in the handler-deps
//     block and invokes validateScriptGenerateWiring after job
//     registration).
//   - internal/app/wire_script_postprocess.go: registerScriptPostProcessors
//     populates the ppReg that validateRequiredProcessors scans.
//   - internal/api/script: ScriptFlowDeps.DriveFolderClient +
//     DocumentCreator (the typed-port shapes both adapters
//     implement).
//   - internal/infrastructure/drive: *drive.Uploader +
//     drive.DocClient (the concrete services the adapters wrap).
//     (the service-side collaborator used by docCreatorImpl.CreateDoc).
//   - internal/application/jobs: appjobs.Compose() (the typed
//     job-type registry queried by validateScriptGenerateWiring).
//   - internal/domain/job: job.TypeScriptGenerate (the canonical
//     job-type ID validated in step (a) of the 3-invariant check).
//   - internal/domain/script: scriptpkg.PlanInvalidError (the
//     typed error returned from validateRequiredProcessors).
//   - internal/application/scripts/adapters: PostProcessorRegistry
//   - ProcessorRequired policy classification (the validator's
//     scanning surface).
package app

import (
	"context"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
)

// driveFolderAdapterImpl wraps drive.Admin as scriptapi.DriveFolderClient.
// FASE 9 Step 3 (June 2026): migrated from *drive.Uploader to drive.Admin
// (Pattern 0 port). GetOrCreateFolder is an Admin operation; the concrete
// *drive.Uploader satisfies drive.Admin structurally.
//
// Lives in this file (composition-root-local infra bridge) per PR3 split;
// wire_script.go consumes it via the *ScriptFlowHandler ScriptFlowDeps.DriveFolderClient
// field at the bottom of the wireScriptFlow orchestrator.
type driveFolderAdapterImpl struct {
	admin drive.Admin
}

// GetOrCreateFolder implements scriptapi.DriveFolderClient. Receiver is
// pointer-nil-tolerant so a missing admin (test fixture / partial
// composition) returns ("", nil) without panicking, matching the pre-PR3
// gating contract in wireScriptFlow.
func (a *driveFolderAdapterImpl) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if a == nil || a.admin == nil {
		return "", nil
	}
	return a.admin.GetOrCreateFolder(ctx, name, parentID)
}

// docCreatorImpl wraps delivery.DocPublisher as scriptapi.DocumentCreator.
// Composition-time build of NewDocumentsService was retired (Sprint 1.0)
// (the service constructor reads the canonical Drive folder at call
// time, which lets the folder ID propagate from cfg.Drive.ScriptsGenFolder()
// without binding at struct-init time).
type docCreatorImpl struct {
	docClient     delivery.DocPublisher
	log           *zap.Logger
	driveFolderID string
}

// CreateDoc implements scriptapi.DocumentCreator. The resolveFolder
// closure is a raw-ID passthrough (caller resolves beforehand) —
// matches the pre-PR3 contract: the canonical Drive folder is derived
// from cfg.Drive.ScriptsGenFolder() at wiring time and forwarded
// absolute. Receiver-nil-tolerance mirrors driveFolderAdapterImpl.
func (d *docCreatorImpl) CreateDoc(ctx context.Context, title, content, folderID string) (string, string) {
	if d == nil || d.docClient == nil {
		return "", ""
	}
	docsSvc := usecase.NewDocumentsService(d.docClient, d.log, d.driveFolderID)
	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil // raw ID assumed (caller resolved beforehand)
	}
	return docsSvc.CreateDoc(ctx, title, content, resolveFolder, folderID, "", false)
}

// ── Composition validation: script.generate wiring must be complete ───

// validateScriptGenerateWiring enforces the 3 canonical invariants
// for `script.generate` to be considered ready for production
// traffic. Issues 7 / P1 (June 2026): the pre-Issue-7 wireScriptFlow
// only log.Warn'd on missing broker / registration failure, which
// silently let the server come up without a working
// script.generate handler. Composition must fail closed so the
// operator sees a clear restart-required message instead of a
// runtime regression.
//
// The 3 invariants:
//
//	(a) Registry has the type. Looks up appjobs.Compose().IsRegistered
//	    for script.generate -- the canonical job-type registry built
//	    in module_media.go::BuildJobsBundle.
//
//	(b) Broker has the handler. The handler-registration itself is
//	    the proof: RegisterJobs just successfully pushed the handler
//	    into the broker. A nil Jobs service at this point means the
//	    gate at line ~N (above) should have already tripped -- the
//	    explicit re-check here is defense in depth.
//
//	(c) At least one worker in the cluster is configured to claim
//	    script.generate jobs. The cluster may advertise the
//	    worker-types list via root.Jobs.WorkerTypes (forward-looking
//	    field; nil-tolerant while clusters in-flight don't expose
//	    it). When the list is exposed and script.generate is missing,
//	    the validator surfaces it; when the list is nil (legacy /
//	    cluster not yet exposing WorkerTypes), the check is skipped
//	    and operators must rely on the canonical worker.ExportTypes
//	    audit at runtime.
//
// Returns the FIRST failing invariant as a typed wireScriptFlow
// error so the composition root can wrap it consistently with the
// other composition validators (validateRequiredProcessors,
// etc.). Tests pin the fail-fast contract in
// internal/application/scripts/jobs/generation_job_test.go.
func validateScriptGenerateWiring(root *ComposeRoot, log *zap.Logger) error {
	// (a) Registry has the type. Direct query against the canonical
	//     composition-time registry. The registry is frozen after
	//     Compose(); this query is branch-free.
	reg := appjobs.Compose()
	if !reg.IsRegistered(scriptpkg.TypeGenerate) {
		return fmt.Errorf("script.generate wiring (a): registry has no entry for %s; rebuild appjobs.Compose()", scriptpkg.TypeGenerate)
	}

	// (b) Broker has the handler. The RegisterJobs success above is
	//     the primary proof; this explicit re-check via the canonical
	//     broker query Service.HasHandler is the defence-in-depth
	//     invariant for the composition root. If a future refactor
	//     decouples RegisterJobs from the call site (or reorders the
	//     two calls), this check still surfaces the "no handler for
	//     script.generate" regression.
	if root == nil || root.Jobs == nil || root.Jobs.Service == nil {
		return fmt.Errorf("script.generate wiring (b): Jobs service is nil; the gate above should have tripped")
	}
	if !root.Jobs.Service.HasHandler(scriptpkg.TypeGenerate) {
		return fmt.Errorf("script.generate wiring (b): broker has no handler for %s; RegisterJobs call above should have registered it", scriptpkg.TypeGenerate)
	}

	// (c) At least one worker in the cluster is configured to claim
	//     script.generate. Forward-looking: when JobsBundle
	//     exposes a WorkerTypes field, uncomment the check below.
	//     Until then, the operator must rely on Worker.ExportTypes
	//     runtime audit.
	if log != nil {
		log.Info("validateScriptGenerateWiring: WorkerTypes not exposed yet; (c) check skipped (forward-looking)",
			zap.String("job_type", scriptpkg.TypeGenerate))
	}
	if log != nil {
		log.Info("validateScriptGenerateWiring: script.generate wiring complete",
			zap.String("job_type", scriptpkg.TypeGenerate))
	}
	return nil
}

// ── Postprocessor clip-search adapter structs (composition-root-local) ──

// artlistClipSearchAdapter wraps usecase.SearchArtlistClips into the
// adapters.ArtlistClipSearcher port. This adapter lives in the
// composition root (NOT in the adapters package) to avoid a circular
// import: adapters cannot import usecase.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this is the canonical
// SOLE adapter between ArtlistClipSearcher and SearchArtlistClips.
type artlistClipSearchAdapter struct {
	svc usecase.ClipServices
}

// SearchClips satisfies adapters.ArtlistClipSearcher.
func (a *artlistClipSearchAdapter) SearchClips(ctx context.Context, title string, phrases []string) []adapters.ArtlistClipMatch {
	if a == nil {
		return nil
	}
	suggestions := usecase.SearchArtlistClips(ctx, a.svc, title, phrases)
	if len(suggestions) == 0 {
		return nil
	}

	// Convert usecase.ScriptArtlistClipSuggestion → adapters.ArtlistClipMatch.
	// adapters cannot import usecase types directly, so we convert at
	// the composition-root boundary.
	matches := make([]adapters.ArtlistClipMatch, 0, len(suggestions))
	for _, s := range suggestions {
		m := adapters.ArtlistClipMatch{
			Phrase:           s.Phrase,
			FolderLink:       s.FolderLink,
			FolderName:       s.FolderName,
			FolderID:         s.FolderID,
			TranslationError: s.TranslationError,
		}
		for _, c := range s.Clips {
			m.ClipNames = append(m.ClipNames, c.Name)
			m.ClipDriveLinks = append(m.ClipDriveLinks, c.DriveLink)
		}
		matches = append(matches, m)
	}
	return matches
}

var _ adapters.ArtlistClipSearcher = (*artlistClipSearchAdapter)(nil)

// ── ClipServices adapter structs (composition-root-local) ─────────────────

// driveCheckServiceAdapter wraps drive.Uploader.FileIsNotTrashed into
// usecase.DriveCheckService. This adapter lives in the composition
// root (NOT in the adapters or usecase packages) to avoid import cycles.
//
// godlike/06 SSOT: this is the canonical SOLE adapter between the
// drive.Uploader and the usecase.DriveCheckService port.
type driveCheckServiceAdapter struct {
	up interface {
		FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
	}
}

// FileIsNotTrashed satisfies usecase.DriveCheckService.
func (a *driveCheckServiceAdapter) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	if a == nil || a.up == nil {
		return false, fmt.Errorf("driveCheckServiceAdapter: drive uploader not wired")
	}
	return a.up.FileIsNotTrashed(ctx, fileID)
}

var _ usecase.DriveCheckService = (*driveCheckServiceAdapter)(nil)

// jobsEnqueueServiceAdapter wraps appjobs.Service.Enqueue into
// usecase.JobEnqueueService. This adapter lives in the composition
// root (NOT in the adapters or usecase packages) to avoid import cycles.
//
// The adapter bridges the typed appjobs.Service.Enqueue(ctx,
// *job.EnqueueRequest) (*job.Job, error) to the interface-based
// usecase.JobEnqueueService.Enqueue(ctx, any) (any, error)
// expected by the artlist background job enqueue path.
//
// godlike/06 SSOT: this is the canonical SOLE adapter between the
// jobs.Service and the usecase.JobEnqueueService port.
type jobsEnqueueServiceAdapter struct {
	svc interface {
		Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
	}
}

// Enqueue satisfies usecase.JobEnqueueService.
func (a *jobsEnqueueServiceAdapter) Enqueue(ctx context.Context, req any) (any, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("jobsEnqueueServiceAdapter: jobs service not wired")
	}
	typedReq, ok := req.(*job.EnqueueRequest)
	if !ok {
		return nil, fmt.Errorf("jobsEnqueueServiceAdapter: req is %T, want *job.EnqueueRequest", req)
	}
	return a.svc.Enqueue(ctx, typedReq)
}

var _ usecase.JobEnqueueService = (*jobsEnqueueServiceAdapter)(nil)

// ── Composition validation: required processors MUST register ────────

// requiredProcessorNames is the canonical list of postprocessor names
// that MUST be registered for a script pipeline to be considered
// production-ready. Composition aborts if any name below is missing.
//
// PR 2 (June 2026): the list mirrors the static ProcessorRequired
// classification declared by each concrete processor. Persistence
// is the single owner of script-table writes (PR 5); Document is
// the canonical doc-creation deliverable. Images / Voiceover /
// Entities / Metadata are ProcessorBestEffort (spec: "configurabile"
// or "best_effort or required based on payload") and not part of
// this list — if they are present at runtime, Run warns; if they
// are absent at runtime, Run warns. Either way, composition does
// NOT fail on them.
//
// PR 3 (June 2026): Entities and Metadata are promoted to
// ProcessorRequired per the user spec. The canonical Composition-time
// validator fails closed if they are not registered; the runtime
// preflight fails closed if a plan requests them and the registry
// has no adapter.
//
// Fase 2 Spina Dorsale (July 2026): "document" removed from
// requiredProcessorNames. Document generation is now a downstream
// job (document.generate), not an inline postprocessor. The
// document processor is no longer registered in the script pipeline.
var requiredProcessorNames = []adapters.ProcessorName{
	adapters.ProcessorPersistence,
	adapters.ProcessorEntities,
	adapters.ProcessorMetadata,
}

// validateRequiredProcessors checks the post-freeze registry for
// every required processor name. Composition fails-closed: if any
// required name is missing, returns a typed error so the operator
// sees a clear restart-required message instead of silent runtime
// panics on the first plan that requested the missing processor.
//
// Returns a *scriptpkg.PlanInvalidError when one or more required
// processors are missing from the registry. Caller is the
// composition root, which wraps this with a context string.
//
// PR 2 (June 2026): gate that closes the "non-canonical WriteScript
// to dragnet" gap left by the previous partial-registration pattern
// (where composition would silently skip a Register call when the
// underlying dep was nil, then runtime would silently skip the
// postprocessor — leaving the script row unwritten).
func validateRequiredProcessors(ppReg *adapters.PostProcessorRegistry, required []adapters.ProcessorName) *scriptpkg.PlanInvalidError {
	if ppReg == nil {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry is nil"},
		}
	}
	if !ppReg.IsFrozen() {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry must be frozen before required-processors validation"},
		}
	}
	var missing []string
	for _, name := range required {
		if !ppReg.Registered(name) {
			missing = append(missing, string(name))
		} else if ppReg.LookupPolicy(name) != adapters.ProcessorRequired {
			// Defensive: composition-side invariant. A name in the
			// required list MUST have the ProcessorRequired
			// classification. If a future PR flips a processor's
			// policy to BestEffort, this check surfaces the
			// dependency drift loudly — the operator MUST update
			// requiredProcessorNames to match.
			missing = append(missing, string(name)+" (registered with non-required policy)")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &scriptpkg.PlanInvalidError{
		ItemID:  "wireScriptFlow",
		Details: []string{"preflight: required postprocessor(s) not registered at composition: " + strings.Join(missing, ", ")},
	}
}

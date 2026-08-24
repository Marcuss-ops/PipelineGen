// Package stockpipeline — orchestrator_step_errors.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of the §12-7 typed sentinel errors surfaced by the 6
// step implementations per godlike/06 SSOT (one canonical owner
// per fact). Each sentinel names the rule it enforces. Callers
// MUST use errors.Is(err, ErrStock*) to inspect the failure class.
//
// godlike/07 typed-error contract: every sentinel is a typed
// errors.New(...). The wrapping chain in each Step.Run is "%w: %v"
// so the typed sentinel propagates verbatim through orchestrator
// + composition-root error forwarding. Callers can errors.Is into
// deeper sentinels like ErrConcurrentLeaseRefutation or
// asset.ErrSHA256Invalid via this propagation.
//
// PR-STOCK-ORCHESTRATOR-SPLIT extracted these from
// orchestrator_steps.go on 2026-07-04. The pre-split file was
// 874 LoC (the user spec referenced 949 LoC; the spec's "slim
// RunResilient ladder ~140 LoC" sub-file would have been empty
// per godlike/07 no-fake-availability — RunResilient lives in
// orchestrator.go today, not in orchestrator_steps.go; the 7
// step file names in the spec implied splitting StockFinalizeStep
// into 3+ sub-step files which is an aggressive split of a single
// Step type rather than the natural 1-file-per-Step unit; the
// minimum-ripple 1-file-per-Step split (6 step files + sentinels
// + slimmed orchestrator_steps.go = 8 files) is the canonical
// interpretation; see the commit body for the full honest scope
// disclosure).
package assets

import "errors"

// §12-7 sentinel errors (godlike/07 typed-error contract).
//
// Each sentinel names the rule it enforces. Callers MUST use
// errors.Is(err, ErrStock*) to inspect the failure class. Wraps
// preserve underlying typed errors via fmt.Errorf("%w: %v", ...)
// so dashboards can errors.Is into deeper sentinels like
// ErrConcurrentLeaseRefutation or asset.ErrSHA256Invalid.
var (
	// ErrStockPublishArtifactFailed is raised when ArtifactPreparation.Prepare
	// returns non-nil for any chunk OR for the per-run metadata.json.
	// The wrapped error is the underlying publisher fault.
	ErrStockPublishArtifactFailed = errors.New("stock.publish: ArtifactPreparation failed")

	// ErrStockFinalizeSpineFailed is raised when JobFinalizer.CompleteWithArtifacts
	// returns non-nil. The wrapped error carries the underlying
	// finalizer typed sentinel (ErrConcurrentLeaseRefutation,
	// ErrRemoteArtifactHashMismatch, ErrCompleteJobRequestMissingFields,
	// etc.) via errors.Is / errors.As.
	ErrStockFinalizeSpineFailed = errors.New("stock.finalize: JobFinalizer spine write failed")

	// ErrStockFinalizeLeaseMissing is raised when runner.Cfg().Lease
	// has empty JobID/WorkerID/LeaseID — HandleJob must thread
	// extractLease(job) into cfg.Lease before RunResilient. This
	// sentinel surfaces composition-time wiring gaps loudly.
	ErrStockFinalizeLeaseMissing = errors.New("stock.finalize: cfg.Lease empty (HandleJob must call extractLease)")

	// ErrStockFnRequired surfaces RunFingerprint() == "" — invokes
	// the canonical godlike/07 "every deployment-fingerprint-derived
	// ID must be non-empty" gate. Composition-time wiring gap.
	ErrStockFnRequired = errors.New("stock.finalize: run fingerprint empty (policyVersion / inputs missing)")

	// ErrStockStageSourcesAllFailed is raised when StockStageSourcesStep.Run
	// was wired with a non-nil SourceStager AND had non-empty plans AND
	// every source in the plan failed to stage (zero *assets.StagedAsset
	// appended to the staged slice). This closes the godlike/07
	// no-fake-availability class where a job could report SUCCEEDED
	// with zero staged assets on Drive. The per-source graceful
	// degradation (Warn + continue on err/nil) is preserved so partial
	// successes still produce partial artifacts — only the all-failed
	// case surfaces this sentinel. PR-STOCK-FAKE-AVAILABILITY-REMOVAL
	// (Wave 1 P0 #2, deadline 2026-07-15).
	ErrStockStageSourcesAllFailed = errors.New("stock.stage_sources: all sources failed to stage") // ErrStockExtractClipsCutterRequired is raised when StockExtractClipsStep.Run
	// ErrStockStageSourcesIncomplete is raised when at least one source
	// stages successfully but one or more planned sources fail. A stock
	// batch that requested multiple sources must not report SUCCEEDED with
	// silently missing videos.
	ErrStockStageSourcesIncomplete = errors.New("stock.stage_sources: one or more sources failed to stage")
	// detects a nil VideoCutter AND non-empty plans — the step has work to do
	// but no cutter to do it with. This closes the godlike/07 no-fake-availability
	// class where a nil cutter silently produced CutPaths=nil, cascading through
	// compose→publish→finalize into an empty manifest.
	//
	// Test-fixture compat: when plans is empty (no work to do), the nil-cutter
	// path still returns nil (zero-clips → zero-cut-paths is the correct outcome).
	// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (Wave 1 P0 #2, follow-up July 2026).
	ErrStockExtractClipsCutterRequired = errors.New("stock.extract_clips: VideoCutter nil but plans non-empty — cutter must be wired for production runs")

	// ErrStockExtractClipsLocalFSRequired is raised when executeCuts
	// finds a nil LocalFSPort — the step needs a filesystem to create
	// the persistent workspace directory for cut outputs. This closes
	// the godlike/07 no-fake-availability class where a nil LocalFS
	// panic'd with nil-pointer dereference instead of surfacing a
	// typed error. PR-STOCK-NOOPFS-REMOVAL (P0.1, July 2026).
	ErrStockExtractClipsLocalFSRequired = errors.New("stock.extract_clips: LocalFSPort nil — filesystem must be wired for production runs")

	// ErrStockExtractClipsDurableStateFailed is raised when the extract
	// step cannot persist its batch/group/artifact lifecycle state. A
	// successful extract step without these writes would allow the
	// orchestrator to mark the step completed while durable state still
	// says planned, extracting, or otherwise incomplete.
	ErrStockExtractClipsDurableStateFailed = errors.New("stock.extract_clips: durable lifecycle state write failed")

	// ErrStockComposeChunksAllFailed is raised when StockComposeChunksStep.Run
	// was wired with a non-nil Renderer AND had non-empty CutPaths AND
	// every chunk failed to render (zero string paths appended to the
	// composed slice). Mirrors ErrStockStageSourcesAllFailed — the
	// godlike/07 no-fake-availability class for the compose step.
	// The per-chunk graceful degradation (Warn + continue on err) is
	// preserved so partial successes still produce partial artifacts.
	// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (Wave 1 P0 #2).
	ErrStockComposeChunksAllFailed = errors.New("stock.compose_chunks: all chunks failed to render")

	// ErrStockPublishStateLost is raised when StockPublishStep.Run
	// is wired with a non-nil ArtifactPreparation BUT state.ComposedPaths
	// is empty — meaning the upstream compose step produced zero composed
	// chunks (or the RunState was lost on resume). This closes the
	// godlike/07 no-fake-availability class where a production run
	// (ArtifactPreparation wired) silently declared SUCCEEDED with zero
	// uploaded chunks because the lenient "len(chunks) == 0 → return nil"
	// guard masked a real state-loss bug. The guard is bypassed ONLY in
	// test-fixture mode (ArtifactPreparation nil) so existing fixture tests
	// remain green. PR-STOCK-RESUME-STATE-LOSS (July 2026).
	ErrStockPublishStateLost = errors.New("stock.publish: ArtifactPreparation wired but ComposedPaths empty — upstream state lost (likely resume-after-crash)")

	// ErrStockFinalizeStateLost is raised when StockFinalizeStep.Run
	// is wired with a non-nil JobFinalizer BUT state.Published is empty —
	// meaning the upstream publish step did not upload any chunks (or the
	// RunState was lost on resume). This closes the godlike/07
	// no-fake-availability class where a production run (JobFinalizer
	// wired) silently declared SUCCEEDED without writing to media_assets
	// because the lenient "len(Published) == 0 → return nil" guard masked
	// a real state-loss bug. The guard is bypassed ONLY in test-fixture
	// mode (JobFinalizer nil) so existing fixture tests remain green.
	// PR-STOCK-RESUME-STATE-LOSS (July 2026).
	ErrStockFinalizeStateLost = errors.New("stock.finalize: JobFinalizer wired but Published empty — upstream state lost (likely resume-after-crash)")

	// ErrFinalizerAbsent is raised when StockFinalizeStep.Run reaches
	// Phase 3+4 without a JobFinalizer wired, closing the silent-success
	// trap where the step previously returned nil ("test-fixture mode")
	// even on production wiring gaps. The production symmetric gate
	// (validateStockSymmetricGate in build_bundles_stock.go) is the
	// upstream enforcement of this invariant at boot time; this
	// sentinel is the in-step body fail-closed (godlike/07
	// no-fake-availability). Test fixtures MUST wire a stubJobFinalizer
	// to satisfy the StepRunner interface contract — there is no
	// longer a "test-fixture mode" silent-success skip path.
	// PR-STOCK-FINALIZER-ABSENT-FAILCLOSED (July 2026).
	ErrFinalizerAbsent = errors.New("stock.finalize: JobFinalizer absent")

	// ErrStockResumeStateReadFailed is raised when the canonical checkpoint
	// store cannot be read, or when a completed step has no corresponding
	// readable state snapshot. Resume must stop rather than continue with
	// an accumulator that cannot be proven canonical.
	ErrStockResumeStateReadFailed = errors.New("stock: resume: canonical checkpoint state unreadable")

	// ErrStockResumeStateInvalid is raised when the orchestrator
	// cannot rehydrate the RunState snapshot from a pre-completed
	// step's result_json, or when it cannot marshal the current
	// RunState to persist a checkpoint. This closes the silent-
	// corruption class where a crash-resume proceeds with a malformed
	// accumulator or where a checkpoint is silently dropped.
	ErrStockResumeStateInvalid = errors.New("stock: resume: RunState checkpoint invalid")

	// ErrStockManifestUnprojectable is raised when the resilient
	// orchestrator produced a manifest that cannot be projected into
	// the legacy *PipelineResult: nil manifest, zero artifacts, or
	// artifacts of which none are projectable (no video chunk and no
	// metadata artifact). This closes the godlike/07 no-fake-
	// availability class where a SUCCEEDED job silently returned
	// total_clips=0/total_chunks=0/chunks=[] even though the pipeline
	// had uploaded real artifacts. Callers MUST fail the run instead
	// of surfacing an all-zeros result.
	ErrStockManifestUnprojectable = errors.New("stock.result: manifest not projectable — no video or metadata artifacts to hydrate")
)

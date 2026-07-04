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
package stockpipeline

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
	ErrStockStageSourcesAllFailed = errors.New("stock.stage_sources: all sources failed to stage")

	// ErrStockComposeChunksAllFailed is raised when StockComposeChunksStep.Run
	// was wired with a non-nil Renderer AND had non-empty CutPaths AND
	// every chunk failed to render (zero string paths appended to the
	// composed slice). Mirrors ErrStockStageSourcesAllFailed — the
	// godlike/07 no-fake-availability class for the compose step.
	// The per-chunk graceful degradation (Warn + continue on err) is
	// preserved so partial successes still produce partial artifacts.
	// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (Wave 1 P0 #2).
	ErrStockComposeChunksAllFailed = errors.New("stock.compose_chunks: all chunks failed to render")
)

package artlist

import (
	"errors"

	"go.uber.org/zap"
)

// gateBlockStatus is the per-item audit Status value stamped when an
// Artlist downloader gate rejects a request. Canonical enum (one entry
// per typed-error Commit of the Fase 6 / Authorization Gate series):
//
//	gateBlockNone                       not a gate-block (continue to mediaProcessor)
//	gateBlockMode                       ErrAcquisitionModeBlocked (Commit 1, July 2026)
//	gateBlockDailyLimit                 ErrDailyLimitExhausted    (Commit 2, July 2026)
//	gateBlockUnauthorized               ErrAccountUnauthorized    (Commit 3, July 2026)
//	gateBlockSessionExpired             ErrSessionExpired         (Commit 4, July 2026)
//
// godlike/06 SSOT (one canonical owner of the mapping): this file is
// the SINGLE canonical surface that maps a typed error to a per-item
// audit Status string. Adding a new typed gate-block error MUST land
// here in the SAME Commit that adds the sentinel — the regression test
// in gate_block_classifier_test.go pins the (error, status) mapping
// verbatim so a future refactor cannot silently lose a status entry.
//
// godlike/07 fail-closed: classifyGateBlock returns gateBlockNone for
// any error that ISN'T a known gate-block sentinel. Callers (stagePro
// cessBatch) MUST treat gateBlockNone as "continue to mediaProcessor"
// — silent skip is forbidden in this direction too. The intent of an
// unsupported error type is preserved (the operator still sees it in
// per-item Error), only the explicit typed-block short-circuit is
// routed through this helper.
type gateBlockStatus string

const (
	gateBlockNone           gateBlockStatus = ""
	gateBlockMode           gateBlockStatus = "blocked_mode"
	gateBlockDailyLimit     gateBlockStatus = "blocked_daily_limit"
	gateBlockUnauthorized   gateBlockStatus = "blocked_unauthorized"
	gateBlockSessionExpired gateBlockStatus = "blocked_session_expired"
)

// asRunTagStatus returns the canonical RunTagItem.Status value for
// each gateBlockStatus. The mapping MUST stay verbatim so the wire
// payload (JSON-stamped per-item) is grep-able across the gateway /
// orchestration / audit layers.
func (g gateBlockStatus) asRunTagStatus() string {
	switch g {
	case gateBlockMode:
		return "blocked_mode"
	case gateBlockDailyLimit:
		return "blocked_daily_limit"
	case gateBlockUnauthorized:
		return "blocked_unauthorized"
	case gateBlockSessionExpired:
		return "blocked_session_expired"
	default:
		return ""
	}
}

// classifyGateBlock returns the canonical gateBlockStatus for err,
// or gateBlockNone for nil / unknown. The mapping lives in ONE place
// (this function) so adding a new typed gate-block error requires:
//
//  1. New sentinel in ports.go (ErrSomething).
//  2. New gateBlockStatus const above.
//  3. New case in classifyGateBlock.
//  4. New case in asRunTagStatus.
//  5. New regression test in gate_block_classifier_test.go.
//
// godlike/06 SSOT: 5 surfaces per new sentinel — the typed-sentinel
// chain (errors.Is) makes the mapping authoritative.
//
// Future commits (2/3/4) extend this function. Commit 1 only wires
// ErrAcquisitionModeBlocked — the other 3 entries are reserved so a
// future regression test can pin their addition in lockstep with the
// sentinel declaration.
func classifyGateBlock(err error) gateBlockStatus {
	switch {
	case err == nil:
		return gateBlockNone
	case errors.Is(err, ErrAcquisitionModeBlocked):
		return gateBlockMode
	// TODO(Fase 6 / Commit 2): errors.Is(err, ErrDailyLimitExhausted) → gateBlockDailyLimit
	// TODO(Fase 6 / Commit 3): errors.Is(err, ErrAccountUnauthorized) → gateBlockUnauthorized
	// TODO(Fase 6 / Commit 4): errors.Is(err, ErrSessionExpired)      → gateBlockSessionExpired
	default:
		return gateBlockNone
	}
}

// gateBlockShortCircuit is the per-item audit plumbing helper called
// from stageProcessBatch after the SourceStager.StageSource call
// returns an error. When the error classifies as a typed gate-block,
// the helper stamps the per-item audit (Status + Error verbatim) +
// bumps resp.Failed so EvaluateRunState verdicts PARTIAL_SUCCESS /
// FAILED honestly. godlike/07 fail-closed: silent-skip is forbidden —
// any typed gate-block MUST reach this helper, even when callers
// already attempted to log-and-continue in the stager path.
//
// Returns the canonical RunTagItem.Status string; empty string means
// "no short-circuit (the caller should continue the normal flow)".
// The caller decides what to do with non-empty Status: in the
// orchestrator that means stamp the item + bump resp.Failed + append
// to resp.Items; in a diagnostic surface it means surface as part of
// the operator-visible state.
//
// mirror parameter is the source of the count bump (resp-like object)
// for unit-testability. callers MUST pass a non-nil mirror.
func gateBlockShortCircuit(item *RunTagItem, err error, mirror gateBlockCounter, log *zap.Logger, failFn func(stage, msg string) error) string {
	if item == nil || err == nil {
		return ""
	}
	gb := classifyGateBlock(err)
	if gb == gateBlockNone {
		return ""
	}
	status := gb.asRunTagStatus()
	item.Status = status
	item.Error = err.Error()
	if mirror != nil {
		mirror.bumpGateBlock()
	}
	if failFn != nil {
		if failErr := failFn("download", status+": "+err.Error()); failErr != nil && log != nil {
			log.Warn("asset_processing.Fail failed", zap.String("clip_id", item.ClipID), zap.Error(failErr))
		}
	}
	return status
}

// gateBlockCounter is the minimal interface the short-circuit needs
// to bump the resp.Failed tally. Returning the surface as an interface
// (rather than a *RunTagResponse pointer) lets the helper be unit-
// tested against an in-memory counter, keeping the regression test
// hermetic.
type gateBlockCounter interface {
	bumpGateBlock()
}

// runRespGateBlockCounter adapts *RunTagResponse to gateBlockCounter.
// The run-state aggregate `resp.Failed` is the canonical tally the
// /api/artlist/runs/:id + diagnostic endpoints read, so bumping it
// here is the single canonical place the typed block surfaces in the
// run-state machine (EvaluateRunState Rule 5 PARTIAL_SUCCESS / Rule 3
// FAILED).
type runRespGateBlockCounter struct {
	resp *RunTagResponse
}

var _ gateBlockCounter = (*runRespGateBlockCounter)(nil)

func (r *runRespGateBlockCounter) bumpGateBlock() {
	if r == nil || r.resp == nil {
		return
	}
	r.resp.Failed++
}

// newGateBlockCounterFor returns the canonical counter adapter wired
// to resp. Used by stageProcessBatch once per pipelineState. The
// pointer indirection lets the helper be called from inside the
// per-clip SafeGoFunc without re-creating an adapter per call.
func newGateBlockCounterFor(resp *RunTagResponse) gateBlockCounter {
	if resp == nil {
		return nil
	}
	return &runRespGateBlockCounter{resp: resp}
}

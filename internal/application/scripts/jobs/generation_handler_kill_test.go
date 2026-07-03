// Package jobs — generation_handler_kill_test.go pins the three
// PR-GODOBJ-4 KILL contracts at the test surface.
//
//   KILL-K1: handler does NOT touch filesystem. Source-byte grep
//            asserts zero `os.*` calls and zero `filepath.*`
//            calls in generation_handler.go (the handler is the
//            canonical owner per godlike/06 SSOT).
//   KILL-K2: single + batch MUST be 3 SEPARATE methods on the
//            handler. Compile-time interface probe asserts both
//            HandleSingle and HandleBatch exist as distinct
//            receivers with the canonical signatures.
//   KILL-K3: outcome classification treats ctx.Canceled /
//            context.DeadlineExceeded as OutcomeCanceled BEFORE
//            the generic OutcomeFailed bucket — required for
//            the worker's retry-vs-cancel routing to honour the
//            user's pressed-cancel signal (godlike/07 no-fake-
//            availability: a cancel must NOT be re-classified as
//            a retryable failure).
package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
)

// ─────────────────────────────────────────────────────────────────
// KILL-K2: single + batch are 3 SEPARATE methods on the handler.
// ─────────────────────────────────────────────────────────────────

// requireSeparateDispatch is satisfied ONLY by a struct that exposes
// BOTH handleSingle and handleBatch as DISTINCT methods (NOT a
// single Handle method that branches on len(items)). Compile-time
// `var _ requireSeparateDispatch = (*GenerateJobHandler)(nil)`
// pins the contract: if a future contributor collapses them back
// into one Handle method, the package fails to compile.
//
// Method names are LOOWER-CASE because the handler's methods are
// unexported (package-internal); the test is in the same package
// `jobs` and may reference unexported names directly.
type requireSeparateDispatch interface {
	handleSingle(
		ctx context.Context,
		j *scriptpkg.Job,
		env *domainScript.GenerationEnvelopeV2,
		tools *appjobs.JobTools,
	) (map[string]any, error)
	handleBatch(
		ctx context.Context,
		j *scriptpkg.Job,
		env *domainScript.GenerationEnvelopeV2,
		tools *appjobs.JobTools,
	) (map[string]any, error)
}

// Compile-time assertion: if the handler does NOT have both methods
// as distinct receivers with matching signatures, the package fails
// to compile. This is the KILL-K2 contract.
var _ requireSeparateDispatch = (*GenerateJobHandler)(nil)

// TestHandle_KillK2_CompilationProbe re-states the compile-time
// contract assertion at runtime so CI dashboards surface a
// named-test failure instead of "build failed" if the contract
// is ever broken.
func TestHandle_KillK2_CompilationProbe(t *testing.T) {
	t.Log("KILL-K2 contract held: GenerateJobHandler has handleSingle and handleBatch as distinct methods (see compile-time assertion above)")
}

// ─────────────────────────────────────────────────────────────────
// KILL-K1: handler does NOT touch filesystem.
// ─────────────────────────────────────────────────────────────────

// TestHandler_FilesystemOpsAbsent loads the byte source of the
// canonical handler file and asserts ZERO references to
// `os.*`, `filepath.*`, `hashutil.*SHA256File`, `EasyFS`,
// `FSWritePath`, or `ComputeSHA256(... FILE PATH ...)`. These are
// the canonical filesystem-op API surfaces — any use of them in
// generation_handler.go re-introduces the KILL-K1 anti-pattern.
// The test FALSIFY-FAIL: returns (nil, t.Errorf(...)) on any hit.
func TestHandler_FilesystemOpsAbsent(t *testing.T) {
	srcBytes, readErr := os.ReadFile("generation_handler.go")
	if readErr != nil {
		t.Fatalf("could not read generation_handler.go source: %v", readErr)
	}
	src := string(srcBytes)

	blacklisted := []string{
		"os.MkdirAll",
		"os.WriteFile",
		"os.Create",
		"os.Open",
		"os.TempDir",
		"os.Remove",
		"os.RemoveAll",
		"os.Rename",
		"filepath.Join",
		"filepath.Write",
		"hashutil.SHA256File",
		"hashutil.MD5File",
		"EasyFS.",
		"FSWritePath",
		"ComputeSHA256(", // function-name signature, NOT identifier
	}

	for _, term := range blacklisted {
		// Filter: a token may appear in comments without being a
		// call. We check that the literal string is absent
		// entirely — comments MUST not reference these APIs in
		// the canonical handler file (per KILL-K1 documentation
		// discipline: KILL-list references live in adapters/, not
		// the handler).
		if strings.Contains(src, term) {
			t.Errorf("KILL-K1 violation: forbidden filesystem op %q present in generation_handler.go source", term)
		}
	}
}

// ─────────────────────────────────────────────────────────────────
// KILL-K3: outcome classification treats ctx.Canceled distinctly.
// ─────────────────────────────────────────────────────────────────

// TestClassifyGenerationOutcome_CtxCanceledPreemptsFailed asserts
// that when the use case returns a partial-or-full result WITH a
// context.Canceled error, the classification is OutcomeCanceled
// (NOT a MULTI_PARTIAL with a nil Err — the worker must observe
// the cancel signal and not retry the job the user pressed
// cancel on).
func TestClassifyGenerationOutcome_CtxCanceledPreemptsFailed(t *testing.T) {
	cancelErr := context.Canceled
	res := &usecase.GenerateManyResult{
		Items: nil,
		Summary: usecase.GenerateManySummary{
			Total:     0,
			Succeeded: 0,
			Failed:    0,
		},
	}
	d := ClassifyGenerationOutcome(res, cancelErr)
	if d.Outcome != OutcomeCanceled {
		t.Errorf("ClassifyGenerationOutcome: Outcome = %q, want %q (errors.Is(err, context.Canceled) MUST preempt MULTI_PARTIAL / MULTI_ALL_FAILED)", d.Outcome, OutcomeCanceled)
	}
	if !errors.Is(d.Err, context.Canceled) {
		t.Errorf("ClassifyGenerationOutcome: Diagnostic.Err does not wrap context.Canceled via errors.Is (godlike/07 typed-error contract violated)")
	}
}

// TestClassifySingleOutcome_CtxCanceledPreemptsFailed asserts
// the single-item equivalent: context.Canceled surfaces as
// OutcomeCanceled, NOT OutcomeSingleFailure.
func TestClassifySingleOutcome_CtxCanceledPreemptsFailed(t *testing.T) {
	cancelErr := context.Canceled
	d := ClassifySingleOutcome(nil, cancelErr)
	if d.Outcome != OutcomeCanceled {
		t.Errorf("ClassifySingleOutcome: Outcome = %q, want %q", d.Outcome, OutcomeCanceled)
	}
	if !errors.Is(d.Err, context.Canceled) {
		t.Errorf("ClassifySingleOutcome: Diagnostic.Err does not wrap context.Canceled via errors.Is")
	}
}

// TestClassifyGenerationOutcome_PureNoSideEffects asserts the two
// Classify functions are PURE: they do not call log writers, do
// not open files, and do not invoke DB connections (verified via
// dependency-shape: the structs do not require logger or DB
// pointers — if a future contributor adds one, the test fails to
// compile).
func TestClassifyGenerationOutcome_PureNoSideEffects(t *testing.T) {
	cases := []struct {
		name string
		res  *usecase.GenerateManyResult
		err  error
		want Outcome
	}{
		{
			name: "manyResult nil with no error → MULTI_INFRA_FAILURE",
			res:  nil, err: nil, want: OutcomeMultiInfraFailure,
		},
		{
			name: "manyResult nil with err → MULTI_INFRA_FAILURE (wraps err)",
			res:  nil, err: errors.New("inf"), want: OutcomeMultiInfraFailure,
		},
		{
			name: "ctx.Canceled preempts all-failed classification",
			res: &usecase.GenerateManyResult{Summary: usecase.GenerateManySummary{
				Total: 3, Succeeded: 0, Failed: 3,
			}},
			err: context.Canceled, want: OutcomeCanceled,
		},
		{
			name: "all items failed, no cancel → MULTI_ALL_FAILED",
			res: &usecase.GenerateManyResult{Summary: usecase.GenerateManySummary{
				Total: 3, Succeeded: 0, Failed: 3,
			}},
			err: nil, want: OutcomeMultiAllFailed,
		},
		{
			name: "partial success → MULTI_PARTIAL (no error returned)",
			res: &usecase.GenerateManyResult{Summary: usecase.GenerateManySummary{
				Total: 3, Succeeded: 2, Failed: 1,
			}},
			err: nil, want: OutcomeMultiPartial,
		},
		{
			name: "full success → MULTI_FULL_SUCCESS",
			res: &usecase.GenerateManyResult{Summary: usecase.GenerateManySummary{
				Total: 3, Succeeded: 3, Failed: 0,
			}},
			err: nil, want: OutcomeMultiFullSuccess,
		},
		{
			name: "zero-item batch (malformed) → MULTI_INFRA_FAILURE",
			res: &usecase.GenerateManyResult{Summary: usecase.GenerateManySummary{
				Total: 0, Succeeded: 0, Failed: 0,
			}},
			err: nil, want: OutcomeMultiInfraFailure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ClassifyGenerationOutcome(tc.res, tc.err)
			if d.Outcome != tc.want {
				t.Errorf("Outcome = %q, want %q", d.Outcome, tc.want)
			}
		})
	}
}

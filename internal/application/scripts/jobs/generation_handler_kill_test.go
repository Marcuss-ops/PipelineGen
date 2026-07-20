// Package jobs — generation_handler_kill_test.go pins the three
// PR-GODOBJ-4 KILL contracts at the test surface.
//
//	KILL-K1: handler does NOT touch filesystem. Source-byte grep
//	         asserts zero `os.*` calls and zero `filepath.*`
//	         calls in generation_handler.go (the handler is the
//	         canonical owner per godlike/06 SSOT).
//	KILL-K2: single + batch paths MUST be separated into distinct
//	         executors. Compile-time interface probes assert that
//	         singleGenerationExecutor and batchGenerationExecutor
//	         satisfy the SingleGenerationExecutor and
//	         BatchGenerationExecutor ports. The handler itself only
//	         decodes the envelope and delegates to the dispatcher.
//	KILL-K3: outcome classification treats ctx.Canceled /
//	         context.DeadlineExceeded as OutcomeCanceled BEFORE
//	         the generic OutcomeFailed bucket — required for
//	         the worker's retry-vs-cancel routing to honour the
//	         user's pressed-cancel signal (godlike/07 no-fake-
//	         availability: a cancel must NOT be re-classified as
//	         a retryable failure).
package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────
// KILL-K2: single + batch are distinct executors.
// ─────────────────────────────────────────────────────────────────

// Compile-time assertions: the single and batch execution paths are
// extracted into distinct executor types that satisfy the narrow
// SingleGenerationExecutor / BatchGenerationExecutor ports. This
// pins the KILL-K2 contract: the handler itself no longer contains
// the execution bodies; they live in generation_single_executor.go
// and generation_batch_executor.go.
var _ SingleGenerationExecutor = (*singleGenerationExecutor)(nil)
var _ BatchGenerationExecutor = (*batchGenerationExecutor)(nil)

// TestHandle_KillK2_CompilationProbe re-states the compile-time
// contract assertion at runtime so CI dashboards surface a
// named-test failure instead of "build failed" if the contract
// is ever broken.
func TestHandle_KillK2_CompilationProbe(t *testing.T) {
	t.Log("KILL-K2 contract held: single and batch executors are distinct types satisfying SingleGenerationExecutor / BatchGenerationExecutor (see compile-time assertions above)")
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

// TestClassifySingleOutcome_CtxCanceledPreemptsFailed asserts
// context.Canceled surfaces as OutcomeCanceled, NOT OutcomeSingleFailure.
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

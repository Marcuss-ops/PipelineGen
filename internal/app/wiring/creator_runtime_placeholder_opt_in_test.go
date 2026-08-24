//go:build voiceover_placeholder
// +build voiceover_placeholder

// Package app — creator_runtime_placeholder_opt_in_test.go (P1-COMPL-12-PLACEHOLDER-CAPABILITY).
//
// Companion `_test.go` for the build-tag-gated affordance in
// `creator_runtime_placeholder_test_only.go`. Same build tag, so this
// file only compiles under `go test -tags voiceover_placeholder ./internal/app/...`.
//
// Purpose:
//
//  1. Exercise `registerVoiceoverGenerateItemPlaceholder` so the
//     affordance function is NOT dead code (AGENTS.md Code Hygiene
//     discipline).
//
//  2. Pin the godlike/07 typed-error contract via `errors.Is` — when
//     the placeholder handler is dispatched against a
//     `voiceover.generate_item` job, the returned error chain MUST
//     unwrap to `ErrVoiceoverNotImplementedOnCreator`.
//
//  3. Verify the compile-time `appjobs.HandlerFunc` cast on the
//     placeholder handler survives (Pattern 0 — typed-port drift
//     detection at compile time).
//
// Default `go test ./...` runs do NOT compile this file; opt in via
// `-tags voiceover_placeholder` to exercise the affordance.

package wiring

import (
	"context"
	"errors"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestRegisterVoiceoverGenerateItemPlaceholder_BindsHandler exercises
// the opt-in affordance helper. It registers the placeholder handler
// to a fresh dispatcher, looks up the bound handler via the canonical
// `AllHandlers()` map (the appjobs.Dispatcher surface; Note: the
// dispatcher does NOT expose a `HasHandler` method — the canonical
// surface is `AllHandlers()` map lookup), and dispatches the handler
// directly to confirm the godlike/07 typed-error sentinel is unwrapped
// correctly by the errors.Is probe.
func TestRegisterVoiceoverGenerateItemPlaceholder_BindsHandler(t *testing.T) {
	dispatcher := appjobs.NewDispatcher()

	if err := registerVoiceoverGenerateItemPlaceholder(dispatcher); err != nil {
		t.Fatalf("registerVoiceoverGenerateItemPlaceholder returned err=%v; expected nil", err)
	}

	// Verify the handler was bound via the canonical AllHandlers() map.
	all := dispatcher.AllHandlers()
	bound, ok := all[appjobs.TypeVoiceoverGenerateItem]
	if !ok {
		t.Fatalf("dispatcher.AllHandlers() map is missing %q after placeholder registration",
			appjobs.TypeVoiceoverGenerateItem)
	}
	if bound == nil {
		t.Fatalf("dispatcher.AllHandlers()[%q] is nil; the handler should be a non-nil HandlerFunc",
			appjobs.TypeVoiceoverGenerateItem)
	}

	// Dispatch through the bound handler directly. The handler is the
	// appjobs.HandlerFunc signature: (ctx, *job.Job, *appjobs.JobTools)
	// → (map[string]any, error).
	stubJob := &job.Job{ID: "stub-job-id", Type: appjobs.TypeVoiceoverGenerateItem, Payload: nil}
	res, err := bound(context.Background(), stubJob, nil)
	if err == nil {
		t.Fatalf("placeholder dispatch must surface a godlike/07 typed error; got result=%v, err=nil", res)
	}
	if !errors.Is(err, ErrVoiceoverNotImplementedOnCreator) {
		t.Fatalf("placeholder dispatch error chain MUST unwrap to ErrVoiceoverNotImplementedOnCreator; got err=%v", err)
	}
	if res != nil {
		t.Fatalf("placeholder handler returned non-nil result map %v alongside typed error; godlike/07 contract requires nil + err on failure paths",
			res)
	}
}

// TestVoiceoverPlaceholderHandler_FailsClosed pins that the
// placeholder handler never produces a non-nil result map alongside
// the typed-error sentinel — godlike/07 no-fake-availability
// contract. A future maintainer who accidentally returns
// (map[string]any, err) would surface here as a CI failure.
func TestVoiceoverPlaceholderHandler_FailsClosed(t *testing.T) {
	stubJob := &job.Job{ID: "stub", Type: appjobs.TypeVoiceoverGenerateItem, Payload: nil}
	res, err := voiceoverPlaceholderHandler(context.Background(), stubJob, nil)
	if err == nil {
		t.Fatalf("voiceoverPlaceholderHandler must return error; got result=%v, err=nil", res)
	}
	if res != nil {
		t.Fatalf("voiceoverPlaceholderHandler must return nil result map alongside typed error; got res=%v", res)
	}
	if !errors.Is(err, ErrVoiceoverNotImplementedOnCreator) {
		t.Fatalf("voiceoverPlaceholderHandler error chain MUST unwrap to ErrVoiceoverNotImplementedOnCreator; got err=%v", err)
	}
}

// TestRegisterVoiceoverGenerateItemPlaceholder_NilDispatcherFailClosed
// pins the typed-sentinel contract for the nil-dispatcher path. A
// future maintainer who accidentally drops the explicit `if dispatcher
// == nil` guard (or returns a non-typed error from this branch) would
// silently lose the godlike/07 surface and surface here as a CI
// failure on the errors.Is probe.
func TestRegisterVoiceoverGenerateItemPlaceholder_NilDispatcherFailClosed(t *testing.T) {
	err := registerVoiceoverGenerateItemPlaceholder(nil)
	if err == nil {
		t.Fatalf("registerVoiceoverGenerateItemPlaceholder(nil) must surface error")
	}
	if !errors.Is(err, ErrVoiceoverNotImplementedOnCreator) {
		t.Fatalf("nil-dispatcher error chain MUST unwrap to ErrVoiceoverNotImplementedOnCreator; got err=%v", err)
	}
}

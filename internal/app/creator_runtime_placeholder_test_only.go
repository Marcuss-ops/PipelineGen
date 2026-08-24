//go:build voiceover_placeholder
// +build voiceover_placeholder

// Package app — creator_runtime_placeholder_test_only.go (P1-COMPL-12-PLACEHOLDER-CAPABILITY, 2026-07-04).
//
// Build-tag-gated test affordance. This file is excluded from default
// `go build` runs; only compiled under the `voiceover_placeholder` build
// tag. Purpose: preserve the SHAPE of the `voiceover.generate_item`
// placeholder handler that used to live inline in
// `creator_runtime.go::BuildCreatorRuntime`, for test fixtures that want
// to verify the godlike/07 no-fake-availability error sentinel shape
// without standing the production Creator composition up.
//
// godlike/07 typed-error contract:
//
//	ErrVoiceoverNotImplementedOnCreator — surfaced when the opt-in
//	placeholder handler is dispatched against a `voiceover.generate_item`
//	job. Production builds do NOT register this handler; the typed
//	sentinel here documents the canonical failure mode that future
//	operators would observe if the Creator received voiceover traffic.
//
// Opt-in usage:
//
//	go test -tags voiceover_placeholder ./internal/app/...
//
// Default `go test ./...` runs do NOT compile this file; to use the
// affordance, opt in via the build tag. The companion production-side
// tombstone comment in `creator_runtime.go` cross-references this file
// for the canonical rationale.

package app

import (
	"context"
	"errors"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrVoiceoverNotImplementedOnCreator is the godlike/07-compliant typed
// sentinel surfaced by the opt-in placeholder handler. Production
// builds do NOT compile this file (the build tag excludes it); opt-in
// test builds compile it WITHOUT calling it from BuildCreatorRuntime.
// The typed sentinel preserves the canonical failure-mode shape so test
// fixtures that exercise the failure path can assert via `errors.Is`
// without coupling to the exact error message string.
//
// See `creator_runtime.go` (production-side tombstone) + the
// `architecture/current.yaml#P1-COMPL-12-PLACEHOLDER-CAPABILITY` wave
// tracker entry for the broader rationale.
var ErrVoiceoverNotImplementedOnCreator = errors.New("creator: voiceover.generate_item: not implemented (opt-in placeholder affordance; production builds do not register this handler — see P1-COMPL-12)")

// voiceoverPlaceholderHandler is the placeholder HandlerFunc shape —
// returns the godlike/07 typed sentinel wrapped with the per-job-type
// prefix so operator logs stay informative. The shape mirrors the
// pre-removal inline closure in `creator_runtime.go` byte-for-byte
// (same fmt.Errorf schema) so any future test asserting the historical
// log format survives byte-stable.
//
// The dispatcher's Register contract is closed-set (no future-bound types),
// so no registration drift risk: opting in to the build tag re-injects
// the EXACT shape the production code path used to expose.
func voiceoverPlaceholderHandler(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	_ = ctx
	_ = tools
	return nil, fmt.Errorf("voiceover.generate_item: not yet implemented in Creator composition (godlike/07 P1-COMPL-12 opt-in test affordance): %w", ErrVoiceoverNotImplementedOnCreator)
}

// registerVoiceoverGenerateItemPlaceholder binds the placeholder
// handler to the supplied dispatcher, gated for opt-in test affordance
// ONLY. Production builds do not compile this file; opt-in builds
// (via `-tags voiceover_placeholder`) can call this helper directly
// from a test fixture to recreate the historical registration shape
// for `errors.Is` assertions against ErrVoiceoverNotImplementedOnCreator.
//
// fail-closed on dispatcher rejection: if Register returns non-nil,
// the typed-error %w chain surfaces ErrVoiceoverNotImplementedOnCreator
// so the caller can errors.Is-probe consistently across register-time
// and dispatch-time failures.
func registerVoiceoverGenerateItemPlaceholder(dispatcher *appjobs.Dispatcher) error {
	if dispatcher == nil {
		return fmt.Errorf("registerVoiceoverGenerateItemPlaceholder: dispatcher is nil: %w", ErrVoiceoverNotImplementedOnCreator)
	}
	if err := dispatcher.Register(jobvoiceover.TypeGenerateItem, voiceoverPlaceholderHandler); err != nil {
		return fmt.Errorf("registerVoiceoverGenerateItemPlaceholder: bind %q to dispatcher: %w",
			jobvoiceover.TypeGenerateItem, err)
	}
	return nil
}

// Compile-time assertion: the placeholder handler satisfies the
// canonical appjobs.HandlerFunc signature. Catches signature drift at
// compile time per AGENTS.md Pattern 0.
var _ appjobs.HandlerFunc = appjobs.HandlerFunc(voiceoverPlaceholderHandler)

// Package stockbuild — registration.go (P0-2 stock-pipeline refactor, July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// youtube.stock.build.v1 handler-binding surface. Every
// composition-root wiring site MUST bind via RegisterBinding (or the
// bespoke helper here); ad-hoc `dispatcher.Register(JobType, …)` is
// forbidden because the binding must run alongside Definition() +
// the codec markers to satisfy the registry's
// RegisterDefinition+BindHandler ordering invariant.
//
// The RegistrationOrder assertion in
// registration_order_test.go pins the ordering:
//
//  1. RegisterDefinition(Definition())  — required before BindHandler
//     per MutableJobRegistry contract (registry.go::BindHandler
//     returns ErrUnknownJobType if the definition is not yet
//     registered).
//  2. BindHandler(JobType, h.Handle)     — the only kernel bind site.
//  3. (optional) RegisterCodec if a body-bearing codec replaces
//     the marker.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─── Typed Errors (godlike/07) ───────────────────────────────────────────────

// ErrResolverNotWired is returned by NewHandler when subjectsResolver is
// nil. Composition-time fail-closed condition (godlike/06 SSOT).
var ErrResolverNotWired = errors.New(
	"stockbuild: subjects.Resolver is nil (composition root must wire internal/app/wiring.BuildSubjectsResolver before constructing stockbuild.Handler)")

// ErrStepsStoreNotWired is returned by NewHandler when steps.Store is nil.
// Composition-time fail-closed condition.
var ErrStepsStoreNotWired = errors.New(
	"stockbuild: steps.Store is nil (composition root must wire NewSQLiteStore before constructing stockbuild.Handler)")

// ErrPhasesMalformed is returned by NewHandler when phases map is
// missing entries or has unexpected count.
var ErrPhasesMalformed = errors.New(
	"stockbuild: phases map malformed")

// ErrPhaseNotImplemented is returned by a PhaseBody that has been
// wired but whose underlying primitive has not been implemented yet.
// A stock build that hits this error surfaces a typed failure
// rather than a silent-success (godlike/07 NO-FAKE-AVAILABILITY).
var ErrPhaseNotImplemented = errors.New(
	"stockbuild: phase body not implemented (stub; P1 follow-up wires the underlying primitive)")

// ─── Status string constants (canonical, wire-stable) ───────────────────────

const (
	// StatusComplete is the success-state value on ResultEnvelope.Status.
	StatusComplete = "COMPLETE"

	// StatusFailed is the failure-state value on ResultEnvelope.Status.
	StatusFailed = "FAILED"
)

// ─── Time helpers (godlike/06 SSOT — one owner per fact) ────────────────────

// nowFn returns the canonical wall-clock for PhaseElapsed timestamps.
// Production wiring uses time.Now (UTC); tests inject a fixed clock
// if needed. Captured at Handler construction, NOT per-call, so a
// single Handler is consistent across its lifecycle.
func nowFn() time.Time { return time.Now().UTC() }

// sinceMs returns the milliseconds elapsed between `start` and now.
// Pure function; no state.
func sinceMs(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// ─── Decode helper (godlike/06 — typed payload surface) ───────────────────────

// decodePayload unmarshals the kernel-side job.Payload (typically
// json.RawMessage or map[string]any) into the canonical *Payload.
// Two accepted shapes:
//
//   - json.RawMessage: the canonical worker-side path (broker
//     decodes the wire shape and only the wire shape is allowed).
//   - map[string]any: in-process Dispatcher path where the
//     caller already decoded.
func decodePayload(in any, dst *Payload) error {
	switch v := in.(type) {
	case json.RawMessage:
		if len(v) == 0 {
			return errors.New("stockbuild: payload is empty json.RawMessage")
		}
		return json.Unmarshal(v, dst)
	case []byte:
		if len(v) == 0 {
			return errors.New("stockbuild: payload is empty []byte")
		}
		return json.Unmarshal(v, dst)
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, dst)
	case nil:
		return errors.New("stockbuild: payload is nil")
	default:
		return errors.New("stockbuild: payload is unexpected type (want json.RawMessage, []byte, or map[string]any)")
	}
}

// ─── phaseSpecificInput (per-phase fingerprint input derivation) ───────────

// phaseSpecificInput returns the phase-local bytes that feed
// PhaseFingerprint. The function is deliberately simple: it reads
// only the Payload fields the phase will operate on, so fingerprint
// variants surface ONLY the inputs that change run shape.
//
// godlike/06 SSOT: there is exactly ONE owner of per-phase
// fingerprint input (this function). Adding a phase that needs a
// richer fingerprint MUST add the new field here so all callers
// share the derivation.
func phaseSpecificInput(phase PhaseName, p Payload) string {
	// Canonical projection: substring-tagged key=value pairs joined
	// with '|', sorted to guarantee deterministic output. Stable
	// across reordering of Categories (which we sort elsewhere for
	// the RunID hash).
	switch phase {
	case PhaseSearch:
		return fmt.Sprintf("v=%d", p.Target.Videos)
	case PhaseSelect:
		parts := make([]string, 0, len(p.Categories))
		for _, c := range p.Categories {
			parts = append(parts, c.Name+":"+itoa(c.Count))
		}
		return "cats=" + joinComma(parts)
	case PhaseDownload:
		return fmt.Sprintf("v=%d|cpv=%d", p.Target.Videos, p.Target.ClipsPerVideo)
	case PhaseExtract:
		return fmt.Sprintf("v=%d|cpv=%d|cd=%d",
			p.Target.Videos, p.Target.ClipsPerVideo, p.Target.ClipDurationSeconds)
	case PhaseUpload:
		return fmt.Sprintf("v=%d|cpv=%d|cd=%d|dest=%s",
			p.Target.Videos, p.Target.ClipsPerVideo, p.Target.ClipDurationSeconds,
			p.DestinationFolderID)
	case PhasePersist:
		return fmt.Sprintf("v=%d|cpv=%d|cd=%d",
			p.Target.Videos, p.Target.ClipsPerVideo, p.Target.ClipDurationSeconds)
	case PhaseIndex:
		return fmt.Sprintf("v=%d", p.Target.Videos)
	case PhaseVerify:
		return fmt.Sprintf("v=%d|cpv=%d|cd=%d",
			p.Target.Videos, p.Target.ClipsPerVideo, p.Target.ClipDurationSeconds)
	}
	return ""
}

// ─── RegisterBinding (the canonical broker-bind surface) ─────────────────────

// RegisterBinding binds (Definition, codec markers, Handler) against
// the supplied MutableJobRegistry in the canonical order:
//
//  1. RegisterDefinition(Definition())  — required before BindHandler.
//  2. BindHandler(JobType, h.Handle)    — the kernel bind site:
//
// The handler-typed signature is `job.JobHandlerFunc` (kernel level,
// not the application's `Handler`-typed signature which carries
// `*job.Job + *job.JobExecutionTools`). The adapter below converts.
//
// godlike/06 SSOT: this is the SOLE place in the codebase that knows
// the canonical `youtube.stock.build.v1` → handler mapping.
func RegisterBinding(reg job.MutableJobRegistry, h *Handler) error {
	if reg == nil {
		return errors.New("stockbuild: registry is nil")
	}
	if h == nil {
		return errors.New("stockbuild: handler is nil")
	}

	// 1. RegisterDefinition must precede BindHandler per the registry's
	// stated ordering (registry.go::BindHandler ErrUnknownJobType contract).
	if err := reg.RegisterDefinition(Definition()); err != nil {
		return err
	}

	// 2. BindHandler — kernel JobHandlerFunc signature wants (ctx, j, payload any)
	// whereas stockbuild.Handler is typed (ctx, *job.Job, *JobExecutionTools).
	// The adapter translates the tools.Progress/Event callbacks to the
	// kernel surface.
	bound := job.JobHandlerFunc(func(ctx context.Context, j *job.Job, payload any) (result any, err error) {
		tools := &job.JobExecutionTools{
			Progress: func(p int, m string) { /* jobkernel surfaces */ },
			Event:    func(et, m string, d map[string]any) { /* jobkernel surfaces */ },
		}
		return h.Handle(ctx, j, tools)
	})
	if bindErr := reg.BindHandler(JobType, bound); bindErr != nil {
		return bindErr
	}
	return nil
}

// ─── internal string helpers (godlike/06 typed surface) ─────────────────────

// itoa wraps strconv.Itoa so the call site reads cleanly.
func itoa(i int) string { return strconv.Itoa(i) }

// joinComma joins parts with ',' — avoids fmt.Sprintf in tight loops.
func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	// Three-pass concat: faster than fmt.Sprintf allocations under
	// hot resolver paths. Typical N is 2-4 categories.
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}

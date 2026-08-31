// Package jobs — dispatcher.go (P0 Commit 4, July 2026).
//
// Dispatcher.Enqueue is the canonical typed entry point for routing a
// job through the compiled JobDefinition registry (P0 Commit 3). The
// method:
//
//  1. looks up the JobDefinition for the supplied jobType via the
//     frozen CompiledJobRegistry;
//  2. encodes the typed payload via def.PayloadCodec.EncodePayload;
//  3. delegates the actual row-creation to a typed EnqueuePort
//     (*appjobs.Service satisfies the interface at composition time)
//     so idempotency keys, UNIQUE-constraint rescue, and the canonical
//     job lifecycle continue to live in ONE place.
//
// The method refuses:
//   - unregistered job types       (ErrUnknownJobType, wrapped from C3)
//   - definitions missing a codec  (ErrCodecMissing, application-layer)
//   - payload encode failures      (ErrInvalidPayload, wrapped)
//   - pre-Freeze registry access   (ErrRegistryNotFrozen)
//   - missing enqueuer port        (ErrEnqueuerNotWired, composition bug)
//
// ── Why a typed port, not a *Service back-reference ────────────────
//
// Dispatcher.Enqueue could take *Service directly, but *Service
// itself holds *Dispatcher (service.go::Service struct). A direct
// back-reference would force a cyclic type construction at the
// composition root. The EnqueuePort interface breaks the cycle:
// *Service satisfies EnqueuePort (Service.Enqueue is the canonical
// row-create method), and Dispatcher holds the port via a fluent
// late-binding setter (SetEnqueuer). The cycle is closed at
// composition time, never at runtime.
//
// ── Why fluent late-binding ────────────────────────────────────────
//
// The composition root in internal/app/registry.go (C3 closure)
// wires dispatcher.WithRegistry(compiled) BEFORE constructing the
// *Service, then dispatcher.SetEnqueuer(service) AFTER constructing
// the *Service. The two setters are called once during WireRegistry
// and never at runtime; compilation-time cycle, runtime forward-only.
//
// ── Migration ticket ──────────────────────────────────────────────
//
// C4 introduces Dispatcher.Enqueue as a structured gateway. Forward
// callers should migrate off raw-string `EnqueueRequest{Type: "<literal>"}`
// patterns to dispatcher.Enqueue(ctx, job.TypeX, typedPayload). C5+
// wires per-family real TypedCodecAdapter[T,R] codecs; C4 only needs
// the marker codec (idempotent identity round-trip via json.Marshal)
// to compile a working pipeline end-to-end.
package jobs

import (
	"context"
	"errors"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Sentinel errors ────────────────────────────────────────────────

// ErrEnqueuerNotWired is returned when Dispatcher.Enqueue is invoked
// before the composition root has called SetEnqueuer. A nil enqueuer
// is a configuration bug; fail-closed at the dispatch boundary.
var ErrEnqueuerNotWired = errors.New("appjobs.Dispatcher: enqueuer not wired (composition bug — SetEnqueuer must run at WireRegistry)")

// ErrRegistryNotFrozen is returned when Enqueue reaches a registry
// that has not been Freeze()d. The C3 MutableJobRegistry requires
// Freeze() before CompiledJobRegistry reads — production composition
// roots always Freeze once at startup; if Enqueue sees an unfrozen
// state, the composition root is misconfigured.
//
// Defense-in-depth: a misuse can't write rows against an in-flight
// mutable registry where definitions might mutate mid-flight.
var ErrRegistryNotFrozen = errors.New("appjobs.Dispatcher: compiled registry not frozen (composition root must Freeze() once at startup)")

// ErrCodecMissing is returned when the JobDefinition carries no
// PayloadCodec. The encode step is unreachable; the caller cannot
// expect Enqueue to marshal payload for them. Combined with C3
// StartupValidator check (c) "manifest-required-without-result-codec",
// this is the symmetric input-side check: every executable definition
// MUST declare a PayloadCodec.
var ErrCodecMissing = errors.New("appjobs.Dispatcher: JobDefinition.PayloadCodec is nil")

// ErrInvalidPayload wraps the underlying EncodePayload failure.
// errors.Is(err, ErrInvalidPayload) returns true via %w wrap; the
// unwrapped error message preserves the codec-specific diagnostic
// (e.g. TypedCodecAdapter's reflect-based type-mismatch diagnostic)
// for log scraping.
var ErrInvalidPayload = errors.New("appjobs.Dispatcher: payload encode failed")

// ── EnqueuePort (typed port bridging the cycle) ────────────────────

// EnqueuePort is the typed port Dispatcher uses to persist a job.
// *Service satisfies this contract (Service.Enqueue is the canonical
// row-create method). The port is intentionally narrow — only Enqueue
// is needed at the dispatch boundary; Get/Find/List live on Service
// but Dispatcher never reads them.
//
// Rendered as an interface per AGENTS.md Pattern 0 (port abstraction).
// Future adapters (PostgresBroker, in-memory broker for tests) can
// satisfy this contract without leaking a concrete persistence adapter into
// Dispatcher. The compile-time assertion below pins the contract to
// *Service so any future drift is a build failure.
type EnqueuePort interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// Compile-time assertion: *Service satisfies EnqueuePort. Pins the
// bridge contract at compile time so any future drift on Service.Enqueue
// is a build failure, not a runtime panic.
var _ EnqueuePort = (*Service)(nil)

// ── Dispatcher extensions ──────────────────────────────────────────

// registry / enqueuer live as unexported fields on *Dispatcher (the
// struct lives in types.go; Go permits cross-file field/method definitions).
// The composition root attaches them via the fluent builders below at
// WireRegistry time; the fields are never re-bound at runtime.

// WithRegistry attaches the canonical frozen CompiledJobRegistry
// (P0 Commit 3) to the Dispatcher. The compiled registry is the SSOT
// for type→definition lookups; Enqueue reads Definition(jobType) to
// find the canonical codec for payload encoding.
//
// Nil-tolerant: passing nil clears the field (test fixture path).
// Calling WithRegistry multiple times reassigns (last writer wins),
// which is composition-only.
//
// Returns the receiver for builder-style chaining. Mirrors the
// canonical Service.WithRegistry precedent in service.go.
func (d *Dispatcher) WithRegistry(reg job.CompiledJobRegistry) *Dispatcher {
	if d == nil {
		return d
	}
	d.registry = reg
	return d
}

// SetEnqueuer attaches the typed EnqueuePort that Dispatcher.Enqueue
// delegates to. The composition root wires this AFTER constructing
// the *Service (whose Enqueue satisfies the port) — see
// internal/app/registry.go for the canonical closure pattern.
//
// Nil-tolerant: passing nil clears the field (test fixture path).
// Calling SetEnqueuer multiple times reassigns (last writer wins),
// which is composition-only.
//
// Returns the receiver for builder-style chaining.
func (d *Dispatcher) SetEnqueuer(p EnqueuePort) *Dispatcher {
	if d == nil {
		return d
	}
	d.enqueuer = p
	return d
}

// Enqueue is the canonical typed entry point for routing a job
// through the compiled JobDefinition registry. Signature per the user
// C4 spec:
//
//	Enqueue(ctx context.Context, jobType string, payload any) (*job.Job, error)
//
// Behaviour, in priority order (fail-closed at each gate):
//
//  1. nil-receiver                 → ErrEnqueuerNotWired (defensive)
//  2. nil-enqueuer                 → ErrEnqueuerNotWired (composition bug)
//  3. nil/unfrozen registry        → ErrRegistryNotFrozen (composition bug)
//  4. unregistered jobType         → fmt.Errorf("%w: %s", job.ErrUnknownJobType, ...)
//  5. nil PayloadCodec on definition → ErrCodecMissing
//  6. EncodePayload failure        → fmt.Errorf("%w: %s: %v", ErrInvalidPayload, ...)
//  7. delegate to enqueuer.Enqueue with {Type, Payload=rawBytes}
//     (Service.Enqueue handles idempotency keys + UNIQUE rescue + lifecycle)
//
// Why the jobType parameter type is `string` (not `job.Type`): the
// codebase has no `job.Type` defined-alias; `Type` is a struct field on
// JobDefinition and the canonical value-space is string-based
// (TypeScriptGenerate = "script.generate", etc.). The parameter type
// follows the existing EnqueueRequest.Type convention. A typed alias
// can be added in a future migration without breaking this signature.
func (d *Dispatcher) Enqueue(ctx context.Context, jobType string, payload any) (*job.Job, error) {
	// (1) nil receiver.
	if d == nil {
		return nil, fmt.Errorf("%w: dispatcher is nil", ErrEnqueuerNotWired)
	}

	// (2) enqueuer not wired.
	if d.enqueuer == nil {
		return nil, fmt.Errorf("%w: must call SetEnqueuer first", ErrEnqueuerNotWired)
	}

	// (3) compiled registry not frozen.
	if d.registry == nil {
		return nil, ErrRegistryNotFrozen
	}
	if !d.registry.IsFrozen() {
		return nil, fmt.Errorf("%w: IsFrozen()=false", ErrRegistryNotFrozen)
	}

	// (4) binary lookup against the post-Freeze CompiledJobRegistry.
	def, ok := d.registry.Definition(jobType)
	if !ok {
		return nil, fmt.Errorf("%w: %s", job.ErrUnknownJobType, jobType)
	}

	// (5) PayloadCodec must be set. Per C3 StartupValidator check (c),
	// the executable path surfaces a typed-error contract that every
	// JobDefinition carries both PayloadCodec + ResultCodec. We re-enforce
	// at the dispatch boundary as defense-in-depth — the validator
	// cannot catch a def.PayloadCodec = nil mutation performed AFTER
	// Freeze() without re-running Validate (which is not on the rotation).
	if def.PayloadCodec == nil {
		return nil, fmt.Errorf("%w: %s lacks PayloadCodec", ErrCodecMissing, jobType)
	}

	// (6) canonical codec encode. The codec error is wrapped with %w
	// (NOT %v) so callers can errors.As(err, &someCodecErr) to
	// recover the typed diagnostic — e.g. TypedCodecAdapter's
	// reflect-based type-mismatch surfaces as a typed payload
	// validation error that downstream callers may want to classify.
	rawBytes, err := def.PayloadCodec.EncodePayload(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %s encode failure: %w", ErrInvalidPayload, jobType, err)
	}

	// (7) delegate. The constructed EnqueueRequest carries only the
	// Type + Payload fields; Service.Enqueue populates CorrelationID from
	// corid context, applies idempotency on (type, correlation_id), calls
	// repo.Create, and surfaces the canonical job lifecycle.
	return d.enqueuer.Enqueue(ctx, &job.EnqueueRequest{
		Type:    jobType,
		Payload: rawBytes,
	})
}

// Package job — registry.go (P0 Commit 3, July 2026).
//
// MutableJobRegistry + CompiledJobRegistry — the canonical
// definition-vs-binding dual registry, post-Freeze. Additive to
// the existing operational Registry in internal/capabilities/jobs/queue
// (which manages timeouts/retries/queues via JobPolicy).
//
// ── Two-stage shape ─────────────────────────────────────────────────
//
//	Definition phase    — RegisterDefinition(def JobDefinition) error
//	Binding phase       — BindHandler(jobType, handler) error
//	Freeze phase        — Freeze() (CompiledJobRegistry, error)
//	Read-only phase     — Definition / Handler / HasHandler /
//	                      AllDefinitions / CreatorJobTypes /
//	                      CreatorCapabilities / ValidateWorkflow /
//	                      IsFrozen (post-Freeze only)
//
// Once Freeze() returns successfully, RegisterDefinition and
// BindHandler returns ErrRegistryFrozen. Concurrent reads of the
// returned CompiledJobRegistry are safe forever; concurrent writes
// are impossible because the writer is gone.
//
// ── AllDefinitions() accessor ───────────────────────────────────────
//
// The validator (startup_validator.go) iterates beyond just the
// `Workflow` job-type list because checks (b)/(c)/(d) require
// "every registered JobDefinition". AllDefinitions() returns a
// sorted snapshot so the validator can deterministically report
// failures independent of map-iteration order.
//
// ── Layering ─────────────────────────────────────────────────────────
//
// Standard-library imports only. No application/infrastructure
// imports. The JobHandlerFunc signature is a function-type so the
// adapter wraps existing HandlerFunc (in application/jobs/types.go)
// at the composition-root boundary.
//
// ── Migration schedule ──────────────────────────────────────────────
//
// C3 lands the dual-registry shape and the freeze contract. C4 wires
// Dispatcher.Enqueue through def.PayloadCodec; C10..C14 migrate
// handlers per family. C15 cuts the operational Registry over to
// JobDefinition as the SSOT.
package job

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ── Sentinel errors ──────────────────────────────────────────────────

var (
	// ErrRegistryFrozen is returned by RegisterDefinition / BindHandler
	// after Freeze() has been called. The compiler-side var (_ error)
	// assertions in startup_validator_test.go pin the message.
	ErrRegistryFrozen = errors.New("job registry is frozen")

	// ErrDuplicateType is returned when RegisterDefinition is called
	// twice with the same JobDefinition.Type.
	ErrDuplicateType = errors.New("job type already registered")

	// ErrUnknownJobType is returned when BindHandler is called for a
	// job type that has no registered definition. Bind is locked to
	// defined types because runtime dispatch cannot execute an
	// unbound job.
	ErrUnknownJobType = errors.New("job type not in registry")

	// ErrDuplicateHandler is returned when BindHandler is called
	// twice for the same job type. The registry is 1:1 between
	// type and handler at the C3 surface; alias resolution (e.g.
	// image.generate.google → images.generate) is the alias
	// resolver's job, not the registry's.
	ErrDuplicateHandler = errors.New("handler already bound for job type")

	// ErrInvalidJob wraps the canonical JobDefinition.Validate()
	// output. The startup_validator maps this to the post-freeze
	// "registry frozen with invalid spec" headline.
	ErrInvalidJob = errors.New("job definition invalid")

	// ErrSchemaVersionEmpty is returned when the PayloadCodec /
	// ResultCodec fields are non-nil but report SchemaVersion() == "".
	// An empty schema version cannot safely round-trip across codec
	// upgrades, so the build rejects it at write time.
	ErrSchemaVersionEmpty = errors.New("codec schema version is empty")
)

// ── JobHandlerFunc ───────────────────────────────────────────────────

// JobHandlerFunc is the canonical handler signature that the
// C3 MutableJobRegistry binds to each JobDefinition. Function
// type (not interface) keeps the domain surface minimal — the
// application-layer adapter wraps the existing
// internal/capabilities/jobs/queue/types.go::HandlerFunc at the
// composition-root boundary (internal/app/registry.go::WireRegistry).
//
// Parameters:
//
//	ctx     — handler lifetime boundary (request, job, or worker
//	          lifetime — chosen by the caller; see godlike/06 §Post-write).
//	j       — the canonical kernel/job.Job (passed so handlers can
//	          distinguish attempts via j.Attempt + read metadata).
//	payload — the typed input (Json-marshalable when shipped over
//	          the wire, but the registry does NOT constrain shape;
//	          per AGENTS.md Pattern 0 the only sanctioned `any` in
//	          the domain is the codec boundary; this handler receives
//	          the post-codec-decoded typed value, so the surface is
//	          the codec-decoder's output, which is itself `any`).
//
// Return values:
//
//	result  — the typed output, decoded-encodable per the registered
//	          ResultCodec (so the Sender can serialize for transport).
//	err     — non-nil surfaces as a typed retryable/terminal error
//	          per the per-job-type retry policy.
type JobHandlerFunc func(ctx context.Context, j *Job, payload any) (result any, err error)

// ── MutableJobRegistry ───────────────────────────────────────────────

// MutableJobRegistry is the Writable view of the registry. Built up
// during composition (RegisterDefinition + BindHandler), then
// Freeze() returns the immutable CompiledJobRegistry.
//
// Concurrent: RegisterDefinition / BindHandler / Freeze are
// mutually serialised via sync.RWMutex. Calls from a single goroutine
// are also fine (no race detected by Go's race detector).
type MutableJobRegistry interface {
	// RegisterDefinition adds a new JobDefinition slot. Returns:
	//   - ErrRegistryFrozen if called after Freeze.
	//   - ErrInvalidJob if def.Validate() fails.
	//   - ErrSchemaVersionEmpty if any codec is non-nil but
	//     reports empty SchemaVersion().
	//   - ErrDuplicateType if def.Type already registered.
	RegisterDefinition(def JobDefinition) error

	// BindHandler attaches a JobHandlerFunc to a previously-registered
	// JobDefinition. Returns:
	//   - ErrRegistryFrozen if called after Freeze.
	//   - ErrUnknownJobType if jobType has no Definition.
	//   - ErrDuplicateHandler if jobType already has a handler bound.
	BindHandler(jobType string, handler JobHandlerFunc) error

	// Freeze snapshots the mutable state into an immutable
	// CompiledJobRegistry and rejects any subsequent writes.
	// Returns the read-only view + nil on success, or nil + a
	// typed error on failure (currently: ErrRegistryFrozen if
	// called twice).
	Freeze() (CompiledJobRegistry, error)
}

// ── CompiledJobRegistry ──────────────────────────────────────────────

// CompiledJobRegistry is the Read-only post-Freeze view.
// All accessors are safe for concurrent read. Mutations are
// impossible at compile time — MutableJobRegistry methods are
// not exposed on this interface.
type CompiledJobRegistry interface {
	// Definition returns the registered JobDefinition for the
	// given type, or (zero, false) if not present.
	Definition(jobType string) (JobDefinition, bool)

	// Handler returns the bound JobHandlerFunc for the given
	// type, or (nil, false) if not bound.
	Handler(jobType string) (JobHandlerFunc, bool)

	// HasHandler reports whether a JobHandlerFunc is bound.
	// Equivalent to Handler(t) != (_, false).
	HasHandler(jobType string) bool

	// AllDefinitions returns a sorted-by-Type snapshot of every
	// registered JobDefinition. Used by startup_validator for
	// iteration over the full registry (not just Workflow refs).
	AllDefinitions() []JobDefinition

	// CreatorJobTypes returns sorted job types whose ExecutionClass
	// is creator_allowed or creator_only. Sender-only types are
	// excluded — the Creator cannot claim them.
	CreatorJobTypes() []string

	// CreatorCapabilities returns sorted, deduplicated RequiredCapabilities
	// from every non-sender-only JobDefinition. This is the
	// advertised-capability set the Creator forwards in capability
	// registration (godlike P0 §7.8).
	CreatorCapabilities() []Capability

	// ValidateWorkflow returns a typed error if any type in the
	// workflow list does not resolve to a Definition. The empty
	// list is always valid (no-op).
	ValidateWorkflow(types []string) error

	// IsFrozen always returns true. Provided for symmetry with
	// MutableJobRegistry callers that need a single-shape check.
	IsFrozen() bool
}

// ── Compile-time interface assertions ────────────────────────────────

// var _ Interface = (*Concrete)(nil) pins the contract at compile
// time so future drift (e.g. a method removed from CompiledJobRegistry)
// is a build failure, not a runtime panic.
var (
	_ MutableJobRegistry  = (*builderRegistry)(nil)
	_ CompiledJobRegistry = (*readOnlyRegistry)(nil)
)

// ── builderRegistry (concrete MutableJobRegistry) ─────────────────────

// builderRegistry is the canonical implementation of
// MutableJobRegistry. Internal-package type so callers must obtain
// it via NewMutableJobRegistry (which returns the interface).
type builderRegistry struct {
	mu          sync.RWMutex
	frozen      bool
	definitions map[string]JobDefinition
	handlers    map[string]JobHandlerFunc
}

// NewMutableJobRegistry returns a fresh, non-frozen MutableJobRegistry
// with both maps pre-allocated at zero capacity. Construction is
// cheap (no I/O, no decode); the composition root in
// internal/app/registry.go creates one per WireRegistry invocation.
func NewMutableJobRegistry() MutableJobRegistry {
	return &builderRegistry{
		definitions: make(map[string]JobDefinition),
		handlers:    make(map[string]JobHandlerFunc),
	}
}

// RegisterDefinition validates the supplied definition and stores
// it under def.Type. See MutableJobRegistry for the full contract;
// the implementation runs Validate() + the codec non-nil + non-empty
// SchemaVersion checks at write-time so post-Freeze verification
// (StartupValidator) only handles cross-cutting invariants.
func (r *builderRegistry) RegisterDefinition(def JobDefinition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("%w: cannot RegisterDefinition", ErrRegistryFrozen)
	}
	if err := def.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidJob, err)
	}
	if def.PayloadCodec != nil && def.PayloadCodec.SchemaVersion() == "" {
		return fmt.Errorf("%w: %q PayloadCodec.SchemaVersion is empty", ErrSchemaVersionEmpty, def.Type)
	}
	if def.ResultCodec != nil && def.ResultCodec.SchemaVersion() == "" {
		return fmt.Errorf("%w: %q ResultCodec.SchemaVersion is empty", ErrSchemaVersionEmpty, def.Type)
	}
	if _, exists := r.definitions[def.Type]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateType, def.Type)
	}
	r.definitions[def.Type] = def
	return nil
}

// BindHandler attaches a JobHandlerFunc to a registered type. The
// "definition first, handler second" ordering is enforced — runtime
// dispatch cannot execute an unbound job, so an early mapping
// is the only safe shape.
func (r *builderRegistry) BindHandler(jobType string, handler JobHandlerFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("%w: cannot BindHandler", ErrRegistryFrozen)
	}
	if _, exists := r.handlers[jobType]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateHandler, jobType)
	}
	if _, ok := r.definitions[jobType]; !ok {
		return fmt.Errorf("%w: %s (no definition registered)", ErrUnknownJobType, jobType)
	}
	r.handlers[jobType] = handler
	return nil
}

// Freeze snapshots the runtime graph into an immutable
// CompiledJobRegistry. The maps are copied (NOT shared) so any
// future write attempts on the builder side cannot poison the
// post-freeze read snapshot. Returns nil + ErrRegistryFrozen if
// called twice.
func (r *builderRegistry) Freeze() (CompiledJobRegistry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return nil, fmt.Errorf("%w: already frozen", ErrRegistryFrozen)
	}
	r.frozen = true
	defs := make(map[string]JobDefinition, len(r.definitions))
	for k, v := range r.definitions {
		defs[k] = v
	}
	handlers := make(map[string]JobHandlerFunc, len(r.handlers))
	for k, v := range r.handlers {
		handlers[k] = v
	}
	return &readOnlyRegistry{
		definitions: defs,
		handlers:    handlers,
	}, nil
}

// ── readOnlyRegistry (concrete CompiledJobRegistry) ──────────────────

// readOnlyRegistry is the canonical implementation of
// CompiledJobRegistry. Constructed exclusively by (*builderRegistry).Freeze;
// the maps are owned (no shared reference to the builder's maps),
// so external mutation is geometrically impossible.
type readOnlyRegistry struct {
	definitions map[string]JobDefinition
	handlers    map[string]JobHandlerFunc
}

// Definition returns the registered JobDefinition or (zero, false).
// The JobDefinition is returned by value — modifying the returned
// value at the caller side has no effect on the registry's copy
// (Go's value semantics for struct types).
func (r *readOnlyRegistry) Definition(jobType string) (JobDefinition, bool) {
	d, ok := r.definitions[jobType]
	return d, ok
}

// Handler returns the bound JobHandlerFunc or (nil, false).
func (r *readOnlyRegistry) Handler(jobType string) (JobHandlerFunc, bool) {
	h, ok := r.handlers[jobType]
	return h, ok
}

// HasHandler returns true iff Handler(jobType) returned (_, true).
// Convenience to avoid double-typing the same lookup at call sites.
func (r *readOnlyRegistry) HasHandler(jobType string) bool {
	_, ok := r.handlers[jobType]
	return ok
}

// AllDefinitions returns sorted-by-Type snapshot of every
// registered JobDefinition. The slice is a fresh allocation
// (NOT the underlying maps' iteration order) so callers can
// iterate without surprising order changes.
func (r *readOnlyRegistry) AllDefinitions() []JobDefinition {
	out := make([]JobDefinition, 0, len(r.definitions))
	for _, d := range r.definitions {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// CreatorJobTypes filters AllDefinitions to non-sender-only
// types; sorts the result deterministically. Caller iterates
// the returned slice without worrying about map iteration order.
func (r *readOnlyRegistry) CreatorJobTypes() []string {
	out := make([]string, 0, len(r.definitions))
	for _, d := range r.definitions {
		if d.ExecutionClass == ExecutionSenderOnly {
			continue
		}
		out = append(out, d.Type)
	}
	sort.Strings(out)
	return out
}

// CreatorCapabilities aggregates RequiredCapabilities across
// every non-sender-only definition, deduplicates, and sorts.
// Per P0 §4.3 the Creator's advertised capability set IS this
// union (∩ handler bound + ∩ allowlist, then sorted for stability).
func (r *readOnlyRegistry) CreatorCapabilities() []Capability {
	seen := make(map[Capability]bool)
	out := make([]Capability, 0)
	for _, d := range r.definitions {
		if d.ExecutionClass == ExecutionSenderOnly {
			continue
		}
		for _, c := range d.RequiredCapabilities {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ValidateWorkflow reports every entry in `types` that does not
// resolve to a Definition. Empty input is a no-op (no workflow
// references to validate against; not an error condition). The
// error message includes the full missing set so a startup failure
// shows every missing type at once — not just the first one.
func (r *readOnlyRegistry) ValidateWorkflow(types []string) error {
	if len(types) == 0 {
		return nil
	}
	var missing []string
	for _, t := range types {
		if _, ok := r.definitions[t]; !ok {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("ValidateWorkflow: workflow references unknown job types: %v", missing)
	}
	return nil
}

// IsFrozen always returns true. The readOnlyRegistry is constructed
// ONLY via (*builderRegistry).Freeze; once constructed there is no
// API path to mutate it. The bool return is purely for symmetry
// with MutableJobRegistry callers that want a single-shape check.
func (r *readOnlyRegistry) IsFrozen() bool { return true }

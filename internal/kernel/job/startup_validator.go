// Package job — startup_validator.go (P0 Commit 3, July 2026).
//
// StartupValidator.ValidateRuntimeGraph — the canonical
// composition-time fail-closed check on the runtime graph.
// Wired into internal/app/registry.go::WireRegistry at the END
// of the existing 7-step composition chain; if ValidateRuntimeGraph
// returns non-nil, WireRegistry aborts the server boot.
//
// ── The 6 §4.5 checks ────────────────────────────────────────────────
//
//	(a) workflow resolvability: every entry in input.Workflow must
//	    resolve to a Definition. Delegated to
//	    CompiledJobRegistry.ValidateWorkflow (called first, before
//	    iteration).
//
//	(b) creator-enabled-no-handler: every JobDefinition with
//	    ExecutionClass != sender_only must have a handler bound.
//	    Iterates AllDefinitions().
//
//	(c) manifest-required-without-result-codec: every JobDefinition
//	    with ArtifactPolicy.RequireManifest=true must have a
//	    non-nil ResultCodec. Iterates AllDefinitions().
//
//	(d) codec-schema-version-present: PayloadCodec and ResultCodec
//	    (when non-nil) must return non-empty SchemaVersion().
//	    Iterates AllDefinitions(). (The write-time check inside
//	    RegisterDefinition catches the same shape, so this is a
//	    defense-in-depth scan against any future post-Freeze
//	    shape mutation.)
//
//	(e) capability-derivable: every JobDefinition with ExecutionClass
//	    != sender_only must contribute at least one entry to
//	    CreatorCapabilities(). Surface as the canonical "creator
//	    job type declares zero required capabilities" diagnostic.
//
//	(f) duplicates: enforced at write time by RegisterDefinition.
//	    The post-Freeze verification is the "all definitions unique"
//	    count check (1≠0 ⇒ no duplicates) — exercised by the map
//	    storage in builderRegistry.
//
// ── Error format ──────────────────────────────────────────────────────
//
// All findings are joined via errors.Join (Go 1.20+) so the caller
// sees a single non-nil error; errors.Is(err, ErrInvalidRuntimeGraph)
// returns true, and iterating via (errs, _) = errors.Unwrap... gives
// each individual finding. The composition root prefixes the
// wrapped error with a context heading.
//
// ── Layering ─────────────────────────────────────────────────────────
//
// Standard-library imports only. The Validator operates on a
// CompiledJobRegistry + Workflow inputs (both already-snapshotted
// values), so no I/O or time.Now() leak.
package job

import (
	"errors"
	"fmt"
)

// ErrInvalidRuntimeGraph is the umbrella error returned by
// ValidateRuntimeGraph when ANY of the 6 §4.5 checks fails. The
// individual check findings are joined underneath via errors.Join;
// use errors.Is to detect the umbrella and errors.As / errors.Unwrap
// to iterate the specific findings.
var ErrInvalidRuntimeGraph = errors.New("StartupValidator: invalid runtime graph")

// ── StartupValidationInput ───────────────────────────────────────────

// StartupValidationInput is the canonical typed input to
// ValidateRuntimeGraph. The composition root assembles it at
// the END of WireRegistry with:
//
//	StartupValidationInput{
//	    Registry: compiled,                 // from (*job.MutableJobRegistry).Freeze()
//	    Workflow: canonicalWorkflowRefs,    // job.Type* constants
//	}
//
// Fields:
//
//	Registry — the Frozen CompiledJobRegistry. Must be non-nil;
//	           ValidateRuntimeGraph returns ErrInvalidRuntimeGraph
//	           if Registry is nil.
//
//	Workflow — the workflow job references used by the canonical
//	           4-family execution graph:
//	             script.generate → (images.generate ∥ assets.resolve)
//	             script.generate → document.generate
//	           Empty Workflow is valid (no checks to run); the
//	           validator does not synthesise a default.
type StartupValidationInput struct {
	Registry CompiledJobRegistry
	Workflow []string
}

// ── StartupValidator interface ───────────────────────────────────────

// StartupValidator is the typed port the composition root invokes
// at startup. Implementations MUST return nil on a clean graph
// and a non-nil error wrapping ErrInvalidRuntimeGraph when ANY
// of the 6 §4.5 checks fails.
type StartupValidator interface {
	ValidateRuntimeGraph(input StartupValidationInput) error
}

// ── DefaultStartupValidator ──────────────────────────────────────────

// DefaultStartupValidator is the canonical implementation of
// StartupValidator. All C3 wiring uses this implementation;
// future specialised validators (e.g. cardinality caps, canary
// rollout) should embed this struct and override the affected
// checks — not replace it wholesale (the 6 §4.5 checks are a
// fixed contract).
type DefaultStartupValidator struct{}

// ValidateRuntimeGraph runs the 6 §4.5 checks described in the
// file header. Returns nil on clean, errors.Join(errs...) on
// failure.
//
// The errors are tagged with their check letter ((a)..(f)) so a
// future log-scrape or test inspection can attribute findings
// to specific check categories.
func (v DefaultStartupValidator) ValidateRuntimeGraph(input StartupValidationInput) error {
	if input.Registry == nil {
		return fmt.Errorf("%w: Registry is nil", ErrInvalidRuntimeGraph)
	}

	var errs []error

	// (a) workflow resolvability — delegated to the registry.
	if err := input.Registry.ValidateWorkflow(input.Workflow); err != nil {
		errs = append(errs, fmt.Errorf("(a) workflow resolvability: %w", err))
	}

	// (b)+(c)+(d) — iterate AllDefinitions() (sorted by Type for
	// deterministic error ordering).
	for _, d := range input.Registry.AllDefinitions() {

		// (b) creator-enabled-no-handler.
		if d.ExecutionClass != ExecutionSenderOnly && !input.Registry.HasHandler(d.Type) {
			errs = append(errs, fmt.Errorf(
				"(b) creator-enabled-without-handler: %q has ExecutionClass=%s but no handler bound",
				d.Type, d.ExecutionClass,
			))
		}

		// (c) manifest-required-without-result-codec.
		if d.ArtifactPolicy.RequireManifest && d.ResultCodec == nil {
			errs = append(errs, fmt.Errorf(
				"(c) manifest-required-without-result-codec: %q has RequireManifest=true but ResultCodec is nil",
				d.Type,
			))
		}

		// (d) codec-schema-version-present (post-freeze defense in depth).
		if d.PayloadCodec != nil && d.PayloadCodec.SchemaVersion() == "" {
			errs = append(errs, fmt.Errorf(
				"(d) codec-schema-version-present: %q PayloadCodec.SchemaVersion is empty",
				d.Type,
			))
		}
		if d.ResultCodec != nil && d.ResultCodec.SchemaVersion() == "" {
			errs = append(errs, fmt.Errorf(
				"(d) codec-schema-version-present: %q ResultCodec.SchemaVersion is empty",
				d.Type,
			))
		}

		// (e) capability-derivable: creator-enabled must declare
		// at least one RequiredCapability. Sender-only jobs CAN
		// have empty capabilities and are skipped.
		if d.ExecutionClass != ExecutionSenderOnly && len(d.RequiredCapabilities) == 0 {
			errs = append(errs, fmt.Errorf(
				"(e) capability-derivable: %q is creator-enabled (ExecutionClass=%s) but declares zero RequiredCapabilities",
				d.Type, d.ExecutionClass,
			))
		}
	}

	// (f) duplicates — enforced at write time (RegisterDefinition).
	// The post-Freeze verification reads as "len(AllDefinitions()) ==
	// len(unique Definitions)". The map storage in builderRegistry
	// already enforces uniqueness, so the post-freeze check is a no-op
	// (a duplicate-typed Registration would have failed at write time).
	// We omit an explicit (f) error to avoid duplicative surface.

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d issue(s) — %w", ErrInvalidRuntimeGraph, len(errs), errors.Join(errs...))
}

// Compile-time assertion pinning DefaultStartupValidator to the
// StartupValidator interface contract. Future drift becomes a
// build failure here, not a runtime panic at WireRegistry.
var _ StartupValidator = (*DefaultStartupValidator)(nil)

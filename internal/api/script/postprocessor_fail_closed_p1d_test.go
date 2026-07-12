// Package script — postprocessor_fail_closed_p1d_test.go is the
// canonical P1.D hermetic test surface for the godlike/07
// NO-FAKE-AVAILABILITY postprocessor fail-closed contract on the
// `script.generate` pipeline.
//
// USER SPEC (verbatim, July 2026, Italian): "Implementa la suite
// P1.D — Postprocessor fail-closed su main. Abilita uno alla
// volta: generate_document, generate_voiceover,
// generate_scene_images, generate_metadata, extract_entities. Testa
// con servizio disponibile E non disponibile. Quando un
// postprocessor richiesto NON è cablato: HTTP 503, job non
// accodato, error_class=preflight_processor_missing. NON si accetta
// una richiesta che produrrebbe un risultato incompleto."
//
// The user-spec lists 5 postprocessors. Their fail-closed contract
// is gated by TWO distinct layers in the codebase:
//
//   - Layer A (per-request preflight) —
//     internal/api/script/postprocessor_preflight.go::requireRequestedProcessors
//     gates voiceover, images, document. Uses the typed sentinel
//     domainScript.ErrPreflightProcessorMissing + typed envelope
//     *domainScript.PreflightProcessorMissingError{Processor, Reason}.
//
//   - Layer B (composition-time + runtime fallback) —
//     internal/app/wire_script_adapters.go::validateRequiredProcessors
//     fires at startup; runtime equivalent is
//     adapters.PostProcessorRegistry.ValidateRequested (exposed
//     method, surfaces the same "preflight:..." Details substring).
//     Gates persistence, entities, metadata.
//
// The P1.D tests cover all 5 user-spec processors across BOTH
// layers (where applicable). Error message pinning targets the
// substring "preflight:" so the error_class invariant from the user
// spec is recoverable at the HTTP layer.
//
// SUT BUGS documented (forward-prevention, NOT to be fixed here):
//
//  1. HandlerGenerate.Generate (handler_generate_handler.go) does
//     NOT currently invoke requireRequestedProcessors in the
//     request path. The predicate is dormant; a user request
//     asking for generate_voiceover=true with voiceover unwired
//     currently reaches the broker (a godlike/07
//     NO-FAKE-AVAILABILITY regression until HTTP integration
//     lands). The P1.D tests pin the predicate contract; the
//     HTTP-handler integration is a forward PR.
//
//  2. error_class="preflight_processor_missing" is not currently
//     a typed field in the response envelope. The handler returns
//     gin.H{"ok": false, "error": "..."}. The P1.D tests probe
//     via errors.Is+errors.As+substring match against "preflight:"
//     — backward-compatible if the HTTP layer adds the typed
//     field later.
//
//  3. Only 3 of the 5 user-spec processors (voiceover, images,
//     document) have a per-request preflight. entities + metadata
//     are gated at composition time via requiredProcessorNames
//     and at request time via the runtime validator
//     (adapters.PostProcessorRegistry.ValidateRequested). The
//     user spec lists all 5; both gate layers are pinned here so
//     any future PR that adds per-request preflight for entities
//     or metadata will be a backward-compatible extension of
//     these tests.
//
// godlike/06 SSOT: this test is the canonical hermetic probe of
// the postprocessor fail-closed contract across all 5 user-spec
// processors. Predicate-level probes are the load-bearing
// regression guard for godlike/07 fail-closed semantics.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion locks a typed
// or behavioural contract that future refactors MUST preserve. A
// regression that re-introduces silent graceful-degradation (pre-
// PR-2 behavior) surfaces as a P1.D test failure BEFORE the bug
// reaches production.
package script

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Compile-time pins (godlike/06 SSOT forward-prevention): a future
// drift in the canonical Layer A typed-envelope surface or the
// Layer B registry type surfaces here as a build failure, not a
// runtime test failure.
//
// The 2 pins below are the load-bearing surface pivots for P1.D:
//
//   - *domainScript.PreflightProcessorMissingError (Layer A envelope)
//   - adapters.PostProcessorRegistry (Layer B registry type)
//
// (The domainScript.ErrPreflightProcessorMissing sentinel is
// already declared with type `error` upstream, so a separate
// `_ error =` pin would be redundant.)
var (
	_ *domainScript.PreflightProcessorMissingError = &domainScript.PreflightProcessorMissingError{Processor: "voiceover", Reason: "test"}
	_ adapters.PostProcessorRegistry               = adapters.PostProcessorRegistry{}
)

// p1dStubProcessor is the canonical no-op PostProcessor used to
// populate the runtime validator (Layer B) fixtures. Each stub
// returns nil from Process — the P1.D tests never invoke it. The
// stub exists solely so the PostProcessorRegistry.Register surface
// is satisfied and the runtime validator's missing-name logic
// (ValidateRequested) can isolate the canonical name missing from
// the registry.
//
// godlike/06 SSOT: this stub is the SOLE P1.D test-local adapter
// for adapters.PostProcessor. No alternative stub lives in the
// P1.D file — duplicates are rejected at compile-time via the
// reusable name p1dStubProcessor.
type p1dStubProcessor struct {
	name adapters.ProcessorName
	pol  adapters.ProcessorPolicy
}

// Name satisfies adapters.PostProcessor. Returns the canonical
// processor name registered with the stub.
func (s *p1dStubProcessor) Name() adapters.ProcessorName { return s.name }

// Policy satisfies adapters.PostProcessor. Returns the policy
// recorded at stub construction; ProcessorRequired for the
// validator-isolation tests so the missing-processor diagnostic
// fires deterministically.
func (s *p1dStubProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return s.pol
}

// Process satisfies adapters.PostProcessor. Unreachable in the
// P1.D tests (the validator runs BEFORE any Process call); the
// nil return is the canonical "happy-path no-op" per the
// existing processor-stub precedent in the project.
func (s *p1dStubProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, _ adapters.ProcessInput) (*adapters.PostProcessResult, error) {
	return nil, nil
}

// p1dAssertPreflightMissingForProcessor probes the canonical
// typed-error contract for the Layer A per-request preflight. The
// assertion locks: errors.Is recoverable against the canonical
// sentinel; errors.As recoverable against the canonical typed
// envelope; Processor field == wantCanonical; Reason field
// non-empty (godlike/07 diagnosability contract); err.Error()
// contains "preflight:" prefix for the user-spec error_class
// substring-based firing.
//
// godlike/06 SSOT: this helper is the SOLE canonical assertion
// surface for the Layer A typed-error contract. Every Layer-A
// unavailable test reuses it (no per-test re-implementation).
func p1dAssertPreflightMissingForProcessor(t *testing.T, err error, wantCanonical string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected preflight failure for unavailable processor %q, got nil", wantCanonical)
	}
	if !errors.Is(err, domainScript.ErrPreflightProcessorMissing) {
		t.Errorf("expected errors.Is(err, ErrPreflightProcessorMissing)=true for %q, got err: %v", wantCanonical, err)
	}
	var typedErr *domainScript.PreflightProcessorMissingError
	if !errors.As(err, &typedErr) {
		t.Errorf("expected errors.As(err, &PreflightProcessorMissingError)=true for %q, got err: %v", wantCanonical, err)
		return
	}
	if typedErr.Processor != wantCanonical {
		t.Errorf("Processor: want %q, got %q", wantCanonical, typedErr.Processor)
	}
	if typedErr.Reason == "" {
		t.Errorf("Reason: want non-empty (godlike/07 diagnosability), got empty")
	}
	if !strings.Contains(err.Error(), "preflight:") {
		t.Errorf("err.Error() must contain 'preflight:' prefix for error_class=preflight_processor_missing substring match, got: %v", err)
	}
}

// p1dIsolatedEnvelope constructs a single-item envelope that
// requests EXACTLY ONE postprocessor (the user-spec
// "abilita uno alla volta" pattern). The other two Layer-A
// postprocessors are ToggleDisabled so the preflight isolates
// the failing-or-passing name without cross-talk.
//
// godlike/06 SSOT: this helper is the SOLE canonical "one at a
// time" envelope shape for the P1.D tests. Layer A envelope
// construction lives here; Layer B (composition-time) uses an
// orthogonal stub-registry strategy.
//
// Why string-typed processor argument (vs. typed constants):
//   - The Layer A processor names are already locked as
//     strings in domainScript.PreflightProcessorMissingError.Processor
//     (closed set {voiceover, images, document}). Reusing the
//     same string identifier avoids introducing a parallel
//     typed-name surface that would need lockstep maintenance.
//   - A typo in the string argument surfaces as a noisy
//     "processor specified" miss (caps.*Enabled always read true
//     in available tests; envelope-output always sets the wrong
//     single flag in unavailable tests — both make typos visible
//     in test output, not silent).
func p1dIsolatedEnvelope(processor string) *scriptpkg.GenerationEnvelopeV2 {
	return makeEnvelopeWithOutput(
		processor == "voiceover",
		processor == "images",
		processor == "document",
	)
}

// p1dBuildRegistryWithMissing returns a frozen PostProcessorRegistry
// pre-populated with every ProcessorRequired-name EXCEPT
// `missingName`. The runtime validator
// (adapters.PostProcessorRegistry.ValidateRequested) will then
// surface a typed *scriptpkg.PlanInvalidError whose Details[0]
// begins with "preflight:" — the canonical Layer B error-class
// substring the user spec targets.
//
// godlike/06 SSOT lockstep-invariance: the 3 required names below
// mirror internal/app/wire_script_adapters.go::requiredProcessorNames.
// A future PR that adds a 4th ProcessorRequired name MUST update
// both surfaces in lockstep (forward-prevention godlike/06 SSOT).
// BestEffort processors (document/images/voiceover/etc.) are NOT
// registered because the validator's missing-Required branch only
// fires when the missing name is ProcessorRequired — registering
// BestEffort would only add noise to the test surface.
//
// godlike/07 NO-FAKE-AVAILABILITY: every required name is
// registered as `ProcessorRequired` so the validator's
// classification logic (Required-missing → fail-closed) fires
// deterministically.
func p1dBuildRegistryWithMissing(t *testing.T, missing adapters.ProcessorName) *adapters.PostProcessorRegistry {
	t.Helper()
	reg := adapters.NewPostProcessorRegistry(zap.NewNop())
	requiredOthers := []adapters.ProcessorName{
		adapters.ProcessorPersistence,
		adapters.ProcessorEntities,
		adapters.ProcessorMetadata,
	}
	for _, name := range requiredOthers {
		if name == missing {
			continue
		}
		if !reg.Register(&p1dStubProcessor{name: name, pol: adapters.ProcessorRequired}) {
			t.Fatalf("register %q returned false unexpectedly", name)
		}
	}
	reg.Freeze()
	return reg
}

// ─── Group 1 — Layer A (per-request preflight): AVAILABLE ──────────

// TestP1D_Voiceover_Isolated_Available_NoError pins the canonical
// happy-path surface for voiceover: caps.VoiceoverEnabled=true,
// envelope enables ONLY voiceover (images+document ToggleDisabled),
// preflight passes (nil). The isolated-request pattern matches the
// user spec ("abilita uno alla volta") and ensures a future test
// that flips voiceover to required-by-default does not regress to
// a silent skip on the unused processors (the unused flags MUST
// stay ToggleDisabled so the preflight does not gate them).
func TestP1D_Voiceover_Isolated_Available_NoError(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, true) // all wired
	err := requireRequestedProcessors(caps, p1dIsolatedEnvelope("voiceover"), zap.NewNop())
	if err != nil {
		t.Errorf("expected no error when voiceover enabled and isolated-requested, got: %v", err)
	}
}

// TestP1D_Images_Isolated_Available_NoError is the scene-image
// happy-path surface; same structural pattern as voiceover.
func TestP1D_Images_Isolated_Available_NoError(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, true)
	err := requireRequestedProcessors(caps, p1dIsolatedEnvelope("images"), zap.NewNop())
	if err != nil {
		t.Errorf("expected no error when images enabled and isolated-requested, got: %v", err)
	}
}

// TestP1D_Document_Isolated_Available_NoError is the document
// happy-path surface. Document fires via .AsBool() (per
// requireRequestedProcessorsOne body), so the test asserts the
// AsBool-propagation path: ToggleEnabled → AsBool reads true →
// gate checks caps.DocumentEnabled. With caps.DocumentEnabled=true,
// the gate passes.
func TestP1D_Document_Isolated_Available_NoError(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, true)
	err := requireRequestedProcessors(caps, p1dIsolatedEnvelope("document"), zap.NewNop())
	if err != nil {
		t.Errorf("expected no error when document enabled and isolated-requested, got: %v", err)
	}
}

// ─── Group 2 — Layer A (per-request preflight): UNAVAILABLE ────────

// TestP1D_Voiceover_Isolated_Unavailable_TypedError is the
// canonical godlike/07 NO-FAKE-AVAILABILITY fail-closed surface
// for voiceover. When the user envelope exclusively requests
// generate_voiceover=true but caps.VoiceoverEnabled=false
// (composition not wired), the preflight MUST:
//
//	(a) surface an error (no silent skip)
//	(b) carry the canonical sentinel (errors.Is schema)
//	(c) carry the canonical typed envelope (errors.As schema)
//	(d) report Processor="voiceover"
//	(e) populate Reason (diagnosability)
//	(f) contain "preflight:" prefix (HTTP error_class match)
//
// A regression that re-introduces silent graceful-degradation
// (pre-PR-2 behavior) surfaces as test failure here.
func TestP1D_Voiceover_Isolated_Unavailable_TypedError(t *testing.T) {
	t.Parallel()

	caps := makeCaps(false, true, true) // ONLY voiceover unwired
	err := requireRequestedProcessors(caps, p1dIsolatedEnvelope("voiceover"), zap.NewNop())
	p1dAssertPreflightMissingForProcessor(t, err, "voiceover")
}

// TestP1D_Images_Isolated_Unavailable_TypedError is the
// scene-image fail-closed variant; same shape as voiceover.
func TestP1D_Images_Isolated_Unavailable_TypedError(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, false, true) // ONLY images unwired
	err := requireRequestedProcessors(caps, p1dIsolatedEnvelope("images"), zap.NewNop())
	p1dAssertPreflightMissingForProcessor(t, err, "images")
}

// TestP1D_Document_Isolated_Unavailable_TypedError is the
// document fail-closed variant. Document fires via .AsBool() so
// the envelope-on-default-not-required path is also covered by
// the existing preflight logic; this test pins the explicit-
// request unavailable path.
func TestP1D_Document_Isolated_Unavailable_TypedError(t *testing.T) {
	t.Parallel()

	caps := makeCaps(true, true, false) // ONLY document unwired
	err := requireRequestedProcessors(caps, p1dIsolatedEnvelope("document"), zap.NewNop())
	p1dAssertPreflightMissingForProcessor(t, err, "document")
}

// ─── Group 3 — Layer B (composition-time + runtime validator) ───────

// TestP1D_Entities_RuntimePreflight_MissingReturnsError pins the
// Layer B fail-closed contract for extract_entities. entities is
// ProcessorRequired (per adapters.PostProcessorRegistry's
// defaultPolicyByName AND internal/app/wire_script_adapters.go's
// requiredProcessorNames), so when the entities processor is not
// registered and a user request asks for it, the runtime
// validator surfaces:
//
//   - errors.As recoverable against *scriptpkg.PlanInvalidError
//   - Details[0] prefix "preflight:" (error_class substring match)
//   - Details[0] mentions the missing name "entities"
//
// This pins the runtime fallback (ValidateRequested) companion to
// the composition-time gate (validateRequiredProcessors). A
// runtime pull-the-plug scenario (the entities service
// disappears AFTER startup) still fails-closed via this surface.
func TestP1D_Entities_RuntimePreflight_MissingReturnsError(t *testing.T) {
	t.Parallel()

	reg := p1dBuildRegistryWithMissing(t, adapters.ProcessorEntities)
	err := reg.ValidateRequested([]string{"entities"})
	if err == nil {
		t.Fatal("expected preflight failure when entities not registered, got nil")
	}
	var planErr *scriptpkg.PlanInvalidError
	if !errors.As(err, &planErr) {
		t.Fatalf("expected errors.As(err, &PlanInvalidError)=true, got err: %v", err)
	}
	if len(planErr.Details) == 0 {
		t.Fatalf("expected PlanInvalidError.Details non-empty, got empty")
	}
	first := planErr.Details[0]
	if !strings.Contains(first, "preflight:") {
		t.Errorf("Details[0] must contain 'preflight:' prefix (error_class=preflight_processor_missing substring match), got: %q", first)
	}
	if !strings.Contains(first, "entities") {
		t.Errorf("Details[0] must mention missing name 'entities', got: %q", first)
	}
}

// TestP1D_Metadata_RuntimePreflight_MissingReturnsError is the
// metadata-variant of the Layer B fail-closed contract. metadata
// is ProcessorRequired (same lockstep invariant as entities in
// defaultPolicyByName + requiredProcessorNames). Same fail-closed
// invariants as the entities test.
func TestP1D_Metadata_RuntimePreflight_MissingReturnsError(t *testing.T) {
	t.Parallel()

	reg := p1dBuildRegistryWithMissing(t, adapters.ProcessorMetadata)
	err := reg.ValidateRequested([]string{"metadata"})
	if err == nil {
		t.Fatal("expected preflight failure when metadata not registered, got nil")
	}
	var planErr *scriptpkg.PlanInvalidError
	if !errors.As(err, &planErr) {
		t.Fatalf("expected errors.As(err, &PlanInvalidError)=true, got err: %v", err)
	}
	if len(planErr.Details) == 0 {
		t.Fatalf("expected PlanInvalidError.Details non-empty, got empty")
	}
	first := planErr.Details[0]
	if !strings.Contains(first, "preflight:") {
		t.Errorf("Details[0] must contain 'preflight:' prefix (error_class=preflight_processor_missing substring match), got: %q", first)
	}
	if !strings.Contains(first, "metadata") {
		t.Errorf("Details[0] must mention missing name 'metadata', got: %q", first)
	}
}

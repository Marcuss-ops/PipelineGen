// Package job — startup_validator_test.go (P0 Commit 3, July 2026).
//
// Tests for DefaultStartupValidator.ValidateRuntimeGraph. Each test
// locks one of the 6 P0 §4.5 checks (a..f) named in the file header
// of startup_validator.go.
package job

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// validFullyWiredDef is the canonical "all-pass" JobDefinition
// for the validator tests: creator-enabled, has a handler, has
// both codecs, declares RequiredCapabilities, and has RequireManifest=true
// so the manifest-required-without-result-codec check is exercised
// when ResultCodec is nil.
func validFullyWiredDef(t *testing.T, jobType string) JobDefinition {
	t.Helper()
	return JobDefinition{
		Type:           jobType,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        10 * time.Minute,
		PayloadCodec:   NewCodecDescriptorMarker("pipelinegen.payload."+jobType+".v1", jobType),
		ResultCodec:    NewCodecDescriptorMarker("pipelinegen.result."+jobType+".v1", jobType),
		ArtifactPolicy: ArtifactPolicy{
			ProducesArtifacts: true,
			RequireManifest:   true,
		},
		RequiredCapabilities: []Capability{
			Capability("cap." + jobType),
		},
	}
}

// allCanonicalFamiliesScene returns a fully-wired registry containing
// the 4 canonical families wired with handler. Used for the all-pass
// happy test.
func allCanonicalFamiliesScene(t *testing.T) CompiledJobRegistry {
	t.Helper()
	r := NewMutableJobRegistry()
	for _, def := range CanonicalJobDefinitions {
		if err := r.RegisterDefinition(def); err != nil {
			t.Fatalf("RegisterDefinition(%s): %v", def.Type, err)
		}
		if err := r.BindHandler(def.Type, dummyHandler); err != nil {
			t.Fatalf("BindHandler(%s): %v", def.Type, err)
		}
	}
	compiled, err := r.Freeze()
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	return compiled
}

// ── (a) workflow resolvability ────────────────────────────────────────

func TestStartupValidator_NilRegistry(t *testing.T) {
	v := DefaultStartupValidator{}
	err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: nil})
	if err == nil {
		t.Fatal("ValidateRuntimeGraph(nil) should fail")
	}
	if !errors.Is(err, ErrInvalidRuntimeGraph) {
		t.Errorf("error should wrap ErrInvalidRuntimeGraph, got %v", err)
	}
}

func TestStartupValidator_AllPass(t *testing.T) {
	compiled := allCanonicalFamiliesScene(t)
	v := DefaultStartupValidator{}

	// Workflow = the canonical 4 family refs.
	wf := []string{
		TypeScriptGenerate, TypeImagesGenerate, TypeDocumentGenerate, TypeAssetsResolve,
	}
	err := v.ValidateRuntimeGraph(StartupValidationInput{
		Registry: compiled,
		Workflow: wf,
	})
	if err != nil {
		t.Fatalf("ValidateRuntimeGraph (all canonical wired): %v", err)
	}
}

func TestStartupValidator_EmptyWorkflow_Valid(t *testing.T) {
	compiled := allCanonicalFamiliesScene(t)
	v := DefaultStartupValidator{}
	if err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled, Workflow: nil}); err != nil {
		t.Errorf("ValidateRuntimeGraph (empty workflow): %v", err)
	}
}

// ── (a) workflow resolvability — broken ──────────────────────────────

func TestStartupValidator_WorkflowMissing(t *testing.T) {
	compiled := allCanonicalFamiliesScene(t)
	v := DefaultStartupValidator{}

	// Inject a workflow reference that resolves nothing.
	wf := []string{TypeScriptGenerate, "workflow.does.not.exist"}
	err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled, Workflow: wf})
	if err == nil {
		t.Fatal("ValidateRuntimeGraph should fail when workflow references unknown")
	}
	if !errors.Is(err, ErrInvalidRuntimeGraph) {
		t.Errorf("error should wrap ErrInvalidRuntimeGraph, got %v", err)
	}
	if !strings.Contains(err.Error(), "(a) workflow resolvability") {
		t.Errorf("error should attribute to (a), got %v", err)
	}
	if !strings.Contains(err.Error(), "workflow.does.not.exist") {
		t.Errorf("error should mention missing type, got %v", err)
	}
}

// ── (b) creator-enabled-without-handler ──────────────────────────────

func TestStartupValidator_CreatorEnabledNoHandler(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.no.handler")
	// Register definition but DO NOT bind a handler.
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, _ := r.Freeze()

	v := DefaultStartupValidator{}
	err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled})
	if err == nil {
		t.Fatal("ValidateRuntimeGraph should fail when creator-enabled has no handler")
	}
	if !errors.Is(err, ErrInvalidRuntimeGraph) {
		t.Errorf("error should wrap ErrInvalidRuntimeGraph, got %v", err)
	}
	if !strings.Contains(err.Error(), "(b) creator-enabled-without-handler") {
		t.Errorf("error should attribute to (b), got %v", err)
	}
	if !strings.Contains(err.Error(), "test.no.handler") {
		t.Errorf("error should mention the unbound type, got %v", err)
	}
}

// (b) canonical regression: sender-only jobs MUST NOT be required
// to have a handler (they run on Sender by definition).
func TestStartupValidator_SenderOnlyNoHandler_OK(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.sender.only")
	def.ExecutionClass = ExecutionSenderOnly
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, _ := r.Freeze()

	v := DefaultStartupValidator{}
	if err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled}); err != nil {
		t.Errorf("sender-only without handler is valid (creator cannot claim it): %v", err)
	}
}

// ── (c) manifest-required-without-result-codec ───────────────────────

func TestStartupValidator_RequireManifestNoResultCodec(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.manifest.required")
	// Bind handler so check (b) passes; nuke ResultCodec to exercise (c).
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.manifest.required", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
	// Reset the registry to clear the ResultCodec too.
	r2 := NewMutableJobRegistry()
	defNoResult := def
	defNoResult.ResultCodec = nil
	if err := r2.RegisterDefinition(defNoResult); err != nil {
		t.Fatalf("RegisterDefinition(defNoResult): %v", err)
	}
	if err := r2.BindHandler("test.manifest.required", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
	compiled, _ := r2.Freeze()

	v := DefaultStartupValidator{}
	err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled})
	if err == nil {
		t.Fatal("ValidateRuntimeGraph should fail when RequireManifest=true + ResultCodec=nil")
	}
	if !errors.Is(err, ErrInvalidRuntimeGraph) {
		t.Errorf("error should wrap ErrInvalidRuntimeGraph, got %v", err)
	}
	if !strings.Contains(err.Error(), "(c) manifest-required-without-result-codec") {
		t.Errorf("error should attribute to (c), got %v", err)
	}
}

// ── (d) codec-schema-version-present ─────────────────────────────────

func TestStartupValidator_SchemaVersionEmpty_FailsAtWrite(t *testing.T) {
	// This test pins that the write-time check (RegisterDefinition)
	// catches empty-SchemaVersion BEFORE the post-freeze validator
	// sees the bad spec. Setting up a registry with an empty
	// SchemaVersion directly is impossible because RegisterDefinition
	// rejects it; so this test confirms the implicit invariant.
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.bad.schema")
	def.PayloadCodec = NewCodecDescriptorMarker("", def.Type)
	err := r.RegisterDefinition(def)
	if err == nil {
		t.Fatal("RegisterDefinition should fail when PayloadCodec.SchemaVersion is empty")
	}
	if !errors.Is(err, ErrSchemaVersionEmpty) {
		t.Errorf("error should wrap ErrSchemaVersionEmpty, got %v", err)
	}
}

// ── (e) capability-derivable ─────────────────────────────────────────

func TestStartupValidator_CapabilityMissing_CreatorEnabled(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.no.caps")
	def.RequiredCapabilities = nil // empty
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.no.caps", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
	compiled, _ := r.Freeze()

	v := DefaultStartupValidator{}
	err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled})
	if err == nil {
		t.Fatal("ValidateRuntimeGraph should fail when creator-enabled has zero RequiredCapabilities")
	}
	if !strings.Contains(err.Error(), "(e) capability-derivable") {
		t.Errorf("error should attribute to (e), got %v", err)
	}
	if !strings.Contains(err.Error(), "test.no.caps") {
		t.Errorf("error should mention the type, got %v", err)
	}
}

func TestStartupValidator_SenderOnlyNoCaps_OK(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.sender.no.caps")
	def.ExecutionClass = ExecutionSenderOnly
	def.RequiredCapabilities = nil
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, _ := r.Freeze()

	v := DefaultStartupValidator{}
	if err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled}); err != nil {
		t.Errorf("sender-only without RequiredCapabilities is valid (creator cannot claim): %v", err)
	}
}

// ── (f) duplicates — already enforced at write time ──────────────────

func TestMutableJobRegistry_DuplicateDefinition_FailsAtWrite(t *testing.T) {
	// Pin the write-time duplicate-rejection invariant: a definition
	// cannot be registered twice, so the post-Freeze (f) check has
	// nothing to find. The test asserts that RegisterDefinition
	// surfaces ErrDuplicateType and the frozen registry has only
	// one entry for the duplicate type.
	r := NewMutableJobRegistry()
	def := validFullyWiredDef(t, "test.duplicate")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition (first): %v", err)
	}
	err := r.RegisterDefinition(def)
	if !errors.Is(err, ErrDuplicateType) {
		t.Fatalf("RegisterDefinition (duplicate) should wrap ErrDuplicateType, got %v", err)
	}
	compiled, _ := r.Freeze()
	all := compiled.AllDefinitions()
	count := 0
	for _, d := range all {
		if d.Type == "test.duplicate" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate definitions count = %d, want exactly 1", count)
	}
}

// ── Multi-error aggregation ──────────────────────────────────────────

func TestStartupValidator_MultipleErrorsAggregated(t *testing.T) {
	// A registry with TWO creator-enabled definitions, neither
	// bound: validator should surface TWO (b) errors. errors.Join
	// groups them into a single non-nil error.
	r := NewMutableJobRegistry()
	def1 := validFullyWiredDef(t, "test.unbound.1")
	def2 := validFullyWiredDef(t, "test.unbound.2")
	for _, d := range []JobDefinition{def1, def2} {
		if err := r.RegisterDefinition(d); err != nil {
			t.Fatalf("RegisterDefinition(%s): %v", d.Type, err)
		}
		// NO BindHandler: both surface check (b).
	}
	compiled, _ := r.Freeze()

	v := DefaultStartupValidator{}
	err := v.ValidateRuntimeGraph(StartupValidationInput{Registry: compiled})
	if err == nil {
		t.Fatal("ValidateRuntimeGraph should fail when multiple creator-enabled types are unbound")
	}
	if !errors.Is(err, ErrInvalidRuntimeGraph) {
		t.Errorf("error should wrap ErrInvalidRuntimeGraph, got %v", err)
	}
	// Both types must appear in the joined error string.
	if !strings.Contains(err.Error(), "test.unbound.1") {
		t.Errorf("error should mention test.unbound.1, got %v", err)
	}
	if !strings.Contains(err.Error(), "test.unbound.2") {
		t.Errorf("error should mention test.unbound.2, got %v", err)
	}
}

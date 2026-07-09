// Package job — registry_test.go (P0 Commit 3, July 2026).
//
// Round-trip + Validate + Freeze tests for JobRegistry +
// StartupValidator. Locks the §4.7 test list (#1–#6, #8–#9)
// plus the freeze-contract tests (#6 hardens via duplicate
// mutation attempts).
package job

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// codecFor returns the canonical test codec for the given job type.
// Uses NewCodecDescriptorMarker (C3 marker helper) so the canonical
// literals under test are wired through the production codec-authoring
// path. Returns the marker struct value (CodecDescriptorMarker) so it
// is assignable to BOTH PayloadCodec AND ResultCodec fields.
func codecFor(jobType string) CodecDescriptorMarker {
	return NewCodecDescriptorMarker("pipelinegen.payload."+jobType+".v1", jobType)
}

func resultCodecFor(jobType string) CodecDescriptorMarker {
	return NewCodecDescriptorMarker("pipelinegen.result."+jobType+".v1", jobType)
}

// validDef builds a canonical happy-path JobDefinition for tests.
// Compose with field-set helpers for failure-case testing.
func validDef(jobType, queue string) JobDefinition {
	return JobDefinition{
		Type:           jobType,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          queue,
		Timeout:        10 * time.Minute,
		PayloadCodec:   codecFor(jobType),
		ResultCodec:    resultCodecFor(jobType),
		ArtifactPolicy: ArtifactPolicy{ProducesArtifacts: true, RequireManifest: true},
		RequiredCapabilities: []Capability{
			Capability("cap." + jobType),
		},
	}
}

// dummyHandler returns a no-op JobHandlerFunc for tests. The return
// shape matches JobHandlerFunc (ctx, *Job, payload any) (result any, err).
func dummyHandler(_ context.Context, _ *Job, _ any) (any, error) {
	return nil, nil
}

// ── MutableJobRegistry tests (§4.7 #1..6) ─────────────────────────────

func TestMutableJobRegistry_RegisterDefinitionHappyPath(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.happy", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
}

func TestMutableJobRegistry_RegisterDefinitionDuplicate(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.dup", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition (first): %v", err)
	}
	err := r.RegisterDefinition(def)
	if err == nil {
		t.Fatal("RegisterDefinition (duplicate) should fail")
	}
	if !errors.Is(err, ErrDuplicateType) {
		t.Errorf("error should wrap ErrDuplicateType, got %v", err)
	}
}

func TestMutableJobRegistry_RegisterDefinitionInvalid(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.invalid", "default")
	def.Type = "" // breaks Validate()
	err := r.RegisterDefinition(def)
	if err == nil {
		t.Fatal("RegisterDefinition (empty Type) should fail")
	}
	if !errors.Is(err, ErrInvalidJob) {
		t.Errorf("error should wrap ErrInvalidJob, got %v", err)
	}
}

func TestMutableJobRegistry_RegisterDefinitionSchemaVersionEmpty(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.empty", "default")
	// Hand-build a marker with empty schema to bypass NewCodecDescriptorMarker's guard.
	def.PayloadCodec = NewCodecDescriptorMarker("", "test.empty")
	err := r.RegisterDefinition(def)
	if err == nil {
		t.Fatal("RegisterDefinition (empty SchemaVersion) should fail")
	}
	if !errors.Is(err, ErrSchemaVersionEmpty) {
		t.Errorf("error should wrap ErrSchemaVersionEmpty, got %v", err)
	}
}

func TestMutableJobRegistry_RegisterDefinitionAfterFreeze(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.frozen", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition (pre-freeze): %v", err)
	}
	if _, err := r.Freeze(); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	err := r.RegisterDefinition(def)
	if err == nil {
		t.Fatal("RegisterDefinition (post-freeze) should fail")
	}
	if !errors.Is(err, ErrRegistryFrozen) {
		t.Errorf("error should wrap ErrRegistryFrozen, got %v", err)
	}
}

func TestMutableJobRegistry_BindHandlerHappyPath(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.bind", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.bind", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
}

func TestMutableJobRegistry_BindHandlerUnknownJobType(t *testing.T) {
	r := NewMutableJobRegistry()
	err := r.BindHandler("test.undefined", dummyHandler)
	if err == nil {
		t.Fatal("BindHandler (unknown type) should fail")
	}
	if !errors.Is(err, ErrUnknownJobType) {
		t.Errorf("error should wrap ErrUnknownJobType, got %v", err)
	}
}

func TestMutableJobRegistry_BindHandlerDuplicate(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.bind.dup", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.bind.dup", dummyHandler); err != nil {
		t.Fatalf("BindHandler (first): %v", err)
	}
	err := r.BindHandler("test.bind.dup", dummyHandler)
	if err == nil {
		t.Fatal("BindHandler (duplicate) should fail")
	}
	if !errors.Is(err, ErrDuplicateHandler) {
		t.Errorf("error should wrap ErrDuplicateHandler, got %v", err)
	}
}

func TestMutableJobRegistry_BindHandlerAfterFreeze(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.bind.frozen", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if _, err := r.Freeze(); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	err := r.BindHandler("test.bind.frozen", dummyHandler)
	if err == nil {
		t.Fatal("BindHandler (post-freeze) should fail")
	}
	if !errors.Is(err, ErrRegistryFrozen) {
		t.Errorf("error should wrap ErrRegistryFrozen, got %v", err)
	}
}

func TestMutableJobRegistry_FreezeHappyPath(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.freeze.ok", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.freeze.ok", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
	compiled, err := r.Freeze()
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if compiled == nil {
		t.Fatal("Freeze returned nil CompiledJobRegistry on success")
	}
	if !compiled.IsFrozen() {
		t.Error("CompiledJobRegistry.IsFrozen() should be true")
	}
}

func TestMutableJobRegistry_FreezeDouble(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.freeze.dup", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if _, err := r.Freeze(); err != nil {
		t.Fatalf("Freeze (first): %v", err)
	}
	_, err := r.Freeze()
	if err == nil {
		t.Fatal("Freeze (double) should fail")
	}
	if !errors.Is(err, ErrRegistryFrozen) {
		t.Errorf("error should wrap ErrRegistryFrozen, got %v", err)
	}
}

// ── CompiledJobRegistry tests ────────────────────────────────────────

func TestCompiledJobRegistry_Definition(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.definition", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, _ := r.Freeze()

	got, ok := compiled.Definition("test.definition")
	if !ok {
		t.Fatal("Definition should return true for registered type")
	}
	if got.Type != "test.definition" {
		t.Errorf("Type = %q, want test.definition", got.Type)
	}

	_, ok = compiled.Definition("test.unknown")
	if ok {
		t.Error("Definition should return false for unregistered type")
	}
}

func TestCompiledJobRegistry_Handler(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.handler", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.handler", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
	compiled, _ := r.Freeze()

	got, ok := compiled.Handler("test.handler")
	if !ok {
		t.Fatal("Handler should return true for bound type")
	}
	if got == nil {
		t.Error("Handler should return non-nil function")
	}
}

func TestCompiledJobRegistry_HasHandler(t *testing.T) {
	r := NewMutableJobRegistry()
	def := validDef("test.has", "default")
	if err := r.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if err := r.BindHandler("test.has", dummyHandler); err != nil {
		t.Fatalf("BindHandler: %v", err)
	}
	compiled, _ := r.Freeze()

	if !compiled.HasHandler("test.has") {
		t.Error("HasHandler should be true for bound type")
	}
	if compiled.HasHandler("test.unbound") {
		t.Error("HasHandler should be false for unbound type")
	}
}

func TestCompiledJobRegistry_AllDefinitions(t *testing.T) {
	r := NewMutableJobRegistry()
	for _, name := range []string{"test.alpha", "test.beta", "test.gamma"} {
		d := validDef(name, "default")
		if err := r.RegisterDefinition(d); err != nil {
			t.Fatalf("RegisterDefinition(%s): %v", name, err)
		}
	}
	compiled, _ := r.Freeze()

	all := compiled.AllDefinitions()
	if len(all) != 3 {
		t.Errorf("AllDefinitions length = %d, want 3", len(all))
	}
	// Sorted-by-Type assertion.
	if all[0].Type != "test.alpha" || all[1].Type != "test.beta" || all[2].Type != "test.gamma" {
		t.Errorf("AllDefinitions not sorted by Type: %v", []string{all[0].Type, all[1].Type, all[2].Type})
	}
}

func TestCompiledJobRegistry_CreatorJobTypes_SenderOnlyExcluded(t *testing.T) {
	r := NewMutableJobRegistry()
	creator := validDef("test.creator", "default")
	creator.ExecutionClass = ExecutionCreatorAllowed
	sender := validDef("test.sender", "default")
	sender.ExecutionClass = ExecutionSenderOnly

	if err := r.RegisterDefinition(creator); err != nil {
		t.Fatalf("RegisterDefinition(creator): %v", err)
	}
	if err := r.RegisterDefinition(sender); err != nil {
		t.Fatalf("RegisterDefinition(sender): %v", err)
	}
	compiled, _ := r.Freeze()

	types := compiled.CreatorJobTypes()
	if len(types) != 1 || types[0] != "test.creator" {
		t.Errorf("CreatorJobTypes = %v, want [test.creator]", types)
	}
}

func TestCompiledJobRegistry_CreatorCapabilities_DedupAndSort(t *testing.T) {
	r := NewMutableJobRegistry()

	// Two creator definitions with overlapping capabilities.
	d1 := validDef("test.cap1", "default")
	d1.RequiredCapabilities = []Capability{"shared", "alpha"}
	if err := r.RegisterDefinition(d1); err != nil {
		t.Fatalf("RegisterDefinition(d1): %v", err)
	}

	d2 := validDef("test.cap2", "default")
	d2.RequiredCapabilities = []Capability{"shared", "beta"}
	if err := r.RegisterDefinition(d2); err != nil {
		t.Fatalf("RegisterDefinition(d2): %v", err)
	}

	// One sender definition that should be excluded.
	ds := validDef("test.send", "default")
	ds.ExecutionClass = ExecutionSenderOnly
	ds.RequiredCapabilities = []Capability{"sender-only"}
	if err := r.RegisterDefinition(ds); err != nil {
		t.Fatalf("RegisterDefinition(ds): %v", err)
	}

	compiled, _ := r.Freeze()

	caps := compiled.CreatorCapabilities()
	want := []Capability{"alpha", "beta", "shared"}
	if len(caps) != len(want) {
		t.Fatalf("CreatorCapabilities length = %d, want %d (got %v)", len(caps), len(want), caps)
	}
	for i := range want {
		if caps[i] != want[i] {
			t.Errorf("CreatorCapabilities[%d] = %q, want %q", i, caps[i], want[i])
		}
	}
}

func TestCompiledJobRegistry_ValidateWorkflow_Happy(t *testing.T) {
	r := NewMutableJobRegistry()
	d := validDef("test.workflow", "default")
	if err := r.RegisterDefinition(d); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, _ := r.Freeze()

	if err := compiled.ValidateWorkflow([]string{"test.workflow"}); err != nil {
		t.Errorf("ValidateWorkflow (known): %v", err)
	}
}

func TestCompiledJobRegistry_ValidateWorkflow_Missing(t *testing.T) {
	r := NewMutableJobRegistry()
	d := validDef("test.workflow.known", "default")
	if err := r.RegisterDefinition(d); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, _ := r.Freeze()

	err := compiled.ValidateWorkflow([]string{"test.workflow.known", "test.workflow.missing"})
	if err == nil {
		t.Fatal("ValidateWorkflow should fail when refs unknown job types")
	}
	if !strings.Contains(err.Error(), "test.workflow.missing") {
		t.Errorf("ValidateWorkflow error should mention missing type, got %v", err)
	}
	// Known type MUST NOT appear as missing from the aggregated message.
	if strings.Contains(err.Error(), "test.workflow.known") {
		t.Errorf("ValidateWorkflow error should NOT mention known types as missing")
	}
}

func TestCompiledJobRegistry_ValidateWorkflow_Empty(t *testing.T) {
	r := NewMutableJobRegistry()
	compiled, _ := r.Freeze() // empty registry
	if err := compiled.ValidateWorkflow(nil); err != nil {
		t.Errorf("ValidateWorkflow(empty): %v", err)
	}
	if err := compiled.ValidateWorkflow([]string{}); err != nil {
		t.Errorf("ValidateWorkflow([]): %v", err)
	}
}

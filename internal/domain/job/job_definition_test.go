// Package job — job_definition_test.go (P0 Commit 1, July 2026).
//
// Round-trip + Validate tests for JobDefinition / ExecutionClass /
// ArtifactPolicy. Locks the canonical shape of the four job families
// the workflow coordinator orchestrates post-cutover
// (script.generate, images.generate, document.generate, assets.resolve).
// The four canonical literal vars at the top of the file are the
// **anchor** for downstream commits: a future contributor adding a
// 5th job family reads the nearest canonical literal var and copies
// the shape.
package job

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// jobDefinitionTestCodec is a minimal stub satisfying the
// PayloadCodec + ResultCodec interfaces (introduced in P0
// Commit 2, elaborating the C1 CodecDescriptor marker with
// Encode/Decode bodies). Implementation is intentionally trivial
// (JSON-marshal round-trip) so canonical-literal tests don't
// rely on production codec behaviour.
type jobDefinitionTestCodec struct {
	jobType       string
	schemaVersion string
}

func (c jobDefinitionTestCodec) SchemaVersion() string { return c.schemaVersion }
func (c jobDefinitionTestCodec) JobType() string       { return c.jobType }

// EncodePayload — PayloadCodec body. Test-only identity round-trip.
func (c jobDefinitionTestCodec) EncodePayload(req any) (json.RawMessage, error) {
	return json.Marshal(req)
}

// DecodePayload — PayloadCodec body. Returns a map[string]any decoded
// from the canonical json bytes; tests assert by reading selected keys.
func (c jobDefinitionTestCodec) DecodePayload(raw json.RawMessage) (any, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// EncodeResult — ResultCodec body. Test-only identity round-trip.
func (c jobDefinitionTestCodec) EncodeResult(resp any) (json.RawMessage, error) {
	return json.Marshal(resp)
}

// DecodeResult — ResultCodec body. Mirrors DecodePayload.
func (c jobDefinitionTestCodec) DecodeResult(raw json.RawMessage) (any, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Compile-time assertions: jobDefinitionTestCodec implements BOTH
// PayloadCodec AND ResultCodec. A future commit that adds another
// method to either interface would break the assertion here FIRST,
// locking the keep-them-in-sync contract.
var (
	_ PayloadCodec = jobDefinitionTestCodec{}
	_ ResultCodec  = jobDefinitionTestCodec{}
)

// ── Canonical JobDefinition literals (single source of truth) ────────
//
// P0 Commit 3 (July 2026) NOTE: the canonical literals physically
// moved out of this test file into canonical_definitions.go as
// EXPORTED package-level vars (CanonicalScriptGenerate /
// CanonicalImagesGenerate / CanonicalDocumentGenerate /
// CanonicalAssetsResolve / CanonicalJobDefinitions) so the
// composition root (internal/app/registry.go::WireRegistry) can
// reference them at startup wiring time. Go's `_test.go` files
// are not importable from production code, so the production-
// visible surface requires a non-test home.
//
// The legacy lower-case aliases below preserve the existing
// test-side naming convention (canonicalScriptGenerate etc.)
// without rewriting every test function. They are TYPE ALIASES —
// zero-cost references to the exported Canonical* vars in
// canonical_definitions.go. A future field-additive change to
// JobDefinition will break the compile in canonical_definitions.go
// first (where the literals sit), not here. Auditing which var
// is canonical: canonical_definitions.go is; this file's locals
// are references only.
var canonicalScriptGenerate = CanonicalScriptGenerate
var canonicalImagesGenerate = CanonicalImagesGenerate
var canonicalDocumentGenerate = CanonicalDocumentGenerate
var canonicalAssetsResolve = CanonicalAssetsResolve
var canonicalFamilies = CanonicalJobDefinitions

// ── Per-family tests ─────────────────────────────────────────────────

// Test 1: script.generate. The canonical literal at the top of this
// file is the ANCHOR — copy its shape when adding a 5th workflow
// job family.
func TestJobDefinition_ScriptGenerate_LiteralCompiles(t *testing.T) {
	d := canonicalScriptGenerate
	if err := d.Validate(); err != nil {
		t.Fatalf("script.generate Validate: %v", err)
	}
	if d.Type != "script.generate" {
		t.Errorf("Type = %q, want %q", d.Type, "script.generate")
	}
	if d.ExecutionClass != ExecutionCreatorAllowed {
		t.Errorf("ExecutionClass = %q, want %q", d.ExecutionClass, ExecutionCreatorAllowed)
	}
	if d.PayloadCodec.JobType() != "script.generate" {
		t.Errorf("PayloadCodec.JobType = %q, want script.generate", d.PayloadCodec.JobType())
	}
	if d.ResultCodec.JobType() != "script.generate" {
		t.Errorf("ResultCodec.JobType = %q, want script.generate", d.ResultCodec.JobType())
	}
	if d.PayloadCodec.SchemaVersion() == "" {
		t.Error("PayloadCodec.SchemaVersion must be non-empty")
	}
	if d.ResultCodec.SchemaVersion() == "" {
		t.Error("ResultCodec.SchemaVersion must be non-empty")
	}
	if d.HandlerKey != "script.generate.handler" {
		t.Errorf("HandlerKey = %q, want script.generate.handler", d.HandlerKey)
	}
}

// Test 2: images.generate. Heavy queue, multi-artifact, capacity-2
// concurrency.
func TestJobDefinition_ImagesGenerate_LiteralCompiles(t *testing.T) {
	d := canonicalImagesGenerate
	if err := d.Validate(); err != nil {
		t.Fatalf("images.generate Validate: %v", err)
	}
	if d.Type != "images.generate" {
		t.Errorf("Type = %q, want %q", d.Type, "images.generate")
	}
	if d.ConcurrencyKey != "global_cap_2" {
		t.Errorf("ConcurrencyKey = %q, want global_cap_2", d.ConcurrencyKey)
	}
	if d.Queue != "heavy" {
		t.Errorf("Queue = %q, want heavy", d.Queue)
	}
}

// Test 3: document.generate. Default queue, single-file artefact.
func TestJobDefinition_DocumentGenerate_LiteralCompiles(t *testing.T) {
	d := canonicalDocumentGenerate
	if err := d.Validate(); err != nil {
		t.Fatalf("document.generate Validate: %v", err)
	}
	if d.Type != "document.generate" {
		t.Errorf("Type = %q, want %q", d.Type, "document.generate")
	}
	if d.Timeout != 15*time.Minute {
		t.Errorf("Timeout = %v, want 15m", d.Timeout)
	}
}

// Test 4: assets.resolve. Pure-data job: zero ArtifactPolicy = no
// files, no manifest.
func TestJobDefinition_AssetsResolve_LiteralCompiles(t *testing.T) {
	d := canonicalAssetsResolve
	if err := d.Validate(); err != nil {
		t.Fatalf("assets.resolve Validate: %v", err)
	}
	if d.Type != "assets.resolve" {
		t.Errorf("Type = %q, want %q", d.Type, "assets.resolve")
	}
	if d.ArtifactPolicy.ProducesArtifacts {
		t.Error("assets.resolve must set ProducesArtifacts=false (pure-data job)")
	}
	if d.ArtifactPolicy.RequireManifest {
		t.Error("assets.resolve must set RequireManifest=false (pure-data job)")
	}
}

// ── All-families Validate pass ──────────────────────────────────────
//
// A single test that re-runs Validate on all four canonical
// families — defensive against a future commit that tightens
// an invariant in a way the per-test Validate check would not
// catch (e.g. adds a new cross-literal invariant).
func TestJobDefinition_Validate_AllFamilies(t *testing.T) {
	for _, d := range canonicalFamilies {
		if err := d.Validate(); err != nil {
			t.Errorf("%q Validate failed: %v", d.Type, err)
		}
	}
}

// ── ExecutionClass.IsValid + String + canonical wire forms ──────────

func TestExecutionClass_IsValid(t *testing.T) {
	cases := []struct {
		in   ExecutionClass
		want bool
	}{
		{ExecutionSenderOnly, true},
		{ExecutionCreatorAllowed, true},
		{ExecutionCreatorOnly, true},
		{"", false},
		{"unknown", false},
		{"SenderOnly", false},        // canonical form is snake_case, not CamelCase
		{"sender-only", false},       // canonical form uses underscore, not hyphen
		{" creator_allowed ", false}, // whitespace-padded form is rejected
	}
	for _, c := range cases {
		if got := c.in.IsValid(); got != c.want {
			t.Errorf("IsValid(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestExecutionClass_WireAndString locks the canonical wire-form
// strings (sender_only, creator_allowed, creator_only) AND
// String() method behaviour in a single test. Renamed from the
// earlier pair (String + CanonicalWireForms) to avoid duplication.
func TestExecutionClass_WireAndString(t *testing.T) {
	want := []struct {
		c    ExecutionClass
		wire string
	}{
		{ExecutionSenderOnly, "sender_only"},
		{ExecutionCreatorAllowed, "creator_allowed"},
		{ExecutionCreatorOnly, "creator_only"},
	}
	for _, w := range want {
		if string(w.c) != w.wire {
			t.Errorf("canonical %s wire form = %q, want %q", w.wire, string(w.c), w.wire)
		}
		if w.c.String() != w.wire {
			t.Errorf("String() for %s = %q, want %q", w.wire, w.c.String(), w.wire)
		}
	}
}

// ── ArtifactPolicy.Validate ──────────────────────────────────────────

func TestArtifactPolicy_Validate_ZeroValueIsValid(t *testing.T) {
	p := ArtifactPolicy{}
	if err := p.Validate(); err != nil {
		t.Errorf("zero-value ArtifactPolicy should be valid (pure-data default), got: %v", err)
	}
}

func TestArtifactPolicy_Validate_RequireManifestWithoutProduce(t *testing.T) {
	p := ArtifactPolicy{RequireManifest: true}
	err := p.Validate()
	if err == nil {
		t.Fatal("RequireManifest=true without ProducesArtifacts=true must fail")
	}
	if !strings.Contains(err.Error(), "RequireManifest=true") || !strings.Contains(err.Error(), "ProducesArtifacts=false") {
		t.Errorf("error should mention both RequireManifest and ProducesArtifacts, got: %v", err)
	}
}

func TestArtifactPolicy_Validate_NegativeBounds(t *testing.T) {
	if err := (ArtifactPolicy{MaxArtifacts: -1}).Validate(); err == nil {
		t.Error("MaxArtifacts=-1 must fail")
	}
	if err := (ArtifactPolicy{MaxTotalBytes: -1}).Validate(); err == nil {
		t.Error("MaxTotalBytes=-1 must fail")
	}
	if err := (ArtifactPolicy{MaxArtifacts: 0, MaxTotalBytes: 0}).Validate(); err != nil {
		t.Errorf("zero bounds should be valid (zero = unbounded), got: %v", err)
	}
}

func TestArtifactPolicy_Validate_HappyCase(t *testing.T) {
	p := ArtifactPolicy{
		ProducesArtifacts: true,
		RequireManifest:   true,
		MaxArtifacts:      32,
		MaxTotalBytes:     1024 * 1024 * 1024,
	}
	if err := p.Validate(); err != nil {
		t.Errorf("happy-case ArtifactPolicy should be valid, got: %v", err)
	}
}

// ── JobDefinition.Validate rejections ────────────────────────────────

func TestJobDefinition_Validate_EmptyType(t *testing.T) {
	d := JobDefinition{
		Type:           "",
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        10 * time.Minute,
	}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "Type is empty") {
		t.Errorf("expected Type-is-empty error, got %v", err)
	}
}

func TestJobDefinition_Validate_WhitespaceType(t *testing.T) {
	d := JobDefinition{
		Type:           "   ",
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        10 * time.Minute,
	}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "Type is empty") {
		t.Errorf("expected Type-is-empty error for whitespace input, got %v", err)
	}
}

func TestJobDefinition_Validate_InvalidExecutionClass(t *testing.T) {
	d := JobDefinition{
		Type:           TypeScriptGenerate,
		ExecutionClass: "",
		Queue:          "default",
		Timeout:        10 * time.Minute,
	}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "ExecutionClass") {
		t.Errorf("expected ExecutionClass error for empty, got %v", err)
	}

	d.ExecutionClass = "rogue_state"
	err = d.Validate()
	if err == nil || !strings.Contains(err.Error(), "ExecutionClass") {
		t.Errorf("expected ExecutionClass error for rogue state, got %v", err)
	}
}

func TestJobDefinition_Validate_EmptyQueue(t *testing.T) {
	d := JobDefinition{
		Type:           TypeScriptGenerate,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "",
		Timeout:        10 * time.Minute,
	}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "Queue is empty") {
		t.Errorf("expected Queue-is-empty error, got %v", err)
	}
}

// TestJobDefinition_Validate_ZeroTimeoutIsValid locks the canonical
// "zero means default" semantics from P0 plan §4.1 — Validate()
// accepts Timeout=0 as the "use the canonical 10-minute default"
// intent. Negative Timeout is rejected (separate test).
func TestJobDefinition_Validate_ZeroTimeoutIsValid(t *testing.T) {
	d := JobDefinition{
		Type:           TypeScriptGenerate,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        0,
	}
	if err := d.Validate(); err != nil {
		t.Errorf("Timeout=0 should be valid (= 'use default' per P0 §4.1), got: %v", err)
	}
}

func TestJobDefinition_Validate_NegativeTimeout(t *testing.T) {
	d := JobDefinition{
		Type:           TypeScriptGenerate,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        -1 * time.Minute,
	}
	err := d.Validate()
	if err == nil {
		t.Error("negative Timeout must fail")
	}
}

func TestJobDefinition_Validate_PassThroughTolerance(t *testing.T) {
	// Pass-through fields (RetryPolicyKey, ConcurrencyKey, codecs,
	// HandlerKey, RequiredCapabilities) MAY be zero/nil for a
	// programmatically-valid (but unbound) definition; Commit 3's
	// StartupValidator layers the global invariants (codec presence,
	// handler binding, etc.) on top of this base check.
	d := JobDefinition{
		Type:           TypeScriptGenerate,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        60 * time.Minute,
		// All pass-through fields zero/nil (unbound state):
		RetryPolicyKey:       "",
		ConcurrencyKey:       "",
		RequiredCapabilities: nil,
		PayloadCodec:         nil,
		ResultCodec:          nil,
		HandlerKey:           "",
		// ArtifactPolicy zero = pure-data default.
	}
	if err := d.Validate(); err != nil {
		t.Errorf("pass-through zero values must Validate, got: %v", err)
	}
}

func TestJobDefinition_Validate_DelegatesArtifactPolicyError(t *testing.T) {
	d := JobDefinition{
		Type:           TypeScriptGenerate,
		ExecutionClass: ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        10 * time.Minute,
		ArtifactPolicy: ArtifactPolicy{
			// RequireManifest=true while ProducesArtifacts=false
			// is the impossible combination ArtifactPolicy rejects.
			RequireManifest: true,
		},
	}
	err := d.Validate()
	if err == nil {
		t.Fatal("Validate should propagate ArtifactPolicy error")
	}
	if !strings.Contains(err.Error(), "RequireManifest") {
		t.Errorf("error should propagate RequireManifest detail, got %v", err)
	}
	if !strings.Contains(err.Error(), TypeScriptGenerate) {
		t.Errorf("wrapped error should carry Type, got %v", err)
	}
}

// ── Capability typed-string contract ────────────────────────────────

func TestCapability_TypedAliasString(t *testing.T) {
	// Capability must be assignable to/from a plain string without
	// surprising conversions.
	var c Capability = "media.image.generate"
	if string(c) != "media.image.generate" {
		t.Errorf("Capability cast = %q, want media.image.generate", string(c))
	}

	// Slices of Capability stay equivalent to []string for the
	// back-compat consumers.
	caps := []Capability{"a", "b"}
	if len(caps) != 2 || caps[0] != "a" || caps[1] != "b" {
		t.Errorf("Capability slice handling broken: %v", caps)
	}
}

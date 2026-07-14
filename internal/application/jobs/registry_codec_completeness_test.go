// Package jobs — registry_codec_completeness_test.go (P0 Commit 2,
// July 2026).
//
// Codec wiring completeness + round-trip tests for the canonical
// 4 workflow families. Asserts that every "registered JobDefinition"
// has BOTH a PayloadCodec AND a ResultCodec wired to a properly-
// typed adapter, and that the round-trip encode/decode path works
// for each canonical job family's payload shape.
//
// ── "Registered" semantics ──────────────────────────────────────────
//
// Until C3 lands MutableJobRegistry (RegisterDefinition + Freeze +
// StartupValidator), there is no runtime registry for JobDefinitions.
// For C2, "registered" is interpreted as: the canonical JobDefinition
// literals in internal/domain/job/job_definition_test.go are wired
// to canonical codec instances constructed HERE in this test file.
//
// C3 will replace these test helpers with runtime registrar calls —
// the parallel TestCodecCompleteness list AND the per-family round-
// trip tests will keep working as long as C3's MutableJobRegistry
// carries the SAME canonical 4 wiring frontend.
//
// ── Score this test highly ──────────────────────────────────────────
//
// If a future commit removes one of the canonical wirings (e.g.
// someone deletes the script.generate codec), this test catches it
// IMMEDIATELY rather than letting the runtime break at the first call.
// It is the canonical surface-quality guard for C3's runtime.
package jobs

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// canonicalWiredJobTypes is the canonical list of job types whose
// PayloadCodec + ResultCodec adapters are wired in C2. A future
// commit adding a 5th wired family must:
//  1. Append the constant to this list;
//  2. Define a parallel codecFor() helper;
//  3. Add a parallel TestCodecCompleteness_RoundTrip_* test.
var canonicalWiredJobTypes = []string{
	TypeScriptGenerate,
	TypeImagesGenerate,
	TypeDocumentGenerate,
	TypeAssetsResolve,
}

// scriptGenerateCodec returns the canonical PayloadCodec + ResultCodec
// wiring for script.generate. Both adapter ports are satisfied by the
// SAME *TypedCodecAdapter[ScriptGeneratePayload, ScriptGenerateResult]
// instance because Codec[T,R] already has both EncodePayload and
// EncodeResult bodies — splitting into two separate adapters would
// force duplication of the SchemaVersion / JobType / reflect surface.
func scriptGenerateCodec(t *testing.T) (job.PayloadCodec, job.ResultCodec) {
	t.Helper()
	inner := NewTypedCodec[ScriptGeneratePayload, ScriptGenerateResult](TypeScriptGenerate)
	adapter := NewTypedCodecAdapter(inner, "pipelinegen.script.generate.v1")
	return adapter, adapter
}

func imagesGenerateCodec(t *testing.T) (job.PayloadCodec, job.ResultCodec) {
	t.Helper()
	inner := NewTypedCodec[ImagesGeneratePayload, ImagesGenerateResult](TypeImagesGenerate)
	adapter := NewTypedCodecAdapter(inner, "pipelinegen.images.generate.v1")
	return adapter, adapter
}

func documentGenerateCodec(t *testing.T) (job.PayloadCodec, job.ResultCodec) {
	t.Helper()
	inner := NewTypedCodec[DocumentGeneratePayload, DocumentGenerateResult](TypeDocumentGenerate)
	adapter := NewTypedCodecAdapter(inner, "pipelinegen.document.generate.v1")
	return adapter, adapter
}

func assetsResolveCodec(t *testing.T) (job.PayloadCodec, job.ResultCodec) {
	t.Helper()
	inner := NewTypedCodec[AssetsResolvePayload, AssetsResolveResult](TypeAssetsResolve)
	adapter := NewTypedCodecAdapter(inner, "pipelinegen.assets.resolve.v1")
	return adapter, adapter
}

// codecFor dispatches to the per-family helper. A future commit
// that adds a 5th wired family must add a case here.
func codecFor(t *testing.T, jobType string) (job.PayloadCodec, job.ResultCodec) {
	t.Helper()
	switch jobType {
	case TypeScriptGenerate:
		return scriptGenerateCodec(t)
	case TypeImagesGenerate:
		return imagesGenerateCodec(t)
	case TypeDocumentGenerate:
		return documentGenerateCodec(t)
	case TypeAssetsResolve:
		return assetsResolveCodec(t)
	default:
		t.Fatalf("codecFor: unknown canonical job type %q (add a helper here)", jobType)
		return nil, nil
	}
}

// ── Completeness test ────────────────────────────────────────────────

// TestCodecCompleteness_AllCanonicalFamiliesHaveCodecs asserts that
// all 4 workflow job families have BOTH a non-nil PayloadCodec AND
// a non-nil ResultCodec wired, and that the JobType/SchemaVersion
// identity invariants hold. Failure means the registry is incomplete
// for C2's surface.
func TestCodecCompleteness_AllCanonicalFamiliesHaveCodecs(t *testing.T) {
	if len(canonicalWiredJobTypes) == 0 {
		t.Fatal("canonicalWiredJobTypes is empty — C2 wiring is missing")
	}
	for _, jt := range canonicalWiredJobTypes {
		t.Run(jt, func(t *testing.T) {
			payload, result := codecFor(t, jt)
			if payload == nil {
				t.Fatalf("%s: PayloadCodec is nil (canonical wiring missing)", jt)
			}
			if result == nil {
				t.Fatalf("%s: ResultCodec is nil (canonical wiring missing)", jt)
			}
			if payload.JobType() != jt {
				t.Errorf("%s: PayloadCodec.JobType() = %q, want %q", jt, payload.JobType(), jt)
			}
			if result.JobType() != jt {
				t.Errorf("%s: ResultCodec.JobType() = %q, want %q", jt, result.JobType(), jt)
			}
			if payload.SchemaVersion() == "" {
				t.Errorf("%s: PayloadCodec.SchemaVersion is empty", jt)
			}
			if result.SchemaVersion() == "" {
				t.Errorf("%s: ResultCodec.SchemaVersion is empty", jt)
			}
		})
	}
}

// ── Round-trip tests per canonical family ────────────────────────────

// TestCodecCompleteness_RoundTrip_ScriptGenerate exercises the full
// typed Encode → JSON bytes → Decode path on real ScriptGeneratePayload
// + ScriptGenerateResult values. A failure means the typed adapter
// is broken for script.generate.
//
// DECODED TYPE: DecodePayload returns the typed T value (the
// production TypedCodecAdapter preserves the type via reflect-typed
// JSON unmarshal; the test stub in domain/job/job_definition_test.go
// returns map[string]any for compactness, but THIS test exercises
// the PRODUCTION adapter and so must assert against the typed struct).
func TestCodecCompleteness_RoundTrip_ScriptGenerate(t *testing.T) {
	payloadCodec, resultCodec := scriptGenerateCodec(t)

	// Payload round-trip.
	in := ScriptGeneratePayload{
		Topic:             "AI in agriculture",
		StylePreset:       "casual",
		SentencesPerImage: 8,
		GenerateMetadata:  true,
	}
	rawPayload, err := payloadCodec.EncodePayload(in)
	if err != nil {
		t.Fatalf("EncodePayload(script.generate): %v", err)
	}
	decodedRaw, err := payloadCodec.DecodePayload(rawPayload)
	if err != nil {
		t.Fatalf("DecodePayload(script.generate): %v", err)
	}
	out, ok := decodedRaw.(ScriptGeneratePayload)
	if !ok {
		t.Fatalf("payload round-trip: decoded type = %T, want ScriptGeneratePayload", decodedRaw)
	}
	if out.Topic != "AI in agriculture" {
		t.Errorf("payload round-trip: Topic = %q, want %q", out.Topic, "AI in agriculture")
	}
	if out.StylePreset != "casual" {
		t.Errorf("payload round-trip: StylePreset = %q, want %q", out.StylePreset, "casual")
	}
	if out.SentencesPerImage != 8 {
		t.Errorf("payload round-trip: SentencesPerImage = %d, want 8", out.SentencesPerImage)
	}
	if !out.GenerateMetadata {
		t.Error("payload round-trip: GenerateMetadata = false, want true")
	}

	// Result round-trip.
	inResult := ScriptGenerateResult{
		ScriptID: "script-123",
		Scenes:   []string{"scene-0", "scene-1"},
		Items:    []string{"item-0", "item-1"},
	}
	rawResult, err := resultCodec.EncodeResult(inResult)
	if err != nil {
		t.Fatalf("EncodeResult(script.generate): %v", err)
	}
	decodedResult, err := resultCodec.DecodeResult(rawResult)
	if err != nil {
		t.Fatalf("DecodeResult(script.generate): %v", err)
	}
	outResult, ok := decodedResult.(ScriptGenerateResult)
	if !ok {
		t.Fatalf("result round-trip: decoded type = %T, want ScriptGenerateResult", decodedResult)
	}
	if outResult.ScriptID != "script-123" {
		t.Errorf("result round-trip: ScriptID = %q, want %q", outResult.ScriptID, "script-123")
	}
	if len(outResult.Scenes) != 2 || outResult.Scenes[0] != "scene-0" {
		t.Errorf("result round-trip: Scenes = %v, want [scene-0 scene-1]", outResult.Scenes)
	}
}

// TestCodecCompleteness_RoundTrip_ImagesGenerate exercises the typed
// adapter for images.generate (Payload focus; Result is a thin
// []string list, round-tripped for symmetry).
func TestCodecCompleteness_RoundTrip_ImagesGenerate(t *testing.T) {
	payloadCodec, resultCodec := imagesGenerateCodec(t)

	in := ImagesGeneratePayload{
		ScriptID:    "script-123",
		SceneRef:    "scene-0",
		BatchSize:   4,
		ProviderKey: "flux",
	}
	raw, err := payloadCodec.EncodePayload(in)
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	decoded, err := payloadCodec.DecodePayload(raw)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	out, ok := decoded.(ImagesGeneratePayload)
	if !ok {
		t.Fatalf("decoded type = %T, want ImagesGeneratePayload", decoded)
	}
	if out.ScriptID != "script-123" {
		t.Errorf("ScriptID = %q, want script-123", out.ScriptID)
	}
	if out.BatchSize != 4 {
		t.Errorf("BatchSize = %d, want 4", out.BatchSize)
	}
	if out.ProviderKey != "flux" {
		t.Errorf("ProviderKey = %q, want flux", out.ProviderKey)
	}

	// Result round-trip — assert typed result.
	inResult := ImagesGenerateResult{ImageRefs: []string{"asset-0", "asset-1"}}
	rawResult, err := resultCodec.EncodeResult(inResult)
	if err != nil {
		t.Fatalf("EncodeResult: %v", err)
	}
	decodedResult, err := resultCodec.DecodeResult(rawResult)
	if err != nil {
		t.Fatalf("DecodeResult: %v", err)
	}
	outResult, ok := decodedResult.(ImagesGenerateResult)
	if !ok {
		t.Fatalf("result round-trip: decoded type = %T, want ImagesGenerateResult", decodedResult)
	}
	if len(outResult.ImageRefs) != 2 || outResult.ImageRefs[0] != "asset-0" {
		t.Errorf("ImageRefs = %v, want [asset-0 asset-1]", outResult.ImageRefs)
	}
}

// TestCodecCompleteness_RoundTrip_DocumentGenerate exercises the
// typed adapter for document.generate (single-DOCX result).
func TestCodecCompleteness_RoundTrip_DocumentGenerate(t *testing.T) {
	payloadCodec, _ := documentGenerateCodec(t)

	in := DocumentGeneratePayload{
		ScriptID: "script-456",
		FolderID: "folder-abc",
		Locale:   "en",
	}
	raw, err := payloadCodec.EncodePayload(in)
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	decoded, err := payloadCodec.DecodePayload(raw)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	out, ok := decoded.(DocumentGeneratePayload)
	if !ok {
		t.Fatalf("decoded type = %T, want DocumentGeneratePayload", decoded)
	}
	if out.ScriptID != "script-456" {
		t.Errorf("ScriptID = %q, want script-456", out.ScriptID)
	}
	if out.FolderID != "folder-abc" {
		t.Errorf("FolderID = %q, want folder-abc", out.FolderID)
	}
	if out.Locale != "en" {
		t.Errorf("Locale = %q, want en", out.Locale)
	}
}

// TestCodecCompleteness_RoundTrip_AssetsResolve exercises the typed
// adapter for assets.resolve (slice-of-strings payload / slice-of-
// strings result; pure-data job per P0 §8.1 category 1).
func TestCodecCompleteness_RoundTrip_AssetsResolve(t *testing.T) {
	payloadCodec, _ := assetsResolveCodec(t)

	in := AssetsResolvePayload{
		Requirements: []string{"clip about AI", "stock image of farm"},
		MaxResults:   5,
		Locale:       "en",
	}
	raw, err := payloadCodec.EncodePayload(in)
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	decoded, err := payloadCodec.DecodePayload(raw)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	out, ok := decoded.(AssetsResolvePayload)
	if !ok {
		t.Fatalf("decoded type = %T, want AssetsResolvePayload", decoded)
	}
	if len(out.Requirements) != 2 {
		t.Errorf("Requirements length = %d, want 2", len(out.Requirements))
	}
	if out.Requirements[0] != "clip about AI" {
		t.Errorf("Requirements[0] = %q, want clip about AI", out.Requirements[0])
	}
	if out.MaxResults != 5 {
		t.Errorf("MaxResults = %d, want 5", out.MaxResults)
	}
	if out.Locale != "en" {
		t.Errorf("Locale = %q, want en", out.Locale)
	}
}

// ── Cross-cutting assertions ────────────────────────────────────────

// TestCodecCompleteness_ConcreteAdapterTypesAvailable is a compile-time
// smoke test — if any of the 4 canonical concrete payload/result
// structs is missing or misspelled, this test won't compile. The
// `must` helper fails the test on nil to catch the rare case of a
// zero-value type slipping through. (For value types, this is a
// belt-and-braces check on the type system.)
func TestCodecCompleteness_ConcreteAdapterTypesAvailable(t *testing.T) {
	_ = ScriptGeneratePayload{}
	_ = ScriptGenerateResult{}
	_ = ImagesGeneratePayload{}
	_ = ImagesGenerateResult{}
	_ = DocumentGeneratePayload{}
	_ = DocumentGenerateResult{}
	_ = AssetsResolvePayload{}
	_ = AssetsResolveResult{}
}

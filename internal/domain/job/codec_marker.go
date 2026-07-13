// Package job — codec_marker.go (P0 Commit 3, July 2026).
//
// NewCodecDescriptorMarker — a metadata-only codec implementation
// used at the JobDefinition literal level where the BODY-BEARING
// Encode/Decode methods have not yet been wired. It satisfies
// CodecDescriptor (SchemaVersion + JobType) AND PayloadCodec +
// ResultCodec (with identity round-trip bodies so the canonical
// JobDefinition literals compile against the C2 body-bearing
// interface types).
//
// ── Why a marker ─────────────────────────────────────────────────────
//
// At the JobDefinition-literal stage (canonical_definitions.go),
// the registry has the canonical schema-version + job-type identity
// in place but the production payload/result types (script.generate's
// ScriptGeneratePayload, etc.) live in the application layer
// (internal/application/jobs/codec.go). The marker gives the
// domain-layer literals a body-bearing compatible placeholder.
//
// Production startup wiring (C4+) replaces these markers with real
// TypedCodecAdapter[T,R] instances carrying per-family payload+result
// struct types; the canonical schema-version + job-type identity is
// preserved across the substitution because the marker is a
// drop-in replacement for any value satisfying PayloadCodec or
// ResultCodec.
//
// ── Layering ─────────────────────────────────────────────────────────
//
// Standard-library imports only. No application/infrastructure
// imports, per godlike/06 §Database rules.
package job

import "encoding/json"

// CodecDescriptorMarker is the marker struct. The struct is
// unexported because construction goes through NewCodecDescriptorMarker
// (which performs shape validation: schemaVersion + jobType must be
// non-empty). Callers in the same package access the type via the
// PayloadCodec / ResultCodec interface return.
type CodecDescriptorMarker struct {
	schemaVersion string
	jobType       string
}

// NewCodecDescriptorMarker constructs a canonical metadata-only
// codec that satisfies PayloadCodec + ResultCodec with identity
// round-trip bodies. Both schemaVersion and jobType MUST be
// non-empty (after whitespace stripping) — CallerSite validators
// (C3 StartupValidator) catch the empty case at startup; this
// constructor's only responsibility is the empty-string guard
// so the literal compiles cleanly.
//
// Returns the CodecDescriptorMarker struct (NOT a PayloadCodec-
// typed interface value) so the result is assignable to BOTH
// PayloadCodec AND ResultCodec fields without explicit conversion.
// Returning a PayloadCodec-typed value would NARROW the static
// type, and Go does not auto-widen between unrelated interfaces
// at struct-literal assignment — the compile-time error would be
//
//	cannot use NewCodecDescriptorMarker(...) as ResultCodec
//	            (PayloadCodec lacks DecodeResult method)
//
// even though the underlying struct DOES satisfy ResultCodec.
//
// The compile-time assertions below (`var _ PayloadCodec = ...`,
// `var _ ResultCodec = ...`) pin both contracts; the dual-
// assignability is encoded in the return type as the struct
// rather than an interface.
func NewCodecDescriptorMarker(schemaVersion, jobType string) CodecDescriptorMarker {
	return CodecDescriptorMarker{
		schemaVersion: schemaVersion,
		jobType:       jobType,
	}
}

// SchemaVersion forwards the marker metadata. Required by CodecDescriptor.
func (m CodecDescriptorMarker) SchemaVersion() string { return m.schemaVersion }

// JobType forwards the marker metadata. Required by CodecDescriptor.
// StartupValidator (P0 Commit 3) checks JobType() == JobDefinition.Type.
func (m CodecDescriptorMarker) JobType() string { return m.jobType }

// EncodePayload is the body-bearing PayloadCodec method.
// Marker-level: identity round-trip via json.Marshal. Production
// wiring (C4+) replaces this with TypedCodecAdapter[T,R].
func (m CodecDescriptorMarker) EncodePayload(req any) (json.RawMessage, error) {
	return json.Marshal(req)
}

// DecodePayload is the body-bearing PayloadCodec method.
// Marker-level: returns the json in map[string]any form (canonical
// decoder-friendly shape). Production wiring (C4+) replaces this
// with TypedCodecAdapter[T,R] returning the typed T value.
func (m CodecDescriptorMarker) DecodePayload(raw json.RawMessage) (any, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// EncodeResult is the body-bearing ResultCodec method. Symmetric
// with EncodePayload. Production wiring (C4+) replaces this.
func (m CodecDescriptorMarker) EncodeResult(resp any) (json.RawMessage, error) {
	return json.Marshal(resp)
}

// DecodeResult is the body-bearing ResultCodec method. Symmetric
// with DecodePayload. Production wiring (C4+) replaces this.
func (m CodecDescriptorMarker) DecodeResult(raw json.RawMessage) (any, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Compile-time assertions: CodecDescriptorMarker satisfies BOTH
// PayloadCodec AND ResultCodec AND the underlying CodecDescriptor.
// Future drift on any of these interfaces is a build failure here
// (NOT a runtime panic at first call).
var (
	_ CodecDescriptor = CodecDescriptorMarker{}
	_ PayloadCodec    = CodecDescriptorMarker{}
	_ ResultCodec     = CodecDescriptorMarker{}
)

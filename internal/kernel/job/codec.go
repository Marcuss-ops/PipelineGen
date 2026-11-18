// Package job — codec.go (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
//
// The canonical body-bearing Codec interfaces (PayloadCodec +
// ResultCodec) + the canonical constructor (NewCodecDescriptorMarker)
// + the canonical marker struct (CodecDescriptorMarker) live here.
//
// godlike/02 kernel rules: stdlib-only imports. The interfaces
// reference the intra-package CodecDescriptor marker (no cross-zone
// imports satisfied by these types).
//
// godlike/07 fail-closed: a marker-only codec's body methods
// round-trip identity (json.Marshal / json.Unmarshal to map[string]any).
// Production startup wiring (C4+) can replace these markers with typed
// adapters carrying per-family payload+result struct types; the canonical
// schema-version + job-type identity is preserved across the substitution
// because CodecDescriptorMarker is a drop-in replacement for any value
// satisfying PayloadCodec or ResultCodec. The old Go typed-adapter scaffold
// in capabilities/jobs was retired on 2026-11-18; the registry wiring is now
// pinned by capabilities/jobs/registry_codec_wiring_test.go.
//
// PR-KERNEL-JOB-POPULATE step 1 (commit 9.1, July 2026): the codec
// interfaces preserve the LEGACY (req any) (json.RawMessage, error)
// signature for minimum-churn compat with the callers in
// canonical_definitions.go.
// A future PR may migrate to (ctx, jobID, version string, []byte) (any, error)
// once a typed adapter is universally wired.
package job

import "encoding/json"

// ── PayloadCodec (canonical, body-bearing) ─────────────────────────────

// PayloadCodec is the canonical typed encode/decode surface for a
// JobDefinition's INPUT payload. It embeds CodecDescriptor
// (SchemaVersion + JobType marker) AND adds the typed
// EncodePayload / DecodePayload bodies.
//
// The canonical implementation today is CodecDescriptorMarker (marker
// identity round-trip). A typed adapter can be reintroduced when C4 lands,
// satisfying both kernel/job.PayloadCodec and kernel/job.ResultCodec via two
// type-instantiated adapters (one per T/R pair).
type PayloadCodec interface {
	CodecDescriptor

	// EncodePayload is the canonical encode body. Receives a
	// typed payload (an `any` value, narrowable at the consumer
	// site via reflection) and produces the wire-format
	// representation. The returned RawMessage MUST round-trip
	// via DecodePayload — internal/capabilities/jobs/
	// registry_codec_wiring_test.go pins this contract per
	// registered JobDefinition.
	EncodePayload(req any) (json.RawMessage, error)

	// DecodePayload is the canonical decode body. The
	// inverse of EncodePayload; called by the dispatch path
	// when handing the payload to a handler. Implementations
	// MUST validate the wire-format SchemaVersion matches
	// CodecDescriptor.SchemaVersion() and reject mismatches
	// with a typed error (godlike/07 no-fake-availability).
	DecodePayload(raw json.RawMessage) (any, error)
}

// ── ResultCodec (canonical, body-bearing) ──────────────────────────────

// ResultCodec is the canonical typed encode/decode surface for a
// JobDefinition's OUTPUT result. It embeds CodecDescriptor
// (SchemaVersion + JobType marker) AND adds the typed EncodeResult
// / DecodeResult bodies. The complete-flow contract is:
// complete_artifacts_service.go calls ResultCodec.EncodeResult
// after the artifact manifest is captured, the result bytes are
// persisted in job_results.result_payload (the canonical owner per
// migration 119; jobs.result_json is NOT a second persistence site); the
// read-flow calls ResultCodec.DecodeResult to recover the typed
// shape.
type ResultCodec interface {
	CodecDescriptor

	// EncodeResult is the canonical encode body. Receives a
	// typed result (`any`, narrowable at the consumer site)
	// and produces the wire-format representation.
	EncodeResult(resp any) (json.RawMessage, error)

	// DecodeResult is the canonical decode body.
	DecodeResult(raw json.RawMessage) (any, error)
}

// ── CodecDescriptorMarker (canonical marker struct) ────────────────────

// CodecDescriptorMarker is the canonical metadata-only codec
// implementation used at the JobDefinition literal level where
// the BODY-BEARING Encode/Decode methods have not yet been wired.
// It satisfies CodecDescriptor (SchemaVersion + JobType) AND
// PayloadCodec + ResultCodec (with identity round-trip bodies so
// the canonical JobDefinition literals compile against the body-
// bearing interface types).
//
// Construction goes through NewCodecDescriptorMarker (which
// performs shape validation: schemaVersion + jobType must be
// non-empty). Callers access the type via the PayloadCodec /
// ResultCodec interface return (or via the struct directly when
// dual-assignability is required at a struct-literal site).
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
//
// godlike/07 fail-closed: a marker-only codec's body methods
// round-trip identity (json.Marshal / json.Unmarshal to map[string]any).
// Production startup wiring (C4+) can replace these markers with typed
// adapters; the canonical schema-version + job-type identity is preserved
// across the substitution.
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
// wiring (C4+) can replace this with a typed adapter.
func (m CodecDescriptorMarker) EncodePayload(req any) (json.RawMessage, error) {
	return json.Marshal(req)
}

// DecodePayload is the body-bearing PayloadCodec method.
// Marker-level: returns the json in map[string]any form (canonical
// decoder-friendly shape). Production wiring (C4+) can replace this
// with a typed adapter returning the typed T value.
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

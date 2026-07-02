// Package job — codec.go (P0 Commit 2, July 2026).
//
// Defines the canonical typed codec interfaces for a JobDefinition's
// input payload (PayloadCodec) and output result (ResultCodec).
// These interfaces are the SSOT for the wire-format contracts that
// the registry records in JobDefinition.PayloadCodec / .ResultCodec.
//
// ── Evolution from C1 ────────────────────────────────────────────────
//
// C1 introduced CodecDescriptor (SchemaVersion + JobType metadata
// only) as a marker interface carried by JobDefinition.PayloadCodec
// / .ResultCodec fields. C2 ELABORATES that marker with the actual
// typed Encode/Decode bodies:
//
//   PayloadCodec  = CodecDescriptor + Encode/Decode payloads
//   ResultCodec   = CodecDescriptor + Encode/Decode results
//
// Why two interfaces (and not a single one): in the C2 design,
// PayloadCodec and ResultCodec carry distinct type-parameter
// requirements. Keeping them separate lets a future registry entry
// use a typed payload codec that has no corresponding result
// codec (or vice versa) without forcing a shared generic constraint
// on the JobDefinition struct.
//
// ── Concrete adapters ───────────────────────────────────────────────
//
// The concrete adapter lives in internal/application/jobs/codec.go.
// The canonical wiring is TypedCodecAdapter[T,R] which adapts the
// existing generic Codec[T,R] infrastructure (the legacy
// application/jobs/codec.go::Codec[T,R] + TypedCodec[T,R]) to
// satisfy BOTH domain.PayloadCodec AND domain.ResultCodec —
// because Codec[T,R] already has both EncodePayload/DecodePayload
// AND EncodeResult/DecodeResult bodies.
//
// ── Why `any` for the typed methods ─────────────────────────────────
//
// The Encode / Decode arguments are `any` because the codec is a
// polymorphic encoder — the canonical justification sanctioned by
// AGENTS.md Pattern 0 ("the only sanctioned use of `any` is codec
// boundaries"). The TypedCodecAdapter does a reflect-based type
// assertion at Encode time (req must be of type T or its pointer
// equivalent) so runtime type errors surface as typed errors
// rather than panics.
//
// ── Status: P0 Commit 2 ─────────────────────────────────────────────
//
// This file ships the canonical interfaces (PayloadCodec +
// ResultCodec). C3's MutableJobRegistry.RegisterDefinition will
// enforce JobDefinition.PayloadCodec != nil for executable jobs
// (handler-bound stages). C3's StartupValidator will additionally
// assert that no creator-enabled job is registered without a
// non-nil ResultCodec (the upload audit trail requires it).

package job

import "encoding/json"

// ── CodecDescriptor (kept from C1 as a marker) ──────────────────────

// CodecDescriptor is the canonical **metadata-only** surface that
// every codec in the registry must satisfy. It is the parent
// interface of PayloadCodec and ResultCodec (both embed it).
//
// Kept from C1 (where it was the only public codec surface, then
// renamed from the in-plan "Codec" to disambiguate from the existing
// `Codec[T,R]` in application/jobs and the concrete `JobCodec` in
// artlist). C2 ELABORATES it with body-bearing extensions:
//
//	PayloadCodec embeds CodecDescriptor and adds Encode/Decode bodies
//	for the typed payload.
//	ResultCodec embeds CodecDescriptor and adds Encode/Decode bodies
//	for the typed result.
//
// This interface is what the JobDefinition validator keys on to
// verify a codec is registered: a struct that lacks SchemaVersion()
// or JobType() cannot satisfy ANY of PayloadCodec / ResultCodec /
// CodecDescriptor, so the wiring check is one assertion.
type CodecDescriptor interface {
	// SchemaVersion is the wire-format version of the codec.
	// Examples: "pipelinegen.payload.script.generate.v1".
	SchemaVersion() string

	// JobType is the canonical job type string this codec
	// pairs with. StartupValidator (P0 Commit 3) enforces
	// CodecDescriptor.JobType() == JobDefinition.Type.
	JobType() string
}

// ── PayloadCodec (P0 Commit 2 — C2) ─────────────────────────────────

// PayloadCodec is the canonical typed encode/decode contract for a
// job's INPUT payload.
//
// A value satisfies PayloadCodec iff:
//
//   - it implements CodecDescriptor (SchemaVersion + JobType above);
//   - it can Encode a typed payload to json.RawMessage;
//   - it can Decode json.RawMessage back to a typed payload of the
//     same logical type.
//
// The Encode argument is `any` because the codec is a polymorphic
// encoder (per AGENTS.md Pattern 0, the only sanctioned use of `any`
// in the codebase is codec boundaries). The adapter in
// internal/application/jobs/codec.go::TypedCodecAdapter[T,R] runs
// a reflect-based type assertion (req must be of type T, or the
// adapter surfaces a typed error) and the registry surface is
// guaranteed that runtime callers pass the correct T at use sites.
//
// Decode returns `any` for the symmetric reason — the caller has
// the JobType as a discriminator and casts the result back to the
// expected type T at the use site. The canonical adapter
// (TypedCodecAdapter[T,R]) does the typed return internally and
// the `any` surface is just the interface seam.
type PayloadCodec interface {
	CodecDescriptor

	// EncodePayload serialises the typed payload to canonical
	// json bytes. Returns a non-nil error if req is not the
	// expected type T (per the adapter's reflect check).
	EncodePayload(req any) (json.RawMessage, error)

	// DecodePayload deserialises the canonical json bytes back
	// to a typed payload value. Returns a non-nil error on
	// malformed input or schema-version mismatch.
	DecodePayload(raw json.RawMessage) (any, error)
}

// ── ResultCodec (P0 Commit 2 — C2) ──────────────────────────────────

// ResultCodec is the canonical typed encode/decode contract for a
// job's OUTPUT result. Symmetric with PayloadCodec.
//
// A value satisfies ResultCodec iff:
//
//   - it implements CodecDescriptor;
//   - it can Encode a typed result to json.RawMessage;
//   - it can Decode json.RawMessage back to a typed result.
//
// The Encode argument is `any` (same rationale as PayloadCodec —
// AGENTS.md Pattern 0 only). The decoder returns `any`.
// Adapter validation lives in
// internal/application/jobs/codec.go::TypedCodecAdapter[T,R].
//
// C3's StartupValidator enforces ResultCodec.JobType() ==
// JobDefinition.Type and (for executable jobs with ArtifactPolicy
// RequireManifest=true) ResultCodec != nil.
type ResultCodec interface {
	CodecDescriptor

	// EncodeResult serialises the typed result to canonical
	// json bytes. Returns a non-nil error if resp is not the
	// expected type R.
	EncodeResult(resp any) (json.RawMessage, error)

	// DecodeResult deserialises the canonical json bytes back
	// to a typed result value.
	DecodeResult(raw json.RawMessage) (any, error)
}

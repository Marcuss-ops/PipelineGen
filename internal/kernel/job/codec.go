// Package job — codec.go (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
// Migration of canonical PayloadCodec / ResultCodec body-bearing
// interfaces from internal/domain/job/codec.go.
//
// CodecDescriptor (the marker interface) is declared in
// job_definition.go (this same kernel subzone). The two body-
// bearing interfaces below extend the marker with Encode/Decode
// bodies so the registry / dispatch path can identify a codec by
// its SchemaVersion + JobType (marker) AND actually invoke its
// body at the corresponding encode/decode phase (interfaces below).
//
// Per godlike/02 kernel rules: stdlib-only imports. The two body-
// bearing interfaces reference the intra-package CodecDescriptor
// marker (no cross-zone imports satisfied by these types).
package job

import "context"

// ── PayloadCodec (canonical, body-bearing) ─────────────────────────────

// PayloadCodec is the canonical typed encode/decode surface for a
// JobDefinition's INPUT payload. It embeds CodecDescriptor
// (SchemaVersion + JobType marker) AND adds the typed
// EncodePayload / DecodePayload bodies.
//
// A valid implementations is the application-layer
// TypedCodecAdapter[T,R] decorator (defined in
// internal/application/jobs/codec.go) which adapts the existing
// Codec[T,R] infrastructure to satisfy both
// kernel/job.PayloadCodec and kernel/job.ResultCodec via two
// type-instantiated adapters (one per T/R pair).
type PayloadCodec interface {
	CodecDescriptor

	// EncodePayload is the canonical encode body. Receives a
	// typed payload (an `any` value, narrowable at the consumer
	// site via reflection) and produces the wire-format
	// representation. The returned []byte MUST round-trip via
	// DecodePayload — the codec round-trip test in
	// internal/application/jobs/registry_codec_completeness_test.go
	// pins this contract per JobDefinition.
	EncodePayload(ctx context.Context, jobID, payloadVersion string, payload any) ([]byte, error)

	// DecodePayload is the canonical decode body. The
	// inverse of EncodePayload; called by the dispatch path
	// when handing the payload to a handler. Implementations
	// MUST validate the wire-format SchemaVersion matches
	// CodecDescriptor.SchemaVersion() and reject mismatches
	// with a typed error (godlike/07 no-fake-availability).
	DecodePayload(ctx context.Context, jobID, payloadVersion string, wire []byte) (any, error)
}

// ── ResultCodec (canonical, body-bearing) ──────────────────────────────

// ResultCodec is the canonical typed encode/decode surface for a
// JobDefinition's OUTPUT result. It embeds CodecDescriptor
// (SchemaVersion + JobType marker) AND adds the typed EncodeResult
// / DecodeResult bodies. The complete-flow contract is:
// complete_artifacts_service.go calls ResultCodec.EncodeResult
// after the artifact manifest is captured, the result bytes are
// persisted in jobs.result_json + job_results.result_json; the
// read-flow calls ResultCodec.DecodeResult to recover the typed
// shape.
type ResultCodec interface {
	CodecDescriptor

	// EncodeResult is the canonical encode body. Receives a
	// typed result (`any`, narrowable at the consumer site)
	// and produces the wire-format representation.
	EncodeResult(ctx context.Context, jobID, resultVersion string, result any) ([]byte, error)

	// DecodeResult is the canonical decode body.
	DecodeResult(ctx context.Context, jobID, resultVersion string, wire []byte) (any, error)
}

// ── NewCodecDescriptorMarker (canonical constructor) ───────────────────

// NewCodecDescriptorMarker is the canonical factory for
// PayloadCodec / ResultCodec markers. Returns a struct that
// implements CodecDescriptor (the SchemaVersion + JobType marker
// contract) and is used by TypedCodecAdapter[T,R] to satisfy
// Body-Bearing-Codec interfaces when only the metadata shape
// is required (e.g. when the application-layer adapter wires a
// new JobDefinition at composition-root startup and a panicking
// body is desired until the actual EncodePayload/DecodePayload
// bodies are declared).
//
// godlike/07 fail-closed: a marker-only codec MUST return errors
// from Encode/Decode bodies; the marker factory does NOT provide
// those bodies. Use the TypedCodecAdapter[T,R] decorator for
// production codecs.
//
// jobType: the canonical JobDefinition.Type discriminator
// (TypeScriptGenerate, TypeImagesGenerate, etc.). codecMarker:
// the wire-format SchemaVersion label (e.g.
// "pipelinegen.payload.script.generate.v1").
func NewCodecDescriptorMarker(codecMarker, jobType string) CodecDescriptor {
	return codecMarkerImpl{codecMarker: codecMarker, jobType: jobType}
}

type codecMarkerImpl struct {
	codecMarker string
	jobType     string
}

func (c codecMarkerImpl) SchemaVersion() string { return c.codecMarker }
func (c codecMarkerImpl) JobType() string       { return c.jobType }

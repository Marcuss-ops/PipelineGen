package jobs

import (
	"encoding/json"
	"fmt"
	"reflect"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Legacy typed Codec surface (kept from pre-C2) ───────────────────

// Codec defines a typed interface for encoding/decoding job payloads and
// results. Each job type should implement this interface to eliminate
// manual map[string]any conversions scattered across handlers.
//
// The JobType() returns a plain string — not models.JobType — so codecs
// are not coupled to the legacy model types.
//
// KEPT FROM PRE-C2: P0 Commit 2 introduces the domain.PayloadCodec /
// domain.ResultCodec interfaces with typed Encode/Decode (returning
// json.RawMessage). TypedCodecAdapter[T,R] in this file is the canonical
// adapter that exposes the existing Codec[T,R] infrastructure AS
// domain.PayloadCodec and domain.ResultCodec. No existing consumer of
// Codec[T,R] is broken — the adapter wraps, does not replace.
type Codec[T any, R any] interface {
	JobType() string
	EncodePayload(req T) map[string]any
	DecodePayload(raw json.RawMessage) (T, error)
	EncodeResult(resp R) map[string]any
	DecodeResult(raw json.RawMessage) (R, error)
}

// TypedCodec is a generic helper using JSON marshal/unmarshal for
// simple codecs. Works for any type with json tags.
//
// KEPT FROM PRE-C2 — see Codec interface doc above for the migration
// path to TypedCodecAdapter[T,R] in P0 Commit 2.
type TypedCodec[T any, R any] struct {
	jobType string
}

func NewTypedCodec[T any, R any](jobType string) *TypedCodec[T, R] {
	return &TypedCodec[T, R]{jobType: jobType}
}

func (c *TypedCodec[T, R]) JobType() string { return c.jobType }

func (c *TypedCodec[T, R]) EncodePayload(req T) map[string]any {
	data, err := json.Marshal(req)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func (c *TypedCodec[T, R]) DecodePayload(raw json.RawMessage) (T, error) {
	var req T
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("failed to decode %T payload: %w", req, err)
	}
	return req, nil
}

func (c *TypedCodec[T, R]) EncodeResult(resp R) map[string]any {
	data, err := json.Marshal(resp)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func (c *TypedCodec[T, R]) DecodeResult(raw json.RawMessage) (R, error) {
	var resp R
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("failed to decode %T result: %w", resp, err)
	}
	return resp, nil
}

// ── C2: job-type string constants ──────────────────────────────────
//
// The four canonical Type* job-type constants (TypeScriptGenerate,
// TypeImagesGenerate, TypeDocumentGenerate, TypeAssetsResolve) are
// used by registry_codec_completeness_test.go as bare identifiers.
// They are NOT declared here — the canonical re-export surface lives
// in this package's registry.go (alongside the other 26 Type* alias
// constants already declared there, e.g.
//
//     TypeScriptGenerate = job.TypeScriptGenerate
//
// per godlike/02 §“Capability-specific constants stay in their owning
// domain package”. Adding a 5th canonical family means adding ONE line
// to the registry.go const block + the matching domain/job/job.go
// declaration, NOT introducing a duplicate const block here.

// ── C2: canonical concrete payload/result types ─────────────────────

// ScriptGeneratePayload is the canonical typed request payload for
// script.generate. Schema version v1 (per the C2 adapter wiring).
type ScriptGeneratePayload struct {
	Topic             string `json:"topic"`
	StylePreset       string `json:"style_preset,omitempty"`
	SentencesPerImage int    `json:"sentences_per_image,omitempty"`
	GenerateMetadata  bool   `json:"generate_metadata,omitempty"`
	ExtractEntities   bool   `json:"extract_entities,omitempty"`
}

// ScriptGenerateResult is the canonical typed response result for
// script.generate.
type ScriptGenerateResult struct {
	ScriptID string   `json:"script_id"`
	Scenes   []string `json:"scene_ids"`
	Items    []string `json:"item_ids"`
}

// ImagesGeneratePayload is the canonical typed request payload for
// images.generate.
type ImagesGeneratePayload struct {
	ScriptID    string `json:"script_id"`
	SceneRef    string `json:"scene_ref,omitempty"`
	BatchSize   int    `json:"batch_size,omitempty"`
	ProviderKey string `json:"provider_key,omitempty"`
}

// ImagesGenerateResult is the canonical typed response result for
// images.generate.
type ImagesGenerateResult struct {
	ImageRefs []string `json:"image_refs"`
}

// DocumentGeneratePayload is the canonical typed request payload for
// document.generate.
type DocumentGeneratePayload struct {
	ScriptID string `json:"script_id"`
	FolderID string `json:"folder_id,omitempty"`
	Locale   string `json:"locale,omitempty"`
}

// DocumentGenerateResult is the canonical typed response result for
// document.generate.
type DocumentGenerateResult struct {
	DocumentID string `json:"document_id"`
	URL        string `json:"url"`
}

// AssetsResolvePayload is the canonical typed request payload for
// assets.resolve.
type AssetsResolvePayload struct {
	Requirements []string `json:"requirements"`
	MaxResults   int      `json:"max_results,omitempty"`
	Locale       string   `json:"locale,omitempty"`
}

// AssetsResolveResult is the canonical typed response result for
// assets.resolve.
type AssetsResolveResult struct {
	AssetRefs []string `json:"asset_refs"`
}

// ── C2: TypedCodecAdapter[T,R] (domain bridge) ──────────────────────

// TypedCodecAdapter[T,R] adapts the existing Codec[T,R] generic
// infrastructure (this file's Codec[T,R] + TypedCodec[T,R]) to
// satisfy the C2 domain interfaces:
//
//   - job.PayloadCodec — body-bearing typed encode/decode for the
//     JobDefinition's INPUT payload. Embeds job.CodecDescriptor
//     (SchemaVersion + JobType).
//   - job.ResultCodec  — body-bearing typed encode/decode for the
//     JobDefinition's OUTPUT result. Embeds job.CodecDescriptor.
//
// Why TWO interfaces satisfied by ONE struct: Codec[T,R] already
// has both EncodePayload/DecodePayload AND EncodeResult/DecodeResult
// bodies. Splitting the adapter into two separate structs would force
// duplication of the SchemaVersion/JobType/reflect-assert surface.
//
// Encode / Decode argument types:
//
//   - EncodePayload(req any) — the adapter uses a reflect-based type
//     check (req must be of type T; mismatches surface as typed
//     errors, not panics). The `any` surface is the only sanctioned
//     use of `any` per AGENTS.md Pattern 0 (codec boundaries are
//     polymorphic by definition).
//   - DecodePayload(raw) → any — the adapter unmarshals to a typed
//     value T internally and returns it as `any`. The caller knows
//     JobType and casts to T at use sites.
//
// Performance note: the adapter BYPASSES TypedCodec.EncodePayload's
// double-roundtrip (which marshals to bytes and back to
// map[string]any). The adapter marshals T → bytes directly via
// json.Marshal, which is what PayloadCodec.EncodePayload returns.
// One marshal/unmarshal cycle is the canonical wire path.
type TypedCodecAdapter[T any, R any] struct {
	codec         Codec[T, R]
	schemaVersion string
}

// NewTypedCodecAdapter wraps the existing Codec[T,R] infrastructure
// with a SchemaVersion tag. Returns a value that satisfies BOTH
// job.PayloadCodec and job.ResultCodec.
func NewTypedCodecAdapter[T any, R any](codec Codec[T, R], schemaVersion string) *TypedCodecAdapter[T, R] {
	return &TypedCodecAdapter[T, R]{codec: codec, schemaVersion: schemaVersion}
}

// SchemaVersion is the wire-format tag carried by the wrapped codec.
func (a *TypedCodecAdapter[T, R]) SchemaVersion() string { return a.schemaVersion }

// JobType forwards to the wrapped Codec[T,R] (which carries the
// canonical job type string).
func (a *TypedCodecAdapter[T, R]) JobType() string { return a.codec.JobType() }

// EncodePayload is the body-bearing PayloadCodec.EncodePayload.
// Validates req is type T (typed error on mismatch), then marshals
// directly via json.Marshal on T (single marshal cycle, bypassing
// the legacy map[string]any double-round).
func (a *TypedCodecAdapter[T, R]) EncodePayload(req any) (json.RawMessage, error) {
	var zero T
	expected := reflect.TypeOf(zero)
	got := reflect.TypeOf(req)
	if got != expected {
		return nil, fmt.Errorf("TypedCodecAdapter[%s]: EncodePayload type mismatch (got %s, want %s)", a.codec.JobType(), got, expected)
	}
	typedReq, _ := req.(T)
	return json.Marshal(typedReq)
}

// DecodePayload is the body-bearing PayloadCodec.DecodePayload.
// Unmarshals raw bytes to a typed T value, returned as `any`.
func (a *TypedCodecAdapter[T, R]) DecodePayload(raw json.RawMessage) (any, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("TypedCodecAdapter[%s]: DecodePayload: %w", a.codec.JobType(), err)
	}
	return v, nil
}

// EncodeResult is the body-bearing ResultCodec.EncodeResult.
// Symmetric to EncodePayload.
func (a *TypedCodecAdapter[T, R]) EncodeResult(resp any) (json.RawMessage, error) {
	var zero R
	expected := reflect.TypeOf(zero)
	got := reflect.TypeOf(resp)
	if got != expected {
		return nil, fmt.Errorf("TypedCodecAdapter[%s]: EncodeResult type mismatch (got %s, want %s)", a.codec.JobType(), got, expected)
	}
	typedResp, _ := resp.(R)
	return json.Marshal(typedResp)
}

// DecodeResult is the body-bearing ResultCodec.DecodeResult.
// Symmetric to DecodePayload.
func (a *TypedCodecAdapter[T, R]) DecodeResult(raw json.RawMessage) (any, error) {
	var v R
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("TypedCodecAdapter[%s]: DecodeResult: %w", a.codec.JobType(), err)
	}
	return v, nil
}

// Compile-time assertions: TypedCodecAdapter[T,R] satisfies BOTH
// job.PayloadCodec AND job.ResultCodec. The static empty-struct
// instantiation is a witness because an empty struct is valid for
// any T/R constraint, so the cast succeeds at compile time.
var (
	_ job.PayloadCodec = (*TypedCodecAdapter[struct{}, struct{}])(nil)
	_ job.ResultCodec  = (*TypedCodecAdapter[struct{}, struct{}])(nil)
)

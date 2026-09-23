package jobs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
// The canonical Type* job-type constants (TypeScriptGenerate,
// TypeImagesGenerate, TypeAssetsResolve) are
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

// ── C2: video.create workflow family (payload + result) ────────────

// VideoCreatePayload is the canonical typed request payload for
// video.create — ONE request that drives the whole durable video
// workflow (script -> media -> voiceover -> audio master -> render ->
// assemble -> mux -> verify -> publish).
//
// Contract discipline (godlike/07 no-fake-availability): this is the
// WHAT, never the HOW. Infrastructure facts (worker addresses,
// RenderingGen URLs, Chronon sockets, local filesystem paths) MUST NOT
// appear here — they are owned by the runtime that executes the
// workflow. The handler decodes with DisallowUnknownFields so a
// payload carrying such fields fails closed instead of being ignored.
type VideoCreatePayload struct {
	Topic           string   `json:"topic"`
	Language        string   `json:"language"`
	DurationSeconds int      `json:"duration_seconds"`
	MediaSources    []string `json:"media_sources"`
	Voiceover       bool     `json:"voiceover"`
	Overlays        bool     `json:"overlays"`
	AspectRatio     string   `json:"aspect_ratio,omitempty"`
	// DeliveryDestinationID is the optional publication destination
	// identity (the 51's delivery registry entry). Empty = the
	// workflow's canonical project destination.
	DeliveryDestinationID string         `json:"delivery_destination_id,omitempty"`
	Metadata              map[string]any `json:"metadata,omitempty"`
}

// VideoCreateMediaSources is the closed vocabulary of media_sources.
var VideoCreateMediaSources = []string{"youtube", "stock", "artlist"}

// Validate fails closed on an incomplete or out-of-contract request.
func (p VideoCreatePayload) Validate() error {
	if strings.TrimSpace(p.Topic) == "" {
		return fmt.Errorf("video.create payload: topic is required")
	}
	if p.DurationSeconds <= 0 {
		return fmt.Errorf("video.create payload: duration_seconds must be > 0")
	}
	if len(p.MediaSources) == 0 {
		return fmt.Errorf("video.create payload: media_sources must not be empty")
	}
	seen := make(map[string]bool, len(p.MediaSources))
	for _, src := range p.MediaSources {
		src = strings.TrimSpace(src)
		known := false
		for _, allowed := range VideoCreateMediaSources {
			if src == allowed {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("video.create payload: media_sources %q is not one of %v", src, VideoCreateMediaSources)
		}
		if seen[src] {
			return fmt.Errorf("video.create payload: media_sources contains duplicate %q", src)
		}
		seen[src] = true
	}
	return nil
}

// FinalVideoArtifact is the durable identity of the published final
// video. Every fact the caller (the 51's Calendar) needs to show and
// replay the video WITHOUT knowing RenderingGen, Chronon or any local
// path: the media registry identity, the Drive identity, the playback
// URL and the certified content facts.
// ── Media identity value objects (media-identity gate, godlike/06) ─
//
// A CONTENT ADDRESS (AssetID/SHA256: what the bytes ARE) and a LOCATION
// (DriveFileID/LocalPath: WHERE the bytes are) are different facts with
// different owners; a struct that binds both can disagree with itself.
// These value objects carry the location facts so identity structs never
// declare them. Embedded anonymously they FLATTEN on the JSON wire, so
// the contract keys (drive_file_id / local_path) are unchanged.
//
// Declared here (the codec contract package) because both the codec
// wire types and the videocreate workflow project them; one owner.

// DriveRef is the Drive LOCATION of bytes (file id).
type DriveRef struct {
	DriveFileID string `json:"drive_file_id,omitempty"`
}

// LocalRef is the producer-local materialization of bytes (a runtime
// cache value, valid in ONE process).
type LocalRef struct {
	LocalPath string `json:"local_path,omitempty"`
}

type FinalVideoArtifact struct {
	AssetID    string `json:"asset_id"`
	MediaURL   string `json:"media_url"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	DurationMS int64  `json:"duration_ms"`
	// DriveRef carries the Drive location (wire key drive_file_id).
	DriveRef
}

// Validate fails closed on an unpublished or uncertified artifact:
// a final video without a durable URL, Drive identity, canonical
// SHA-256 or non-trivial size/duration is not a result.
func (a FinalVideoArtifact) Validate() error {
	if strings.TrimSpace(a.AssetID) == "" {
		return fmt.Errorf("final_video.asset_id is required")
	}
	if strings.TrimSpace(a.MediaURL) == "" {
		return fmt.Errorf("final_video.media_url is required")
	}
	if strings.TrimSpace(a.DriveFileID) == "" {
		return fmt.Errorf("final_video.drive_file_id is required")
	}
	if !digest.IsCanonicalSHA256(a.SHA256) {
		return fmt.Errorf("final_video.sha256 must be a canonical SHA-256")
	}
	if a.SizeBytes <= 0 {
		return fmt.Errorf("final_video.size_bytes must be > 0")
	}
	if a.DurationMS <= 0 {
		return fmt.Errorf("final_video.duration_ms must be > 0")
	}
	return nil
}

// ThumbnailArtifact is the durable identity of an already-generated
// cover image (optional; see ThumbnailContext for the 51-side path).
type ThumbnailArtifact struct {
	AssetID   string `json:"asset_id"`
	MediaURL  string `json:"media_url"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	// DriveRef carries the Drive location (wire key drive_file_id).
	DriveRef
}

// ThumbnailContext is the cover-generation context handed BACK to the
// caller so the thumbnail stays owned by the component that already
// owns covers (InstaeditLogin on the 51), instead of duplicating that
// feature inside the render lane (PipelineGen documents that
// cover/thumbnail is NOT a rendering-lane phase).
type ThumbnailContext struct {
	Title           string   `json:"title"`
	Subjects        []string `json:"subjects"`
	SuggestedPrompt string   `json:"suggested_prompt"`
	FrameAssetIDs   []string `json:"frame_asset_ids,omitempty"`
}

// VideoCreateChildren is the child-job ledger of the workflow: the
// durable broker IDs of every child the parent orchestrated, grouped
// by stage. Recovery and replay audit read this instead of trusting
// process memory.
type VideoCreateChildren struct {
	Script    string   `json:"script,omitempty"`
	YouTube   []string `json:"youtube,omitempty"`
	Stock     []string `json:"stock,omitempty"`
	Voiceover string   `json:"voiceover,omitempty"`
	Render    []string `json:"render,omitempty"`
	Assembly  []string `json:"assembly,omitempty"`
}

// VideoCreateResult is the canonical typed response result for
// video.create — the contract the 51 polls. It is returned ONLY when
// the job is about to turn SUCCEEDED: the final video is published
// (Drive + media registry), certified (ffprobe facts + SHA-256) and
// the child ledger is complete. A missing final audio stream fails the
// workflow (FINAL_VIDEO_AUDIO_MISSING) instead of producing a result.
type VideoCreateResult struct {
	VideoID          string              `json:"video_id"`
	ScriptAssetID    string              `json:"script_asset_id,omitempty"`
	FinalVideo       FinalVideoArtifact  `json:"final_video"`
	Thumbnail        *ThumbnailArtifact  `json:"thumbnail,omitempty"`
	ThumbnailContext *ThumbnailContext   `json:"thumbnail_context,omitempty"`
	Children         VideoCreateChildren `json:"children"`
	DurationMS       int64               `json:"duration_ms"`
	CompletedStages  []string            `json:"completed_stages"`
}

// Validate fails closed on a result that cannot be consumed by the
// caller (missing video identity, uncertified final video, or a
// workflow that claims success without naming its completed stages).
func (r VideoCreateResult) Validate() error {
	if strings.TrimSpace(r.VideoID) == "" {
		return fmt.Errorf("video.create result: video_id is required")
	}
	if err := r.FinalVideo.Validate(); err != nil {
		return fmt.Errorf("video.create result: %w", err)
	}
	if r.DurationMS <= 0 {
		return fmt.Errorf("video.create result: duration_ms must be > 0")
	}
	if len(r.CompletedStages) == 0 {
		return fmt.Errorf("video.create result: completed_stages must not be empty")
	}
	return nil
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

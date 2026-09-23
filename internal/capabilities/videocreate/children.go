package videocreate

import (
	"encoding/json"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Child-job identity (the §8 idempotency contract) ──────────────────
//
// Every child the workflow fans out to derives its idempotency key from
// ONE root key (the caller's idempotency key, e.g.
// "calendar:item-128:2026-09-24"). Re-running video.create — a retry, a
// broker redelivery, a restart mid-render — therefore converges on the
// SAME children:
//
//	calendar:128:2026-09-24:script
//	calendar:128:2026-09-24:youtube:scene:001
//	calendar:128:2026-09-24:stock:scene:003
//	calendar:128:2026-09-24:voiceover
//	calendar:128:2026-09-24:render:scene:001
//	calendar:128:2026-09-24:assemble
//
// The broker's (client_id, idempotency_key) UNIQUE pair answers a
// duplicate enqueue with the EXISTING child, so a replayed workflow
// cannot create a second script, re-download media, re-generate the
// voiceover or produce two videos. This is what makes the 30-day
// calendar safe to retry.

// ChildClientID is the canonical client identity the workflow enqueues
// its children under. Children are internal M2M consumers of the job
// registry, and this constant is the dedup namespace that separates
// their (client_id, idempotency_key) pairs from external submitters.
const ChildClientID = "internal:video.create"

// RootKey returns the idempotency root for a job. The caller-controlled
// M2M idempotency key is the root when present (it is exactly the
// calendar item identity); an internal submit with no key falls back to
// the job id, which is equally stable across retries of the SAME job.
func RootKey(idempotencyKey, jobID string) string {
	if k := strings.TrimSpace(idempotencyKey); k != "" {
		return k
	}
	return strings.TrimSpace(jobID)
}

// ChildKey derives one child idempotency key from the workflow root.
// The suffixes are the §8 canonical vocabulary; scene indexes are
// 1-based and zero-padded to 3 so the keys sort in timeline order.
func ChildKey(root, suffix string) string {
	return strings.TrimSpace(root) + ":" + suffix
}

// SceneChildKey derives a per-scene child key ("…:youtube:scene:001").
func SceneChildKey(root, family string, sceneIndex int) string {
	return ChildKey(root, fmt.Sprintf("%s:scene:%03d", family, sceneIndex))
}

// ChildCorrelationID scopes one child's correlation id away from its
// parent's. The broker dedupes on (type, correlation_id): a child that
// inherited the parent's running correlation id would resolve to the
// PARENT and never be created (the clip.render submit→settle lesson, in
// continuation.go::SettleCorrelationID). The scope is deterministic so a
// redelivered parent addresses the identical child.
func ChildCorrelationID(parentCorrelationID, jobType, childKey string) string {
	base := strings.TrimSpace(parentCorrelationID)
	if base == "" {
		base = jobType
	}
	return base + ":vc:" + childKey
}

// ── Typed child request payloads (stage-side wire projection) ─────────
//
// Each struct is the stage-side projection of the corresponding child
// family's wire contract: what the workflow MUST tell that child and
// nothing more. The children themselves keep owning their envelopes —
// video.create never re-implements their behaviour. Infrastructure
// facts (worker addresses, engine URLs, local paths) are deliberately
// absent here as well: they belong to the runtime executing the child.

// ScriptChildRequest drives the script.generate child (the §9 first
// stage: script + scene plan + canonical timeline facts).
type ScriptChildRequest struct {
	Topic           string `json:"topic"`
	Language        string `json:"language"`
	DurationSeconds int    `json:"duration_seconds"`
	// MediaSources is echoed so the script stage can pre-bind scene
	// sources to the families the media stage will search.
	MediaSources []string `json:"media_sources,omitempty"`
}

// MediaAcquireChildRequest drives one acquisition child
// (youtube_clip.extract for a YouTube candidate, media.stock for a
// stock/Artlist candidate). scene_index is 1-based timeline order.
type MediaAcquireChildRequest struct {
	Project     string `json:"project,omitempty"`
	VideoName   string `json:"video_name,omitempty"`
	SceneIndex  int    `json:"scene_index"`
	Source      string `json:"source"`
	SourceURL   string `json:"source_url,omitempty"`
	Query       string `json:"query,omitempty"`
	StartSecond int    `json:"start_second,omitempty"`
	EndSecond   int    `json:"end_second,omitempty"`
}

// VoiceoverChildRequest drives the voiceover.generate child (§12: the
// EXTERNAL-SAFE parent, never the internal voiceover.generate_item).
type VoiceoverChildRequest struct {
	Project         string   `json:"project,omitempty"`
	Language        string   `json:"language"`
	ScriptAssetID   string   `json:"script_asset_id"`
	Scenes          []string `json:"scenes,omitempty"`
	DurationSeconds int      `json:"duration_seconds"`
}

// RenderChildRequest drives one clip.render child (§14: RenderingGen →
// Chronon is behind that boundary; video.create never talks to Chronon).
type RenderChildRequest struct {
	Project      string   `json:"project,omitempty"`
	SceneID      string   `json:"scene_id"`
	SceneIndex   int      `json:"scene_index"`
	SourceRefs   []string `json:"source_refs"`
	OverlayPlan  bool     `json:"overlay_plan"`
	AspectRatio  string   `json:"aspect_ratio,omitempty"`
	Language     string   `json:"language"`
	TextSegments []string `json:"text_segments,omitempty"`
}

// AssemblePrepareChildRequest drives the canonical assembly.prepare
// child (§15: the production assembly path —
// internal/kernel/assembly's copy-certified contract, never a private
// concat and never an unwired assembler "because it exists").
type AssemblePrepareChildRequest struct {
	AssemblyID string            `json:"assembly_id"`
	Segments   []AssembleSegment `json:"segments"`
}

// AssembleFinalizeChildRequest drives the canonical assembly.finalize
// child (timeline order over the certified segments).
type AssembleFinalizeChildRequest struct {
	AssemblyID  string          `json:"assembly_id"`
	Preparation string          `json:"preparation_id"`
	Timeline    []AssembleScene `json:"timeline"`
}

// AssembleScene is one timeline entry of the assembly contract.
type AssembleScene struct {
	SceneID string `json:"scene_id"`
	AssetID string `json:"asset_id"`
}

// AssembleSegment is one copy-certified render segment admitted to the
// canonical assembler (kernel/media.AssemblyContract facts: closed GOP,
// first-frame keyframe, stream signature). The workflow refuses to
// assemble a segment whose copy certification is missing — the
// assembler rule is copy-only and there is no re-encode fallback.
type AssembleSegment struct {
	AssetID         string `json:"asset_id"`
	SHA256          string `json:"sha256"`
	DurationMS      int64  `json:"duration_ms"`
	CopyCertified   bool   `json:"copy_certified"`
	ContractID      string `json:"contract_id,omitempty"`
	StreamSignature string `json:"stream_signature,omitempty"`
}

// marshalChild encodes one typed child payload for the broker. The
// payload travels with the canonical parent link so the child's
// terminal commit can report back to the workflow's ledger without
// process-local memory (kernel/job.ParentLink).
func marshalChild(payload any, parentJobID, parentRunID string) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("videocreate: encode child payload: %w", err)
	}
	return job.InjectParentLink(raw, job.ParentLink{
		ParentJobID: parentJobID,
		ParentRunID: parentRunID,
	}), nil
}

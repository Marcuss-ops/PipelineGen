// Package scriptgeneration — model_render.go: the render and audio projection
// types of the pure domain model (ZERO I/O: the standard library plus the
// kernel's pure contract types, never an adapter), plus the CONTRACT of the
// central RenderingGen queue those types travel to and from.
//
// RenderReference, RenderArtifact, FinalAudioReference, AudioPipelineMetrics,
// TTSSSceneMetric and DocumentsConfig — the shapes the durable runner hands to
// the document/assembly stages. The primitive value types live in
// model_values.go and the aggregates in model.go.
//
// The queue section further down owns what the queue IS (the wire job shape, its
// state says, and the capabilities a client may implement); render_queue.go owns
// the enqueuer that speaks it. The two were one file until 2026-09-15: the
// contract, the vocabulary and the shared completion/re-arm rules grew past the
// strict per-file cap while they were being converged onto one owner.
//
// Extracted 2026-09-12 from model.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"encoding/json"
	"fmt"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

// ── Central RenderingGen queue contract (kernel-owned) ──────────────────
// The wire shapes, the terminal-state vocabulary, the completion/re-arm rules
// and the canonical request identity live in
// internal/kernel/script/renderqueue: the producer (this capability) and the
// consumer (internal/platform/renderinggen, an adapter) must be able to speak
// the same contract without importing each other. These are ALIASES, not
// re-declarations — the JSON shape, the field set and the methods are the
// kernel type, so the two sides cannot drift.
type (
	RenderArtifact                = renderqueue.RenderArtifact
	RenderQueueAsset              = renderqueue.RenderQueueAsset
	RenderQueueJob                = renderqueue.RenderQueueJob
	RenderQueuePreparedPackageRef = renderqueue.RenderQueuePreparedPackageRef
	RenderFrameRange              = renderqueue.RenderFrameRange
	RenderQueueClient             = renderqueue.RenderQueueClient
	RenderQueueBatchSubmitter     = renderqueue.RenderQueueBatchSubmitter
	RenderQueueChildrenReader     = renderqueue.RenderQueueChildrenReader
	RenderQueueWaiter             = renderqueue.RenderQueueWaiter
	RenderQueueRetrier            = renderqueue.RenderQueueRetrier
	RenderCompletionMetrics       = renderqueue.RenderCompletionMetrics
)

// ErrJobExists is returned by RenderQueueClient.Submit when a job with the
// same ID was already enqueued (idempotent replay).
var ErrJobExists = renderqueue.ErrJobExists

// ErrRenderRequestConflict is returned when a queue job id already exists but
// its immutable request identity differs from the one being submitted.
var ErrRenderRequestConflict = renderqueue.ErrRenderRequestConflict

// defaultQueuePollInterval mirrors the kernel default so the enqueuer keeps its
// historical cadence from ONE declaration.
const defaultQueuePollInterval = renderqueue.DefaultPollInterval

// NewRenderQueueAsset builds the wire projection from the canonical identity.
func NewRenderQueueAsset(ref kernelasset.Ref, logicalPath, sourceURL string) RenderQueueAsset {
	return renderqueue.NewRenderQueueAsset(ref, logicalPath, sourceURL)
}

// FinalAudioReference is the canonical delivery-audio contract.
type FinalAudioReference = audiocontract.FinalAudioReference

// AudioPipelineMetrics is an audio-friendly API projection of canonical
// observability stages and operations. It is not an authority and must not
// acquire independent timers or persistence writers.
//
// Relationship to the legacy domain contract: internal/kernel/script
// .GenerationTimings carries an overlapping set of flat *_ms audio fields used
// only by the migration-only internal/capabilities/scripts/usecase path. That
// struct is a legacy projection; this struct is the authority. Field map:
//
//	GenerationTimings (domain, legacy)  AudioPipelineMetrics (projection)
//	tts_total_ms                          tts_ms
//	audio_mix_ms                          mix_ms
//	audio_encode_ms                       aac_encode_ms
//	audio_probe_ms                        probe_ms
//	audio_hash_ms                         hash_ms
//	audio_pipeline_total_ms               total_ms
//	final_audio_duration_ms               audio_duration_ms
//	audio_encode_passes                   audio_encode_passes (unchanged)
//	tts_calls                             tts_calls (unchanged)
//	timeline_compile_ms                   timeline_compile_ms (unchanged)
//	audio_plan_compile_ms                 audio_plan_compile_ms (unchanged)
//	clip_audio_prepare_ms                 clip_audio_prepare_ms (unchanged)
//
// Projection-only fields with no legacy equivalent: media_fetch_ms, upload_ms,
// audio_rtf, audio_speed, tts_scenes.
//
// Do NOT add new timing fields here as an authority; derive them from the
// canonical RunReport and keep this struct stable for API consumers.
type AudioPipelineMetrics struct {
	TTSMS        int64 `json:"tts_ms"`
	MediaFetchMS int64 `json:"media_fetch_ms"`
	// AudioAssetResolveMS is the owner-measured BGM/SFX asset resolution
	// boundary. It is separate from plan compilation so resolver latency is
	// visible instead of being folded into the audio-plan phase.
	AudioAssetResolveMS int64             `json:"audio_asset_resolve_ms"`
	TimelineCompileMS   int64             `json:"timeline_compile_ms"`
	AudioPlanCompileMS  int64             `json:"audio_plan_compile_ms"`
	ClipAudioPrepareMS  int64             `json:"clip_audio_prepare_ms"`
	MixMS               int64             `json:"mix_ms"`
	AACEncodeMS         int64             `json:"aac_encode_ms"`
	ProbeMS             int64             `json:"probe_ms"`
	HashMS              int64             `json:"hash_ms"`
	UploadMS            int64             `json:"upload_ms"`
	TotalMS             int64             `json:"total_ms"`
	AudioDurationMS     int64             `json:"audio_duration_ms"`
	TTSCalls            int               `json:"tts_calls"`
	AudioRTF            float64           `json:"audio_rtf"`
	AudioSpeed          float64           `json:"audio_speed"`
	AudioEncodePasses   int               `json:"audio_encode_passes"`
	TTSScenes           []TTSSSceneMetric `json:"tts_scenes,omitempty"`
	// VoiceoverRequested counts every (scene, language) text that needed a
	// voiceover. VoiceoverReused counts scene-projection reuse (a voiceover
	// reference already attached before dispatch). VoiceoverGenerated counts
	// the items dispatched to the voiceover pipeline.
	VoiceoverRequested int `json:"voiceover_requested,omitempty"`
	VoiceoverReused    int `json:"voiceover_reused,omitempty"`
	VoiceoverGenerated int `json:"voiceover_generated,omitempty"`
	// VoiceoverDBCacheHits counts the dispatched items served by the
	// cross-run SQLite fingerprint cache (0 TTS calls, 0 uploads, 0
	// finalize). Warm identical runs report Generated == DBCacheHits and
	// TTSCalls == 0.
	VoiceoverDBCacheHits int `json:"voiceover_db_cache_hits,omitempty"`
	// CacheOutcome names how this run obtained the master: "HIT_PLAN" when a
	// previously certified artifact was reused for an identical plan, empty
	// when the audio was rendered. It is evidence of reuse, not a decision.
	CacheOutcome string `json:"cache_outcome,omitempty"`
}

type TTSSSceneMetric struct {
	SceneID          string   `json:"scene_id"`
	Language         Language `json:"language"`
	DurationMS       int64    `json:"duration_ms"`
	Characters       int      `json:"characters"`
	Words            int      `json:"words"`
	OutputDurationMS int64    `json:"output_duration_ms,omitempty"`
}

// marshalRenderingGenOverlayPlan converts a PipelineGen OverlayPlan into the
// RenderingGen wire payload (millisecond item timing), and runtimeFontAssets
// resolves the canonical font assets the queue job must carry. Both are wire-
// model conversions, so they live next to RenderQueueJob/RenderQueueAsset.

func marshalRenderingGenOverlayPlan(plan capmodel.OverlayPlan) ([]byte, error) {
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(wire["items"], &items); err != nil {
		return nil, err
	}
	for i, item := range items {
		// Map pin scope is PipelineGen geocoding metadata used to select and
		// rank locations. It is not part of RenderingGen's overlay-plan.v1
		// contract (which deliberately accepts only renderable pin fields), so
		// keep it on the in-memory plan but omit it from the worker payload.
		if encodedMap := item["map"]; len(encodedMap) > 0 {
			var mapWire map[string]json.RawMessage
			if err := json.Unmarshal(encodedMap, &mapWire); err != nil {
				return nil, fmt.Errorf("decode item %d map: %w", i, err)
			}
			// A raster-deferred map has no basemap asset by design. It must be
			// folded into the certified producer-video import before this wire
			// mapper is reached; fail closed instead of silently dropping the
			// routing marker and emitting an incomplete native map plan.
			if _, deferred := mapWire["dynamic_renderer_only"]; deferred {
				return nil, fmt.Errorf("item %d deferred map must use the certified producer import path", i)
			}
			// The geographic glow is consumed only by the dynamic single-map
			// pre-render path. A map folded into a shared semantic scene is
			// rendered by RenderingGen's map compiler, whose contract has no
			// area-glow input.
			delete(mapWire, "area_glow_radius_km")
			if encodedPins := mapWire["pins"]; len(encodedPins) > 0 {
				var pins []map[string]json.RawMessage
				if err := json.Unmarshal(encodedPins, &pins); err != nil {
					return nil, fmt.Errorf("decode item %d map pins: %w", i, err)
				}
				for _, pin := range pins {
					delete(pin, "scope")
				}
				wirePins, err := json.Marshal(pins)
				if err != nil {
					return nil, fmt.Errorf("encode item %d map pins: %w", i, err)
				}
				mapWire["pins"] = wirePins
			}
			wireMap, err := json.Marshal(mapWire)
			if err != nil {
				return nil, fmt.Errorf("encode item %d map: %w", i, err)
			}
			item["map"] = wireMap
		}
		var durationUS int64
		if encoded := item["duration_us"]; len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &durationUS); err != nil {
				return nil, fmt.Errorf("decode item %d duration_us: %w", i, err)
			}
		}
		if durationUS > 0 {
			var startMS, endMS int64
			if err := json.Unmarshal(item["start_ms"], &startMS); err != nil {
				return nil, fmt.Errorf("decode item %d start_ms: %w", i, err)
			}
			if err := json.Unmarshal(item["end_ms"], &endMS); err != nil {
				return nil, fmt.Errorf("decode item %d end_ms: %w", i, err)
			}
			if endMS <= startMS {
				return nil, fmt.Errorf("item %d has invalid millisecond timing %d-%d", i, startMS, endMS)
			}
			encoded, err := json.Marshal(endMS - startMS)
			if err != nil {
				return nil, err
			}
			item["duration_ms"] = encoded
		}
	}
	wireItems, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	wire["items"] = wireItems
	return json.Marshal(wire)
}

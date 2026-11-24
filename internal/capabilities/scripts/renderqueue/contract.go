// Package renderqueue — contract.go: the render and audio projection
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
package renderqueue

import (
	"encoding/json"
	"fmt"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	kernrenderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
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
	RenderQueueAsset              = kernrenderqueue.RenderQueueAsset
	RenderQueueJob                = kernrenderqueue.RenderQueueJob
	RenderQueuePreparedPackageRef = kernrenderqueue.RenderQueuePreparedPackageRef
	RenderFrameRange              = kernrenderqueue.RenderFrameRange
	RenderQueueClient             = kernrenderqueue.RenderQueueClient
	RenderQueueBatchSubmitter     = kernrenderqueue.RenderQueueBatchSubmitter
	RenderQueueChildrenReader     = kernrenderqueue.RenderQueueChildrenReader
	RenderQueueWaiter             = kernrenderqueue.RenderQueueWaiter
	RenderQueueRetrier            = kernrenderqueue.RenderQueueRetrier
	RenderCompletionMetrics       = kernrenderqueue.RenderCompletionMetrics
)

// ErrJobExists is returned by RenderQueueClient.Submit when a job with the
// same ID was already enqueued (idempotent replay).
var ErrJobExists = kernrenderqueue.ErrJobExists

// ErrRenderRequestConflict is returned when a queue job id already exists but
// its immutable request identity differs from the one being submitted.
var ErrRenderRequestConflict = kernrenderqueue.ErrRenderRequestConflict

// defaultQueuePollInterval mirrors the kernel default so the enqueuer keeps its
// historical cadence from ONE declaration.
const DefaultQueuePollInterval = kernrenderqueue.DefaultPollInterval

// NewRenderQueueAsset builds the wire projection from the canonical identity.
func NewRenderQueueAsset(ref kernelasset.Ref, logicalPath, sourceURL string) RenderQueueAsset {
	return kernrenderqueue.NewRenderQueueAsset(ref, logicalPath, sourceURL)
}

// marshalRenderingGenOverlayPlan converts a PipelineGen OverlayPlan into the
// RenderingGen wire payload (millisecond item timing), and runtimeFontAssets
// resolves the canonical font assets the queue job must carry. Both are wire-
// model conversions, so they live next to RenderQueueJob/RenderQueueAsset.

func MarshalRenderingGenOverlayPlan(plan capmodel.OverlayPlan) ([]byte, error) {
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

package scriptgeneration

import (
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// These wire structs mirror the strict CompiledRenderPlanV2 contract owned by
// the Master. Keep field order and omission rules aligned with
// velox-shared/contract.CompiledRenderPlanV2: the Master verifies canonical
// JSON byte-for-byte before dispatching the plan to worker 51.
type finalJobCompiledPlan struct {
	PlanVersion      int                      `json:"plan_version"`
	TimelineRevision int64                    `json:"timeline_revision"`
	TimelineSHA256   string                   `json:"timeline_sha256"`
	DurationUS       int64                    `json:"duration_us"`
	Output           finalJobPlanOutput       `json:"output"`
	FinalAudio       finalJobPlanAudio        `json:"final_audio"`
	VideoTracks      []finalJobPlanVideoTrack `json:"video_tracks"`
	Assets           []finalJobPlanAsset      `json:"assets"`
}

type finalJobPlanOutput struct {
	Container    string `json:"container"`
	VideoCodec   string `json:"video_codec"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FPSNum       int    `json:"fps_num"`
	FPSDen       int    `json:"fps_den"`
	PixelFormat  string `json:"pixel_format,omitempty"`
	ProfileID    string `json:"profile_id,omitempty"`
	CodecProfile string `json:"codec_profile,omitempty"`
	CodecLevel   string `json:"codec_level,omitempty"`
	GOPSize      int    `json:"gop_size,omitempty"`
	BFrames      int    `json:"b_frames"`
	ClosedGOP    bool   `json:"closed_gop,omitempty"`
	TimeBaseNum  int    `json:"time_base_num,omitempty"`
	TimeBaseDen  int    `json:"time_base_den,omitempty"`
}

type finalJobPlanAudio struct {
	Mode             string `json:"mode"`
	AssetID          string `json:"asset_id"`
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
	Codec            string `json:"codec"`
	SampleRateHz     int    `json:"sample_rate_hz"`
	Channels         int    `json:"channels"`
	DurationUS       int64  `json:"duration_us"`
	TimelineRevision int64  `json:"timeline_revision"`
	TimelineSHA256   string `json:"timeline_sha256"`
}

type finalJobPlanVideoTrack struct {
	TrackID  string                `json:"track_id"`
	Segments []finalJobPlanSegment `json:"segments"`
}

type finalJobPlanSegment struct {
	SegmentID          string `json:"segment_id"`
	AssetID            string `json:"asset_id"`
	SHA256             string `json:"sha256"`
	TimelineStartFrame int64  `json:"timeline_start_frame"`
	FrameCount         int64  `json:"frame_count"`
	SourceInUS         int64  `json:"source_in_us"`
	SourceDurationUS   int64  `json:"source_duration_us"`
}

type finalJobPlanAsset struct {
	AssetID            string `json:"asset_id"`
	AssetKey           string `json:"asset_key,omitempty"`
	SHA256             string `json:"sha256"`
	SizeBytes          int64  `json:"size_bytes"`
	Kind               string `json:"kind"`
	MIME               string `json:"mime,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	FrameCount         int64  `json:"frame_count,omitempty"`
	TimelineRevision   int64  `json:"timeline_revision,omitempty"`
	TimelineSHA256     string `json:"timeline_sha256,omitempty"`
	TimelineStartFrame int64  `json:"timeline_start_frame,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
}

type finalJobBaseSegment struct {
	startUS, endUS int64
	sourceInUS     int64
	assetID        string
	sha256         string
}

type finalJobOverlayWindow struct {
	startUS, endUS int64
	artifactInUS   int64
	assetID        string
	sha256         string
}

func finalJobCompiledPlanPayload(scenes []map[string]any, overlayRows []any, overlayManifest map[string]any, audio FinalAudioReference, audioRef map[string]any, timelineDurationUS int64) (string, string, error) {
	if len(scenes) == 0 {
		return "", "", fmt.Errorf("final_job V2 render plan requires at least one visual scene")
	}
	if audio.DurationUS <= 0 && audio.DurationMS > 0 {
		audio.DurationUS = audio.DurationMS * 1000
	}
	if audio.DurationUS <= 0 || audio.DurationMS <= 0 || !audio.CopyEligible || !audio.FinalMix {
		return "", "", fmt.Errorf("final_job V2 render plan requires certified final audio")
	}
	if timelineDurationUS <= 0 {
		return "", "", fmt.Errorf("final_job V2 render plan requires a positive canonical duration")
	}
	// The plan's output duration must match the certified media duration. The
	// canonical scene/audio timeline can differ by a few milliseconds because
	// the final AAC container reports packet-rounded duration; keep that drift
	// within the existing 40 ms alignment allowance, then use the actual final
	// audio duration as the worker timeline SSOT.
	if delta := audio.DurationUS - timelineDurationUS; delta < -40_000 || delta > 40_000 {
		return "", "", fmt.Errorf("final_job V2 final audio duration %dus does not match canonical timeline %dus", audio.DurationUS, timelineDurationUS)
	}
	timelineDurationUS = audio.DurationUS
	delivery := finalJobPlanDelivery()
	fpsNum, fpsDen := int64(delivery.FPS.Num), int64(delivery.FPS.Den)
	assets := &finalJobPlanAssets{}
	base, err := finalJobPlanBaseSegments(scenes, assets)
	if err != nil {
		return "", "", err
	}
	if err := assets.addRaw(audioRef, "final_audio", audio.DurationUS, 0, 0, 0); err != nil {
		return "", "", fmt.Errorf("final_job V2 final audio: %w", err)
	}
	baseEnd := base[len(base)-1].endUS
	delta := timelineDurationUS - baseEnd
	if delta < -40_000 || delta > 40_000 {
		return "", "", fmt.Errorf("final_job V2 scene duration %dus does not match certified final audio %dus", baseEnd, timelineDurationUS)
	}
	base[len(base)-1].endUS = timelineDurationUS
	if base[len(base)-1].endUS <= base[len(base)-1].startUS {
		return "", "", fmt.Errorf("final_job V2 final scene has no positive duration after audio alignment")
	}

	windows := make([]finalJobOverlayWindow, 0)
	manifests := []map[string]any{}
	if overlayManifest != nil {
		if overlayManifest["schema_version"] == "pipelinegen.overlay-manifest-set.v1" {
			rawManifests, ok := overlayManifest["manifests"].([]any)
			if !ok || len(rawManifests) == 0 {
				return "", "", fmt.Errorf("final_job overlay manifest set has no manifests")
			}
			for _, raw := range rawManifests {
				manifest, ok := raw.(map[string]any)
				if !ok {
					return "", "", fmt.Errorf("final_job overlay manifest set contains an invalid manifest")
				}
				manifests = append(manifests, manifest)
			}
		} else {
			manifests = append(manifests, overlayManifest)
		}
	}
	for _, manifest := range manifests {
		asset, ok := manifest["asset"].(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("final_job overlay manifest has no typed asset")
		}
		assetID := finalJobStringValue(asset["asset_id"])
		sha := strings.ToLower(finalJobStringValue(asset["sha256"]))
		durationUS := finalJobInt64(asset["duration_ms"]) * 1000
		if durationUS <= 0 {
			// The manifest asset is a timeline-wide render. Some renderer versions
			// omit duration_ms while still certifying frame_count; the canonical
			// final timeline is the exact span the asset occupies.
			durationUS = timelineDurationUS
		}
		frameCount := finalJobInt64(asset["frame_count"])
		if err := assets.addRaw(asset, "video", durationUS, delivery.Width, delivery.Height, frameCount); err != nil {
			return "", "", fmt.Errorf("final_job overlay timeline asset: %w", err)
		}
		entries, ok := manifest["entries"].([]any)
		if !ok {
			return "", "", fmt.Errorf("final_job overlay manifest entries have an invalid shape")
		}
		for i, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				return "", "", fmt.Errorf("final_job overlay manifest entry %d has an invalid shape", i)
			}
			startUS, endUS := finalJobInt64(entry["in_us"]), finalJobInt64(entry["out_us"])
			artifactInUS, artifactOutUS := startUS, endUS
			if value, present := entry["artifact_in_us"]; present {
				artifactInUS = finalJobInt64(value)
			}
			if value, present := entry["artifact_out_us"]; present {
				artifactOutUS = finalJobInt64(value)
			}
			if startUS < 0 || endUS <= startUS || artifactInUS < 0 || artifactOutUS-artifactInUS != endUS-startUS {
				return "", "", fmt.Errorf("final_job overlay manifest entry %d has a non-projectable timing window", i)
			}
			if startUS >= timelineDurationUS {
				continue
			}
			if endUS > timelineDurationUS {
				artifactOutUS -= endUS - timelineDurationUS
				endUS = timelineDurationUS
			}
			windows = append(windows, finalJobOverlayWindow{startUS: startUS, endUS: endUS, artifactInUS: artifactInUS, assetID: assetID, sha256: sha})
		}
	}
	for i, raw := range overlayRows {
		row, ok := raw.(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("final_job dedicated overlay row %d has an invalid shape", i)
		}
		assetID := finalJobStringValue(row["asset_id"])
		sha := strings.ToLower(finalJobStringValue(row["sha256"]))
		startFrame, endFrame := finalJobInt64(row["start_frame"]), finalJobInt64(row["end_frame"])
		if startFrame < 0 || endFrame <= startFrame {
			return "", "", fmt.Errorf("final_job dedicated overlay row %d has an invalid frame window", i)
		}
		startUS := startFrame * 1_000_000 * fpsDen / fpsNum
		endUS := endFrame * 1_000_000 * fpsDen / fpsNum
		if startUS >= timelineDurationUS {
			continue
		}
		if endUS > timelineDurationUS {
			endUS = timelineDurationUS
		}
		durationUS := finalJobInt64(row["duration_ms"]) * 1000
		if durationUS <= 0 {
			durationUS = endUS - startUS
		}
		if err := assets.addRaw(row, "video", durationUS, delivery.Width, delivery.Height, finalJobInt64(row["frame_count"])); err != nil {
			return "", "", fmt.Errorf("final_job dedicated overlay row %d: %w", i, err)
		}
		windows = append(windows, finalJobOverlayWindow{startUS: startUS, endUS: endUS, artifactInUS: 0, assetID: assetID, sha256: sha})
	}
	windows, err = finalJobMergeOverlayWindows(windows)
	if err != nil {
		return "", "", err
	}
	segments, err := finalJobComposePlanSegments(base, windows, timelineDurationUS, fpsNum, fpsDen)
	if err != nil {
		return "", "", err
	}
	if len(segments) == 0 {
		return "", "", fmt.Errorf("final_job V2 render plan has no video segments")
	}

	timelineRevision := int64(1)
	timelineMaterial := map[string]any{
		"duration_us":             timelineDurationUS,
		"audio_plan_sha256":       strings.ToLower(strings.TrimSpace(audio.PlanSHA256)),
		"overlay_timeline_sha256": "",
	}
	if len(manifests) == 1 {
		if asset, ok := manifests[0]["asset"].(map[string]any); ok {
			timelineMaterial["overlay_timeline_sha256"] = strings.ToLower(finalJobStringValue(asset["sha256"]))
		}
	} else if len(manifests) > 1 {
		hashes := make([]string, 0, len(manifests))
		for _, manifest := range manifests {
			asset := manifest["asset"].(map[string]any)
			hashes = append(hashes, strings.ToLower(finalJobStringValue(asset["sha256"])))
		}
		timelineMaterial["overlay_timeline_sha256"] = digest.SHA256String(strings.Join(hashes, "\x00"))
	}
	timelineBytes, err := json.Marshal(timelineMaterial)
	if err != nil {
		return "", "", fmt.Errorf("marshal final_job V2 timeline identity: %w", err)
	}
	timelineSHA := digest.SHA256String(string(timelineBytes))
	plan := finalJobCompiledPlan{
		PlanVersion: 2, TimelineRevision: timelineRevision, TimelineSHA256: timelineSHA,
		DurationUS: timelineDurationUS,
		Output:     finalJobPlanOutputBlock(),
		FinalAudio: finalJobPlanAudio{
			Mode: "FINAL_AUDIO_COPY", AssetID: finalJobStringValue(audioRef["asset_id"]),
			SHA256: strings.ToLower(finalJobStringValue(audioRef["sha256"])), SizeBytes: finalJobInt64(audioRef["size_bytes"]),
			Codec: delivery.AudioCodec, SampleRateHz: delivery.AudioSampleRate, Channels: delivery.AudioChannels,
			DurationUS:       audio.DurationUS,
			TimelineRevision: timelineRevision, TimelineSHA256: timelineSHA,
		},
		VideoTracks: []finalJobPlanVideoTrack{{TrackID: "main-video", Segments: segments}},
		Assets:      assets.sorted(),
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return "", "", fmt.Errorf("marshal final_job canonical V2 render plan: %w", err)
	}
	return string(canonical), digest.SHA256String(string(canonical)), nil
}

// finalJobPlanBaseSegments describes one source segment per scene and types
// every stock asset INTO the shared registry the caller already owns.
func finalJobPlanBaseSegments(scenes []map[string]any, assets *finalJobPlanAssets) ([]finalJobBaseSegment, error) {
	base := make([]finalJobBaseSegment, 0, len(scenes))
	var cursorUS int64
	for i, scene := range scenes {
		asset, ok := scene["stock"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("final_job scene %d has no video asset", i)
		}
		durationSeconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) || durationSeconds <= 0 {
			return nil, fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationUS := int64(math.Round(durationSeconds * 1_000_000))
		assetDurationUS := finalJobInt64(asset["duration_ms"]) * 1000
		if err := assets.addRaw(asset, "video", assetDurationUS, finalJobInt(asset["width"]), finalJobInt(asset["height"]), finalJobInt64(asset["frame_count"])); err != nil {
			return nil, fmt.Errorf("final_job scene %d: %w", i, err)
		}
		sourceInUS := finalJobInt64(asset["start_ms"]) * 1000
		assetID := finalJobStringValue(asset["asset_id"])
		sha := strings.ToLower(finalJobStringValue(asset["sha256"]))
		base = append(base, finalJobBaseSegment{startUS: cursorUS, endUS: cursorUS + durationUS, sourceInUS: sourceInUS, assetID: assetID, sha256: sha})
		cursorUS += durationUS
	}
	return base, nil
}

func finalJobMergeOverlayWindows(input []finalJobOverlayWindow) ([]finalJobOverlayWindow, error) {
	sort.SliceStable(input, func(i, j int) bool {
		if input[i].startUS != input[j].startUS {
			return input[i].startUS < input[j].startUS
		}
		return input[i].endUS < input[j].endUS
	})
	merged := make([]finalJobOverlayWindow, 0, len(input))
	for _, current := range input {
		if current.endUS <= current.startUS || current.assetID == "" || len(current.sha256) != 64 {
			return nil, fmt.Errorf("final_job overlay timeline contains an invalid replacement window")
		}
		if len(merged) > 0 && current.startUS < merged[len(merged)-1].endUS {
			prior := &merged[len(merged)-1]
			if prior.assetID != current.assetID || prior.sha256 != current.sha256 || prior.artifactInUS-prior.startUS != current.artifactInUS-current.startUS {
				return nil, fmt.Errorf("final_job overlay replacement windows overlap with different artifact timing")
			}
			if current.endUS > prior.endUS {
				prior.endUS = current.endUS
			}
			continue
		}
		// Adjacent windows of the SAME artifact that are also contiguous inside
		// it are one continuous take: keeping them apart would pay two fragment
		// encodes (and two verifications, two uploads) for one piece of video.
		if len(merged) > 0 {
			prior := &merged[len(merged)-1]
			if prior.assetID == current.assetID && prior.sha256 == current.sha256 &&
				prior.endUS == current.startUS &&
				prior.artifactInUS+(prior.endUS-prior.startUS) == current.artifactInUS {
				prior.endUS = current.endUS
				continue
			}
		}
		merged = append(merged, current)
	}
	return merged, nil
}

func finalJobComposePlanSegments(base []finalJobBaseSegment, overlays []finalJobOverlayWindow, durationUS, fpsNum, fpsDen int64) ([]finalJobPlanSegment, error) {
	segments := make([]finalJobPlanSegment, 0, len(base)+2*len(overlays))
	cursorUS := int64(0)
	appendBase := func(startUS, endUS int64) error {
		if endUS <= startUS {
			return nil
		}
		for _, source := range base {
			inUS, outUS := max(startUS, source.startUS), min(endUS, source.endUS)
			if outUS <= inUS {
				continue
			}
			segments = append(segments, finalJobPlanSegment{
				SegmentID: fmt.Sprintf("segment-%04d", len(segments)), AssetID: source.assetID, SHA256: source.sha256,
				TimelineStartFrame: finalJobFrameAtUS(inUS, fpsNum, fpsDen),
				FrameCount:         finalJobFrameAtUS(outUS, fpsNum, fpsDen) - finalJobFrameAtUS(inUS, fpsNum, fpsDen),
				SourceInUS:         source.sourceInUS + (inUS - source.startUS), SourceDurationUS: outUS - inUS,
			})
		}
		return nil
	}
	for _, overlay := range overlays {
		if overlay.startUS < cursorUS || overlay.endUS > durationUS {
			return nil, fmt.Errorf("final_job overlay windows are not ordered within the output timeline")
		}
		if err := appendBase(cursorUS, overlay.startUS); err != nil {
			return nil, err
		}
		startFrame := finalJobFrameAtUS(overlay.startUS, fpsNum, fpsDen)
		endFrame := finalJobFrameAtUS(overlay.endUS, fpsNum, fpsDen)
		segments = append(segments, finalJobPlanSegment{
			SegmentID: fmt.Sprintf("segment-%04d", len(segments)), AssetID: overlay.assetID, SHA256: overlay.sha256,
			TimelineStartFrame: startFrame, FrameCount: endFrame - startFrame,
			SourceInUS: overlay.artifactInUS, SourceDurationUS: overlay.endUS - overlay.startUS,
		})
		cursorUS = overlay.endUS
	}
	if err := appendBase(cursorUS, durationUS); err != nil {
		return nil, err
	}
	segments = finalJobMergeContiguousSegments(segments)
	for i, segment := range segments {
		if segment.FrameCount <= 0 || segment.SourceDurationUS <= 0 {
			return nil, fmt.Errorf("final_job V2 segment %d has an empty frame or source window", i)
		}
	}
	return segments, nil
}

// finalJobMergeContiguousSegments collapses neighbouring segments that are the
// same source window run end to end: same asset, contiguous on the timeline and
// contiguous inside the source. The picture is identical by construction — a
// fragment is just a window of one asset — but every fragment costs one local
// encode, one verification and one upload, so gluing them is pure savings.
// Anything that is not provably one continuous window is left untouched.
func finalJobMergeContiguousSegments(segments []finalJobPlanSegment) []finalJobPlanSegment {
	if len(segments) < 2 {
		return segments
	}
	merged := make([]finalJobPlanSegment, 0, len(segments))
	for _, current := range segments {
		if len(merged) > 0 {
			prior := &merged[len(merged)-1]
			if prior.AssetID == current.AssetID && prior.SHA256 == current.SHA256 &&
				prior.TimelineStartFrame+prior.FrameCount == current.TimelineStartFrame &&
				prior.SourceInUS+prior.SourceDurationUS == current.SourceInUS {
				prior.FrameCount += current.FrameCount
				prior.SourceDurationUS += current.SourceDurationUS
				continue
			}
		}
		merged = append(merged, current)
	}
	// Renumber so the segment ids stay dense and stable for the receipt and the
	// prepared-fragment identity (which hashes the segment id).
	for i := range merged {
		merged[i].SegmentID = fmt.Sprintf("segment-%04d", i)
	}
	return merged
}

func finalJobFrameAtUS(us, fpsNum, fpsDen int64) int64 {
	numerator := us * fpsNum
	denominator := int64(1_000_000) * fpsDen
	return (numerator + denominator/2) / denominator
}

func finalJobInt64(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var i int64
		_, _ = fmt.Sscan(strings.TrimSpace(n), &i)
		return i
	default:
		return 0
	}
}

func finalJobInt(value any) int {
	return int(finalJobInt64(value))
}

// --- the asset registry both plan builders share ---

// finalJobPlanAssets is the ONE registry a CompiledRenderPlanV2 is built
// through. The payload builder (scenes + overlay manifests) and the producer
// (prepared fragments replacing those sources) both add their assets here, so
// the identity rule, the duplicate rule, the MIME mapping, the sort order and
// the output block exist once instead of in two builders that must agree.
//
// Adding a raw (map-shaped) asset and adding an already-typed asset are the
// same operation on the same identity; only the parsing differs.
type finalJobPlanAssets struct {
	items []finalJobPlanAsset
}

// finalJobPlanDelivery is the delivery identity the plan describes. It is the
// kernel assembly contract, not a copy: geometry, cadence, GOP, timebase and
// audio layout move together when the contract moves.
func finalJobPlanDelivery() kernelmedia.AssemblyMediaContract {
	return kernelmedia.DefaultAssemblyMediaContract()
}

// finalJobPlanOutputBlock is the plan's output block, derived from that contract.
func finalJobPlanOutputBlock() finalJobPlanOutput {
	delivery := finalJobPlanDelivery()
	return finalJobPlanOutput{
		Container: delivery.Container, VideoCodec: delivery.VideoCodec,
		Width: delivery.Width, Height: delivery.Height,
		FPSNum: delivery.FPS.Num, FPSDen: delivery.FPS.Den, PixelFormat: delivery.PixelFormat,
		ProfileID: delivery.ID, CodecProfile: delivery.VideoProfile, CodecLevel: delivery.VideoLevel,
		GOPSize: delivery.KeyframeInterval, BFrames: delivery.BFrames, ClosedGOP: delivery.ClosedGOP,
		TimeBaseNum: delivery.VideoTimeBase.Num, TimeBaseDen: delivery.VideoTimeBase.Den,
	}
}

func finalJobPlanAssetMIME(kind string) string {
	switch kind {
	case "final_audio":
		return "audio/mp4"
	default:
		return "video/mp4"
	}
}

// add types one asset into the plan. A repeated asset id must describe the same
// bytes, size and kind: the plan is an immutable document, so a second
// reference that disagrees is a producer bug, not an update.
func (a *finalJobPlanAssets) add(asset finalJobPlanAsset) error {
	if asset.AssetID == "" || len(asset.SHA256) != 64 || asset.SizeBytes <= 0 || asset.DurationUS <= 0 {
		return fmt.Errorf("asset requires id, 64-character SHA-256, positive size, and duration")
	}
	for _, prior := range a.items {
		if prior.AssetID != asset.AssetID {
			continue
		}
		if prior.SHA256 != asset.SHA256 || prior.SizeBytes != asset.SizeBytes || prior.Kind != asset.Kind {
			return fmt.Errorf("asset %q was referenced with conflicting identity", asset.AssetID)
		}
		return nil
	}
	a.items = append(a.items, asset)
	return nil
}

// addRaw types a map-shaped source reference (stock scene, overlay artifact or
// final audio) into the same registry.
func (a *finalJobPlanAssets) addRaw(raw map[string]any, kind string, durationUS int64, width, height int, frameCount int64) error {
	return a.add(finalJobPlanAsset{
		AssetID: finalJobStringValue(raw["asset_id"]),
		SHA256:  strings.ToLower(finalJobStringValue(raw["sha256"])),
		// SizeBytes is checked by add; the raw map spells it in bytes already.
		SizeBytes: finalJobInt64(raw["size_bytes"]),
		Kind:      kind, MIME: finalJobPlanAssetMIME(kind),
		DurationUS: durationUS, Width: width, Height: height, FrameCount: frameCount,
	})
}

// set replaces the registry contents (the producer re-binds every source to a
// prepared fragment, so its result is the complete list, not an increment).
func (a *finalJobPlanAssets) set(assets []finalJobPlanAsset) error {
	a.items = nil
	for _, asset := range assets {
		if err := a.add(asset); err != nil {
			return err
		}
	}
	return nil
}

// sorted returns the plan's asset list in the canonical order (asset id), which
// is what the plan digest covers.
func (a *finalJobPlanAssets) sorted() []finalJobPlanAsset {
	sort.Slice(a.items, func(i, j int) bool { return a.items[i].AssetID < a.items[j].AssetID })
	return a.items
}

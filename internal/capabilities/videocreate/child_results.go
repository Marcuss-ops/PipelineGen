package videocreate

import (
	"encoding/json"
	"fmt"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Typed child result decoders: the REAL result shapes ───────────────
//
// godlike/06 SSOT: every decoder below consumes the child family's OWN
// result wire contract — the exact JSON the registered handler writes
// into job.Result — and projects it onto the workflow's durable stage
// facts. children_contracts_test.go pins each decoder against real
// handler-shaped fixtures so a child-side result change breaks the
// build here instead of silently emptying a stage.

// ── script.generate (two live result shapes) ──────────────────────────

// scriptDurableWire mirrors the durable single-item script.generate
// result map (scripts/jobs/generation_handler.go::Handle, durable
// branch): {run_id, parent_state, result, stage_progress,
// __artifact_manifest}.
type scriptDurableWire struct {
	RunID       string                    `json:"run_id"`
	ParentState string                    `json:"parent_state"`
	Result      *scriptgen.GenerateResult `json:"result"`
	Manifest    *job.ArtifactManifest     `json:"__artifact_manifest"`
}

// decodeScriptChildResult decodes BOTH real script.generate result
// shapes into the workflow's durable projection:
//
//  1. the durable single-item lane ({run_id, parent_state, result:
//     GenerateResult, __artifact_manifest}), whose canonical projection
//     DurableResultToDomain preserves the SpecScene/voiceover surface;
//  2. the batch/envelope lane (GenerationEnvelopeResult with
//     items[0].result).
//
// Both converge on kernel/script's GenerationResult so one projection
// owns the workflow's view of a generated script.
func decodeScriptChildResult(raw json.RawMessage) (ScriptChildResult, error) {
	if len(raw) == 0 {
		return ScriptChildResult{}, fmt.Errorf("videocreate: script.generate returned no result")
	}
	var durable scriptDurableWire
	durableErr := json.Unmarshal(raw, &durable)
	if durableErr == nil && durable.Result != nil {
		plan := json.RawMessage(nil)
		if durable.Result.AudioPlan != nil {
			if encoded, err := json.Marshal(durable.Result.AudioPlan); err == nil {
				plan = encoded
			}
		}
		assetID := scriptAssetIDFromManifest(durable.Manifest)
		if assetID == "" {
			assetID = durable.RunID
		}
		return projectScriptResult(scriptgen.DurableResultToDomain(durable.Result), plan, assetID), nil
	}
	var envelope scriptpkg.GenerationEnvelopeResult
	envelopeErr := json.Unmarshal(raw, &envelope)
	if envelopeErr == nil && len(envelope.Items) > 0 {
		item := envelope.Items[0]
		if item.Error != "" {
			return ScriptChildResult{}, fmt.Errorf("videocreate: script.generate item %s failed: %s (%s)", item.ItemID, item.Error, item.ErrorCode)
		}
		return projectScriptResult(item.Result, nil, item.ItemID), nil
	}
	if durableErr != nil {
		return ScriptChildResult{}, fmt.Errorf("videocreate: script.generate result is not a known contract shape: %v", durableErr)
	}
	return ScriptChildResult{}, fmt.Errorf("videocreate: script.generate result is not a known contract shape")
}

// projectScriptResult is the shared projection over the canonical
// kernel/script result (both lanes converge here).
func projectScriptResult(res *scriptpkg.GenerationResult, plan json.RawMessage, scriptAssetID string) ScriptChildResult {
	out := ScriptChildResult{ScriptAssetID: scriptAssetID, AudioPlan: plan}
	if res == nil {
		return out
	}
	for _, scene := range res.Output.SpecScene.Scenes {
		out.Scenes = append(out.Scenes, scene.ID)
		text := strings.TrimSpace(scene.Text)
		if text == "" {
			text = strings.TrimSpace(scene.DisplayText)
		}
		out.TextSegments = append(out.TextSegments, text)
		if vo := scene.Bindings.Voiceover; vo != nil && strings.TrimSpace(vo.LocalPath) != "" {
			out.Voiceovers = append(out.Voiceovers, VoiceoverAssetRef{
				SceneID:    scene.ID,
				LocalRef:   LocalRef{LocalPath: vo.LocalPath},
				DurationMS: vo.DurationMs,
			})
		}
	}
	if res.FinalAudio != nil {
		fa := res.FinalAudio
		out.FinalAudio = &FinalAudioChildResult{
			AssetID:              fa.AssetID,
			ContentSHA:           fa.FinalAudioSHA256,
			DurationMS:           fa.DurationMS,
			SampleRate:           fa.SampleRate,
			Channels:             fa.Channels,
			ChannelLayout:        fa.ChannelLayout,
			Codec:                fa.Codec,
			Profile:              fa.Profile,
			Bitrate:              fa.Bitrate,
			SizeBytes:            fa.SizeBytes,
			StartPTS:             fa.StartPTS,
			FinalMix:             fa.FinalMix,
			CopyEligible:         fa.CopyEligible,
			AudioContractVersion: fa.AudioContractVersion,
			AudioPlanVersion:     fa.AudioPlanVersion,
			AudioPlanSHA256:      fa.AudioPlanSHA256,
			LocalRef:             LocalRef{LocalPath: fa.Path},
		}
	}
	return out
}

// scriptAssetIDFromManifest returns the script artifact identity from
// the canonical artifact manifest (the script_json artifact is the
// durable script surface of the child).
func scriptAssetIDFromManifest(manifest *job.ArtifactManifest) string {
	if manifest == nil {
		return ""
	}
	for _, artifact := range manifest.Artifacts {
		if string(artifact.Kind) == "script_json" || string(artifact.Kind) == "script" {
			return artifact.ID
		}
	}
	return ""
}

// ── youtube_clip.extract ──────────────────────────────────────────────

// youtubeAcquireWire mirrors the youtube_clip.extract result map
// (youtube/jobs/job_handler.go::buildResultMap). Per-item rows use the
// canonical ExtractItem type the handler serialises.
type youtubeAcquireWire struct {
	OK            bool                       `json:"ok"`
	SourceURL     string                     `json:"source_url"`
	VideoID       string                     `json:"video_id"`
	Stats         *youtubetypes.ExtractStats `json:"stats"`
	Items         []youtubetypes.ExtractItem `json:"items"`
	Error         string                     `json:"error"`
	DriveFolderID string                     `json:"drive_folder_id"`
}

// ── media.stock ───────────────────────────────────────────────────────

// ── voiceover.generate (fan-out parent + per-item children) ───────────

// voiceoverFanoutWire mirrors the voiceover.generate parent result map
// (voiceover/service/jobs/generate_handler.go::toFanoutResultMap).
type voiceoverFanoutWire struct {
	OK                 bool     `json:"ok"`
	ParentJobID        string   `json:"parent_job_id"`
	RequestID          string   `json:"request_id"`
	TotalOutputs       int      `json:"total_outputs"`
	EnqueuedCount      int      `json:"enqueued_count"`
	FailedEnqueueCount int      `json:"failed_enqueue_count"`
	ChildJobIDs        []string `json:"child_job_ids"`
	PerLanguage        []string `json:"per_language"`
	ParentState        string   `json:"parent_state"`
}

// voiceoverItemWire mirrors the voiceover.generate_item result map
// (voiceover/service/jobs/generate_item_handler.go::toItemResultMap).
type voiceoverItemWire struct {
	JobID       string `json:"job_id"`
	ParentJobID string `json:"parent_job_id"`
	RequestID   string `json:"request_id"`
	Language    string `json:"language"`
	Status      string `json:"status"`
	OK          bool   `json:"ok"`
	Voice       string `json:"voice"`
	DriveLink   string `json:"drive_link"`
	DriveFileID string `json:"drive_file_id"`
	LocalPath   string `json:"local_path"`
	Error       string `json:"error"`
	ErrorCode   string `json:"error_code"`
	Timing      *struct {
		DurationUS int64 `json:"duration_us"`
	} `json:"timing,omitempty"`
}

// decodeVoiceoverItemResult projects one voiceover.generate_item
// result map onto the workflow's voiceover materialization facts.
func decodeVoiceoverItemResult(raw json.RawMessage) (VoiceoverAssetRef, error) {
	var wire voiceoverItemWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return VoiceoverAssetRef{}, fmt.Errorf("videocreate: voiceover.generate_item result: %w", err)
	}
	if !wire.OK {
		return VoiceoverAssetRef{}, fmt.Errorf("videocreate: voiceover.generate_item %s failed: %s (%s)", wire.JobID, wire.Error, wire.ErrorCode)
	}
	ref := VoiceoverAssetRef{
		Language: wire.Language,
		DriveRef: DriveRef{DriveFileID: wire.DriveFileID},
		LocalRef: LocalRef{LocalPath: wire.LocalPath},
	}
	if wire.Timing != nil {
		ref.DurationMS = wire.Timing.DurationUS / 1000
	}
	return ref, nil
}

// ── clip.render ───────────────────────────────────────────────────────

// renderContractWire is the clip.render output-contract block the
// worker reports (cliprender/worker_result.go::renderedResult).
type renderContractWire struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPSNum      int    `json:"fps_num"`
	FPSDen      int    `json:"fps_den"`
	VideoCodec  string `json:"video_codec"`
	AudioCodec  string `json:"audio_codec"`
	PixelFormat string `json:"pixel_format"`
}

// renderRunWire is the clip.render render-outcome block (the fields
// this workflow projects from renderedResult's `render` map).
type renderRunWire struct {
	OutputPath        string  `json:"output_path"`
	SizeBytes         int64   `json:"size_bytes"`
	DurationSec       float64 `json:"duration_sec"`
	Backend           string  `json:"backend"`
	AudioCopyEligible *bool   `json:"audio_copy_eligible"`
}

// renderAssetWire is the published derived-asset block. The Drive
// location lives in the DriveRef value object, so a content address
// never shares one struct declaration with a location (the
// percheck_media_identity_no_location_fields gate).
type renderAssetWire struct {
	AssetID string `json:"asset_id"`
	DriveRef
	SizeBytes         int64  `json:"size_bytes"`
	PublicationStatus string `json:"publication_status"`
}

// renderResultWire mirrors the clip.render result map
// (cliprender/worker_result.go::renderedResult): the sealed plan, the
// render outcome and the published derived asset.
type renderResultWire struct {
	JobID         string             `json:"job_id"`
	SourceAssetID string             `json:"source_asset_id"`
	ContractID    string             `json:"contract_id"`
	Contract      renderContractWire `json:"contract"`
	Render        renderRunWire      `json:"render"`
	Asset         renderAssetWire    `json:"asset"`
}

// renderSubmissionWire is the submit-phase envelope of the clip.render
// async boundary (cliprender/worker.go: phase="submitted",
// parent_state="waiting_children", child_job_id pointing at the settle
// continuation that owns materialize/probe/publish).
type renderSubmissionWire struct {
	Phase      string `json:"phase"`
	ChildJobID string `json:"child_job_id"`
}

// renderSettleChildID returns the settle continuation job id when raw is
// a submit-phase result, else "". Consumers must follow that child
// instead of decoding the submit-phase result as a materialized segment.
func renderSettleChildID(raw json.RawMessage) string {
	var wire renderSubmissionWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ""
	}
	if strings.TrimSpace(wire.Phase) != "submitted" {
		return ""
	}
	return strings.TrimSpace(wire.ChildJobID)
}

// renderSettleError marks a failure at the render settle boundary
// (remote render execution): the one failure class a bounded
// re-attempt can recover from. Contract and payload failures never
// carry it.
type renderSettleError struct {
	id     string
	reason string
}

func (e renderSettleError) Error() string {
	return fmt.Sprintf("clip.render settle %s failed: %s", e.id, e.reason)
}

// decodeRenderChildResult projects one clip.render result map onto the
// workflow's render-segment facts. The copy certification itself is
// DERIVED (certifyRenderedSegment) from the reported output contract +
// a fresh probe of the segment file — it is never taken on trust from a
// field alone.
func decodeRenderChildResult(raw json.RawMessage) (RenderChildResult, error) {
	var wire renderResultWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return RenderChildResult{}, fmt.Errorf("videocreate: clip.render result: %w", err)
	}
	if wire.Asset.AssetID == "" || wire.Render.OutputPath == "" {
		return RenderChildResult{}, fmt.Errorf("videocreate: clip.render result has no asset/local materialization")
	}
	return RenderChildResult{
		AssetID:       wire.Asset.AssetID,
		SourceAssetID: wire.SourceAssetID,
		DurationMS:    int64(wire.Render.DurationSec*1000 + 0.5),
		ContractID:    wire.ContractID,
		Contract: RenderContractFacts{
			Width:       wire.Contract.Width,
			Height:      wire.Contract.Height,
			FPSNum:      wire.Contract.FPSNum,
			FPSDen:      wire.Contract.FPSDen,
			VideoCodec:  wire.Contract.VideoCodec,
			AudioCodec:  wire.Contract.AudioCodec,
			PixelFormat: wire.Contract.PixelFormat,
		},
		LocalRef: LocalRef{LocalPath: wire.Render.OutputPath},
	}, nil
}

// assembleSegment projects the render result onto the VeloxEditing
// assembly boundary's segment. The copy certification is DERIVED from
// the reported assembly-ready output contract (closed GOP + first-frame
// keyframe are that contract's own guarantees) — never taken on trust
// from an unrelated field. A segment whose contract is not the
// assembly-ready contract fails closed here.
func (r RenderChildResult) assembleSegment() (AssembleSegment, error) {
	if r.AssetID == "" || strings.TrimSpace(r.LocalPath) == "" {
		return AssembleSegment{}, fmt.Errorf("videocreate: render result has no asset/local materialization")
	}
	if r.ContractID != kernelmedia.AssemblyMediaContractID {
		return AssembleSegment{}, fmt.Errorf("videocreate: render segment contract %q is not the assembly-ready contract %s (no copy certification)", r.ContractID, kernelmedia.AssemblyMediaContractID)
	}
	return AssembleSegment{
		AssetID:       r.AssetID,
		DurationMS:    r.DurationMS,
		CopyCertified: true,
		ContractID:    r.ContractID,
		Contract:      r.Contract,
		LocalRef:      r.LocalRef,
	}, nil
}

// ── acquisition (family dispatcher) ───────────────────────────────────

// decodeAcquireChildResult projects one acquisition child result (the
// youtube_clip.extract or media.stock wire shape) onto the workflow's
// acquired-clip facts.
func decodeAcquireChildResult(jobType string, raw json.RawMessage) (AcquireChildResult, error) {
	switch jobType {
	case appjobs.TypeYouTubeClipExtract:
		var wire youtubeAcquireWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return AcquireChildResult{}, fmt.Errorf("videocreate: youtube_clip.extract result: %w", err)
		}
		for _, item := range wire.Items {
			if item.Status == "failed" || item.Error != "" {
				continue
			}
			if item.ID == "" && item.LocalPath == "" {
				continue
			}
			return AcquireChildResult{
				AssetID:    item.ID,
				ContentSHA: item.LegacyFileMD5,
				DurationMS: int64(item.Duration) * 1000,
				MediaType:  "video",
				Source:     "youtube",
				SourceURL:  wire.SourceURL,
				DriveRef:   DriveRef{DriveFileID: item.DriveFileID},
				LocalRef:   LocalRef{LocalPath: item.LocalPath},
			}, nil
		}
		return AcquireChildResult{}, fmt.Errorf("videocreate: youtube_clip.extract produced no committed clip (stats=%+v, error=%q)", wire.Stats, wire.Error)
	default:
		var wire stockpipeline.StockJobResult
		if err := json.Unmarshal(raw, &wire); err != nil {
			return AcquireChildResult{}, fmt.Errorf("videocreate: media.stock result: %w", err)
		}
		for _, chunk := range wire.Chunks {
			if !chunk.Rendered || chunk.LocalPath == "" {
				continue
			}
			return AcquireChildResult{
				AssetID:    stockAssetIDFromManifest(wire.Manifest, chunk.SHA256),
				ContentSHA: chunk.SHA256,
				DurationMS: int64((chunk.TimelineEnd - chunk.TimelineStart) * 1000),
				MediaType:  "video",
				Source:     "stock",
				DriveRef:   DriveRef{DriveFileID: chunk.DriveFileID},
				LocalRef:   LocalRef{LocalPath: chunk.LocalPath},
			}, nil
		}
		return AcquireChildResult{}, fmt.Errorf("videocreate: media.stock produced no rendered chunk (final_status=%q)", wire.FinalStatus)
	}
}

// stockAssetIDFromManifest resolves the registered media identity of a
// stock chunk from the canonical artifact manifest (the chunk's
// artifact carries the asset id, keyed by its content SHA-256). It
// returns empty rather than inventing an identity when the manifest has
// no matching entry (godlike/07 no-fake-availability).
func stockAssetIDFromManifest(manifest *job.ArtifactManifest, sha256 string) string {
	if manifest == nil || sha256 == "" {
		return ""
	}
	for _, artifact := range manifest.Artifacts {
		if !strings.EqualFold(artifact.SHA256, sha256) {
			continue
		}
		if id, ok := artifact.ArtifactMetadata["asset_id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
		return artifact.ID
	}
	return ""
}

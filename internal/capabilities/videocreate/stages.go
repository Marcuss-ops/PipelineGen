package videocreate

import (
	"context"
	"encoding/json"
	"fmt"

	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Child result projections (the stage-side wire contract) ───────────
//
// Each is the typed fact set the workflow keeps from a child family's
// REAL result wire contract (decoded by child_results.go). They carry
// DURABLE identities first (asset id, content SHA-256, Drive file id,
// duration, media type); the local path is an execution detail the
// media plane needs and is rehydrated after a restart by re-reading the
// child's result (recovery.go).

// VoiceoverAssetRef is one voiceover materialization: per scene from
// the script stage (§9), or per item from voiceover.generate (§12).
type VoiceoverAssetRef struct {
	SceneID  string `json:"scene_id,omitempty"`
	Language string `json:"language,omitempty"`
	AssetID  string `json:"asset_id,omitempty"`
	DriveRef
	LocalRef
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// ScriptChildResult is the script.generate child result (§9: script +
// scene plan + canonical timeline facts), projected from the real
// durable/envelope result shapes.
type ScriptChildResult struct {
	ScriptAssetID string   `json:"script_asset_id"`
	Scenes        []string `json:"scenes"`
	// Voiceovers are the per-scene voiceover materializations the script
	// stage produced (the §13 audio-master inputs when no canonical
	// master was reused).
	Voiceovers []VoiceoverAssetRef `json:"voiceovers,omitempty"`
	// AudioPlan is the audio pipeline's COMPILED plan (JSON) when the
	// script stage produced one. The workflow reuses it verbatim — it
	// never re-derives mix decisions (§13).
	AudioPlan json.RawMessage `json:"audio_plan,omitempty"`
	// FinalAudio is the §9 reuse rule: when the script stage already
	// produced the canonical voiceover master, video.create reuses it
	// instead of fanning out a second voiceover.generate.
	FinalAudio   *FinalAudioChildResult `json:"final_audio,omitempty"`
	TextSegments []string               `json:"text_segments,omitempty"`
}

// FinalAudioChildResult is the durable + materialized identity of an
// already-produced canonical audio master (the §9 reuse input) — the
// complete copy-eligibility fact set mux_audio_copy validates.
type FinalAudioChildResult struct {
	AssetID              string `json:"asset_id"`
	ContentSHA           string `json:"content_sha256"`
	DurationMS           int64  `json:"duration_ms"`
	SampleRate           int    `json:"sample_rate"`
	Channels             int    `json:"channels"`
	ChannelLayout        string `json:"channel_layout"`
	Codec                string `json:"codec"`
	Profile              string `json:"profile"`
	Bitrate              int64  `json:"bitrate"`
	SizeBytes            int64  `json:"size_bytes"`
	StartPTS             int64  `json:"start_pts"`
	FinalMix             bool   `json:"final_mix"`
	CopyEligible         bool   `json:"copy_eligible"`
	AudioContractVersion string `json:"audio_contract_version"`
	AudioPlanVersion     string `json:"audio_plan_version"`
	AudioPlanSHA256      string `json:"audio_plan_sha256"`
	// LocalRef carries the worker-local materialization (wire key
	// local_path; execution detail).
	LocalRef
}

// masterAudio projects the child's final-audio facts onto the canonical
// media-plane master type (§9 reuse: mux this master verbatim).
func (f *FinalAudioChildResult) masterAudio() *MasteredAudio {
	if f == nil {
		return nil
	}
	return &MasteredAudio{
		Asset: audio.FinalAudioAsset{
			AssetID:              f.AssetID,
			AudioContractVersion: f.AudioContractVersion,
			AudioPlanVersion:     f.AudioPlanVersion,
			AudioPlanSHA256:      f.AudioPlanSHA256,
			FinalAudioSHA256:     f.ContentSHA,
			Codec:                f.Codec,
			Profile:              f.Profile,
			SampleRate:           f.SampleRate,
			Channels:             f.Channels,
			ChannelLayout:        f.ChannelLayout,
			DurationMS:           f.DurationMS,
			StartPTS:             f.StartPTS,
			Bitrate:              f.Bitrate,
			SizeBytes:            f.SizeBytes,
			FinalMix:             f.FinalMix,
			CopyEligible:         f.CopyEligible,
		},
		Path: f.LocalPath,
	}
}

// AcquireChildResult is one acquisition child result
// (youtube_clip.extract / media.stock), projected from the real result
// maps.
type AcquireChildResult struct {
	AssetID    string `json:"asset_id"`
	ContentSHA string `json:"content_sha256"`
	DurationMS int64  `json:"duration_ms"`
	MediaType  string `json:"media_type"`
	Source     string `json:"source"`
	SourceURL  string `json:"source_url,omitempty"`
	// The location facts live in the value objects (media-identity
	// gate): DriveRef flattens to drive_file_id and LocalRef (the
	// worker-local materialization, an execution detail) to
	// local_path.
	DriveRef
	LocalRef
}

// RenderContractFacts is the clip.render output-contract block the
// child reports (the §14/§15 evidence the assembler gate admits).
type RenderContractFacts struct {
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	FPSNum      int    `json:"fps_num,omitempty"`
	FPSDen      int    `json:"fps_den,omitempty"`
	VideoCodec  string `json:"video_codec,omitempty"`
	AudioCodec  string `json:"audio_codec,omitempty"`
	PixelFormat string `json:"pixel_format,omitempty"`
}

// RenderChildResult is one clip.render child result (§14: the render
// boundary is RenderingGen → Chronon and stays behind clip.render),
// projected from the real renderedResult map.
type RenderChildResult struct {
	AssetID       string              `json:"asset_id"`
	SourceAssetID string              `json:"source_asset_id"`
	DurationMS    int64               `json:"duration_ms"`
	ContractID    string              `json:"contract_id,omitempty"`
	Contract      RenderContractFacts `json:"contract,omitempty"`
	// LocalRef carries the worker-local materialization (wire key
	// local_path; execution detail).
	LocalRef
}

// ── stageFunc: one durable workflow step ──────────────────────────────

type stageFunc func(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error)

// stageFuncs binds each durable step key to its implementation. The
// coordinator drives ordering/durability; a stage only computes and
// returns its durable output.
var stageFuncs = map[string]stageFunc{
	"01_script":        runScriptStage,
	"02_media_search":  runMediaSearchStage,
	"03_media_acquire": runMediaAcquireStage,
	"04_voiceover":     runVoiceoverStage,
	"05_audio_master":  runAudioMasterStage,
	"06_overlay_plan":  runOverlayPlanStage,
	"07_render":        runRenderStage,
	"08_assemble":      runAssembleStage,
	"09_audio_mux":     runAudioMuxStage,
	"10_verify":        runVerifyStage,
	"11_publish":       runPublishStage,
}

// ── 01_script ────────────────────────────────────────────────────────
//
// The §9 first stage. ONE script.generate child (key "<root>:script")
// produces the script, the scene plan and the canonical timeline facts
// through the child's REAL wire contract (GenerationEnvelopeV2 in,
// the durable/envelope result shapes out). The §9 reuse rule lands
// here too: when the child's result already carries the canonical
// final audio, the voiceover and audio-master steps will be SKIPPED
// and that master is reused — video.create never fans out work the
// children already did.

func runScriptStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	childKey := ChildKey(run.RootKey, "script")
	payload, err := ScriptChildPayload(run.Request, run.Job.Project, run.Job.VideoName, childKey)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	childID, err := run.enqueue(ctx, spec, childKey, appjobs.TypeScriptGenerate, payload)
	if err != nil {
		return out, fmt.Errorf("%w: script stage enqueue: %v", ErrWorkflowFailed, err)
	}
	out.ChildJobs = []string{childID}
	child, err := run.await(ctx, spec, childID)
	if err != nil {
		return out, fmt.Errorf("%w: script stage wait: %v", ErrWorkflowFailed, err)
	}
	if child.Status != job.StatusSucceeded {
		return out, fmt.Errorf("%w: script.generate child %s failed: %s", ErrWorkflowFailed, childID, child.Error)
	}
	res, err := decodeScriptChildResult(child.Result)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrWorkflowFailed, err)
	}
	if res.ScriptAssetID == "" || len(res.Scenes) == 0 {
		return out, fmt.Errorf("%w: script child produced no script asset/scenes", ErrWorkflowFailed)
	}
	out.ScriptAssetID = res.ScriptAssetID
	out.Scenes = res.Scenes
	out.TextSegments = res.TextSegments
	out.Voiceovers = res.Voiceovers
	out.AudioPlan = res.AudioPlan
	out.Artifacts = []StageArtifactRef{{AssetID: res.ScriptAssetID, Kind: "script", MediaType: "script"}}
	if res.FinalAudio != nil {
		// §9 reuse rule: the canonical master already exists — carry
		// BOTH its durable identity and its certified master facts so
		// 04/05 are SKIPPED and 09 muxes this master verbatim.
		ref := StageArtifactRef{
			AssetID:    res.FinalAudio.AssetID,
			Kind:       "final_audio",
			ContentSHA: res.FinalAudio.ContentSHA,
			DurationMS: res.FinalAudio.DurationMS,
			MediaType:  "audio",
		}
		out.FinalAudio = &ref
		out.AudioMaster = res.FinalAudio.masterAudio()
		run.Facts.FinalAudio = out.AudioMaster
	}
	run.Facts.ScriptAssetID = res.ScriptAssetID
	run.Facts.Scenes = res.Scenes
	run.Facts.TextSegments = res.TextSegments
	run.Facts.AudioPlanJSON = res.AudioPlan
	run.Facts.VoiceoverItems = res.Voiceovers
	return out, nil
}

// ── 02_media_search ──────────────────────────────────────────────────
//
// The §10 discovery stage. The workflow calls the canonical aggregator
// port (the backend behind /api/media/search) EXACTLY ONCE with the
// requested families and applies deterministic selection: candidates
// rank by score (ties broken by source url) and are assigned to scenes
// in timeline order. It consumes the existing ranking; it never
// re-implements it and never calls itself over HTTP.

func runMediaSearchStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	limit := 2 * len(run.Facts.Scenes)
	if limit < 8 {
		limit = 8
	}
	candidates, err := run.Deps.Search.Search(ctx, MediaSearchRequest{
		Topic:    run.Request.Topic,
		Language: run.Request.Language,
		Sources:  run.Request.MediaSources,
		Limit:    limit,
	})
	if err != nil {
		return out, fmt.Errorf("%w: media search: %v", ErrWorkflowFailed, err)
	}
	selection := selectCandidates(candidates, run.Facts.Scenes)
	if len(selection) == 0 {
		return out, fmt.Errorf("%w: media search returned no usable candidates", ErrWorkflowFailed)
	}
	out.Selection = selection
	run.Facts.Candidates = selection
	return out, nil
}

// selectCandidates is the deterministic selection: stable order by
// score desc then source url asc, then one candidate per scene in
// timeline order (cycling when there are fewer candidates than scenes).
func selectCandidates(candidates []MediaCandidate, scenes []string) []MediaCandidate {
	sorted := append([]MediaCandidate(nil), candidates...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && betterCandidate(sorted[j], sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	selection := make([]MediaCandidate, 0, len(scenes))
	for i := range scenes {
		if len(sorted) == 0 {
			break
		}
		selection = append(selection, sorted[i%len(sorted)])
	}
	return selection
}

func betterCandidate(a, b MediaCandidate) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.SourceURL < b.SourceURL
}

// ── 03_media_acquire ─────────────────────────────────────────────────
//
// The §11 acquisition stage. Each selected candidate becomes ONE child
// of the family that owns it (youtube_clip.extract for YouTube,
// media.stock for stock/Artlist) with its derived per-scene key and the
// family's REAL wire payload. The children own download / cut /
// normalize / register; the workflow records only their durable
// outputs (asset id, content hash, Drive identity, duration, media
// type).

func runMediaAcquireStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	selection := run.Facts.Candidates
	if len(selection) == 0 {
		if rec := run.State.StageRecordFor("02_media_search"); rec != nil {
			selection = rec.Output.Selection
		}
	}
	if len(selection) == 0 {
		return out, fmt.Errorf("%w: media acquire has no selection to materialize", ErrWorkflowFailed)
	}
	sceneSeconds := run.sceneBudgetSeconds()
	type pending struct {
		id     string
		src    string
		jobTyp string
	}
	var issued []pending
	for i, cand := range selection {
		sceneIndex := i + 1
		family, jobType := acquireFamily(cand.Source)
		payload, _, err := AcquireChildPayload(cand, run.Request, run.Job.Project, run.Job.VideoName, sceneSeconds)
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
		}
		childID, err := run.enqueue(ctx, spec, SceneChildKey(run.RootKey, family, sceneIndex), jobType, payload)
		if err != nil {
			return out, fmt.Errorf("%w: media acquire enqueue: %v", ErrWorkflowFailed, err)
		}
		issued = append(issued, pending{id: childID, src: cand.Source, jobTyp: jobType})
	}
	for _, p := range issued {
		child, err := run.await(ctx, spec, p.id)
		if err != nil {
			return out, fmt.Errorf("%w: media acquire wait: %v", ErrWorkflowFailed, err)
		}
		if child.Status != job.StatusSucceeded {
			return out, fmt.Errorf("%w: acquire child %s failed: %s", ErrWorkflowFailed, p.id, child.Error)
		}
		res, err := decodeAcquireChildResult(p.jobTyp, child.Result)
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrWorkflowFailed, err)
		}
		if res.AssetID == "" {
			return out, fmt.Errorf("%w: acquire child %s produced no registered asset id", ErrWorkflowFailed, p.id)
		}
		out.ChildJobs = append(out.ChildJobs, p.id)
		out.Artifacts = append(out.Artifacts, StageArtifactRef{
			AssetID:    res.AssetID,
			Kind:       "media",
			ContentSHA: res.ContentSHA,
			DriveRef:   DriveRef{DriveFileID: res.DriveFileID},
			DurationMS: res.DurationMS,
			MediaType:  res.MediaType,
			Source:     res.Source,
			SourceURL:  res.SourceURL,
		})
		run.Facts.Acquired = append(run.Facts.Acquired, AcquiredClip{
			Ref:        out.Artifacts[len(out.Artifacts)-1],
			LocalPath:  res.LocalPath,
			Source:     res.Source,
			SceneIndex: len(out.ChildJobs),
		})
	}
	return out, nil
}

// acquireFamily maps a media source to its acquisition family and the
// canonical child job type that owns it.
func acquireFamily(source string) (family, jobType string) {
	if source == "youtube" {
		return "youtube", appjobs.TypeYouTubeClipExtract
	}
	return "stock", appjobs.TypeMediaStock
}

// sceneBudgetSeconds is the per-scene duration budget derived from the
// requested duration and the scene count (the acquisition window and
// stock clip-length input).
func (r *Run) sceneBudgetSeconds() int {
	scenes := len(r.Facts.Scenes)
	if scenes <= 0 {
		scenes = 1
	}
	per := r.Request.DurationSeconds / scenes
	if per < 5 {
		per = 5
	}
	return per
}

// ── 04_voiceover ─────────────────────────────────────────────────────
//
// The §12 stage. voiceover.generate (the EXTERNAL-SAFE parent — never
// the internal voiceover.generate_item child) is fanned out ONLY when
// the request asked for it and the §9 reuse rule did not already
// supply a canonical master. The parent fans out one generate_item
// child per scene text; the workflow collects their REAL per-item
// results (the voiceover materializations §13 mixes from).

func runVoiceoverStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	if run.voiceoverNeeded() != needVoiceover {
		out.Skipped = true
		return out, nil
	}
	cmd, err := VoiceoverChildPayload(run.Facts.TextSegments, run.Request.Language, run.Job.Project)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrWorkflowFailed, err)
	}
	childID, err := run.enqueue(ctx, spec, ChildKey(run.RootKey, "voiceover"), appjobs.TypeVoiceoverGenerate, cmd)
	if err != nil {
		return out, fmt.Errorf("%w: voiceover stage enqueue: %v", ErrWorkflowFailed, err)
	}
	out.ChildJobs = []string{childID}
	child, err := run.await(ctx, spec, childID)
	if err != nil {
		return out, fmt.Errorf("%w: voiceover stage wait: %v", ErrWorkflowFailed, err)
	}
	if child.Status != job.StatusSucceeded {
		return out, fmt.Errorf("%w: voiceover.generate child %s failed: %s", ErrWorkflowFailed, childID, child.Error)
	}
	var fanout voiceoverFanoutWire
	if err := childResult(child, &fanout); err != nil {
		return out, fmt.Errorf("%w: voiceover.generate result: %v", ErrWorkflowFailed, err)
	}
	if len(fanout.ChildJobIDs) == 0 {
		return out, fmt.Errorf("%w: voiceover.generate fanned out no item children", ErrWorkflowFailed)
	}
	items := make([]VoiceoverAssetRef, 0, len(fanout.ChildJobIDs))
	for _, itemID := range fanout.ChildJobIDs {
		itemJob, err := run.await(ctx, spec, itemID)
		if err != nil {
			return out, fmt.Errorf("%w: voiceover item wait: %v", ErrWorkflowFailed, err)
		}
		if itemJob.Status != job.StatusSucceeded {
			return out, fmt.Errorf("%w: voiceover.generate_item child %s failed: %s", ErrWorkflowFailed, itemID, itemJob.Error)
		}
		ref, err := decodeVoiceoverItemResult(itemJob.Result)
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrWorkflowFailed, err)
		}
		items = append(items, ref)
	}
	out.Voiceovers = items
	out.Artifacts = make([]StageArtifactRef, 0, len(items))
	for _, ref := range items {
		out.Artifacts = append(out.Artifacts, StageArtifactRef{
			AssetID:   ref.AssetID,
			Kind:      "voiceover",
			DriveRef:  DriveRef{DriveFileID: ref.DriveFileID},
			MediaType: "audio",
			Source:    ref.Language,
		})
	}
	run.Facts.VoiceoverItems = items
	return out, nil
}

// voiceoverDecision is the §9 reuse rule's typed outcome.
type voiceoverDecision int

const (
	needVoiceover voiceoverDecision = iota
	reuseFinalAudio
	noVoiceoverRequested
)

// voiceoverNeeded implements the §9 rule exactly:
//
//	final audio already exists → reuse it (skip voiceover + audio master)
//	voiceover=false             → skip (the final video keeps its own audio)
//	else                        → fan out voiceover.generate
func (r *Run) voiceoverNeeded() voiceoverDecision {
	if r.Facts.FinalAudio != nil {
		return reuseFinalAudio
	}
	if !r.Request.Voiceover {
		return noVoiceoverRequested
	}
	return needVoiceover
}

// ── 05_audio_master ──────────────────────────────────────────────────
//
// The §13 stage: voiceover + BGM + SFX → render_audio_plan →
// canonical_final_audio, through the canonical media plane (the
// AudioMaster port). The COMPILED plan comes from the script/voiceover
// children's canonical timeline output — re-deriving mix decisions here
// would be a second audio authority. A missing compiled plan fails
// closed (godlike/07: never invent a mix).

func runAudioMasterStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	if run.voiceoverNeeded() != needVoiceover {
		// Reused master (or no voiceover at all): nothing to compile.
		out.Skipped = true
		return out, nil
	}
	if len(run.Facts.AudioPlanJSON) == 0 {
		return out, fmt.Errorf("%w: audio master: no compiled audio plan in the script/voiceover outputs", ErrWorkflowFailed)
	}
	var plan audio.CompiledAudioPlan
	if err := json.Unmarshal(run.Facts.AudioPlanJSON, &plan); err != nil {
		return out, fmt.Errorf("%w: audio master: decode compiled plan: %v", ErrWorkflowFailed, err)
	}
	assets := run.resolvedAudioAssets()
	if len(assets) == 0 {
		return out, fmt.Errorf("%w: audio master: no voiceover materialization to mix", ErrWorkflowFailed)
	}
	mastered, err := run.Deps.Audio.Master(ctx, MasterRequest{
		Plan:       plan,
		Assets:     assets,
		OutputPath: run.workPath("canonical_final_audio.aac"),
	})
	if err != nil {
		return out, fmt.Errorf("%w: audio master: %v", ErrWorkflowFailed, err)
	}
	ref := StageArtifactRef{
		AssetID:    mastered.Asset.AssetID,
		Kind:       "final_audio",
		ContentSHA: mastered.Asset.FinalAudioSHA256,
		DurationMS: mastered.Asset.DurationMS,
		MediaType:  "audio",
	}
	out.Artifacts = []StageArtifactRef{ref}
	out.FinalAudio = &ref
	out.AudioMaster = &mastered
	run.Facts.FinalAudio = &mastered
	return out, nil
}

// resolvedAudioAssets resolves the durable audio identities to the
// media plane's resolved inputs (asset id + local materialization): the
// voiceover materializations of 04 (or the §9 per-scene voiceovers when
// 04 was skipped).
func (r *Run) resolvedAudioAssets() audio.ResolvedAudioAssets {
	var out audio.ResolvedAudioAssets
	for _, ref := range r.voiceoverAssets() {
		if ref.LocalPath == "" {
			continue
		}
		assetID := ref.AssetID
		if assetID == "" {
			assetID = ref.SceneID
		}
		out = append(out, audio.ResolvedAudioAsset{
			AssetID:    assetID,
			Path:       ref.LocalPath,
			DurationUS: ref.DurationMS * 1000,
		})
	}
	return out
}

// voiceoverAssets is the durable voiceover input set: 04's item
// materializations when the stage ran, else the §9 per-scene
// voiceovers of 01. State-driven so recovery sees the same set.
func (r *Run) voiceoverAssets() []VoiceoverAssetRef {
	if rec := r.State.StageRecordFor("04_voiceover"); rec != nil && !rec.Output.Skipped && len(rec.Output.Voiceovers) > 0 {
		return rec.Output.Voiceovers
	}
	if rec := r.State.StageRecordFor("01_script"); rec != nil {
		return rec.Output.Voiceovers
	}
	return nil
}

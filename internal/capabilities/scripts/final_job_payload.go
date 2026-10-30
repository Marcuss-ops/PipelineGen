package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type FinalJobStockFile struct{ ID, Name string }

// finalJobRejectedStockDriveIDs contains stock files proven incompatible with
// the remote copy-only packet mux. Keep these exclusions on the 77 side: the
// 51 worker must reject an unsafe source window instead of snapping its cut.
var finalJobRejectedStockDriveIDs = map[string]struct{}{
	"1xvvjxin09xbwy7qqignzlmpjftckql_o": {},
}

func finalJobStockFileAllowed(file FinalJobStockFile) bool {
	_, rejected := finalJobRejectedStockDriveIDs[strings.ToLower(strings.TrimSpace(file.ID))]
	return !rejected
}

type FinalJobAssetResolver interface {
	ResolveFinalJobAsset(context.Context, string) (map[string]any, error)
	ListFinalJobStockFolder(context.Context, string) ([]FinalJobStockFile, error)
	// FinalJobPublishedFileSize reports the byte size of a published Drive
	// file. A clip-only scene is sent as the CERTIFIED localized render this
	// pipeline produced (background, watermark and burnt subtitles included),
	// whose bytes already live in Drive: the run result carries its identity
	// and duration, but not its size, so the reference is completed here from
	// the same Drive metadata every other runtime asset uses.
	FinalJobPublishedFileSize(context.Context, string) (int64, error)
}

// finalJobStockAssetResolver is the sole Drive-backed source path in the final
// job. Other scene and generated assets use ResolveFinalJobAsset's HTTPS flow.
type finalJobStockAssetResolver interface {
	ResolveFinalJobStockAsset(context.Context, string) (map[string]any, error)
}

// FinalJobVideoSegmentRequest is one exact, frame-aligned fragment that the
// remote copy-only assembler will place on the final timeline. Preparation
// happens before PREPARE/FINALIZE; the remote worker receives only the
// prepared HTTPS asset and never transcodes it.
type FinalJobVideoSegmentRequest struct {
	SegmentID          string
	SourceAssetID      string
	SourceSHA256       string
	SourceInUS         int64
	SourceDurationUS   int64
	TimelineStartFrame int64
	FrameCount         int64
	TimelineRevision   int64
	TimelineSHA256     string
}

// FinalJobVideoSegmentPreparer materializes each selected stock/clip/overlay
// window as one assembly-ready fragment. Implementations may reuse an
// existing certified artifact when its bytes and exact window already match.
type FinalJobVideoSegmentPreparer interface {
	PrepareFinalJobVideoSegment(context.Context, map[string]any, FinalJobVideoSegmentRequest) (map[string]any, error)
}

func resolveFinalJobStockAsset(ctx context.Context, resolver FinalJobAssetResolver, id string) (map[string]any, error) {
	if stockResolver, ok := resolver.(finalJobStockAssetResolver); ok {
		return stockResolver.ResolveFinalJobStockAsset(ctx, id)
	}
	return resolveFinalJobAsset(ctx, resolver, id)
}

func BuildFinalJobPayloads(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult, resolver FinalJobAssetResolver, preparers ...FinalJobVideoSegmentPreparer) (map[string]any, map[string]any, error) {
	if result == nil || result.CanonicalTimeline == nil || result.CanonicalTimeline.DurationUS <= 0 {
		return nil, nil, fmt.Errorf("final_job requires a canonical timeline with positive duration")
	}
	// Audio is optional for render-only runs (for example phrase animation
	// previews). When present, retain the certified mux checks below. The
	// canonical video timeline remains the duration authority when audio is off.
	hasFinalAudio := result.FinalAudio != nil && result.FinalAudio.DurationMS > 0
	// The finalizer muxes this single canonical mix at video time zero. A
	// non-zero encoded PTS would shift the voiceover against every scene even
	// when the reported duration matches, so reject it before handing off to 51.
	if hasFinalAudio && result.FinalAudio.StartPTS != 0 {
		return nil, nil, fmt.Errorf("final_job final audio start_pts is %d, want 0 for voiceover alignment", result.FinalAudio.StartPTS)
	}
	if len(result.Scenes) == 0 {
		return nil, nil, fmt.Errorf("final_job has no generated scenes")
	}
	scenesByID := make(map[string]Scene, len(result.Scenes))
	for _, scene := range result.Scenes {
		scenesByID[scene.ID] = scene
	}
	stockFiles := make(map[string][]FinalJobStockFile)
	stockAssetCache := make(map[string]map[string]any)
	stockFolderCursors := make(map[string]int)
	remoteScenes := make([]map[string]any, 0, len(result.CanonicalTimeline.Segments))
	clipOrdinal := 0
	var plannedDurationMS int64
	var scriptText strings.Builder
	runtimeAssets := make([]any, 0)
	seenRuntimeAssets := make(map[string]bool)
	var audioRef map[string]any
	// The remote video is built from ONE language: the certified renders of the
	// source language, or the single render language the run produced. The
	// decision is made once here instead of per scene, and an ambiguous run is
	// reported by the clip-only path rather than silently resolved.
	renderLanguage, renderLanguageErr := finalJobRenderLanguage(req, result)
	consumedPhraseOverlays := make(map[string]struct{})

	for _, segment := range result.CanonicalTimeline.Segments {
		localScene, sceneExists := scenesByID[segment.ID]
		fixedMedia := segment.FixedMedia || (sceneExists && localScene.ExecutionMode.IsFixedMedia())
		if !fixedMedia && !sceneExists {
			return nil, nil, fmt.Errorf("final_job scene %q has no generated scene", segment.ID)
		}
		if fixedMedia {
			type fixedClip struct {
				assetID    string
				durationUS int64
				offsetUS   int64
			}
			fixedClips := make([]fixedClip, 0)
			// The protected timeline intents define the exact cut duration and
			// order. The Drive asset's full duration may be longer (for example,
			// 15/19/13s source files used for three 5s intro cuts).
			for _, intent := range segment.EffectiveAudioIntents() {
				assetID := strings.TrimSpace(intent.ClipAssetID)
				if assetID == "" {
					continue
				}
				durationUS := intent.TimelineDurationUS
				fixedClips = append(fixedClips, fixedClip{assetID: assetID, durationUS: durationUS, offsetUS: intent.TimelineOffsetUS})
			}
			// SourceDurationUS describes the complete source file, not the cut
			// placed on the timeline. When a cut has no explicit timeline duration,
			// derive it from the next cut's offset or the fixed segment boundary.
			if len(fixedClips) > 1 {
				allAtSameOffset := true
				for _, clip := range fixedClips {
					if clip.offsetUS != 0 {
						allAtSameOffset = false
						break
					}
				}
				if allAtSameOffset {
					for i := range fixedClips {
						if fixedClips[i].durationUS <= 0 {
							fixedClips[i].durationUS = segment.DurationUS / int64(len(fixedClips))
						}
					}
				} else {
					for i := range fixedClips {
						if fixedClips[i].durationUS > 0 {
							continue
						}
						startUS := fixedClips[i].offsetUS
						endUS := segment.DurationUS
						if i+1 < len(fixedClips) && fixedClips[i+1].offsetUS > startUS {
							endUS = fixedClips[i+1].offsetUS
						}
						if endUS > startUS {
							fixedClips[i].durationUS = endUS - startUS
						}
					}
				}
			}
			if len(fixedClips) == 0 {
				for _, clip := range localScene.Clips {
					if clip != nil && strings.TrimSpace(clip.ID) != "" {
						var durationUS int64
						if clip.SourceOutMS > clip.SourceInMS {
							durationUS = (clip.SourceOutMS - clip.SourceInMS) * 1000
						}
						fixedClips = append(fixedClips, fixedClip{assetID: strings.TrimSpace(clip.ID), durationUS: durationUS})
					}
				}
			}
			if len(fixedClips) == 0 && localScene.Clip != nil && strings.TrimSpace(localScene.Clip.ID) != "" {
				var durationUS int64
				if localScene.Clip.SourceOutMS > localScene.Clip.SourceInMS {
					durationUS = (localScene.Clip.SourceOutMS - localScene.Clip.SourceInMS) * 1000
				}
				fixedClips = append(fixedClips, fixedClip{assetID: strings.TrimSpace(localScene.Clip.ID), durationUS: durationUS})
			}
			if len(fixedClips) == 0 {
				return nil, nil, fmt.Errorf("fixed scene %q has no clip assets", segment.ID)
			}
			for _, fixed := range fixedClips {
				asset, err := resolveFinalJobFixedMediaAsset(ctx, resolver, result, segment.ID, fixed.assetID, renderLanguage)
				if err != nil {
					return nil, nil, fmt.Errorf("resolve intro clip %s: %w", fixed.assetID, err)
				}
				durationMS, _ := asset["duration_ms"].(int64)
				if fixed.durationUS > 0 {
					durationMS = (fixed.durationUS + 999) / 1000
				}
				if durationMS <= 0 {
					remainingMS := (segment.DurationUS + 999) / 1000
					if remainingMS > 0 {
						durationMS = remainingMS / int64(len(fixedClips))
					}
				}
				if durationMS <= 0 {
					durationMS = 5000
				}
				remoteScenes = append(remoteScenes, compositeVideoScene(fmt.Sprintf("scene-%04d", clipOrdinal), clipOrdinal, "clip", asset, durationMS, "Protected intro clip"))
				appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "clip", asset)
				plannedDurationMS += durationMS
				clipOrdinal++
			}
			continue
		}
		if !sceneExists {
			return nil, nil, fmt.Errorf("final_job scene %q has no generated scene", segment.ID)
		}
		if req.MediaMode == scriptpkg.MediaModeStockOnly && (localScene.Stock == nil || strings.TrimSpace(localScene.Stock.FolderID) == "") {
			return nil, nil, fmt.Errorf("final_job stock_only scene %q has no stock folder binding; refusing to route it through the clip path", segment.ID)
		}
		// A scene whose clips are ALL stock-marked is audio-only by contract:
		// clip render was deliberately skipped for it, so no certified clip
		// render exists. Its video MUST come from its stock folder binding;
		// without one there is no visual source and the remote worker would
		// receive a scene it cannot composite.
		if sceneVisualClipsAreStockOnly(localScene) && (localScene.Stock == nil || strings.TrimSpace(localScene.Stock.FolderID) == "") {
			return nil, nil, fmt.Errorf("final_job scene %q has only stock-marked clips and no stock folder binding; its video must come from a stock folder (segments[].stock_folder_id)", segment.ID)
		}
		if localScene.Stock == nil || strings.TrimSpace(localScene.Stock.FolderID) == "" {
			// Clip-only scene: the runtime receives the clip THIS pipeline
			// produced, never the unmodified source clip from the library.
			var asset map[string]any
			var durationMS int64
			asset, durationMS, err := clipSceneRuntimeAsset(ctx, resolver, result, renderLanguage, renderLanguageErr, segment.ID)
			if err != nil {
				if len(result.LocalizedRenders) != 0 {
					return nil, nil, err
				}
				// A phrase-only scene has no source video to localize. Its certified
				// full-frame phrase render is already the visual for that scene, so
				// hand that video to the compositor as the clip and don't overlay the
				// same animation a second time.
				var overlayID string
				var ok bool
				var overlayErr error
				asset, durationMS, overlayID, ok, overlayErr = finalJobPhraseOverlayClip(ctx, result, segment.ID)
				if overlayErr != nil {
					return nil, nil, overlayErr
				}
				if !ok {
					// Keep the original clip error but say why the phrase-only
					// fallback did not apply: it needs exactly one text_phrase
					// item for this scene with a certified copy-eligible video
					// artifact. Returning the bare clip error hid that reason.
					return nil, nil, fmt.Errorf("%w; phrase-only fallback did not apply for scene %q (need exactly one text_phrase item with a certified copy-eligible video artifact)", err, segment.ID)
				}
				consumedPhraseOverlays[overlayID] = struct{}{}
			}
			// A certified scene render often contains only the selected source
			// clip's original duration, while its narration can be much longer.
			// Repeat that same pipeline-produced visual in bounded chunks to fill
			// the canonical scene duration; otherwise the final video ends before
			// the certified audio and later overlays are lost.
			remainingMS := (segment.DurationUS + 999) / 1000
			if remainingMS <= 0 {
				return nil, nil, fmt.Errorf("final_job clip-only scene %q has no positive canonical duration", segment.ID)
			}
			for remainingMS > 0 {
				chunkMS := min(durationMS, remainingMS)
				remoteScenes = append(remoteScenes, compositeVideoScene(fmt.Sprintf("scene-%04d", clipOrdinal), clipOrdinal, "clip", asset, chunkMS, firstFinalJobValue(localScene.Text[req.SourceLanguage], req.Title, localScene.ID)))
				plannedDurationMS += chunkMS
				clipOrdinal++
				remainingMS -= chunkMS
			}
			appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "clip", asset)
			if text := localScene.Text[req.SourceLanguage]; text != "" {
				scriptText.WriteString(text)
				scriptText.WriteByte('\n')
			}
			continue
		}
		folderID := strings.TrimSpace(localScene.Stock.FolderID)
		files, ok := stockFiles[folderID]
		if !ok {
			listed, err := resolver.ListFinalJobStockFolder(ctx, folderID)
			if err != nil {
				return nil, nil, fmt.Errorf("list selected stock folder %s: %w", folderID, err)
			}
			files = listed
			// Do not put a source whose required copy-only cut is known to fall
			// between keyframes into the remote timeline. The next eligible file
			// in this folder is selected in its place; if none remain, fail here
			// on 77 instead of spending a remote worker attempt on a guaranteed
			// mux rejection.
			eligible := make([]FinalJobStockFile, 0, len(files))
			for _, file := range files {
				if finalJobStockFileAllowed(file) {
					eligible = append(eligible, file)
				}
			}
			files = eligible
			if len(files) == 0 {
				return nil, nil, fmt.Errorf("selected stock folder %s contains no copy-compatible video files after excluding known keyframe-incompatible sources", folderID)
			}
			stockFiles[folderID] = files
		}
		remainingMS := (segment.DurationUS + 999) / 1000
		folderIndex := stockFolderCursors[folderID]
		sceneText := firstFinalJobValue(localScene.Text[req.SourceLanguage], req.Title, localScene.ID)
		for remainingMS > 0 {
			file := files[folderIndex%len(files)]
			folderIndex++
			stockFolderCursors[folderID] = folderIndex
			asset, ok := stockAssetCache[file.ID]
			if !ok {
				var err error
				asset, err = resolveFinalJobStockAsset(ctx, resolver, file.ID)
				if err != nil {
					return nil, nil, fmt.Errorf("resolve stock file %s (%s): %w", file.Name, file.ID, err)
				}
				stockAssetCache[file.ID] = asset
			}
			durationMS, _ := asset["duration_ms"].(int64)
			if durationMS <= 0 {
				return nil, nil, fmt.Errorf("stock file %s has no canonical duration", file.Name)
			}
			chunkMS := durationMS
			if chunkMS > remainingMS {
				chunkMS = remainingMS
			}
			// Stock may be sourced from Drive, but the worker only receives its
			// immutable HTTPS Objectstore reference.
			appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "stock", asset)
			remoteScenes = append(remoteScenes, compositeVideoScene(fmt.Sprintf("scene-%04d", clipOrdinal), clipOrdinal, "stock", asset, chunkMS, sceneText))
			plannedDurationMS += chunkMS
			clipOrdinal++
			remainingMS -= chunkMS
			folderIndex++
		}
		if text := localScene.Text[req.SourceLanguage]; text != "" {
			scriptText.WriteString(text)
			scriptText.WriteByte('\n')
		}
	}
	if clipOrdinal == 0 {
		return nil, nil, fmt.Errorf("final_job produced no remote scenes")
	}
	if err := enforceFinalJobMinimumSceneDuration(remoteScenes, 100); err != nil {
		return nil, nil, err
	}
	plannedDurationMS = finalJobSceneDurationMS(remoteScenes)
	if hasFinalAudio {
		if delta := plannedDurationMS - result.FinalAudio.DurationMS; delta < -40 || delta > 40 {
			return nil, nil, fmt.Errorf("final_job scene duration %dms does not match certified final audio %dms (tolerance 40ms)", plannedDurationMS, result.FinalAudio.DurationMS)
		}
		if err := enforceFinalJobPreflight(remoteScenes, result.FinalAudio.DurationMS); err != nil {
			return nil, nil, err
		}
		var err error
		audioRef, err = finalJobAudioAsset(ctx, *result.FinalAudio)
		if err != nil {
			return nil, nil, err
		}
		appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "final_audio", audioRef)
	} else if delta := plannedDurationMS*1000 - result.CanonicalTimeline.DurationUS; delta < -40_000 || delta > 40_000 {
		return nil, nil, fmt.Errorf("final_job scene duration %dms does not match canonical timeline %dus", plannedDurationMS, result.CanonicalTimeline.DurationUS)
	}
	remoteOverlays, overlayManifest, err := finalJobOverlayHandoff(ctx, result, consumedPhraseOverlays)
	if err != nil {
		return nil, nil, err
	}
	if overlayManifest != nil {
		if asset, ok := overlayManifest["asset"].(map[string]any); ok {
			appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "overlay_timeline", asset)
		}
	}
	for _, raw := range remoteOverlays {
		if asset, ok := raw.(map[string]any); ok {
			appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "overlay", asset)
		}
	}
	if audioRef == nil {
		return nil, nil, fmt.Errorf("final_job V2 worker submission requires certified final audio")
	}
	compiledPlan, compiledPlanSHA, err := finalJobCompiledPlanPayload(remoteScenes, remoteOverlays, overlayManifest, *result.FinalAudio, audioRef, result.CanonicalTimeline.DurationUS)
	if err != nil {
		return nil, nil, err
	}
	attempt := 1
	if result.RemoteFinalJob != nil && result.RemoteFinalJob.Attempt > 0 {
		attempt = result.RemoteFinalJob.Attempt
	}
	key := finalJobAttemptIdempotencyKey(req.IdempotencyKey, runID, attempt)
	pre := map[string]any{
		"idempotency_key":             key,
		"job_type":                    "video.render.v2",
		"render_only":                 true,
		"video_name":                  finalJobVideoName(req, runID),
		"script_text":                 strings.TrimSpace(scriptText.String()),
		"compiled_render_plan_json":   compiledPlan,
		"compiled_render_plan_sha256": compiledPlanSHA,
	}
	finalize := map[string]any{
		"idempotency_key": key + "-finalize",
		"runtime_assets":  runtimeAssets,
	}
	// The Master has a strict FINALIZE DTO. Overlay and legacy runtime hints
	// stay inside its opaque runtime_payload; the worker's executable timing
	// source is compiled_render_plan_json above.
	runtimePayload := map[string]any{
		"video_name":  finalJobVideoName(req, runID),
		"script_text": strings.TrimSpace(scriptText.String()),
		"overlays":    remoteOverlays,
	}
	if overlayManifest != nil {
		runtimePayload["overlay_manifest"] = overlayManifest
	}
	if hasFinalAudio {
		runtimePayload["runtime_audio"] = map[string]any{
			"voiceover_asset_id":         strings.TrimSpace(result.FinalAudio.AssetID),
			"voiceover_volume":           1.0,
			"voiceover_duration_seconds": float64(result.FinalAudio.DurationMS) / 1000,
		}
	}
	finalize["runtime_payload"] = runtimePayload
	if len(preparers) > 0 && preparers[0] != nil {
		compiledPlan, runtimeAssets, err = prepareFinalJobVideoSegments(ctx, compiledPlan, runtimeAssets, preparers[0])
		if err != nil {
			return nil, nil, err
		}
		compiledPlanSHA = digest.SHA256String(compiledPlan)
		// CompiledRenderPlanV2 is the executable timeline SSOT. Runtime payload
		// metadata must not leak unprepared source videos to the remote job.
		delete(runtimePayload, "overlays")
		delete(runtimePayload, "overlay_manifest")
	}
	pre["compiled_render_plan_json"] = compiledPlan
	pre["compiled_render_plan_sha256"] = compiledPlanSHA
	finalize["runtime_assets"] = runtimeAssets
	if len(preparers) > 0 && preparers[0] != nil {
		if err := validateFinalJobHTTPSRuntimeAssets(runtimeAssets); err != nil {
			return nil, nil, err
		}
	}
	return pre, finalize, nil
}

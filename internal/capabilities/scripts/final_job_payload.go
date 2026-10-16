package scriptgeneration

import (
	"context"
	"fmt"
	"math"
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

func BuildFinalJobPayloads(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult, resolver FinalJobAssetResolver) (map[string]any, map[string]any, error) {
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
				asset, durationMS, overlayID, ok, overlayErr = finalJobPhraseOverlayClip(result, segment.ID)
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
				asset, err = resolver.ResolveFinalJobAsset(ctx, file.ID)
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
			// Keep the stock video on Drive. The remote worker receives this
			// immutable reference and owns materialization/prefetch on its host.
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
		audioRef, err := finalJobAudioAsset(*result.FinalAudio)
		if err != nil {
			return nil, nil, err
		}
		appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "final_audio", audioRef)
	} else if delta := plannedDurationMS*1000 - result.CanonicalTimeline.DurationUS; delta < -40_000 || delta > 40_000 {
		return nil, nil, fmt.Errorf("final_job scene duration %dms does not match canonical timeline %dus", plannedDurationMS, result.CanonicalTimeline.DurationUS)
	}
	for _, bgm := range req.BackgroundMusic {
		asset, err := resolver.ResolveFinalJobAsset(ctx, bgm.AssetID)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve background music %q for remote prefetch: %w", bgm.AssetID, err)
		}
		music := make(map[string]any, len(asset)+1)
		for key, value := range asset {
			music[key] = value
		}
		music["kind"] = "audio"
		for _, track := range result.AudioPlan.Tracks {
			if string(track.Role) != "BGM" {
				continue
			}
			for _, event := range track.Events {
				if event.AssetID == bgm.AssetID && event.SourceDurationUS > 0 {
					music["duration_ms"] = event.SourceDurationUS / 1000
					break
				}
			}
		}
		if music["duration_ms"] == nil {
			return nil, nil, fmt.Errorf("final_job background music %q has no canonical source duration", bgm.AssetID)
		}
		appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "music", music)
	}
	remoteOverlays, err := finalJobOverlayAssetsExcluding(result, consumedPhraseOverlays)
	if err != nil {
		return nil, nil, err
	}
	key := finalJobIdempotencyKey(req.IdempotencyKey, runID)
	pre := map[string]any{
		"idempotency_key": key,
		"job_type":        "scene.composite.v1",
		"copy_only":       true,
		"video_name":      finalJobVideoName(req, runID),
		"script_text":     strings.TrimSpace(scriptText.String()),
		"scenes":          remoteScenes,
		"output":          map[string]any{"width": 1920, "height": 1080, "fps": 24, "format": "mp4"},
		"delivery_plan":   []any{map[string]any{"destination_id": "drive-production", "priority": 1, "retry_budget": 3}},
	}
	finalize := map[string]any{
		"idempotency_key": key + "-finalize",
		"overlays":        remoteOverlays,
		"runtime_assets":  runtimeAssets,
	}
	if hasFinalAudio {
		runtimeAudio := map[string]any{
			"voiceover_asset_id":         strings.TrimSpace(result.FinalAudio.AssetID),
			"voiceover_volume":           1.0,
			"voiceover_duration_seconds": float64(result.FinalAudio.DurationMS) / 1000,
		}
		// Master accepts runtime data under this field; its typed request
		// rejects unknown top-level runtime_audio fields.
		finalize["runtime_payload"] = map[string]any{"runtime_audio": runtimeAudio}
	}
	return pre, finalize, nil
}

func finalJobSceneDurationMS(scenes []map[string]any) int64 {
	var total int64
	for _, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if ok && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds > 0 {
			total += int64(math.Round(seconds * 1000))
		}
	}
	return total
}

// trimFinalJobSceneTail removes a small rounding surplus from the end of the
// video timeline while preserving the minimum duration of every scene. The
// packet-copy renderer requires its video timeline not to exceed final audio.
func trimFinalJobSceneTail(scenes []map[string]any, surplusMS, minimumMS int64) error {
	if surplusMS <= 0 {
		return nil
	}
	for i := len(scenes) - 1; i >= 0 && surplusMS > 0; i-- {
		seconds, ok := scenes[i]["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return fmt.Errorf("final_job scene %d has an invalid duration while aligning audio", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		available := durationMS - minimumMS
		if available <= 0 {
			continue
		}
		trim := min(available, surplusMS)
		scenes[i]["duration_seconds"] = float64(durationMS-trim) / 1000
		surplusMS -= trim
	}
	if surplusMS > 0 {
		return fmt.Errorf("final_job cannot trim %dms to fit the certified audio without violating minimum scene duration", surplusMS)
	}
	return nil
}

// finalJobIdempotencyKey stays stable when the same submitted request is
// retried under a new local run id. The remote Master uses this key to return
// the already-prepared render instead of starting a duplicate video job.
// Older/internal callers without a request key retain the run-id behavior.
func finalJobIdempotencyKey(requestKey, runID string) string {
	requestKey = strings.TrimSpace(requestKey)
	if requestKey == "" {
		return "creator-77-" + strings.TrimSpace(runID)
	}
	// 2026-09-28: the digest SSOT (godlike/06) owns the algorithm. This call
	// used to import crypto/sha256 directly, which the archcheck
	// digest_sha256_import_outside_ssot gate rejects; SHA256String returns the
	// same lowercase hex, so the first 32 characters are the exact 16 bytes the
	// previous hex.EncodeToString(sum[:16]) produced.
	return "creator-77-request-" + digest.SHA256String(requestKey)[:32]
}

// compositeVideoScene preserves source identity in the remote contract. Stock
// footage and caller-provided clips both carry silent video in the `stock`
// media slot, but `kind` determines which pipeline policy owns that video.
// Keep it explicit so stock can never inherit clip subtitle/transcript work.
func compositeVideoScene(id string, index int, kind string, asset map[string]any, durationMS int64, text string) map[string]any {
	ref := make(map[string]any, len(asset))
	for k, v := range asset {
		// Materialized local paths are only for the 77-side compositor. The 51
		// resolves this identity from Drive and rejects unknown request fields.
		if k == "local_path" {
			continue
		}
		ref[k] = v
	}
	if driveID := strings.TrimSpace(fmt.Sprint(ref["drive_file_id"])); driveID != "" {
		ref["url"] = driveFileWebLink(driveID)
	}
	scene := map[string]any{"scene_id": id, "index": index, "kind": kind, "duration_seconds": float64(durationMS) / 1000, "stock": ref}
	// PREPARE requires non-empty text on every scene, but stock scene text is
	// rendered as subtitles. Stock footage is intentionally caption-free, so
	// discard any narration/title and satisfy the schema with an invisible
	// separator. Clip scenes retain their real caption when provided.
	if kind == "stock" || strings.TrimSpace(text) == "" {
		text = "\u200b"
	}
	scene["text"] = text
	return scene
}

// The remote renderer requires each scene to be at least 100 ms. Audio and
// source timing can leave a short final remainder (for example, 16 ms) after
// millisecond rounding. Borrow that remainder from preceding scenes while
// keeping every scene above the limit and preserving the total runtime.
func enforceFinalJobMinimumSceneDuration(scenes []map[string]any, minimumMS int64) error {
	if minimumMS <= 0 {
		return fmt.Errorf("final_job minimum scene duration must be positive")
	}
	for i, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			return fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		if durationMS >= minimumMS {
			continue
		}
		deficit := minimumMS - durationMS
		for previous := i - 1; previous >= 0 && deficit > 0; previous-- {
			previousSeconds, valid := scenes[previous]["duration_seconds"].(float64)
			if !valid || math.IsNaN(previousSeconds) || math.IsInf(previousSeconds, 0) {
				return fmt.Errorf("final_job scene %d has an invalid duration", previous)
			}
			previousMS := int64(math.Round(previousSeconds * 1000))
			available := previousMS - minimumMS
			if available <= 0 {
				continue
			}
			take := min(available, deficit)
			scenes[previous]["duration_seconds"] = float64(previousMS-take) / 1000
			deficit -= take
		}
		if deficit > 0 {
			return fmt.Errorf("final_job scene %d is shorter than %dms with no preceding duration to borrow", i, minimumMS)
		}
		scene["duration_seconds"] = float64(minimumMS) / 1000
	}
	return nil
}

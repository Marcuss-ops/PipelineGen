package scriptgeneration

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type FinalJobStockFile struct{ ID, Name string }
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
	if result == nil || result.CanonicalTimeline == nil || result.FinalAudio == nil || result.FinalAudio.DurationMS <= 0 {
		return nil, nil, fmt.Errorf("final_job requires a canonical timeline and published final audio")
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

	for _, segment := range result.CanonicalTimeline.Segments {
		localScene, sceneExists := scenesByID[segment.ID]
		fixedMedia := segment.FixedMedia || (sceneExists && localScene.ExecutionMode.IsFixedMedia())
		if !fixedMedia && !sceneExists {
			return nil, nil, fmt.Errorf("final_job scene %q has no generated scene", segment.ID)
		}
		if fixedMedia {
			ids := []string{}
			for _, clip := range localScene.Clips {
				if clip != nil && strings.TrimSpace(clip.ID) != "" {
					ids = append(ids, strings.TrimSpace(clip.ID))
				}
			}
			if len(ids) == 0 && localScene.Clip != nil && strings.TrimSpace(localScene.Clip.ID) != "" {
				ids = append(ids, strings.TrimSpace(localScene.Clip.ID))
			}
			if len(ids) == 0 {
				for _, intent := range segment.EffectiveAudioIntents() {
					if intent.ClipAssetID != "" {
						ids = append(ids, intent.ClipAssetID)
					}
				}
			}
			if len(ids) == 0 {
				return nil, nil, fmt.Errorf("fixed scene %q has no clip assets", segment.ID)
			}
			for _, assetID := range ids {
				asset, err := resolveFinalJobFixedMediaAsset(ctx, resolver, result, segment.ID, assetID, renderLanguage)
				if err != nil {
					return nil, nil, fmt.Errorf("resolve intro clip %s: %w", assetID, err)
				}
				durationMS, _ := asset["duration_ms"].(int64)
				if durationMS <= 0 {
					durationMS = 5000
				}
				remoteScenes = append(remoteScenes, compositeStockScene(fmt.Sprintf("scene-%04d", clipOrdinal), clipOrdinal, asset, durationMS, "Protected intro clip"))
				appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "clip", asset)
				plannedDurationMS += durationMS
				clipOrdinal++
			}
			continue
		}
		if !sceneExists {
			return nil, nil, fmt.Errorf("final_job scene %q has no generated scene", segment.ID)
		}
		if localScene.Stock == nil || strings.TrimSpace(localScene.Stock.FolderID) == "" {
			// Clip-only scene: the runtime receives the clip THIS pipeline
			// produced, never the unmodified source clip from the library.
			asset, durationMS, err := clipSceneRuntimeAsset(ctx, resolver, result, renderLanguage, renderLanguageErr, segment.ID)
			if err != nil {
				return nil, nil, err
			}
			remoteScenes = append(remoteScenes, compositeStockScene(fmt.Sprintf("scene-%04d", clipOrdinal), clipOrdinal, asset, durationMS, firstFinalJobValue(localScene.Text[req.SourceLanguage], req.Title, localScene.ID)))
			appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "clip", asset)
			plannedDurationMS += durationMS
			clipOrdinal++
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
			if len(files) == 0 {
				return nil, nil, fmt.Errorf("selected stock folder %s contains no video files", folderID)
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
			remoteScenes = append(remoteScenes, compositeStockScene(fmt.Sprintf("scene-%04d", clipOrdinal), clipOrdinal, asset, chunkMS, sceneText))
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
	if delta := plannedDurationMS - result.FinalAudio.DurationMS; delta < -40 || delta > 40 {
		return nil, nil, fmt.Errorf("final_job scene duration %dms does not match certified final audio %dms (tolerance 40ms)", plannedDurationMS, result.FinalAudio.DurationMS)
	}
	audioRef, err := finalJobAudioAsset(*result.FinalAudio)
	if err != nil {
		return nil, nil, err
	}
	appendFinalJobRuntimeAsset(&runtimeAssets, seenRuntimeAssets, "final_audio", audioRef)
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
	remoteOverlays, err := finalJobOverlayAssets(result)
	if err != nil {
		return nil, nil, err
	}
	key := "creator-77-" + strings.TrimSpace(runID)
	pre := map[string]any{
		"idempotency_key": key,
		"job_type":        "scene.composite.v1",
		"copy_only":       true,
		"video_name":      firstFinalJobValue(req.OutputName, req.Title, runID),
		"script_text":     strings.TrimSpace(scriptText.String()),
		"scenes":          remoteScenes,
		"output":          map[string]any{"width": 1920, "height": 1080, "fps": 24, "format": "mp4"},
		"delivery_plan":   []any{map[string]any{"destination_id": "drive-production", "priority": 1, "retry_budget": 3}},
	}
	runtimeAudio := map[string]any{
		"voiceover_asset_id":         strings.TrimSpace(result.FinalAudio.AssetID),
		"voiceover_volume":           1.0,
		"voiceover_duration_seconds": float64(result.FinalAudio.DurationMS) / 1000,
	}
	finalize := map[string]any{
		"idempotency_key": key + "-finalize",
		"overlays":        remoteOverlays,
		"runtime_assets":  runtimeAssets,
		// The Master finalize endpoint accepts runtime data under this field;
		// its typed request rejects unknown top-level runtime_audio fields.
		"runtime_payload": map[string]any{"runtime_audio": runtimeAudio},
	}
	return pre, finalize, nil
}

// finalJobOverlayAssets projects the already-rendered semantic overlay items
// to the Master finalizer. Their bytes live on Drive; only the remote worker
// fetches them and applies them over the stock timeline.
func finalJobOverlayAssets(result *GenerateResult) ([]any, error) {
	if result == nil || result.OverlayPlan == nil || len(result.OverlayPlan.Items) == 0 {
		return []any{}, nil
	}
	if result.OverlayRender == nil || len(result.OverlayRender.Items) == 0 {
		return nil, fmt.Errorf("final_job has semantic overlays but no published overlay render artifacts")
	}
	byID := make(map[string]RenderArtifact, len(result.OverlayRender.Items))
	for _, rendered := range result.OverlayRender.Items {
		if rendered.Artifact != nil {
			byID[strings.TrimSpace(rendered.ItemID)] = *rendered.Artifact
		}
	}
	fpsNum, fpsDen := result.OverlayPlan.FPSNum, result.OverlayPlan.FPSDen
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, fmt.Errorf("final_job overlay plan has invalid frame rate %d/%d", fpsNum, fpsDen)
	}
	out := make([]any, 0, len(result.OverlayPlan.Items))
	frameGuardUS := (1_000_000*int64(fpsDen) + int64(fpsNum) - 1) / int64(fpsNum)
	for index, item := range result.OverlayPlan.Items {
		artifact, ok := byID[strings.TrimSpace(item.ID)]
		if !ok {
			return nil, fmt.Errorf("final_job overlay %q has no published render artifact", item.ID)
		}
		driveID := strings.TrimSpace(artifact.DriveFileID)
		sha := strings.TrimSpace(artifact.SHA256)
		if driveID == "" || len(sha) != 64 || artifact.SizeBytes <= 0 {
			return nil, fmt.Errorf("final_job overlay %q is not a certified published Drive artifact", item.ID)
		}
		startUS, endUS := item.StartUSValue(), item.EndUSValue()
		if item.Kind == "image" {
			scheduledStartUS, scheduledEndUS, scheduleErr := scheduleFinalJobSceneImage(result, item, frameGuardUS)
			if scheduleErr != nil {
				return nil, scheduleErr
			}
			startUS, endUS = scheduledStartUS, scheduledEndUS
		}
		startFrame := startUS * int64(fpsNum) / (1_000_000 * int64(fpsDen))
		endFrame := (endUS*int64(fpsNum) + 1_000_000*int64(fpsDen) - 1) / (1_000_000 * int64(fpsDen))
		if endFrame <= startFrame {
			return nil, fmt.Errorf("final_job overlay %q has an empty frame interval", item.ID)
		}
		out = append(out, map[string]any{
			"id": item.ID, "asset_id": firstFinalJobValue(artifact.ID, driveID),
			"drive_file_id": driveID, "url": driveFileWebLink(driveID),
			"sha256": sha, "size_bytes": artifact.SizeBytes,
			"start_frame": startFrame, "end_frame": endFrame, "frame_count": endFrame - startFrame,
			"mode": "replace", "z_index": index + 1, "audio_mode": "preserve_final_audio",
		})
	}
	return out, nil
}

// compositeStockScene emits one remote scene. The video reference stays in the
// `stock` slot for BOTH cases, because the slot describes a SILENT video: the
// remote `scene.composite.v1` worker renders video only and the certified final
// voiceover/BGM mix is supplied separately through runtime_audio. A clip-only
// scene's ref is a certified localized render, whose own voiceover track is
// deliberately not the mix — putting it in `clip` would add a second
// scene_clip_audio track and mix the render's audio into the final video.
func compositeStockScene(id string, index int, asset map[string]any, durationMS int64, text string) map[string]any {
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
	return map[string]any{"scene_id": id, "index": index, "kind": "clip", "text": text, "duration_seconds": float64(durationMS) / 1000, "stock": ref}
}

// ── Certified rendered-clip handoff ───────────────────────────────────
//
// A clip-only run has no stock folder to chunk: its video IS the certified
// localized render of each scene. These helpers select that render and project
// it into the runtime asset reference. The source clip in the media library is
// never sent: it still carries the unmodified picture that this pipeline
// replaces (background, watermark, burnt subtitles).

// finalJobRenderLanguage resolves the single language whose certified renders
// feed the remote video. An empty language means the run produced no certified
// render at all (a stock-only run), which is not an error by itself. A run with
// renders in several languages and none in the source language is ambiguous and
// reports an error instead of picking one.
func finalJobRenderLanguage(req GenerateRequest, result *GenerateResult) (string, error) {
	if result == nil || len(result.LocalizedRenders) == 0 {
		return "", nil
	}
	preferred := strings.TrimSpace(string(req.SourceLanguage))
	if preferred != "" {
		for _, rendered := range result.LocalizedRenders {
			if strings.EqualFold(strings.TrimSpace(string(rendered.Language)), preferred) {
				return strings.TrimSpace(string(rendered.Language)), nil
			}
		}
	}
	languages := make(map[string]struct{}, len(result.LocalizedRenders))
	for _, rendered := range result.LocalizedRenders {
		if language := strings.TrimSpace(string(rendered.Language)); language != "" {
			languages[language] = struct{}{}
		}
	}
	if len(languages) == 1 {
		for language := range languages {
			return language, nil
		}
	}
	available := make([]string, 0, len(languages))
	for language := range languages {
		available = append(available, language)
	}
	sort.Strings(available)
	return "", fmt.Errorf("no certified render for source language %q and the run produced %d render languages (%s)", preferred, len(available), strings.Join(available, ", "))
}

// certifiedLocalizedRendersForScene returns the certified renders of one scene
// for one language. A render is certified only when its published MP4 identity
// is complete (drive file id + 64-char sha256 + positive duration): the runtime
// copies those bytes by identity and never re-renders them here.
func certifiedLocalizedRendersForScene(result *GenerateResult, sceneID, language string) []LocalizedRenderResult {
	if result == nil {
		return nil
	}
	var out []LocalizedRenderResult
	for _, rendered := range result.LocalizedRenders {
		if !strings.EqualFold(strings.TrimSpace(rendered.SceneID), sceneID) {
			continue
		}
		if language != "" && !strings.EqualFold(strings.TrimSpace(string(rendered.Language)), language) {
			continue
		}
		if !localizedRenderIsCertifiedClip(rendered) {
			continue
		}
		out = append(out, rendered)
	}
	return out
}

// certifiedLocalizedRenderForClip is the fixed-media projection: one certified
// render for the exact (scene, clip, language) unit, or none.
func certifiedLocalizedRenderForClip(result *GenerateResult, sceneID, clipID, language string) (LocalizedRenderResult, bool) {
	if result == nil || strings.TrimSpace(clipID) == "" {
		return LocalizedRenderResult{}, false
	}
	for _, rendered := range certifiedLocalizedRendersForScene(result, sceneID, language) {
		if strings.EqualFold(strings.TrimSpace(rendered.ClipID), strings.TrimSpace(clipID)) {
			return rendered, true
		}
	}
	return LocalizedRenderResult{}, false
}

func localizedRenderIsCertifiedClip(rendered LocalizedRenderResult) bool {
	return strings.TrimSpace(rendered.DriveFileID) != "" && len(strings.TrimSpace(rendered.SHA256)) == 64 && rendered.DurationMS > 0
}

// clipSceneRuntimeAsset builds the runtime asset reference of a clip-only scene
// from its certified localized render. A scene without a certified render — or
// a scene that produced more than one — fails closed: the clip-only contract is
// "send the clip this pipeline produced", so there is no fallback to the
// unmodified source clip.
func clipSceneRuntimeAsset(ctx context.Context, resolver FinalJobAssetResolver, result *GenerateResult, renderLanguage string, renderLanguageErr error, sceneID string) (map[string]any, int64, error) {
	if renderLanguageErr != nil {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q: %w", sceneID, renderLanguageErr)
	}
	rendered := certifiedLocalizedRendersForScene(result, sceneID, renderLanguage)
	if len(rendered) == 0 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has no certified rendered clip for language %q: refusing to hand the unmodified source clip to the runtime", sceneID, renderLanguage)
	}
	if len(rendered) > 1 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has %d certified rendered clips for language %q: a clip-only final job sends exactly one rendered clip per scene", sceneID, len(rendered), renderLanguage)
	}
	return renderedClipAssetRef(ctx, resolver, rendered[0], sceneID)
}

// resolveFinalJobFixedMediaAsset prefers the certified render of one fixed
// (intro/outro) clip over the unmodified library clip, and keeps the library
// asset as the fallback: a fixed section may legitimately be declared without a
// render lane, which is the pre-existing behaviour for protected intros.
func resolveFinalJobFixedMediaAsset(ctx context.Context, resolver FinalJobAssetResolver, result *GenerateResult, sceneID, clipID, renderLanguage string) (map[string]any, error) {
	if renderLanguage != "" {
		if rendered, ok := certifiedLocalizedRenderForClip(result, sceneID, clipID, renderLanguage); ok {
			asset, _, err := renderedClipAssetRef(ctx, resolver, rendered, sceneID)
			return asset, err
		}
	}
	return resolver.ResolveFinalJobAsset(ctx, clipID)
}

// renderedClipAssetRef projects a certified localized render into the runtime
// asset reference the remote scene copies. The key set matches the reference
// ResolveFinalJobAsset produces, so the remote sees one asset shape regardless
// of whether the bytes came from the media library or from this render lane.
func renderedClipAssetRef(ctx context.Context, resolver FinalJobAssetResolver, rendered LocalizedRenderResult, sceneID string) (map[string]any, int64, error) {
	driveID := strings.TrimSpace(rendered.DriveFileID)
	sha := strings.TrimSpace(rendered.SHA256)
	if driveID == "" || len(sha) != 64 || rendered.DurationMS <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is not a certified published MP4 (drive_file_id, 64-char sha256 and positive duration required)", sceneID, rendered.AssetID)
	}
	size, err := resolver.FinalJobPublishedFileSize(ctx, driveID)
	if err != nil {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s: %w", sceneID, driveID, err)
	}
	if size <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s has no published byte size", sceneID, driveID)
	}
	assetID := strings.TrimSpace(rendered.AssetID)
	if assetID == "" {
		assetID = driveID
	}
	return map[string]any{
		"asset_id": assetID, "drive_file_id": driveID, "url": driveFileWebLink(driveID),
		"sha256": sha, "size_bytes": size, "duration_ms": rendered.DurationMS,
	}, rendered.DurationMS, nil
}

func firstFinalJobValue(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return "final-job"
}

func appendFinalJobRuntimeAsset(dst *[]any, seen map[string]bool, role string, ref map[string]any) {
	if ref == nil {
		return
	}
	id := strings.TrimSpace(fmt.Sprint(ref["drive_file_id"]))
	if id == "" {
		id = strings.TrimSpace(fmt.Sprint(ref["asset_id"]))
	}
	if id == "" || seen[role+":"+id] {
		return
	}
	seen[role+":"+id] = true
	item := make(map[string]any, len(ref)+1)
	for key, value := range ref {
		// Keep worker-local cache paths out of the remote FINALIZE contract.
		if key == "local_path" {
			continue
		}
		item[key] = value
	}
	if driveID := strings.TrimSpace(fmt.Sprint(ref["drive_file_id"])); driveID != "" {
		item["url"] = driveFileWebLink(driveID)
	}
	item["role"] = role
	*dst = append(*dst, item)
}

func finalJobAudioAsset(audio FinalAudioReference) (map[string]any, error) {
	driveID := driveFileIDFromLink(audio.DriveLink)
	if driveID == "" || len(strings.TrimSpace(audio.FinalAudioSHA256)) != 64 || audio.SizeBytes <= 0 || audio.DurationMS <= 0 || !audio.FinalMix || !audio.CopyEligible {
		return nil, fmt.Errorf("final_job requires a published, certified FINAL_AUDIO_COPY mix with Drive file ID, SHA-256, size, and duration")
	}
	if !strings.EqualFold(strings.TrimSpace(audio.Codec), "aac") || !strings.EqualFold(strings.TrimSpace(audio.Profile), "LC") || audio.SampleRate != 48000 || audio.Channels != 2 || !strings.EqualFold(strings.TrimSpace(audio.ChannelLayout), "stereo") {
		return nil, fmt.Errorf("final_job final audio is not canonical AAC-LC 48 kHz stereo")
	}
	return map[string]any{
		"asset_id": audio.AssetID, "drive_file_id": driveID,
		"url": driveFileWebLink(driveID), "sha256": strings.TrimSpace(audio.FinalAudioSHA256),
		"size_bytes": audio.SizeBytes, "duration_ms": audio.DurationMS,
		"codec": audio.Codec, "profile": audio.Profile, "sample_rate": audio.SampleRate,
		"channels": audio.Channels, "channel_layout": audio.ChannelLayout,
		"strategy": "FINAL_AUDIO_COPY", "copy_eligible": audio.CopyEligible,
		"audio_contract_version": audio.AudioContractVersion, "audio_plan_version": audio.AudioPlanVersion,
		"audio_plan_sha256": audio.PlanSHA256, "final_mix": audio.FinalMix,
	}, nil
}

func driveFileWebLink(driveID string) string {
	// The Master resolver accepts the canonical deferred-Drive locator, which
	// carries the exact Drive file ID without making the 77 fetch the asset.
	return "velox-drive://" + strings.TrimSpace(driveID)
}

func driveFileIDFromLink(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if part == "d" && i+1 < len(parts) && parts[i+1] != "" {
			return parts[i+1]
		}
		if part == "file" && i+2 < len(parts) && parts[i+1] == "d" && parts[i+2] != "" {
			return parts[i+2]
		}
	}
	return ""
}

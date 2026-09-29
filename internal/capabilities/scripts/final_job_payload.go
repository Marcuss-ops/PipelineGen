package scriptgeneration

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
	if err := enforceFinalJobMinimumSceneDuration(remoteScenes, 100); err != nil {
		return nil, nil, err
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

// scheduleFinalJobSceneImage moves a contextual scene image to the first
// available interval after its planned start. The Master currently accepts
// only mode=replace and rejects intersecting overlay windows, while local
// semantic cards can occupy the same opening beat. Preserve every certified
// overlay and move only the generic scene image within its own scene window.
func scheduleFinalJobSceneImage(result *GenerateResult, image capabilityoverlay.OverlayItem, frameGuardUS int64) (int64, int64, error) {
	start := image.StartUSValue()
	end := image.EndUSValue()
	duration := end - start
	if duration <= 0 {
		return 0, 0, fmt.Errorf("final_job scene image %q has an empty timing window", image.ID)
	}
	sceneStart, sceneEnd := int64(0), int64(0)
	if result != nil {
		if result.CanonicalTimeline != nil {
			for _, scene := range result.CanonicalTimeline.Segments {
				if scene.ID == image.SceneID {
					sceneStart, sceneEnd = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
					break
				}
			}
		}
		for _, scene := range result.ResolvedScenes {
			if scene.ID == image.SceneID {
				sceneStart, sceneEnd = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
				break
			}
		}
	}
	if sceneEnd > sceneStart {
		if start < sceneStart {
			start = sceneStart
		}
		if end > sceneEnd {
			end = sceneEnd
			duration = end - start
		}
	}
	if result == nil || result.OverlayPlan == nil {
		return start, start + duration, nil
	}
	occupied := make([][2]int64, 0)
	for _, other := range result.OverlayPlan.Items {
		if other.ID == image.ID || other.SceneID != image.SceneID {
			continue
		}
		otherStart, otherEnd := other.StartUSValue(), other.EndUSValue()
		if otherEnd > otherStart {
			occupied = append(occupied, [2]int64{otherStart, otherEnd})
		}
	}
	sort.Slice(occupied, func(i, j int) bool { return occupied[i][0] < occupied[j][0] })
	candidate := start
	for {
		moved := false
		for _, window := range occupied {
			if candidate < window[1] && window[0] < candidate+duration {
				candidate = window[1] + frameGuardUS
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	if sceneEnd > sceneStart && candidate+duration > sceneEnd {
		return 0, 0, fmt.Errorf("final_job scene image %q cannot fit a non-overlapping %dµs window in scene %q", image.ID, duration, image.SceneID)
	}
	return candidate, candidate + duration, nil
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

// ── Certified rendered-clip handoff ───────────────────────────────────
//
// finalJobVideoName derives the Master submission identity from the run, never
// from the caller's title/project. Two runs of the same topic share a title
// (OutputName defaults to the title), and an identical video_name made the
// second submission collide on the Master's replace_overlap guard (HTTP 422).
// A run-scoped suffix keeps the readable title prefix while guaranteeing the
// identity is unique per run and stable across retries of the same run.
func finalJobVideoName(req GenerateRequest, runID string) string {
	base := firstFinalJobValue(req.OutputName, req.Title, "final-job")
	suffix := finalJobIdentitySuffix(runID)
	if suffix == "" {
		return base
	}
	return base + " [" + suffix + "]"
}

// finalJobIdentitySuffix reduces the run id to a deterministic, filesystem- and
// URL-safe token. It keeps the trailing segment (the run's unique uuid tail)
// and drops anything that would need escaping in an identity field.
func finalJobIdentitySuffix(runID string) string {
	id := strings.TrimSpace(runID)
	if id == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	suffix := strings.Trim(b.String(), "-")
	if len(suffix) > 16 {
		suffix = suffix[len(suffix)-16:]
	}
	return suffix
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

package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type finalJobPayloadResolver struct{}

func (finalJobPayloadResolver) ResolveFinalJobAsset(context.Context, string) (map[string]any, error) {
	return map[string]any{"asset_id": "drive-asset", "drive_file_id": "drive-asset", "url": "velox-drive://drive-asset", "sha256": strings.Repeat("b", 64), "size_bytes": int64(20), "duration_ms": int64(2000)}, nil
}

func TestEnforceFinalJobMinimumSceneDurationBorrowsFromEarlierScene(t *testing.T) {
	scenes := []map[string]any{
		{"duration_seconds": 2.0},
		{"duration_seconds": 0.016},
	}

	if err := enforceFinalJobMinimumSceneDuration(scenes, 100); err != nil {
		t.Fatalf("enforceFinalJobMinimumSceneDuration: %v", err)
	}
	if got := scenes[0]["duration_seconds"]; got != 1.916 {
		t.Fatalf("preceding scene duration = %v, want 1.916", got)
	}
	if got := scenes[1]["duration_seconds"]; got != 0.1 {
		t.Fatalf("short scene duration = %v, want 0.1", got)
	}
	if got := scenes[0]["duration_seconds"].(float64) + scenes[1]["duration_seconds"].(float64); got != 2.016 {
		t.Fatalf("adjusted total duration = %v, want 2.016", got)
	}
}

func TestTrimFinalJobSceneTailFitsVideoWithinFinalAudio(t *testing.T) {
	scenes := []map[string]any{{"duration_seconds": 1.0}, {"duration_seconds": 0.5}}
	if err := trimFinalJobSceneTail(scenes, 8, 100); err != nil {
		t.Fatalf("trimFinalJobSceneTail: %v", err)
	}
	if got := finalJobSceneDurationMS(scenes); got != 1492 {
		t.Fatalf("video duration = %dms, want 1492ms after trimming the 8ms surplus", got)
	}
	if got := scenes[1]["duration_seconds"]; got != 0.492 {
		t.Fatalf("last scene duration = %v, want 0.492s", got)
	}
}

func TestScheduleFinalJobSceneImageMovesAfterReplaceOverlays(t *testing.T) {
	image := capabilityoverlay.OverlayItem{ID: "scene-image", SceneID: "scene-1", Kind: "image", StartUS: 5_000_000, DurationUS: 5_000_000}
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", TimelineStartUS: 0, DurationUS: 20_000_000}}},
		OverlayPlan: &capabilityoverlay.OverlayPlan{Items: []capabilityoverlay.OverlayItem{
			image,
			{ID: "entity-card", SceneID: "scene-1", Kind: "entity_image", StartUS: 6_000_000, DurationUS: 4_000_000},
			{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", StartUS: 12_000_000, DurationUS: 2_000_000},
		}},
	}
	frameGuardUS := int64((1_000_000 + 24 - 1) / 24)
	start, end, err := scheduleFinalJobSceneImage(result, image, frameGuardUS)
	if err != nil {
		t.Fatalf("scheduleFinalJobSceneImage: %v", err)
	}
	if start != 14_000_000+frameGuardUS || end != 19_000_000+frameGuardUS {
		t.Fatalf("scheduled image window = %d-%d, want 14s-19s with frame guard %d", start, end, frameGuardUS)
	}
}

func TestEnforceFinalJobMinimumSceneDurationFailsWhenNoTimeCanBeBorrowed(t *testing.T) {
	scenes := []map[string]any{{"duration_seconds": 0.016}}
	if err := enforceFinalJobMinimumSceneDuration(scenes, 100); err == nil {
		t.Fatal("expected a short single-scene timeline to be rejected")
	}
}

func TestBuildFinalJobPayloadsRequiresPublishedOverlayAssets(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 1_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 1_000_000}}},
		FinalAudio:        &FinalAudioReference{AssetID: "audio-1", DriveLink: "https://drive.google.com/file/d/audio-drive-id/view", FinalAudioSHA256: strings.Repeat("a", 64), SizeBytes: 12, DurationMS: 1000, Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", FinalMix: true, CopyEligible: true},
		Scenes:            []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "run-1",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			Items: []capabilityoverlay.OverlayItem{{ID: "phrase-overlay", SceneID: "scene-1", TemplateID: "phrase", StartMs: 100, EndMs: 500}},
		},
	}
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "no published overlay render artifacts") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want missing published overlay error", err)
	}
}

func TestBuildFinalJobPayloadsStockOnlyNeverFallsBackToClip(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 1000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 1000_000}}},
		FinalAudio:        certifiedFinalAudio(1000),
		Scenes:            []Scene{{ID: "scene-1", Clip: &ClipReference{ID: "yt_wrong_clip"}}},
	}
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt", MediaMode: scriptpkg.MediaModeStockOnly}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "stock_only scene") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want fail-closed stock-only routing", err)
	}
}

func (finalJobPayloadResolver) ListFinalJobStockFolder(context.Context, string) ([]FinalJobStockFile, error) {
	return []FinalJobStockFile{{ID: "drive-asset", Name: "stock.mp4"}}, nil
}

func (finalJobPayloadResolver) FinalJobPublishedFileSize(context.Context, string) (int64, error) {
	return 7_000_000, nil
}

// TestFinalJobAudioInputRestoresClipAudioAtFinalJobGain pins the 2026-09-30
// clip-audio fix: the remote final-job projection keeps AudioClip intents and
// stamps the restored-mix gain instead of dropping them. The delivered video
// therefore mixes the clip's original audio under narration, and the original
// GenerateResult stays untouched.
func TestFinalJobAudioInputRestoresClipAudioAtFinalJobGain(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "stock-clip"},
		AudioIntents: []capabilityaudio.AudioIntent{
			{Mode: capabilityaudio.AudioClip, ClipAssetID: "stock-clip"},
		},
		Voiceover: map[Language]AudioReference{"pt": {ID: "vo-pt", FilePath: "/tmp/vo.mp3"}},
	}}}
	compiled := finalJobAudioInput(result, "pt")
	intents := compiled.Scenes[0].AudioIntents
	if len(intents) != 2 {
		t.Fatalf("final-job audio intents = %#v, want the restored clip plus the voiceover", intents)
	}
	var clipIntent *capabilityaudio.AudioIntent
	var voIntent *capabilityaudio.AudioIntent
	for i := range intents {
		switch intents[i].Mode {
		case capabilityaudio.AudioClip:
			clipIntent = &intents[i]
		case capabilityaudio.AudioVoiceover:
			voIntent = &intents[i]
		}
	}
	if clipIntent == nil || clipIntent.ClipAssetID != "stock-clip" {
		t.Fatalf("clip intent was dropped: %#v", intents)
	}
	if clipIntent.GainDB != kernelaudio.FinalJobRestoredClipGainDB {
		t.Fatalf("restored clip gain = %v, want %v", clipIntent.GainDB, kernelaudio.FinalJobRestoredClipGainDB)
	}
	if voIntent == nil || voIntent.VoiceoverAssetID != "vo-pt" {
		t.Fatalf("voiceover intent missing or wrong: %#v", intents)
	}
	if result.Scenes[0].AudioIntents[0].GainDB != 0 {
		t.Fatal("finalJobAudioInput mutated the original scene intents")
	}
	if result.Scenes[0].Audio.Mode != capabilityaudio.AudioClip || result.Scenes[0].Audio.GainDB != 0 {
		t.Fatal("finalJobAudioInput mutated the original scene")
	}
}

func TestFinalJobAudioInputAddsIntermediateClipAudioBesideVoiceover(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-middle", Clip: &ClipReference{
			ID: "middle-clip", DurationUS: 12_000_000, AudioPath: "/tmp/middle.mp4",
			SourceInMS: 2_000, SourceOutMS: 8_000,
		},
		Voiceover: map[Language]AudioReference{"pt": {ID: "vo-pt", FilePath: "/tmp/vo.m4a", Duration: 6}},
	}}}
	projected := finalJobAudioInput(result, "pt")
	intents := projected.Scenes[0].AudioIntents
	if len(intents) != 2 {
		t.Fatalf("intermediate clip audio intents = %#v, want clip audio and voiceover", intents)
	}
	var clip, voiceover *capabilityaudio.AudioIntent
	for i := range intents {
		switch intents[i].Mode {
		case capabilityaudio.AudioClip:
			clip = &intents[i]
		case capabilityaudio.AudioVoiceover:
			voiceover = &intents[i]
		}
	}
	if clip == nil || clip.ClipAssetID != "middle-clip" || clip.SourceInUS != 2_000_000 || clip.SourceDurationUS != 6_000_000 || !clip.UseOriginalAudio {
		t.Fatalf("intermediate clip audio window = %#v", clip)
	}
	if clip.GainDB != kernelaudio.FinalJobRestoredClipGainDB {
		t.Fatalf("intermediate clip gain = %v, want %v", clip.GainDB, kernelaudio.FinalJobRestoredClipGainDB)
	}
	if voiceover == nil || voiceover.VoiceoverAssetID != "vo-pt" {
		t.Fatalf("intermediate scene voiceover missing: %#v", intents)
	}
	_, plan, _, _, err := CompileCanonicalAudioPlanAudioOnly(projected, "pt", capabilityaudio.DefaultAudioProfile())
	if err != nil {
		t.Fatalf("compile final-job audio master: %v", err)
	}
	if len(eventsForRole(plan, capabilityaudio.TrackClipAudio)) != 1 || len(eventsForRole(plan, capabilityaudio.TrackVoiceover)) != 1 {
		t.Fatalf("compiled intermediate mix must contain both source clip and voiceover tracks: %#v", plan.Tracks)
	}
}

func TestFinalJobAudioInputLeavesStockScenesVisualOnly(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-stock", Clip: &ClipReference{ID: "source-clip", DurationUS: 10_000_000},
		Stock:     &scriptpkg.StockBinding{AssetID: "stock-video"},
		Voiceover: map[Language]AudioReference{"pt": {ID: "vo-pt", FilePath: "/tmp/vo.m4a"}},
	}}}
	projected := finalJobAudioInput(result, "pt")
	for _, intent := range projected.Scenes[0].AudioIntents {
		if intent.Mode == capabilityaudio.AudioClip {
			t.Fatalf("stock-selected scene inherited source clip audio: %#v", intent)
		}
	}
}

func TestRestoreFinalJobFixedMediaAfterAudioProjection(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{{ID: "scene-intro", ExecutionMode: scriptpkg.SceneExecutionFixedMedia}}}
	projected := finalJobAudioInput(*result, "pt")
	if projected.Scenes[0].ExecutionMode.IsFixedMedia() {
		t.Fatal("audio projection should not retain the source-audio firewall")
	}
	timeline := &capabilityaudio.CanonicalTimeline{Segments: []capabilityaudio.TimelineSegment{{ID: "scene-intro"}}}
	restoreFinalJobFixedMedia(result, timeline)
	if !timeline.Segments[0].FixedMedia {
		t.Fatal("fixed-media marker was not restored for the remote payload")
	}
}

// certifiedFinalAudio is the published FINAL_AUDIO_COPY mix every final_job
// build requires, shared by the clip-only cases below.
func certifiedFinalAudio(durationMS int64) *FinalAudioReference {
	return &FinalAudioReference{
		AssetID: "audio-1", DriveLink: "https://drive.google.com/file/d/audio-drive-id/view",
		FinalAudioSHA256: strings.Repeat("a", 64), SizeBytes: 12, DurationMS: durationMS,
		Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", FinalMix: true, CopyEligible: true,
	}
}

// TestBuildFinalJobPayloadsClipOnlyScenesSendTheCertifiedRenderedClip pins the
// clip-only handoff contract: the runtime receives the clip THIS pipeline
// produced (certified localized render, background + watermark + burnt
// subtitles already applied), and the unmodified source clip from the media
// library never reaches the payload.
func TestBuildFinalJobPayloadsClipOnlyScenesSendTheCertifiedRenderedClip(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 30_000_000,
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-1", DurationUS: 30_000_000,
				Video:        capabilityaudio.VideoSegment{AssetID: "yt_source_clip"},
				AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioClip, ClipAssetID: "yt_source_clip", SourceDurationUS: 12_000_000, UseOriginalAudio: true}},
			}},
		},
		FinalAudio: certifiedFinalAudio(30_000),
		Scenes:     []Scene{{ID: "scene-1", Clip: &ClipReference{ID: "yt_source_clip"}, Text: map[Language]string{"en": "Dolly lands the hotel joke."}}},
		LocalizedRenders: []LocalizedRenderResult{{
			SceneID: "scene-1", Language: "en", ClipID: "yt_source_clip", Status: "UPLOADED",
			AssetID: "derived-render-1", DriveFileID: "render-drive-1", DriveLink: "https://drive.google.com/file/d/render-drive-1/view",
			SHA256: strings.Repeat("d", 64), DurationMS: 12_000, RenderSource: "fresh_gpu",
		}},
	}
	pre, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "Dolly", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	scenes, ok := pre["scenes"].([]map[string]any)
	if !ok || len(scenes) != 3 {
		t.Fatalf("scenes = %#v, want three chunks filling the 30s scene", pre["scenes"])
	}
	if scenes[0]["text"] != "Dolly lands the hotel joke." {
		t.Errorf("scene text = %v, want the generated scene text", scenes[0]["text"])
	}
	if scenes[0]["kind"] != "clip" {
		t.Errorf("clip-only scene kind = %v, want clip", scenes[0]["kind"])
	}
	if scenes[0]["duration_seconds"] != 12.0 || scenes[1]["duration_seconds"] != 12.0 || scenes[2]["duration_seconds"] != 6.0 {
		t.Fatalf("scene durations = %v, %v, %v; want 12s, 12s, 6s", scenes[0]["duration_seconds"], scenes[1]["duration_seconds"], scenes[2]["duration_seconds"])
	}
	ref, ok := scenes[0]["stock"].(map[string]any)
	if !ok {
		t.Fatalf("scene video ref = %#v, want the silent-video reference block", scenes[0]["stock"])
	}
	if ref["drive_file_id"] != "render-drive-1" || ref["url"] != "velox-drive://render-drive-1" || ref["sha256"] != strings.Repeat("d", 64) || ref["size_bytes"] != int64(7_000_000) || ref["duration_ms"] != int64(12_000) {
		t.Fatalf("scene video ref = %#v, want the certified rendered clip identity", ref)
	}
	if ref["asset_id"] != "derived-render-1" {
		t.Errorf("scene video asset_id = %v, want the derived render asset", ref["asset_id"])
	}
	assets, ok := finalize["runtime_assets"].([]any)
	if !ok {
		t.Fatalf("runtime_assets = %#v, want a list", finalize["runtime_assets"])
	}
	clipRole := 0
	for _, raw := range assets {
		asset := raw.(map[string]any)
		if asset["role"] == "clip" {
			clipRole++
			if asset["drive_file_id"] != "render-drive-1" {
				t.Errorf("runtime clip asset = %#v, want the rendered clip", asset)
			}
		}
	}
	if clipRole != 1 {
		t.Fatalf("runtime clip assets = %d, want 1", clipRole)
	}
	encoded, err := json.Marshal(map[string]any{"pre": pre, "finalize": finalize})
	if err != nil {
		t.Fatalf("marshal payloads: %v", err)
	}
	if bytes.Contains(encoded, []byte("yt_source_clip")) {
		t.Fatalf("payload still carries the unmodified source clip: %s", encoded)
	}
}

// TestBuildFinalJobPayloadsClipOnlySceneWithoutRenderFailsClosed pins that a
// clip-only scene with no certified render fails the build instead of quietly
// handing the runtime the unmodified source clip.
func TestBuildFinalJobPayloadsClipOnlySceneWithoutRenderFailsClosed(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 12_000_000,
			Segments:   []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 12_000_000, Video: capabilityaudio.VideoSegment{AssetID: "yt_source_clip"}}},
		},
		FinalAudio: certifiedFinalAudio(12_000),
		Scenes:     []Scene{{ID: "scene-1", Clip: &ClipReference{ID: "yt_source_clip"}, Text: map[Language]string{"en": "Dolly lands the hotel joke."}}},
	}
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "Dolly", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "refusing to hand the unmodified source clip") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want the fail-closed clip-only error", err)
	}
}

// TestBuildFinalJobPayloadsFixedMediaPrefersTheCertifiedRenderedClip pins that a
// fixed (intro/outro) clip also prefers its certified render, while a fixed
// section declared without a render keeps the library asset (pre-existing
// behaviour for protected intros).
func TestBuildFinalJobPayloadsFixedMediaPrefersTheCertifiedRenderedClip(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 15_000_000,
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-intro", DurationUS: 15_000_000, FixedMedia: true,
			}},
		},
		FinalAudio: certifiedFinalAudio(15_000),
		// The fixed section carries no generated scene of its own; the guard
		// only requires the run to have produced scenes at all.
		Scenes: []Scene{
			{ID: "scene-intro", ExecutionMode: scriptpkg.SceneExecutionFixedMedia, Clips: []*ClipReference{
				{ID: "intro-source-clip-1", SourceInMS: 0, SourceOutMS: 5000},
				{ID: "intro-source-clip-2", SourceInMS: 0, SourceOutMS: 5000},
				{ID: "intro-source-clip-3", SourceInMS: 0, SourceOutMS: 5000},
			}},
			{ID: "scene-body", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}},
		},
		LocalizedRenders: []LocalizedRenderResult{
			{SceneID: "scene-intro", Language: "en", ClipID: "intro-source-clip-1", Status: "UPLOADED", AssetID: "derived-intro-render-1", DriveFileID: "intro-render-drive-1", SHA256: strings.Repeat("e", 64), DurationMS: 15_000},
			{SceneID: "scene-intro", Language: "en", ClipID: "intro-source-clip-2", Status: "UPLOADED", AssetID: "derived-intro-render-2", DriveFileID: "intro-render-drive-2", SHA256: strings.Repeat("f", 64), DurationMS: 19_000},
			{SceneID: "scene-intro", Language: "en", ClipID: "intro-source-clip-3", Status: "UPLOADED", AssetID: "derived-intro-render-3", DriveFileID: "intro-render-drive-3", SHA256: strings.Repeat("c", 64), DurationMS: 13_000},
		},
	}
	pre, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "Dolly", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	scenes := pre["scenes"].([]map[string]any)
	if len(scenes) != 3 {
		t.Fatalf("scenes = %d, want three fixed-media clip cuts", len(scenes))
	}
	ref := scenes[0]["stock"].(map[string]any)
	if ref["drive_file_id"] != "intro-render-drive-1" {
		t.Fatalf("fixed-media ref = %#v, want the certified rendered intro clip", ref)
	}
	if scenes[0]["duration_seconds"] != 5.0 {
		t.Fatalf("fixed-media scene duration = %v, want the 5s canonical timeline cut, not the 15s file duration", scenes[0]["duration_seconds"])
	}
	if scenes[1]["duration_seconds"] != 5.0 || scenes[2]["duration_seconds"] != 5.0 {
		t.Fatalf("fixed-media clip durations = %v, %v, %v; want 5s each", scenes[0]["duration_seconds"], scenes[1]["duration_seconds"], scenes[2]["duration_seconds"])
	}
}

// TestBuildFinalJobPayloadsFixedMediaWithoutRenderKeepsTheLibraryClip keeps the
// documented fallback honest: no render lane for a fixed section means the
// resolved library clip is still sent.
func TestBuildFinalJobPayloadsFixedMediaWithoutRenderKeepsTheLibraryClip(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 2_000_000,
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-intro", DurationUS: 2_000_000, FixedMedia: true,
				AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioClip, ClipAssetID: "intro-source-clip", SourceDurationUS: 2_000_000, UseOriginalAudio: true, ProtectedOriginalAudio: true}},
			}},
		},
		// The fake resolver reports a 2000 ms library clip: the fixed-media
		// duration comes from the resolved asset, so the certified final audio
		// and the segment agree at 2 s.
		FinalAudio: certifiedFinalAudio(2_000),
		Scenes:     []Scene{{ID: "scene-body", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
	}
	pre, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "Dolly", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	scenes := pre["scenes"].([]map[string]any)
	ref := scenes[0]["stock"].(map[string]any)
	if ref["drive_file_id"] != "drive-asset" {
		t.Fatalf("fixed-media ref = %#v, want the resolved library clip", ref)
	}
}

func TestBuildFinalJobPayloadsDoesNotSendSubtitlesWithStockChunks(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 5_000_000,
			Segments:   []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 5_000_000}},
		},
		FinalAudio: &FinalAudioReference{AssetID: "audio-1", DriveLink: "https://drive.google.com/file/d/audio-drive-id/view", FinalAudioSHA256: strings.Repeat("a", 64), SizeBytes: 12, DurationMS: 5000, Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", FinalMix: true, CopyEligible: true},
		Scenes:     []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
	}
	pre, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	scenes, ok := pre["scenes"].([]map[string]any)
	if !ok {
		t.Fatalf("scenes has type %T, want []map[string]any", pre["scenes"])
	}
	if len(scenes) != 3 {
		t.Fatalf("scenes = %d, want 3 stock chunks", len(scenes))
	}
	for i, scene := range scenes {
		if scene["kind"] != "stock" {
			t.Errorf("scene %d kind = %v, want stock", i, scene["kind"])
		}
		if _, hasText := scene["text"]; hasText {
			t.Errorf("stock scene %d carries text that the remote renderer could burn as subtitles: %#v", i, scene["text"])
		}
		if _, duplicated := scene["clip"]; duplicated {
			t.Errorf("scene %d sends video with source audio as a clip", i)
		}
		if _, ok := scene["stock"].(map[string]any); !ok {
			t.Errorf("scene %d has no single silent stock asset", i)
		}
	}
	runtimePayload, ok := finalize["runtime_payload"].(map[string]any)
	if !ok {
		t.Fatalf("runtime_payload = %#v, want nested runtime contract", finalize["runtime_payload"])
	}
	runtimeAudio, ok := runtimePayload["runtime_audio"].(map[string]any)
	if !ok || runtimeAudio["voiceover_asset_id"] != "audio-1" || runtimeAudio["voiceover_duration_seconds"] != 5.0 {
		t.Fatalf("runtime_audio = %#v, want published final mix reference", runtimePayload["runtime_audio"])
	}
	if assets, ok := finalize["runtime_assets"].([]any); !ok || len(assets) != 2 {
		t.Fatalf("runtime_assets = %#v, want deduplicated stock plus final audio", finalize["runtime_assets"])
	} else {
		foundAudio := false
		for _, raw := range assets {
			asset, ok := raw.(map[string]any)
			if ok && asset["role"] == "final_audio" {
				foundAudio = asset["drive_file_id"] == "audio-drive-id" && asset["sha256"] == strings.Repeat("a", 64)
			}
		}
		if !foundAudio {
			t.Fatalf("runtime_assets = %#v, missing the certified Drive final audio asset", assets)
		}
	}
}

func TestBuildFinalJobPayloadsRejectsShiftedFinalAudio(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 1_000_000,
			Segments:   []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 1_000_000}},
		},
		FinalAudio: certifiedFinalAudio(1000),
		Scenes:     []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
	}
	result.FinalAudio.StartPTS = 1
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "start_pts is 1, want 0") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want non-zero final-audio PTS rejection", err)
	}
}

func TestBuildFinalJobPayloadsSendsDriveStockAndPublishedOverlaysToWorker(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 5_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 5_000_000}}},
		FinalAudio:        certifiedFinalAudio(5000),
		Scenes:            []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "run-1",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			Items: []capabilityoverlay.OverlayItem{{ID: "phrase-overlay", SceneID: "scene-1", TemplateID: "phrase", StartMs: 100, EndMs: 4500, Text: "hello"}},
		},
		OverlayRender: &RenderReference{Items: []OverlayItemRenderReference{{ItemID: "phrase-overlay", Artifact: &RenderArtifact{ID: "overlay-1", DriveFileID: "overlay-drive-1", SHA256: strings.Repeat("d", 64), SizeBytes: 50, CopyEligible: true}}}},
	}
	pre, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	scenes := pre["scenes"].([]map[string]any)
	if len(scenes) != 3 {
		t.Fatalf("scenes = %d, want three timing chunks", len(scenes))
	}
	for _, scene := range scenes {
		stock := scene["stock"].(map[string]any)
		if stock["drive_file_id"] != "drive-asset" {
			t.Fatalf("scene stock = %#v, want source Drive asset", stock)
		}
		if _, hasClip := scene["clip"]; hasClip {
			t.Fatalf("scene %#v also has clip; source audio would be mixed twice", scene)
		}
	}
	overlays := finalize["overlays"].([]any)
	if len(overlays) != 1 {
		t.Fatalf("finalize overlays = %d, want one Drive overlay reference", len(overlays))
	}
	overlay := overlays[0].(map[string]any)
	if overlay["drive_file_id"] != "overlay-drive-1" || overlay["url"] != "velox-drive://overlay-drive-1" || overlay["size_bytes"] != int64(50) || overlay["start_frame"] != int64(3) || overlay["end_frame"] != int64(108) || overlay["frame_count"] != int64(105) || overlay["mode"] != "replace" {
		t.Fatalf("overlay payload = %#v, want remote Drive overlay on the frame grid", overlay)
	}
	for _, raw := range finalize["runtime_assets"].([]any) {
		if raw.(map[string]any)["role"] == "final_composite" {
			t.Fatal("payload contains locally precomposed stock media")
		}
	}
}

func TestFinalJobIdempotencyKeyStableAcrossLocalRunRetries(t *testing.T) {
	first := finalJobIdempotencyKey("milton-r8-request", "run-attempt-1")
	retry := finalJobIdempotencyKey("milton-r8-request", "run-attempt-2")
	if first != retry {
		t.Fatalf("retry idempotency key changed: first=%q retry=%q", first, retry)
	}
	if !strings.HasPrefix(first, "creator-77-request-") {
		t.Fatalf("request idempotency key = %q, want creator-77-request- prefix", first)
	}
	if other := finalJobIdempotencyKey("milton-r9-request", "run-attempt-2"); other == first {
		t.Fatalf("different request keys collided: %q", other)
	}
	if fallback := finalJobIdempotencyKey("", "run-attempt-2"); fallback != "creator-77-run-attempt-2" {
		t.Fatalf("empty request-key fallback = %q", fallback)
	}
}

func TestFinalJobRemoteAssetReferencesOmitWorkerLocalPaths(t *testing.T) {
	ref := map[string]any{
		"asset_id": "stock-1", "drive_file_id": "drive-1",
		"url": "velox-drive://drive-1", "sha256": strings.Repeat("b", 64),
		"local_path": "/tmp/worker-only.mp4",
	}
	scene := compositeVideoScene("scene-1", 0, "stock", ref, 5000, "scene")
	stock := scene["stock"].(map[string]any)
	if _, ok := stock["local_path"]; ok {
		t.Fatalf("remote scene stock contains worker-local path: %#v", stock)
	}
	if stock["url"] != "velox-drive://drive-1" {
		t.Fatalf("remote scene stock URL = %v, want its direct Drive link", stock["url"])
	}
	var runtimeAssets []any
	appendFinalJobRuntimeAsset(&runtimeAssets, map[string]bool{}, "stock", ref)
	asset := runtimeAssets[0].(map[string]any)
	if _, ok := asset["local_path"]; ok {
		t.Fatalf("remote runtime asset contains worker-local path: %#v", asset)
	}
	if asset["url"] != "velox-drive://drive-1" {
		t.Fatalf("remote runtime asset URL = %v, want its direct Drive link", asset["url"])
	}
}

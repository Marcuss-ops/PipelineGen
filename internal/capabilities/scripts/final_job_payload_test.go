package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type finalJobPayloadResolver struct{}

func (finalJobPayloadResolver) ResolveFinalJobAsset(context.Context, string) (map[string]any, error) {
	return map[string]any{"asset_id": "drive-asset", "drive_file_id": "drive-asset", "url": "velox-drive://drive-asset", "sha256": strings.Repeat("b", 64), "size_bytes": int64(20), "duration_ms": int64(2000)}, nil
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

func (finalJobPayloadResolver) ListFinalJobStockFolder(context.Context, string) ([]FinalJobStockFile, error) {
	return []FinalJobStockFile{{ID: "drive-asset", Name: "stock.mp4"}}, nil
}

func (finalJobPayloadResolver) FinalJobPublishedFileSize(context.Context, string) (int64, error) {
	return 7_000_000, nil
}

func TestFinalJobAudioInputKeepsVoiceoverAndDropsClipAudio(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: "stock-clip"},
		AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioClip, ClipAssetID: "stock-clip"}},
		Voiceover:    map[Language]AudioReference{"pt": {ID: "vo-pt", FilePath: "/tmp/vo.mp3"}},
	}}}
	compiled := finalJobAudioInput(result, "pt")
	intents := compiled.Scenes[0].AudioIntents
	if len(intents) != 1 || intents[0].Mode != capabilityaudio.AudioVoiceover || intents[0].VoiceoverAssetID != "vo-pt" {
		t.Fatalf("final-job audio intents = %#v, want only the source-language voiceover", intents)
	}
	if result.Scenes[0].Audio.Mode != capabilityaudio.AudioClip {
		t.Fatal("finalJobAudioInput mutated the original scene")
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
			DurationUS: 12_000_000,
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-1", DurationUS: 12_000_000,
				Video:        capabilityaudio.VideoSegment{AssetID: "yt_source_clip"},
				AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioClip, ClipAssetID: "yt_source_clip", SourceDurationUS: 12_000_000, UseOriginalAudio: true}},
			}},
		},
		FinalAudio: certifiedFinalAudio(12_000),
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
	if !ok || len(scenes) != 1 {
		t.Fatalf("scenes = %#v, want one clip-only scene", pre["scenes"])
	}
	if scenes[0]["text"] != "Dolly lands the hotel joke." {
		t.Errorf("scene text = %v, want the generated scene text", scenes[0]["text"])
	}
	ref, ok := scenes[0]["stock"].(map[string]any)
	if !ok {
		t.Fatalf("scene video ref = %#v, want the silent-video reference block", scenes[0]["stock"])
	}
	if ref["drive_file_id"] != "render-drive-1" || ref["url"] != "https://drive.google.com/file/d/render-drive-1/view?usp=drive_link" || ref["sha256"] != strings.Repeat("d", 64) || ref["size_bytes"] != int64(7_000_000) || ref["duration_ms"] != int64(12_000) {
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
			DurationUS: 5_000_000,
			Segments: []capabilityaudio.TimelineSegment{{
				ID: "scene-intro", DurationUS: 5_000_000, FixedMedia: true,
				AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioClip, ClipAssetID: "intro-source-clip", SourceDurationUS: 5_000_000, UseOriginalAudio: true, ProtectedOriginalAudio: true}},
			}},
		},
		FinalAudio: certifiedFinalAudio(5_000),
		// The fixed section carries no generated scene of its own; the guard
		// only requires the run to have produced scenes at all.
		Scenes: []Scene{{ID: "scene-body", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
		LocalizedRenders: []LocalizedRenderResult{{
			SceneID: "scene-intro", Language: "en", ClipID: "intro-source-clip", Status: "UPLOADED",
			AssetID: "derived-intro-render", DriveFileID: "intro-render-drive", SHA256: strings.Repeat("e", 64), DurationMS: 5_000,
		}},
	}
	pre, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "Dolly", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	scenes := pre["scenes"].([]map[string]any)
	if len(scenes) != 1 {
		t.Fatalf("scenes = %d, want the single fixed-media scene", len(scenes))
	}
	ref := scenes[0]["stock"].(map[string]any)
	if ref["drive_file_id"] != "intro-render-drive" {
		t.Fatalf("fixed-media ref = %#v, want the certified rendered intro clip", ref)
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

func TestBuildFinalJobPayloadsProvidesTextForEveryStockChunk(t *testing.T) {
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
		if scene["text"] == "" {
			t.Errorf("scene %d has empty text", i)
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
	if overlay["drive_file_id"] != "overlay-drive-1" || overlay["url"] != "https://drive.google.com/file/d/overlay-drive-1/view?usp=drive_link" || overlay["size_bytes"] != int64(50) || overlay["start_frame"] != int64(2) || overlay["end_frame"] != int64(108) {
		t.Fatalf("overlay payload = %#v, want remote Drive overlay and timeline window", overlay)
	}
	for _, raw := range finalize["runtime_assets"].([]any) {
		if raw.(map[string]any)["role"] == "final_composite" {
			t.Fatal("payload contains locally precomposed stock media")
		}
	}
}

func TestFinalJobRemoteAssetReferencesOmitWorkerLocalPaths(t *testing.T) {
	ref := map[string]any{
		"asset_id": "stock-1", "drive_file_id": "drive-1",
		"url": "velox-drive://drive-1", "sha256": strings.Repeat("b", 64),
		"local_path": "/tmp/worker-only.mp4",
	}
	scene := compositeStockScene("scene-1", 0, ref, 5000, "scene")
	stock := scene["stock"].(map[string]any)
	if _, ok := stock["local_path"]; ok {
		t.Fatalf("remote scene stock contains worker-local path: %#v", stock)
	}
	if stock["url"] != "https://drive.google.com/file/d/drive-1/view?usp=drive_link" {
		t.Fatalf("remote scene stock URL = %v, want its direct Drive link", stock["url"])
	}
	var runtimeAssets []any
	appendFinalJobRuntimeAsset(&runtimeAssets, map[string]bool{}, "stock", ref)
	asset := runtimeAssets[0].(map[string]any)
	if _, ok := asset["local_path"]; ok {
		t.Fatalf("remote runtime asset contains worker-local path: %#v", asset)
	}
	if asset["url"] != "https://drive.google.com/file/d/drive-1/view?usp=drive_link" {
		t.Fatalf("remote runtime asset URL = %v, want its direct Drive link", asset["url"])
	}
}

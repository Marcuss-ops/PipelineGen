package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

type comedianClipFixture struct {
	id             string
	sourceInUS     int64
	sourceDuration int64
	timelineStart  int64
	duration       int64
}

func TestComediansFullAudioE2ECertification(t *testing.T) {
	fixtures := []comedianClipFixture{
		{id: "clip-a", sourceInUS: 33_200_000, sourceDuration: 5_600_000, timelineStart: 12_400_000, duration: 5_600_000},
		{id: "clip-b", sourceInUS: 7_100_000, sourceDuration: 6_500_000, timelineStart: 22_000_000, duration: 6_500_000},
		{id: "clip-c", sourceInUS: 91_000_000, sourceDuration: 4_000_000, timelineStart: 35_000_000, duration: 4_000_000},
		{id: "clip-d", sourceInUS: 15_500_000, sourceDuration: 6_000_000, timelineStart: 47_000_000, duration: 6_000_000},
	}

	timeline := comediansTimeline(fixtures)
	if err := timeline.Validate(); err != nil {
		t.Fatalf("comedians canonical timeline is invalid: %v", err)
	}

	bgm := []audio.AudioLayer{{
		AssetID:         "bgm-comedy-bed",
		TimelineStartUS: 0,
		DurationUS:      timeline.DurationUS,
		GainDB:          -18,
	}}
	// The explicit Whoop and the five random effects are already resolved
	// before compilation. There is no random operation left in the sealed plan.
	sfx := []audio.AudioLayer{
		{AssetID: "whoop-explicit", TimelineStartUS: 8_400_000, DurationUS: 400_000, GainDB: -4},
		{AssetID: "random-sfx-01", TimelineStartUS: 13_700_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-02", TimelineStartUS: 24_600_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-03", TimelineStartUS: 33_100_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-04", TimelineStartUS: 41_200_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-05", TimelineStartUS: 50_400_000, DurationUS: 350_000, GainDB: -8},
	}
	plan, err := audio.CompileWithLayers(timeline, audio.DefaultAudioProfile(), bgm, sfx, []audio.AudioAutomation{
		{TargetLayer: "BGM", StartUS: 12_400_000, EndUS: 18_000_000, GainDB: -24, AttackUS: 100_000, ReleaseUS: 250_000},
	})
	if err != nil {
		t.Fatalf("compile comedians audio plan: %v", err)
	}
	if len(plan.Events) != 8 || len(plan.BackgroundMusic) != 1 || len(plan.SFX) != 6 || plan.PlanSHA256 == "" {
		t.Fatalf("unexpected full-audio plan shape: events=%d bgm=%d sfx=%d hash=%q", len(plan.Events), len(plan.BackgroundMusic), len(plan.SFX), plan.PlanSHA256)
	}
	if plan.BackgroundMusic[0].AssetID != "bgm-comedy-bed" || plan.BackgroundMusic[0].TimelineStartUS != 0 || plan.BackgroundMusic[0].DurationUS != 53_000_000 {
		t.Fatalf("BGM layer is not canonical: %+v", plan.BackgroundMusic[0])
	}
	wantSFX := []struct {
		id       string
		start    int64
		duration int64
	}{
		{"whoop-explicit", 8_400_000, 400_000},
		{"random-sfx-01", 13_700_000, 350_000},
		{"random-sfx-02", 24_600_000, 350_000},
		{"random-sfx-03", 33_100_000, 350_000},
		{"random-sfx-04", 41_200_000, 350_000},
		{"random-sfx-05", 50_400_000, 350_000},
	}
	for i, want := range wantSFX {
		if plan.SFX[i].AssetID != want.id || plan.SFX[i].TimelineStartUS != want.start || plan.SFX[i].DurationUS != want.duration {
			t.Fatalf("resolved SFX[%d] drifted: got=%+v want=%+v", i, plan.SFX[i], want)
		}
	}
	assertComediansClipSync(t, timeline, plan, fixtures)
	strategy, err := audio.ResolveAudioRenderStrategy(nil, plan)
	if err != nil || strategy != audio.TimelineMix {
		t.Fatalf("pre-render strategy=%q err=%v", strategy, err)
	}

	assetsDir := t.TempDir()
	assets := make(audio.ResolvedAudioAssets, 0, 4+4+1+6)
	for _, id := range []string{"vo-a", "vo-b", "vo-c", "vo-d", "clip-a", "clip-b", "clip-c", "clip-d", "bgm-comedy-bed", "whoop-explicit", "random-sfx-01", "random-sfx-02", "random-sfx-03", "random-sfx-04", "random-sfx-05"} {
		path := filepath.Join(assetsDir, id+".media")
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		assets = append(assets, audio.ResolvedAudioAsset{AssetID: id, Path: path})
	}

	runner := &comediansAudioRunner{audioBytes: []byte("certified final audio bytes")}
	client := NewClient("unused-in-test", "ffmpeg", nil)
	client.runner = runner
	processor := &VideoProcessor{client: client}
	combinedAudio, err := NewCombinedAudioRenderer(processor)
	if err != nil {
		t.Fatal(err)
	}
	finalAudio, metrics, err := combinedAudio.Render(context.Background(), plan, assets)
	if err != nil {
		t.Fatalf("render certified final audio: %v", err)
	}
	if metrics.AudioEncodePasses != 1 {
		t.Fatalf("audio compilation must encode exactly once, got %d passes", metrics.AudioEncodePasses)
	}
	if err := audio.ValidateFinalAudio(audio.FinalAudioAsset{
		AssetID:              finalAudio.AssetID,
		AudioContractVersion: finalAudio.AudioContractVersion,
		AudioPlanVersion:     finalAudio.AudioPlanVersion,
		AudioPlanSHA256:      finalAudio.PlanSHA256,
		FinalAudioSHA256:     finalAudio.FinalAudioSHA256,
		Codec:                finalAudio.Codec,
		Profile:              finalAudio.Profile,
		SampleRate:           finalAudio.SampleRate,
		Channels:             finalAudio.Channels,
		ChannelLayout:        finalAudio.ChannelLayout,
		DurationMS:           finalAudio.DurationMS,
		StartPTS:             finalAudio.StartPTS,
		Bitrate:              finalAudio.Bitrate,
		SizeBytes:            finalAudio.SizeBytes,
		FinalMix:             finalAudio.FinalMix,
		CopyEligible:         finalAudio.CopyEligible,
	}, plan); err != nil {
		t.Fatalf("final audio certification failed: %v", err)
	}
	finalAudioAsset := audio.FinalAudioAsset{
		AssetID:              finalAudio.AssetID,
		AudioContractVersion: finalAudio.AudioContractVersion,
		AudioPlanVersion:     finalAudio.AudioPlanVersion,
		AudioPlanSHA256:      finalAudio.PlanSHA256,
		FinalAudioSHA256:     finalAudio.FinalAudioSHA256,
		Codec:                finalAudio.Codec,
		Profile:              finalAudio.Profile,
		SampleRate:           finalAudio.SampleRate,
		Channels:             finalAudio.Channels,
		ChannelLayout:        finalAudio.ChannelLayout,
		DurationMS:           finalAudio.DurationMS,
		StartPTS:             finalAudio.StartPTS,
		Bitrate:              finalAudio.Bitrate,
		SizeBytes:            finalAudio.SizeBytes,
		FinalMix:             finalAudio.FinalMix,
		CopyEligible:         finalAudio.CopyEligible,
	}
	copyStrategy, err := audio.ResolveAudioRenderStrategy(&finalAudioAsset, plan)
	if err != nil || copyStrategy != audio.FinalAudioCopy {
		t.Fatalf("final audio strategy=%q err=%v, want FINAL_AUDIO_COPY", copyStrategy, err)
	}

	videoPath := filepath.Join(t.TempDir(), "rendered-video.mp4")
	if err := os.WriteFile(videoPath, []byte("certified video bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "final.mp4")
	if err := processor.MuxFinalAudioCopy(context.Background(), videoPath, finalAudio.Path, outputPath, finalAudioAsset); err != nil {
		t.Fatalf("FINAL_AUDIO_COPY mux failed: %v", err)
	}

	if len(runner.calls) != 2 || runner.calls[0].Operation != OperationRenderAudioPlan || runner.calls[1].Operation != OperationMuxAudioCopy {
		t.Fatalf("unexpected executor sequence: %#v", runner.calls)
	}
	if runner.calls[1].AudioPlanBytes != 0 || runner.calls[1].AudioCodec != "" || runner.calls[1].Operation == OperationRenderAudioPlan {
		t.Fatalf("mux path unexpectedly remixed/re-encoded audio: %#v", runner.calls[1])
	}
}

func comediansTimeline(fixtures []comedianClipFixture) audio.CanonicalTimeline {
	segments := make([]audio.TimelineSegment, 0, len(fixtures)*2)
	cursor := int64(0)
	for i, fixture := range fixtures {
		voiceoverDuration := fixture.timelineStart - cursor
		segments = append(segments, audio.TimelineSegment{
			ID:              fmt.Sprintf("voiceover-%c", 'a'+i),
			Index:           len(segments),
			TimelineStartUS: cursor,
			DurationUS:      voiceoverDuration,
			Audio: audio.AudioIntent{
				Mode:             audio.AudioVoiceover,
				VoiceoverAssetID: fmt.Sprintf("vo-%c", 'a'+i),
				SourceDurationUS: voiceoverDuration,
			},
		})
		segments = append(segments, audio.TimelineSegment{
			ID:              fixture.id,
			Index:           len(segments),
			TimelineStartUS: fixture.timelineStart,
			DurationUS:      fixture.duration,
			Video: audio.VideoSegment{
				AssetID:          fixture.id,
				SourceInUS:       fixture.sourceInUS,
				SourceDurationUS: fixture.sourceDuration,
			},
			Audio: audio.AudioIntent{
				Mode:             audio.AudioClip,
				ClipAssetID:      fixture.id,
				SourceInUS:       fixture.sourceInUS,
				SourceDurationUS: fixture.sourceDuration,
				UseOriginalAudio: true,
			},
		})
		cursor = fixture.timelineStart + fixture.duration
	}
	return audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: cursor, Segments: segments}
}

func assertComediansClipSync(t *testing.T, timeline audio.CanonicalTimeline, plan audio.CompiledAudioPlan, fixtures []comedianClipFixture) {
	t.Helper()
	for _, fixture := range fixtures {
		var video, event *audio.TimelineSegment
		for i := range timeline.Segments {
			if timeline.Segments[i].Video.AssetID == fixture.id {
				video = &timeline.Segments[i]
				break
			}
		}
		for i := range timeline.Segments {
			if timeline.Segments[i].Audio.ClipAssetID == fixture.id {
				candidate := timeline.Segments[i]
				event = &candidate
				break
			}
		}
		if video == nil || event == nil {
			t.Fatalf("missing canonical video/audio segment for %s", fixture.id)
		}
		if video.TimelineStartUS != fixture.timelineStart || video.DurationUS != fixture.duration || video.Video.SourceInUS != fixture.sourceInUS || video.Video.SourceDurationUS != fixture.sourceDuration {
			t.Fatalf("video timing drift for %s: %+v", fixture.id, video)
		}
		if event.TimelineStartUS != video.TimelineStartUS || event.DurationUS != video.DurationUS || event.Audio.SourceInUS != video.Video.SourceInUS || event.Audio.SourceDurationUS != video.Video.SourceDurationUS || !event.Audio.UseOriginalAudio {
			t.Fatalf("audio/video sync drift for %s: video=%+v audio=%+v", fixture.id, video, event)
		}
	}
	for _, fixture := range fixtures {
		found := false
		for _, event := range plan.Events {
			if event.Type == audio.EventClip && event.AssetID == fixture.id {
				found = true
				if event.TimelineStartUS != fixture.timelineStart || event.SourceInUS != fixture.sourceInUS || event.SourceDurationUS != fixture.sourceDuration || !event.UseOriginalAudio {
					t.Fatalf("compiled audio event drift for %s: %+v", fixture.id, event)
				}
			}
		}
		if !found {
			t.Fatalf("compiled audio event missing for %s", fixture.id)
		}
	}
	wantVoiceovers := []string{"vo-a", "vo-b", "vo-c", "vo-d"}
	voiceoverIndex := 0
	for _, event := range plan.Events {
		if event.Type != audio.EventVoiceover {
			continue
		}
		if voiceoverIndex >= len(wantVoiceovers) || event.AssetID != wantVoiceovers[voiceoverIndex] {
			t.Fatalf("voiceover event %d drifted: %+v", voiceoverIndex, event)
		}
		voiceoverIndex++
	}
	if voiceoverIndex != len(wantVoiceovers) {
		t.Fatalf("voiceover cardinality = %d, want %d", voiceoverIndex, len(wantVoiceovers))
	}
}

type comedianExecutorCall struct {
	Operation      Operation
	AudioPlanBytes int
	AudioCodec     string
}

type comediansAudioRunner struct {
	calls      []comedianExecutorCall
	audioBytes []byte
}

func (r *comediansAudioRunner) Run(_ context.Context, _ string, input []byte) ([]byte, []byte, error) {
	var req request
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, nil, err
	}
	r.calls = append(r.calls, comedianExecutorCall{Operation: req.Operation, AudioPlanBytes: len(req.AudioPlan), AudioCodec: req.AudioCodec})
	switch req.Operation {
	case OperationRenderAudioPlan:
		var plan audio.CompiledAudioPlan
		if err := json.Unmarshal(req.AudioPlan, &plan); err != nil {
			return nil, nil, fmt.Errorf("decode audio plan in executor: %w", err)
		}
		if err := plan.Validate(); err != nil {
			return nil, nil, fmt.Errorf("executor received invalid audio plan: %w", err)
		}
		available := make(map[string]struct{}, len(req.AudioAssets))
		for _, asset := range req.AudioAssets {
			if asset.AssetID == "" || asset.Path == "" {
				return nil, nil, fmt.Errorf("executor received unresolved audio asset: %+v", asset)
			}
			available[asset.AssetID] = struct{}{}
		}
		for _, event := range plan.Events {
			if event.Type == audio.EventSilence {
				continue
			}
			if _, ok := available[event.AssetID]; !ok {
				return nil, nil, fmt.Errorf("executor is missing primary audio asset %q", event.AssetID)
			}
			if event.Type == audio.EventClip && !event.UseOriginalAudio {
				return nil, nil, fmt.Errorf("executor received a clip event without original audio")
			}
		}
		for _, layer := range append(append([]audio.AudioLayer{}, plan.BackgroundMusic...), plan.SFX...) {
			if _, ok := available[layer.AssetID]; !ok {
				return nil, nil, fmt.Errorf("executor is missing layer asset %q", layer.AssetID)
			}
		}
		if err := os.WriteFile(req.OutputPath, r.audioBytes, 0o600); err != nil {
			return nil, nil, err
		}
		return []byte(`{"ok":true,"operation":"render_audio_plan","metadata":{"duration_sec":53,"bitrate":128000,"audio_codec":"aac","audio_profile":"LC","sample_rate":48000,"channels":2,"start_pts":0,"has_audio":true}}`), nil, nil
	case OperationMuxAudioCopy:
		return []byte(`{"ok":true,"operation":"mux_audio_copy"}`), nil, nil
	default:
		return nil, nil, fmt.Errorf("unexpected executor operation %q", req.Operation)
	}
}

var _ commandRunner = (*comediansAudioRunner)(nil)

package rustexec

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		{id: "clip-a", sourceInUS: 33_200_000, sourceDuration: 5_600_000, timelineStart: 0, duration: 5_600_000},
		{id: "clip-b", sourceInUS: 7_100_000, sourceDuration: 6_500_000, timelineStart: 5_600_000, duration: 6_500_000},
		{id: "clip-c", sourceInUS: 91_000_000, sourceDuration: 4_000_000, timelineStart: 12_100_000, duration: 4_000_000},
		{id: "clip-d", sourceInUS: 15_500_000, sourceDuration: 6_000_000, timelineStart: 16_100_000, duration: 6_000_000},
	}

	timeline := comediansTimeline(fixtures)
	if err := timeline.Validate(); err != nil {
		t.Fatalf("comedians canonical timeline is invalid: %v", err)
	}

	bgmLayers := []audio.AudioLayer{{
		AssetID:         "bgm-comedy-bed",
		TimelineStartUS: 0,
		DurationUS:      timeline.DurationUS,
		GainDB:          -18,
	}}
	// The explicit Whoop and the five random effects are already resolved
	// before compilation. There is no random operation left in the sealed plan.
	sfx := []audio.AudioLayer{
		{AssetID: "whoop-explicit", TimelineStartUS: 1_000_000, DurationUS: 400_000, GainDB: -4},
		{AssetID: "random-sfx-01", TimelineStartUS: 7_000_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-02", TimelineStartUS: 10_000_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-03", TimelineStartUS: 14_000_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-04", TimelineStartUS: 19_000_000, DurationUS: 350_000, GainDB: -8},
		{AssetID: "random-sfx-05", TimelineStartUS: 21_000_000, DurationUS: 350_000, GainDB: -8},
	}
	plan, err := audio.CompileWithLayers(timeline, audio.DefaultAudioProfile(), bgmLayers, sfx, []audio.AudioAutomation{
		{TargetTrackID: "bgm", StartUS: 0, EndUS: 5_600_000, GainDB: -24, AttackUS: 100_000, ReleaseUS: 250_000},
	})
	if err != nil {
		t.Fatalf("compile comedians audio plan: %v", err)
	}
	if len(timeline.Segments) != 4 || len(trackEvents(plan, audio.TrackVoiceover)) != 4 || len(trackEvents(plan, audio.TrackClipAudio)) != 4 || len(trackEvents(plan, audio.TrackBGM)) != 1 || len(trackEvents(plan, audio.TrackSFX)) != 6 || plan.PlanSHA256 == "" {
		t.Fatalf("unexpected full-audio plan shape: segments=%d vo=%d clip=%d bgm=%d sfx=%d hash=%q", len(timeline.Segments), len(trackEvents(plan, audio.TrackVoiceover)), len(trackEvents(plan, audio.TrackClipAudio)), len(trackEvents(plan, audio.TrackBGM)), len(trackEvents(plan, audio.TrackSFX)), plan.PlanSHA256)
	}
	if len(plan.Tracks[0].Events) != 4 || len(plan.Tracks[1].Events) != 4 || plan.Tracks[0].Role != audio.TrackVoiceover || plan.Tracks[1].Role != audio.TrackClipAudio {
		t.Fatalf("combined scene audio was not compiled into simultaneous primary tracks: %#v", plan.Tracks[:2])
	}
	bgm := trackEvents(plan, audio.TrackBGM)[0]
	if bgm.AssetID != "bgm-comedy-bed" || bgm.TimelineStartUS != 0 || bgm.DurationUS != timeline.DurationUS {
		t.Fatalf("BGM track is not canonical: %+v", bgm)
	}
	wantSFX := []struct {
		id       string
		start    int64
		duration int64
	}{
		{"whoop-explicit", 1_000_000, 400_000},
		{"random-sfx-01", 7_000_000, 350_000},
		{"random-sfx-02", 10_000_000, 350_000},
		{"random-sfx-03", 14_000_000, 350_000},
		{"random-sfx-04", 19_000_000, 350_000},
		{"random-sfx-05", 21_000_000, 350_000},
	}
	for i, want := range wantSFX {
		event := trackEvents(plan, audio.TrackSFX)[i]
		if event.AssetID != want.id || event.TimelineStartUS != want.start || event.DurationUS != want.duration {
			t.Fatalf("resolved SFX[%d] drifted: got=%+v want=%+v", i, event, want)
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
	if metrics.MixMS != 4120 || metrics.AACEncodeMS != 7130 || metrics.ProbeMS != 281 || metrics.HashMS != 144 {
		t.Fatalf("rust sub-timings not mapped to canonical metrics: %+v", metrics)
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

func trackEvents(plan audio.CompiledAudioPlan, role audio.AudioTrackRole) []audio.AudioEvent {
	var events []audio.AudioEvent
	for _, track := range plan.Tracks {
		if track.Role == role {
			events = append(events, track.Events...)
		}
	}
	return events
}

func allTrackEvents(plan audio.CompiledAudioPlan) []audio.AudioEvent {
	var events []audio.AudioEvent
	for _, track := range plan.Tracks {
		events = append(events, track.Events...)
	}
	return events
}

func comediansTimeline(fixtures []comedianClipFixture) audio.CanonicalTimeline {
	segments := make([]audio.TimelineSegment, 0, len(fixtures))
	for i, fixture := range fixtures {
		voiceover := audio.AudioIntent{Mode: audio.AudioVoiceover, VoiceoverAssetID: fmt.Sprintf("vo-%c", 'a'+i), SourceDurationUS: fixture.duration}
		clipAudio := audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: fixture.id, SourceInUS: fixture.sourceInUS, SourceDurationUS: fixture.sourceDuration, UseOriginalAudio: true}
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
			Audio:        voiceover,
			AudioIntents: []audio.AudioIntent{voiceover, clipAudio},
		})
	}
	return audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: 22_100_000, Segments: segments}
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
			for _, intent := range timeline.Segments[i].EffectiveAudioIntents() {
				if intent.Mode == audio.AudioClip && intent.ClipAssetID == fixture.id {
					candidate := timeline.Segments[i]
					event = &candidate
					break
				}
			}
			if event != nil {
				break
			}
		}
		if video == nil || event == nil {
			t.Fatalf("missing canonical video/audio segment for %s", fixture.id)
		}
		if video.TimelineStartUS != fixture.timelineStart || video.DurationUS != fixture.duration || video.Video.SourceInUS != fixture.sourceInUS || video.Video.SourceDurationUS != fixture.sourceDuration {
			t.Fatalf("video timing drift for %s: %+v", fixture.id, video)
		}
		var clipIntent audio.AudioIntent
		for _, intent := range event.EffectiveAudioIntents() {
			if intent.Mode == audio.AudioClip {
				clipIntent = intent
				break
			}
		}
		if event.TimelineStartUS != video.TimelineStartUS || event.DurationUS != video.DurationUS || clipIntent.SourceInUS != video.Video.SourceInUS || clipIntent.SourceDurationUS != video.Video.SourceDurationUS || !clipIntent.UseOriginalAudio {
			t.Fatalf("audio/video sync drift for %s: video=%+v audio=%+v", fixture.id, video, event)
		}
	}
	for _, fixture := range fixtures {
		found := false
		for _, event := range allTrackEvents(plan) {
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
	for _, event := range allTrackEvents(plan) {
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
		for _, event := range allTrackEvents(plan) {
			if event.Type == audio.EventBGM || event.Type == audio.EventSFX {
				if _, ok := available[event.AssetID]; !ok {
					return nil, nil, fmt.Errorf("executor is missing layer asset %q", event.AssetID)
				}
			}
		}
		if err := os.WriteFile(req.OutputPath, r.audioBytes, 0o600); err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(r.audioBytes)
		return []byte(fmt.Sprintf(`{"ok":true,"operation":"render_audio_plan","metadata":{"duration_sec":22.1,"bitrate":128000,"audio_codec":"aac","audio_profile":"LC","sample_rate":48000,"channels":2,"start_pts":0,"has_audio":true,"mix_ms":4120,"aac_encode_ms":7130,"probe_ms":281,"hash_ms":144,"final_audio_sha256":"%s"}}`, hex.EncodeToString(sum[:]))), nil, nil
	case OperationMuxAudioCopy:
		return []byte(`{"ok":true,"operation":"mux_audio_copy"}`), nil, nil
	default:
		return nil, nil, fmt.Errorf("unexpected executor operation %q", req.Operation)
	}
}

var _ commandRunner = (*comediansAudioRunner)(nil)

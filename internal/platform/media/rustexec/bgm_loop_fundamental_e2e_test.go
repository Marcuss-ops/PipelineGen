// Package rustexec — bgm_loop_fundamental_e2e_test.go is the plan's
// fundamental certification: video 75s, BGM 20s loop, 3 SFX, voiceover
// preserved, BGM ducking active, one mastered AAC-LC 48kHz stereo file of
// exactly 75s.
//
// The plan is compiled through the canonical orchestrator
// (scriptgeneration.CompileAudioWithIntents → audio.CompileWithLayers) so
// Go decides every timing fact — including the loop expansion — and the
// real Rust/FFmpeg renderer only executes the sealed plan. The mastered
// file is then probed and analysed by frequency band: the loop restart, the
// truncated last BGM event, the ducking under voiceover, and the three SFX
// placements are all certified objectively, not just asserted on the JSON.
package rustexec

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// mapAudioAssetSource is the in-test AudioAssetSource: asset_id → verified
// path + certified duration. It mirrors the composition-root adapter's
// contract (the capability never sees Drive or the filesystem).
type mapAudioAssetSource struct {
	assets map[string]audio.ResolvedAudioAsset
}

func (s mapAudioAssetSource) ResolveAudioAsset(_ context.Context, assetID string) (audio.ResolvedAudioAsset, error) {
	asset, ok := s.assets[assetID]
	if !ok {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("unknown audio asset %q", assetID)
	}
	return asset, nil
}

var _ scripts.AudioAssetSource = mapAudioAssetSource{}

// fundamentalTimeline is the 75s three-scene timeline (25s each) whose
// certified voiceover speech windows are [0,20s), [25s,47s) and [50s,68s).
func fundamentalTimeline(voDurations map[string]int64) audio.CanonicalTimeline {
	windowDurations := []struct {
		id        string
		voAssetID string
		start     int64
	}{
		{"scene-1", "vo-1", 0},
		{"scene-2", "vo-2", 25_000_000},
		{"scene-3", "vo-3", 50_000_000},
	}
	segments := make([]audio.TimelineSegment, 0, len(windowDurations))
	for i, w := range windowDurations {
		segments = append(segments, audio.TimelineSegment{
			ID:              w.id,
			Index:           i,
			TimelineStartUS: w.start,
			DurationUS:      25_000_000,
			AudioIntents: []audio.AudioIntent{{
				Mode:               audio.AudioVoiceover,
				VoiceoverAssetID:   w.voAssetID,
				SourceDurationUS:   voDurations[w.voAssetID],
				TimelineOffsetUS:   0,
				TimelineDurationUS: 25_000_000,
			}},
		})
	}
	return audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: 75_000_000, Segments: segments}
}

// fundamentalBGM is the plan's canonical BGM intent: 20s asset, looped over
// the whole video, with fades and ducking under voiceover.
func fundamentalBGM() []scriptpkg.BackgroundMusicIntent {
	return []scriptpkg.BackgroundMusicIntent{{
		AssetID:            "bgm_20s",
		StartMS:            0,
		End:                &scriptpkg.AudioTimelineEnd{VideoEnd: true},
		Loop:               true,
		GainDB:             -24,
		FadeInMS:           1000,
		FadeOutMS:          1800,
		DuckUnderVoiceover: true,
		DuckGainDB:         -30,
		DuckAttackMS:       120,
		DuckReleaseMS:      350,
	}}
}

// fundamentalSFX are the three effects of the fundamental test: 12s, 31s
// and 69s, the first one trimmed inside its source.
func fundamentalSFX() []scriptpkg.SoundEffectIntent {
	return []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx_whoosh", AtMS: 12_000, SourceInMS: 250, DurationMS: 900, GainDB: -8},
		{AssetID: "sfx_impact", AtMS: 31_000, GainDB: -5},
		{AssetID: "sfx_boom", AtMS: 69_000, GainDB: -3},
	}
}

// probeAudioStreamField reads one stream field from the mastered file with
// ffprobe (e.g. codec_name, profile, sample_rate, channels).
func probeAudioStreamField(t *testing.T, ffprobe, path, key string) string {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream="+key, "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe %s field %s: %v: %s", path, key, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestFundamental75sBGMLoopRealRenderCertification runs the plan's
// fundamental test against the real Rust/FFmpeg renderer: video 75s, BGM
// 20s loop → 4 deterministic events (0-20, 20-40, 40-60, 60-75), 3 SFX at
// 12s/31s/69s, voiceover preserved, BGM ducked under voiceover, and one
// mastered AAC-LC 48kHz stereo file of exactly 75s.
func TestFundamental75sBGMLoopRealRenderCertification(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	// ── 1. Real assets: distinct tones so the mastered file can be
	// certified by frequency band. The bands are chosen with at least ~3
	// octaves of separation so the 2nd-order bandpass analysis filters
	// isolate each source (a 0dB 1000Hz voiceover leaks ~-16dB into a
	// [200,400]Hz band, which would swamp the ducked BGM — the 60Hz BGM
	// keeps the ducking window measurable). BGM 60Hz (looped), VO 1000Hz,
	// SFX 2000/2500/8000Hz.
	assetsDir := t.TempDir()
	bgmPath := filepath.Join(assetsDir, "bgm_20s.wav")
	generateToneAsset(t, ffmpegPath, bgmPath, 60, 20)

	voSpecs := []struct {
		id        string
		durationS float64
	}{
		{"vo-1", 20},
		{"vo-2", 22},
		{"vo-3", 18},
	}
	voPaths := make(map[string]string, len(voSpecs))
	voDurations := make(map[string]int64, len(voSpecs))
	for _, spec := range voSpecs {
		path := filepath.Join(assetsDir, spec.id+".wav")
		generateToneAsset(t, ffmpegPath, path, 1000, spec.durationS)
		voPaths[spec.id] = path
		voDurations[spec.id] = probeDurationUS(t, ffprobePath, path)
	}

	sfxSpecs := []struct {
		id        string
		freq      float64
		durationS float64
	}{
		{"sfx_whoosh", 2000, 2}, // must be >= source_in 250ms + duration 900ms
		{"sfx_impact", 2500, 0.5},
		{"sfx_boom", 8000, 0.3},
	}
	sfxPaths := make(map[string]string, len(sfxSpecs))
	sfxDurations := make(map[string]int64, len(sfxSpecs))
	for _, spec := range sfxSpecs {
		path := filepath.Join(assetsDir, spec.id+".wav")
		generateToneAsset(t, ffmpegPath, path, spec.freq, spec.durationS)
		sfxPaths[spec.id] = path
		sfxDurations[spec.id] = probeDurationUS(t, ffprobePath, path)
	}

	// ── 2. Canonical compile through the orchestrator: Go decides the
	// loop expansion, SFX placement, fades and ducking — Rust only
	// executes the sealed plan.
	timeline := fundamentalTimeline(voDurations)
	if err := timeline.Validate(); err != nil {
		t.Fatalf("fundamental canonical timeline is invalid: %v", err)
	}
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm_20s":    {AssetID: "bgm_20s", Path: bgmPath, DurationUS: probeDurationUS(t, ffprobePath, bgmPath)},
		"sfx_whoosh": {AssetID: "sfx_whoosh", Path: sfxPaths["sfx_whoosh"], DurationUS: sfxDurations["sfx_whoosh"]},
		"sfx_impact": {AssetID: "sfx_impact", Path: sfxPaths["sfx_impact"], DurationUS: sfxDurations["sfx_impact"]},
		"sfx_boom":   {AssetID: "sfx_boom", Path: sfxPaths["sfx_boom"], DurationUS: sfxDurations["sfx_boom"]},
	}}
	result, err := scripts.CompileAudioWithIntents(context.Background(), timeline, audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", fundamentalBGM(), fundamentalSFX(), source)
	if err != nil {
		t.Fatalf("compile fundamental audio intents: %v", err)
	}
	plan := result.Plan

	// BGM: exactly 4 deterministic loop events, the last truncated on the
	// 75s window — Rust never invents a loop.
	bgm := trackEvents(plan, audio.TrackBGM)
	wantBGM := []audio.AudioEvent{
		{EventID: "bgm-0", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 0, DurationUS: 20_000_000, SourceInUS: 0, SourceDurationUS: 20_000_000, GainDB: -24},
		{EventID: "bgm-1", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 20_000_000, DurationUS: 20_000_000, SourceInUS: 0, SourceDurationUS: 20_000_000, GainDB: -24},
		{EventID: "bgm-2", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 40_000_000, DurationUS: 20_000_000, SourceInUS: 0, SourceDurationUS: 20_000_000, GainDB: -24},
		{EventID: "bgm-3", Type: audio.EventBGM, AssetID: "bgm_20s", TimelineStartUS: 60_000_000, DurationUS: 15_000_000, SourceInUS: 0, SourceDurationUS: 15_000_000, GainDB: -24},
	}
	if len(bgm) != len(wantBGM) {
		t.Fatalf("bgm events = %+v, want %d loop events", bgm, len(wantBGM))
	}
	for i := range wantBGM {
		if bgm[i] != wantBGM[i] {
			t.Fatalf("bgm[%d] = %+v\nwant     %+v", i, bgm[i], wantBGM[i])
		}
	}

	// SFX: whoosh with trims, impact/boom sized from their sources.
	sfx := trackEvents(plan, audio.TrackSFX)
	wantSFX := []audio.AudioEvent{
		{EventID: "sfx-0", Type: audio.EventSFX, AssetID: "sfx_whoosh", TimelineStartUS: 12_000_000, DurationUS: 900_000, SourceInUS: 250_000, SourceDurationUS: 900_000, GainDB: -8},
		{EventID: "sfx-1", Type: audio.EventSFX, AssetID: "sfx_impact", TimelineStartUS: 31_000_000, DurationUS: 500_000, SourceInUS: 0, SourceDurationUS: 500_000, GainDB: -5},
		{EventID: "sfx-2", Type: audio.EventSFX, AssetID: "sfx_boom", TimelineStartUS: 69_000_000, DurationUS: 300_000, SourceInUS: 0, SourceDurationUS: 300_000, GainDB: -3},
	}
	if len(sfx) != len(wantSFX) {
		t.Fatalf("sfx events = %+v, want %d", sfx, len(wantSFX))
	}
	for i := range wantSFX {
		if sfx[i] != wantSFX[i] {
			t.Fatalf("sfx[%d] = %+v\nwant    %+v", i, sfx[i], wantSFX[i])
		}
	}

	// Voiceover preserved: one event per scene.
	vo := trackEvents(plan, audio.TrackVoiceover)
	if len(vo) != 3 {
		t.Fatalf("voiceover events = %d, want 3 (voiceover must be preserved)", len(vo))
	}

	// Ducking + fades on the canonical bgm track.
	if len(plan.Automation) != 4 {
		t.Fatalf("automation = %+v, want 1 fade + 3 duck entries", plan.Automation)
	}
	fade := plan.Automation[0]
	if fade.TargetTrackID != "bgm" || fade.StartUS != 0 || fade.EndUS != 75_000_000 || fade.AttackUS != 1_000_000 || fade.ReleaseUS != 1_800_000 {
		t.Fatalf("fade automation = %+v", fade)
	}
	wantDuckWindows := [][2]int64{{0, 20_000_000}, {25_000_000, 47_000_000}, {50_000_000, 68_000_000}}
	for i, w := range wantDuckWindows {
		d := plan.Automation[i+1]
		if d.TargetTrackID != "bgm" || d.TriggerTrackID != "voiceover" || d.StartUS != w[0] || d.EndUS != w[1] || d.GainDB != -30 {
			t.Fatalf("duck automation[%d] = %+v, want window [%d,%d) at -30dB", i, d, w[0], w[1])
		}
	}

	// Sealed 75s plan with the canonical policy.
	if plan.DurationUS != 75_000_000 || plan.MixPolicy != audio.MixVoiceoverWithDuckedClip || plan.PlanSHA256 == "" {
		t.Fatalf("plan contract broken: duration=%d policy=%q sealed=%v", plan.DurationUS, plan.MixPolicy, plan.PlanSHA256 != "")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan is invalid: %v", err)
	}

	// ── 3. Real render: one Rust render_audio_plan pass, one AAC master.
	renderAssets := result.Assets
	for _, id := range []string{"vo-1", "vo-2", "vo-3"} {
		renderAssets = append(renderAssets, audio.ResolvedAudioAsset{AssetID: id, Path: voPaths[id]})
	}
	finalAudioPath := renderRealAudio(t, musclesPath, ffmpegPath, plan, renderAssets)

	// ── 4. Master certification: exactly 75s, AAC-LC, 48kHz, stereo.
	masterDuration := probeDurationUS(t, ffprobePath, finalAudioPath)
	if masterDuration < 74_900_000 || masterDuration > 75_100_000 {
		t.Fatalf("master duration = %dus, want exactly ~75s", masterDuration)
	}
	if got := probeAudioStreamField(t, ffprobePath, finalAudioPath, "codec_name"); got != "aac" {
		t.Fatalf("master codec = %q, want aac", got)
	}
	if got := probeAudioStreamField(t, ffprobePath, finalAudioPath, "profile"); got != "LC" {
		t.Fatalf("master profile = %q, want LC", got)
	}
	if got := probeAudioStreamField(t, ffprobePath, finalAudioPath, "sample_rate"); got != "48000" {
		t.Fatalf("master sample rate = %q, want 48000", got)
	}
	if got := probeAudioStreamField(t, ffprobePath, finalAudioPath, "channels"); got != "2" {
		t.Fatalf("master channels = %q, want 2 (stereo)", got)
	}

	// ── 5. Frequency certification on the mastered file.
	// Voiceover: present while speaking, absent between/after speech.
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 0, 20, 800, 1200); got <= -30 {
		t.Fatalf("voiceover tone missing in [0,20): %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, finalAudioPath, 20, 25, 800, 1200); got >= -25 {
		t.Fatalf("voiceover must be absent in [20,25): mean %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 25, 47, 800, 1200); got <= -30 {
		t.Fatalf("voiceover tone missing in [25,47): %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, finalAudioPath, 47, 50, 800, 1200); got >= -25 {
		t.Fatalf("voiceover must be absent in [47,50): mean %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 50, 68, 800, 1200); got <= -30 {
		t.Fatalf("voiceover tone missing in [50,68): %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, finalAudioPath, 68, 75, 800, 1200); got >= -25 {
		t.Fatalf("voiceover must be absent in [68,75): mean %.2f dB", got)
	}

	// BGM loop: the 20s source restarts at 20s and 40s (events 2 and 3)
	// and the truncated last event still covers [60,75) — the music is
	// present all the way to the video end, with no brutal cut.
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 20, 25, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 2 missing in [20,25): %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 40, 45, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 3 missing in [40,45): %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 70, 75, 30, 90); got <= -40 {
		t.Fatalf("bgm loop event 4 (truncated tail) missing in [70,75): %.2f dB", got)
	}

	// BGM ducking: during the voiceover the music sits at -30dB (ducked),
	// between scenes it returns to its -24dB base. The 60Hz band is chosen
	// ~4 octaves below the 1000Hz voiceover so the narration's bandpass
	// leakage (~-42dB) stays well under the ducked level and the duck is
	// objectively measurable: the BGM band must be quieter while the
	// narration speaks.
	ducked := bandMeanVolumeDB(t, ffmpegPath, finalAudioPath, 0, 20, 30, 90)
	base := bandMeanVolumeDB(t, ffmpegPath, finalAudioPath, 20, 25, 30, 90)
	if ducked >= base-2 {
		t.Fatalf("bgm ducking not effective: mean during VO %.2f dB vs base %.2f dB", ducked, base)
	}

	// SFX: the three placements are audible in their own bands (the boom
	// sits at 8kHz so it cannot leak into the voiceover's absence window).
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 11.5, 12.5, 1800, 2200); got <= -40 {
		t.Fatalf("whoosh sfx missing at 12s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 30.5, 31.5, 2300, 2700); got <= -40 {
		t.Fatalf("impact sfx missing at 31s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 68.5, 69.5, 7500, 8500); got <= -40 {
		t.Fatalf("boom sfx missing at 69s: %.2f dB", got)
	}
}

// TestFundamentalPlanWireAssets certify the exact asset table the executor
// receives: BGM deduped once across BGM+SFX, three SFX, three voiceovers —
// asset_id → path, nothing else on the wire.
func TestFundamentalPlanWireAssets(t *testing.T) {
	source := mapAudioAssetSource{assets: map[string]audio.ResolvedAudioAsset{
		"bgm_20s":    {AssetID: "bgm_20s", Path: "/m/bgm.m4a", DurationUS: 20_000_000},
		"sfx_whoosh": {AssetID: "sfx_whoosh", Path: "/m/whoosh.m4a", DurationUS: 2_000_000},
		"sfx_impact": {AssetID: "sfx_impact", Path: "/m/impact.m4a", DurationUS: 500_000},
		"sfx_boom":   {AssetID: "sfx_boom", Path: "/m/boom.m4a", DurationUS: 300_000},
	}}
	voDurations := map[string]int64{"vo-1": 20_000_000, "vo-2": 22_000_000, "vo-3": 18_000_000}
	result, err := scripts.CompileAudioWithIntents(context.Background(), fundamentalTimeline(voDurations), audio.DefaultAudioProfile(), "voiceover_with_ducked_clip", fundamentalBGM(), fundamentalSFX(), source)
	if err != nil {
		t.Fatalf("compile fundamental audio intents: %v", err)
	}
	if len(result.Assets) != 4 {
		t.Fatalf("asset table = %+v, want 4 deduped entries (bgm + 3 sfx)", result.Assets)
	}
	for _, asset := range result.Assets {
		if asset.AssetID == "" || asset.Path == "" || asset.DurationUS <= 0 {
			t.Fatalf("unresolved asset on the wire: %+v", asset)
		}
	}
	seen := map[string]bool{}
	for _, asset := range result.Assets {
		if seen[asset.AssetID] {
			t.Fatalf("duplicate asset id %q in the wire table", asset.AssetID)
		}
		seen[asset.AssetID] = true
	}
	for _, want := range []string{"bgm_20s", "sfx_whoosh", "sfx_impact", "sfx_boom"} {
		if !seen[want] {
			t.Fatalf("wire table missing %q", want)
		}
	}
}

// TestRendererNeverLoopsShortSourceFailsClosed certifies the "Go decide,
// Rust executes" rule at the boundary: when a compiled plan declares ONE
// BGM event whose source range exceeds the real asset (i.e. the Go loop
// expander was NOT run), the Rust renderer fails with "shorter than
// required source range" — it never fabricates a loop to fill the timeline.
func TestRendererNeverLoopsShortSourceFailsClosed(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}

	// A 20s source declared as a single 30s event — the loop was not
	// expanded, so the renderer must refuse rather than repeat the 20s
	// source one-and-a-half times.
	assetsDir := t.TempDir()
	bgmPath := filepath.Join(assetsDir, "bgm_short.wav")
	generateToneAsset(t, ffmpegPath, bgmPath, 300, 20)

	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 30_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 30_000_000,
			AudioIntents: []audio.AudioIntent{{Mode: audio.AudioSilence}},
		}},
	}
	plan, err := audio.CompileWithLayers(timeline, audio.DefaultAudioProfile(),
		[]audio.AudioLayer{{AssetID: "bgm_short", TimelineStartUS: 0, DurationUS: 30_000_000, GainDB: -24}},
		nil, nil)
	if err != nil {
		t.Fatalf("compile short-source plan: %v", err)
	}
	bgm := trackEvents(plan, audio.TrackBGM)
	if len(bgm) != 1 || bgm[0].SourceDurationUS != 30_000_000 {
		t.Fatalf("plan must declare a single 30s event, got %+v", bgm)
	}

	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
	combinedAudio, err := NewCombinedAudioRenderer(processor)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = combinedAudio.Render(context.Background(), plan, audio.ResolvedAudioAssets{
		{AssetID: "bgm_short", Path: bgmPath},
	})
	if err == nil {
		t.Fatal("renderer must fail closed when the source is shorter than the declared event range (it must never invent a loop)")
	}
	if !strings.Contains(err.Error(), "shorter than required source range") {
		t.Fatalf("expected a short-source error, got: %v", err)
	}
}

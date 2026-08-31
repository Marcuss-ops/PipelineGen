package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// prepareClipAudioAssets seals the physical source audio contract before the
// canonical timeline is compiled. VOICEOVER_DUCKED_CLIP cannot be compiled
// from a Drive URL or a semantic clip ID: the renderer needs a verified local
// file. Existing local paths are reused; missing paths are materialized once
// through the optional clip resolver.
func prepareClipAudioAssets(ctx context.Context, result *GenerateResult, source ClipAudioAssetSource, policy capabilityaudio.AudioMixPolicy) (int64, error) {
	if result == nil {
		return 0, nil
	}
	needsFixedAudio := false
	for i := range result.Scenes {
		if result.Scenes[i].ExecutionMode.IsFixedMedia() {
			needsFixedAudio = true
			break
		}
	}
	if policy.Normalize() != capabilityaudio.MixVoiceoverWithDuckedClip && !needsFixedAudio {
		return 0, nil
	}
	started := time.Now()
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*ClipReference{scene.Clip}
		}
		for _, clip := range clips {
			if clip == nil || strings.TrimSpace(clip.ID) == "" || !sceneUsesClipAudio(*scene, clip.ID) {
				continue
			}
			fixed := scene.ExecutionMode.IsFixedMedia()
			if fixed && source == nil {
				return 0, fmt.Errorf("scene %s fixed media clip %s requires an original audio source", scene.ID, clip.ID)
			}
			path := strings.TrimSpace(clip.AudioPath)
			if path == "" {
				path = strings.TrimSpace(clip.Path)
			}
			if path != "" && !fixed {
				if _, err := os.Stat(path); err == nil {
					clip.AudioPath = path
					continue
				}
				// A caller may already have supplied the canonical path while
				// running without a resolver (for example, an audio-only
				// renderer that owns validation). Preserve that explicit path;
				// resolver-backed production flows still materialize and verify
				// missing files below.
				if source == nil {
					clip.AudioPath = path
					continue
				}
			}
			if source == nil {
				return 0, fmt.Errorf("scene %s clip %s requires original audio, but clip materializer is not wired", scene.ID, clip.ID)
			}
			resolved, err := source.ResolveClipAudioAsset(ctx, clip.ID)
			if err != nil {
				return 0, fmt.Errorf("scene %s clip %s audio materialization failed: %w", scene.ID, clip.ID, err)
			}
			if strings.TrimSpace(resolved.Path) == "" {
				return 0, fmt.Errorf("scene %s clip %s audio materialized to an empty path", scene.ID, clip.ID)
			}
			if _, err := os.Stat(resolved.Path); err != nil {
				return 0, fmt.Errorf("scene %s clip %s materialized audio is not readable: %w", scene.ID, clip.ID, err)
			}
			clip.AudioPath = resolved.Path
			if fixed {
				if err := applyFixedPlaybackWindow(scene, clip, resolved.DurationUS); err != nil {
					return 0, fmt.Errorf("scene %s clip %s fixed playback validation failed: %w", scene.ID, clip.ID, err)
				}
			} else if err := clampClipAudioRanges(scene, clip.ID, resolved.DurationUS); err != nil {
				return 0, fmt.Errorf("scene %s clip %s audio duration validation failed: %w", scene.ID, clip.ID, err)
			}
		}
		if scene.ExecutionMode.IsFixedMedia() {
			if err := rebuildFixedMediaTiming(scene); err != nil {
				return 0, fmt.Errorf("scene %s fixed playback timing rebuild failed: %w", scene.ID, err)
			}
		}
	}
	return time.Since(started).Milliseconds(), nil
}

// clampClipAudioRanges reconciles provider metadata with the bytes actually
// materialized. Clip registries can contain rounded/optimistic durations; the
// audio renderer must never be asked for a source range beyond the local file.
func clampClipAudioRanges(scene *Scene, clipID string, durationUS int64) error {
	needsProbe := false
	for _, intent := range scene.AudioIntents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID && intent.SourceDurationUS > 0 {
			needsProbe = true
		}
	}
	if scene.Audio.Mode == capabilityaudio.AudioClip && scene.Audio.ClipAssetID == clipID && scene.Audio.SourceDurationUS > 0 {
		needsProbe = true
	}
	if !needsProbe {
		return nil
	}
	if durationUS <= 0 {
		return nil
	}
	clamp := func(intent *capabilityaudio.AudioIntent) {
		if intent == nil || intent.Mode != capabilityaudio.AudioClip || intent.ClipAssetID != clipID {
			return
		}
		available := durationUS - intent.SourceInUS
		if available <= 0 {
			return
		}
		if intent.SourceDurationUS > available {
			intent.SourceDurationUS = available
		}
	}
	for i := range scene.AudioIntents {
		clamp(&scene.AudioIntents[i])
	}
	clamp(&scene.Audio)
	return nil
}

func applyFixedPlaybackWindow(scene *Scene, clip *ClipReference, sourceDurationUS int64) error {
	if scene == nil || clip == nil || scene.FixedPlayback == nil {
		return fmt.Errorf("fixed playback policy is missing")
	}
	if sourceDurationUS <= 0 {
		return fmt.Errorf("original audio duration is unknown")
	}
	playback := scene.FixedPlayback.Normalize()
	startUS := playback.SourceInMS * 1000
	endUS := playback.SourceOutMS * 1000
	if endUS == 0 {
		endUS = sourceDurationUS
	}
	if startUS < 0 || endUS <= startUS || endUS > sourceDurationUS {
		return fmt.Errorf("source window [%d,%d]ms exceeds original audio duration %dms", playback.SourceInMS, playback.SourceOutMS, sourceDurationUS/1000)
	}
	clip.SourceInMS = playback.SourceInMS
	clip.SourceOutMS = endUS / 1000
	for i := range scene.AudioIntents {
		intent := &scene.AudioIntents[i]
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clip.ID {
			intent.SourceInUS = startUS
			intent.SourceDurationUS = endUS - startUS
			intent.TimelineDurationUS = endUS - startUS
		}
	}
	if scene.Audio.Mode == capabilityaudio.AudioClip && scene.Audio.ClipAssetID == clip.ID {
		scene.Audio.SourceInUS = startUS
		scene.Audio.SourceDurationUS = endUS - startUS
		scene.Audio.TimelineDurationUS = endUS - startUS
	}
	// The caller rebuilds the complete ordered projection after every fixed
	// clip has been resolved. Rebuilding here would reject a valid two-clip
	// section while the later clip is still awaiting its certified duration.
	return nil
}

// rebuildFixedMediaTiming seals the ordered fixed-media projection after all
// original-audio durations are known. Every clip keeps the policy's source
// window, while TimelineOffsetUS makes the clips consecutive in declaration
// order and scene duration is the exact sum of their selected windows.
func rebuildFixedMediaTiming(scene *Scene) error {
	if scene == nil || !scene.ExecutionMode.IsFixedMedia() {
		return nil
	}
	clips := scene.Clips
	if len(clips) == 0 && scene.Clip != nil {
		clips = []*ClipReference{scene.Clip}
	}
	if len(clips) == 0 {
		return fmt.Errorf("no fixed-media clips")
	}
	var offsetUS int64
	for _, clip := range clips {
		if clip == nil || strings.TrimSpace(clip.ID) == "" {
			return fmt.Errorf("fixed-media clip id is required")
		}
		if clip.SourceInMS < 0 || clip.SourceOutMS <= clip.SourceInMS {
			return fmt.Errorf("clip %s has no resolved source window", clip.ID)
		}
		windowUS := (clip.SourceOutMS - clip.SourceInMS) * 1000
		matched := false
		for i := range scene.AudioIntents {
			intent := &scene.AudioIntents[i]
			if intent.Mode != capabilityaudio.AudioClip || intent.ClipAssetID != clip.ID {
				continue
			}
			intent.SourceInUS = clip.SourceInMS * 1000
			intent.SourceDurationUS = windowUS
			intent.TimelineOffsetUS = offsetUS
			intent.TimelineDurationUS = windowUS
			intent.UseOriginalAudio = true
			intent.ProtectedOriginalAudio = true
			intent.GainDB = 0
			matched = true
		}
		if !matched {
			return fmt.Errorf("clip %s has no original-audio intent", clip.ID)
		}
		if offsetUS > int64(^uint64(0)>>1)-windowUS {
			return fmt.Errorf("fixed-media duration overflows")
		}
		offsetUS += windowUS
	}
	if offsetUS <= 0 {
		return fmt.Errorf("fixed-media duration is empty")
	}
	scene.DurationUS = offsetUS
	scene.DurationMS = offsetUS / 1000
	if len(scene.AudioIntents) > 0 {
		scene.Audio = scene.AudioIntents[0]
	}
	return nil
}

func sceneUsesClipAudio(scene Scene, clipID string) bool {
	for _, intent := range scene.AudioIntents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID {
			return true
		}
	}
	return scene.Audio.Mode == capabilityaudio.AudioClip && scene.Audio.ClipAssetID == clipID
}

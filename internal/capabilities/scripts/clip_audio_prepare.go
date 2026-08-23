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
	if result == nil || policy.Normalize() != capabilityaudio.MixVoiceoverWithDuckedClip {
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
			path := strings.TrimSpace(clip.AudioPath)
			if path == "" {
				path = strings.TrimSpace(clip.Path)
			}
			if path != "" {
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
			if err := clampClipAudioRanges(scene, clip.ID, resolved.DurationUS); err != nil {
				return 0, fmt.Errorf("scene %s clip %s audio duration validation failed: %w", scene.ID, clip.ID, err)
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

func sceneUsesClipAudio(scene Scene, clipID string) bool {
	for _, intent := range scene.AudioIntents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID {
			return true
		}
	}
	return scene.Audio.Mode == capabilityaudio.AudioClip && scene.Audio.ClipAssetID == clipID
}

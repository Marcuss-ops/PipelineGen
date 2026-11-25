package clips

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// MergeAudioCompileTimings re-asserts the subtimings that the compile
// boundary cannot measure itself.
//
// CompileCanonicalAudioPlanAudioOnly* returns its OWN scriptaudio.AudioCompileTimings
// (it probes the timeline/plan phases it owns). Assigning that value straight
// over the struct also replaces ClipAudioPrepareMS with the compile's probe
// window, which made the reported clip_audio_prepare_ms a near-zero constant
// even when PrepareClipAudioAssets had just spent tens of seconds
// materializing clip audio. The prepare measured here is the OWNER of that
// subtiming, so it is re-applied after the compile returns.
func MergeAudioCompileTimings(compiled scriptaudio.AudioCompileTimings, measuredClipPrepareMS int64) scriptaudio.AudioCompileTimings {
	compiled.ClipAudioPrepareMS = measuredClipPrepareMS
	return compiled
}

// clipAudioPrepareConcurrency bounds the clip-audio materialization pool. The
// resolutions are object-store/network I/O, not CPU work, so the pool is
// deliberately deeper than the former serial loop while still bounded: one
// plan cannot open an unbounded number of streams at once.
const clipAudioPrepareConcurrency = 8

// PrepareClipAudioAssets seals the physical source audio contract before the
// canonical timeline is compiled. VOICEOVER_DUCKED_CLIP cannot be compiled
// from a Drive URL or a semantic clip ID: the renderer needs a verified local
// file. Existing local paths are reused; missing paths are materialized once
// through the optional clip resolver.
//
// Resolution runs as a bounded fan-out. The IDs are known before any TTS
// finishes, so waiting for one clip before starting the next was pure latency:
// every independent clip was serialized behind the previous download. The
// scene/projection mutation below stays single-threaded and in declaration
// order, so the compiled plan is byte-identical to the serial version.
func PrepareClipAudioAssets(ctx context.Context, result *contract.GenerateResult, source contract.ClipAudioAssetSource, policy capabilityaudio.AudioMixPolicy) (int64, error) {
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

	// Collect the clips that actually need the resolver. The serial validation
	// stays here, before any goroutine starts, so wiring errors keep failing
	// before I/O is issued.
	type clipAudioPrepareTask struct {
		scene *contract.Scene
		clip  *contract.ClipReference
		fixed bool
	}
	var tasks []clipAudioPrepareTask
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*contract.ClipReference{scene.Clip}
		}
		for _, clip := range clips {
			if clip == nil || strings.TrimSpace(clip.ID) == "" || !SceneUsesClipAudio(*scene, clip.ID) {
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
			tasks = append(tasks, clipAudioPrepareTask{scene: scene, clip: clip, fixed: fixed})
		}
	}

	if len(tasks) > 0 {
		// Resolve each distinct clip once. A clip referenced by two scenes is
		// one download; the first declaration supplies the error context.
		unique := make([]string, 0, len(tasks))
		firstTask := make(map[string]clipAudioPrepareTask, len(tasks))
		for _, task := range tasks {
			if _, exists := firstTask[task.clip.ID]; exists {
				continue
			}
			firstTask[task.clip.ID] = task
			unique = append(unique, task.clip.ID)
		}

		type clipAudioResolution struct {
			resolved capabilityaudio.ResolvedAudioAsset
			err      error
		}
		resolutions := make([]clipAudioResolution, len(unique))
		slots := make(chan struct{}, clipAudioPrepareConcurrency)
		var wg sync.WaitGroup
		for i, clipID := range unique {
			i, clipID := i, clipID
			wg.Add(1)
			concurrent.SafeGo("prepare-clip-audio-"+clipID, func() {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-ctx.Done():
					resolutions[i].err = ctx.Err()
					return
				}
				resolved, err := source.ResolveClipAudioAsset(ctx, clipID)
				if err != nil {
					task := firstTask[clipID]
					resolutions[i].err = fmt.Errorf("scene %s clip %s audio materialization failed: %w", task.scene.ID, clipID, err)
					return
				}
				if strings.TrimSpace(resolved.Path) == "" {
					task := firstTask[clipID]
					resolutions[i].err = fmt.Errorf("scene %s clip %s audio materialized to an empty path", task.scene.ID, clipID)
					return
				}
				if _, statErr := os.Stat(resolved.Path); statErr != nil {
					task := firstTask[clipID]
					resolutions[i].err = fmt.Errorf("scene %s clip %s materialized audio is not readable: %w", task.scene.ID, clipID, statErr)
					return
				}
				resolutions[i].resolved = resolved
			})
		}
		wg.Wait()
		// Report the first failure in scene/clip declaration order, exactly as
		// the serial loop did, rather than the first goroutine to finish.
		for _, resolution := range resolutions {
			if resolution.err != nil {
				return 0, resolution.err
			}
		}

		resolvedByID := make(map[string]capabilityaudio.ResolvedAudioAsset, len(unique))
		for i, clipID := range unique {
			resolvedByID[clipID] = resolutions[i].resolved
		}
		for _, task := range tasks {
			resolved := resolvedByID[task.clip.ID]
			if strings.TrimSpace(resolved.Path) == "" {
				// A SafeGo panic leaves the slot empty; fail closed instead of
				// writing an unusable path into the plan.
				return 0, fmt.Errorf("scene %s clip %s audio materialized to an empty path", task.scene.ID, task.clip.ID)
			}
			task.clip.AudioPath = resolved.Path
			if task.fixed {
				if err := ApplyFixedPlaybackWindow(task.scene, task.clip, resolved.DurationUS); err != nil {
					return 0, fmt.Errorf("scene %s clip %s fixed playback validation failed: %w", task.scene.ID, task.clip.ID, err)
				}
			} else if err := ClampClipAudioRanges(task.scene, task.clip.ID, resolved.DurationUS); err != nil {
				return 0, fmt.Errorf("scene %s clip %s audio duration validation failed: %w", task.scene.ID, task.clip.ID, err)
			}
		}
	}

	for i := range result.Scenes {
		if result.Scenes[i].ExecutionMode.IsFixedMedia() {
			if err := RebuildFixedMediaTiming(&result.Scenes[i]); err != nil {
				return 0, fmt.Errorf("scene %s fixed playback timing rebuild failed: %w", result.Scenes[i].ID, err)
			}
		}
	}
	return time.Since(started).Milliseconds(), nil
}

// ClampClipAudioRanges reconciles provider metadata with the bytes actually
// materialized. Clip registries can contain rounded/optimistic durations; the
// audio renderer must never be asked for a source range beyond the local file.
func ClampClipAudioRanges(scene *contract.Scene, clipID string, durationUS int64) error {
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

func ApplyFixedPlaybackWindow(scene *contract.Scene, clip *contract.ClipReference, sourceDurationUS int64) error {
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

// RebuildFixedMediaTiming seals the ordered fixed-media projection after all
// original-audio durations are known. Every clip keeps the policy's source
// window, while TimelineOffsetUS makes the clips consecutive in declaration
// order and scene duration is the exact sum of their selected windows.
func RebuildFixedMediaTiming(scene *contract.Scene) error {
	if scene == nil || !scene.ExecutionMode.IsFixedMedia() {
		return nil
	}
	clips := scene.Clips
	if len(clips) == 0 && scene.Clip != nil {
		clips = []*contract.ClipReference{scene.Clip}
	}
	if len(clips) == 0 {
		return fmt.Errorf("no fixed-media clips")
	}
	silent := scene.FixedPlayback != nil && scene.FixedPlayback.Normalize().AudioMode == scriptpkg.FixedPlaybackSilence
	var offsetUS int64
	for _, clip := range clips {
		if clip == nil || strings.TrimSpace(clip.ID) == "" {
			return fmt.Errorf("fixed-media clip id is required")
		}
		if clip.SourceInMS < 0 || clip.SourceOutMS <= clip.SourceInMS {
			return fmt.Errorf("clip %s has no resolved source window", clip.ID)
		}
		windowUS := (clip.SourceOutMS - clip.SourceInMS) * 1000
		if !silent {
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
		}
		if offsetUS > int64(^uint64(0)>>1)-windowUS {
			return fmt.Errorf("fixed-media duration overflows")
		}
		offsetUS += windowUS
	}
	if offsetUS <= 0 {
		return fmt.Errorf("fixed-media duration is empty")
	}
	if silent {
		scene.AudioIntents = []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioSilence, TimelineDurationUS: offsetUS}}
	}
	scene.DurationUS = offsetUS
	scene.DurationMS = offsetUS / 1000
	if len(scene.AudioIntents) > 0 {
		scene.Audio = scene.AudioIntents[0]
	}
	return nil
}

func SceneUsesClipAudio(scene contract.Scene, clipID string) bool {
	for _, intent := range scene.AudioIntents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID {
			return true
		}
	}
	return scene.Audio.Mode == capabilityaudio.AudioClip && scene.Audio.ClipAssetID == clipID
}

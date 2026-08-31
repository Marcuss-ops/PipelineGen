// Package scriptgeneration — media_preflight.go implements the fail-fast
// Media Requirement Preflight that runs after normalize/source resolve in
// parallel with Gemma (scene text generation).
//
// P0.5: after normalize, the preflight verifies all media assets the
// pipeline will need — clip files, original audio streams, BGM/SFX
// assets, Drive folders, watermark assets — BEFORE Gemma and TTS spend
// minutes of work. A preflight failure fails the run immediately at the
// join point, so no LLM/TTS work is wasted on a run that would fail at
// audio compile anyway (e.g. missing clip audio for VOICEOVER_DUCKED_CLIP).
//
// The preflight runs EVERY check in parallel and collects ALL failures,
// so the operator sees the complete picture in one run instead of
// failing → fixing → failing → fixing across N retries.
//
// godlike/07 NO-FAKE-AVAILABILITY: every check is fail-closed. An
// unavailable resolver (nil port) for a required check is itself a
// preflight failure.
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrMediaPreflight identifies a fail-closed media requirement failure.
// Callers can probe it with errors.Is and inspect the structured
// MediaPreflightError with errors.As.
var ErrMediaPreflight = errors.New("scriptgeneration: media preflight failed")

// MediaPreflightError carries the complete structured preflight result.
type MediaPreflightError struct {
	Result PreflightResult
}

func (e *MediaPreflightError) Error() string {
	if e == nil {
		return ErrMediaPreflight.Error()
	}
	if message := e.Result.Error(); message != "" {
		return fmt.Sprintf("%s: %s", ErrMediaPreflight, message)
	}
	return ErrMediaPreflight.Error()
}

func (e *MediaPreflightError) Unwrap() error { return ErrMediaPreflight }

// AsError converts a failed result to the canonical typed error.
func (r PreflightResult) AsError() error {
	if !r.HasFailures() {
		return nil
	}
	return &MediaPreflightError{Result: r}
}

// PreflightResult carries every failure found during the media preflight.
// A nil or empty PreflightFailure slice means all checks passed.
type PreflightResult struct {
	Failures []PreflightFailure
	WallMS   int64
}

// HasFailures returns true when at least one check failed.
func (r PreflightResult) HasFailures() bool { return len(r.Failures) > 0 }

// Error returns all failures joined by newlines.
func (r PreflightResult) Error() string {
	if len(r.Failures) == 0 {
		return ""
	}
	parts := make([]string, len(r.Failures))
	for i, f := range r.Failures {
		parts[i] = f.Error()
	}
	return strings.Join(parts, "\n")
}

// PreflightFailure is one discrete media requirement check failure.
type PreflightFailure struct {
	Category string
	AssetID  string
	Detail   string
}

func (f PreflightFailure) Error() string {
	if f.AssetID != "" {
		return fmt.Sprintf("[%s] %s: %s", f.Category, f.AssetID, f.Detail)
	}
	return fmt.Sprintf("[%s] %s", f.Category, f.Detail)
}

// ────────────────────────────────────────────────────────────────────────
// MediaPreflight — ports
// ────────────────────────────────────────────────────────────────────────

// ClipPreflighter verifies a clip ID is reachable (fast probe).
type ClipPreflighter interface {
	ProbeClip(ctx context.Context, clipID string) error
}

// MediaPreflightInput carries everything needed to verify media
// requirements for one run.
type FixedClipPreflight struct {
	ClipID      string
	SourceInMS  int64
	SourceOutMS int64
}

// FixedSectionPreflight describes one literal intro/outro contract before
// downstream generation begins. It lets the preflight reject malformed fixed
// media before the LLM is invoked, rather than discovering it during scene
// injection after generation.
type FixedSectionPreflight struct {
	Name     string
	ClipIDs  []string
	Playback scriptpkg.FixedPlaybackPolicy
}

type MediaPreflightInput struct {
	ClipIDs           []string
	IntroClipIDs      []string
	FixedClips        []FixedClipPreflight
	FixedSections     []FixedSectionPreflight
	ClipProber        ClipPreflighter
	ClipAudioSource   ClipAudioAssetSource
	MixPolicy         capabilityaudio.AudioMixPolicy
	BGMIDs            []string
	SFXIDs            []string
	AudioAssetSource  AudioAssetSource
	RenderEnabled     bool
	WatermarkAssetID  string
	WatermarkResolver ClipPreflighter
	// BackgroundAssetID is probed only when background.mode=asset — the
	// materialized background layer is a render requirement like the
	// watermark, so a missing asset fails the run before any LLM/TTS work.
	BackgroundAssetID  string
	BackgroundResolver ClipPreflighter
}

// RunMediaPreflight executes all independent asset checks concurrently and
// returns every failure. Fixed-section contract validation is performed before
// probes so malformed fixed media fails closed at the pre-generation gate.
func RunMediaPreflight(ctx context.Context, in MediaPreflightInput) PreflightResult {
	started := time.Now()

	var (
		mu       sync.Mutex
		failures []PreflightFailure
		wg       sync.WaitGroup
	)

	// Validate the fixed-media contract synchronously before any asset probe.
	// A malformed intro/outro is a hard request failure and must not reach the
	// LLM, translator, or TTS phases.
	for _, section := range in.FixedSections {
		name := strings.TrimSpace(section.Name)
		if name == "" {
			name = "fixed"
		}
		if len(section.ClipIDs) < 1 || len(section.ClipIDs) > 2 {
			failures = append(failures, PreflightFailure{
				Category: "fixed_media", AssetID: name,
				Detail: "fixed section must contain 1 or 2 clip_ids",
			})
		}
		if !section.Playback.Valid() {
			failures = append(failures, PreflightFailure{
				Category: "fixed_media", AssetID: name,
				Detail: "playback must use audio_mode=original_clip with a valid source window",
			})
		}
	}

	// Flatten: one goroutine per check item. Add to wg BEFORE spawning.
	// ── Clip existence ──────────────────────────────────────────
	allClipIDs := make([]string, 0, len(in.ClipIDs)+len(in.IntroClipIDs)+len(in.FixedClips))
	allClipIDs = append(allClipIDs, in.ClipIDs...)
	allClipIDs = append(allClipIDs, in.IntroClipIDs...)
	for _, fixed := range in.FixedClips {
		allClipIDs = append(allClipIDs, fixed.ClipID)
	}
	for _, id := range allClipIDs {
		id := id
		if in.ClipProber == nil {
			mu.Lock()
			failures = append(failures, PreflightFailure{
				Category: "clip", AssetID: id,
				Detail: "clip prober not wired — cannot verify clip existence",
			})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := in.ClipProber.ProbeClip(ctx, id); err != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{
					Category: "clip", AssetID: id,
					Detail: fmt.Sprintf("clip not reachable: %v", err),
				})
				mu.Unlock()
			}
		}()
	}

	// ── Original audio stream ───────────────────────────────────
	// Fixed media always requires its authoritative original audio, regardless
	// of the request-level mix policy. Ordinary generated clip audio retains
	// the legacy VOICEOVER_DUCKED_CLIP gate below.
	fixedIDs := make(map[string]struct{}, len(in.FixedClips))
	for _, fixed := range in.FixedClips {
		fixedIDs[fixed.ClipID] = struct{}{}
		if in.ClipAudioSource == nil {
			mu.Lock()
			failures = append(failures, PreflightFailure{
				Category: "fixed_clip_audio", AssetID: fixed.ClipID,
				Detail: "clip audio source not wired — cannot verify authoritative original audio",
			})
			mu.Unlock()
			continue
		}
		fixed := fixed
		wg.Add(1)
		go func() {
			defer wg.Done()
			resolved, err := in.ClipAudioSource.ResolveClipAudioAsset(ctx, fixed.ClipID)
			if err != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{Category: "fixed_clip_audio", AssetID: fixed.ClipID, Detail: fmt.Sprintf("authoritative original audio unavailable: %v", err)})
				mu.Unlock()
				return
			}
			if err := validateFixedClipAudio(resolved, fixed); err != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{Category: "fixed_clip_audio", AssetID: fixed.ClipID, Detail: err.Error()})
				mu.Unlock()
			}
		}()
	}
	if in.MixPolicy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip {
		for _, id := range in.ClipIDs {
			if _, isFixed := fixedIDs[id]; isFixed {
				continue
			}
			id := id
			if in.ClipAudioSource == nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{
					Category: "clip_audio", AssetID: id,
					Detail: "clip audio source not wired — cannot verify original audio stream for VOICEOVER_DUCKED_CLIP",
				})
				mu.Unlock()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				resolved, err := in.ClipAudioSource.ResolveClipAudioAsset(ctx, id)
				if err != nil {
					mu.Lock()
					failures = append(failures, PreflightFailure{
						Category: "clip_audio", AssetID: id,
						Detail: fmt.Sprintf("original audio stream unavailable: %v", err),
					})
					mu.Unlock()
					return
				}
				if _, statErr := os.Stat(resolved.Path); statErr != nil {
					mu.Lock()
					failures = append(failures, PreflightFailure{
						Category: "clip_audio", AssetID: id,
						Detail: fmt.Sprintf("resolved audio path not readable: %s: %v", resolved.Path, statErr),
					})
					mu.Unlock()
				}
			}()
		}
	}

	// Resolve each canonical audio asset once. An effect may intentionally be
	// placed on many scenes, but concurrent materialization of the same Drive
	// asset races on the shared content-addressed `.part` file.
	bgmIDs := uniqueCanonicalAudioIDs(in.BGMIDs)
	sfxIDs := uniqueCanonicalAudioIDs(in.SFXIDs)

	// ── BGM assets ────────────────────────────────────────────
	for _, id := range bgmIDs {
		id := id
		if in.AudioAssetSource == nil {
			mu.Lock()
			failures = append(failures, PreflightFailure{
				Category: "bgm", AssetID: id,
				Detail: "audio asset source not wired — cannot verify BGM",
			})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			resolved, err := in.AudioAssetSource.ResolveAudioAsset(ctx, id)
			if err != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{
					Category: "bgm", AssetID: id,
					Detail: fmt.Sprintf("BGM asset unavailable: %v", err),
				})
				mu.Unlock()
				return
			}
			if _, statErr := os.Stat(resolved.Path); statErr != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{
					Category: "bgm", AssetID: id,
					Detail: fmt.Sprintf("BGM file not readable: %s: %v", resolved.Path, statErr),
				})
				mu.Unlock()
			}
		}()
	}

	// ── SFX assets ────────────────────────────────────────────
	for _, id := range sfxIDs {
		id := id
		if in.AudioAssetSource == nil {
			mu.Lock()
			failures = append(failures, PreflightFailure{
				Category: "sfx", AssetID: id,
				Detail: "audio asset source not wired — cannot verify SFX",
			})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			resolved, err := in.AudioAssetSource.ResolveAudioAsset(ctx, id)
			if err != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{
					Category: "sfx", AssetID: id,
					Detail: fmt.Sprintf("SFX asset unavailable: %v", err),
				})
				mu.Unlock()
				return
			}
			if _, statErr := os.Stat(resolved.Path); statErr != nil {
				mu.Lock()
				failures = append(failures, PreflightFailure{
					Category: "sfx", AssetID: id,
					Detail: fmt.Sprintf("SFX file not readable: %s: %v", resolved.Path, statErr),
				})
				mu.Unlock()
			}
		}()
	}

	// ── Watermark asset ────────────────────────────────────────
	if in.RenderEnabled && strings.TrimSpace(in.WatermarkAssetID) != "" {
		id := in.WatermarkAssetID
		if in.WatermarkResolver == nil {
			failures = append(failures, PreflightFailure{
				Category: "watermark", AssetID: id,
				Detail: "watermark resolver not wired",
			})
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := in.WatermarkResolver.ProbeClip(ctx, id); err != nil {
					mu.Lock()
					failures = append(failures, PreflightFailure{
						Category: "watermark", AssetID: id,
						Detail: fmt.Sprintf("watermark asset unavailable: %v", err),
					})
					mu.Unlock()
				}
			}()
		}
	}

	// ── Background asset (mode=asset only) ─────────────────────
	if in.RenderEnabled && strings.TrimSpace(in.BackgroundAssetID) != "" {
		id := in.BackgroundAssetID
		if in.BackgroundResolver == nil {
			failures = append(failures, PreflightFailure{
				Category: "background", AssetID: id,
				Detail: "background resolver not wired",
			})
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := in.BackgroundResolver.ProbeClip(ctx, id); err != nil {
					mu.Lock()
					failures = append(failures, PreflightFailure{
						Category: "background", AssetID: id,
						Detail: fmt.Sprintf("background asset unavailable: %v", err),
					})
					mu.Unlock()
				}
			}()
		}
	}

	wg.Wait()
	return PreflightResult{
		Failures: failures,
		WallMS:   time.Since(started).Milliseconds(),
	}
}

func validateFixedClipAudio(resolved capabilityaudio.ResolvedAudioAsset, fixed FixedClipPreflight) error {
	if strings.TrimSpace(resolved.Path) == "" {
		return fmt.Errorf("authoritative original audio resolved to an empty path")
	}
	if _, err := os.Stat(resolved.Path); err != nil {
		return fmt.Errorf("authoritative original audio path not readable: %s: %w", resolved.Path, err)
	}
	if fixed.SourceInMS < 0 || fixed.SourceOutMS < 0 || (fixed.SourceOutMS > 0 && fixed.SourceOutMS <= fixed.SourceInMS) {
		return fmt.Errorf("source window is invalid")
	}
	if resolved.DurationUS <= 0 {
		if fixed.SourceOutMS == 0 {
			return fmt.Errorf("complete-clip source window requires a certified original audio duration")
		}
		return nil
	}
	if fixed.SourceInMS*1000 >= resolved.DurationUS {
		return fmt.Errorf("source window starts at %dms beyond original audio duration %dms", fixed.SourceInMS, resolved.DurationUS/1000)
	}
	if fixed.SourceOutMS > 0 && fixed.SourceOutMS*1000 > resolved.DurationUS {
		return fmt.Errorf("source window [%d,%d]ms exceeds original audio duration %dms", fixed.SourceInMS, fixed.SourceOutMS, resolved.DurationUS/1000)
	}
	return nil
}

func uniqueCanonicalAudioIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := capabilityaudio.CanonicalAssetID(raw)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

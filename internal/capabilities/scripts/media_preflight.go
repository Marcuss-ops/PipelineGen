// Package scriptgeneration — media_preflight.go implements the fail-fast
// Media Requirement Preflight. The runner executes it SYNCHRONOUSLY after
// normalization and BEFORE the first LLM call: fixed intro/outro assets,
// original clip audio streams, BGM/SFX assets, Drive folders, and watermark
// assets are all verified before Gemma, translation, or TTS spend work. It
// is deliberately NOT parallel with Gemma — a preflight failure aborts the
// run before generation starts, so no LLM/TTS work is wasted on a run that
// would fail at audio compile anyway (e.g. missing clip audio for
// VOICEOVER_DUCKED_CLIP).
//
// The preflight runs EVERY check concurrently and collects ALL failures,
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
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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

// ImageProviderHealthProbe verifies that at least one image-retrieval provider
// is healthy BEFORE the run spends generation work. The per-provider
// Diagnostics() surface (/api/system/doctor) already reported this state, but
// nothing consulted it at job start: a degraded provider was discovered only
// by burning a complete durable run. A probe failure is a media-preflight
// failure, so no LLM/TTS work is wasted on a run that cannot retrieve images.
type ImageProviderHealthProbe interface {
	ProbeImageProviderHealth(ctx context.Context) error
}

// VidRushProviderAvailabilityProbe verifies that a provider explicitly enabled
// by the media plan was actually registered in the composed materialization
// registry. This is a registration check, not a network health check.
type VidRushProviderAvailabilityProbe interface {
	ProbeVidRushProvider(ctx context.Context, provider string) error
}

// VidRushProviderAvailabilityPreflight rechecks the static provider registry
// when a durable run resumes after MEDIA_PREFLIGHT. A retry must not skip a
// known-missing composition dependency merely because its asset checks already
// completed in an earlier attempt.
type VidRushProviderAvailabilityPreflight interface {
	RunVidRushProviderAvailability(ctx context.Context, req GenerateRequest) PreflightResult
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
	// ImageProviderHealth is optional: nil means the run does not depend on
	// internet image retrieval (clip-only / fixed-media runs) and no probe is
	// issued. A non-nil probe MUST pass for the preflight to succeed.
	ImageProviderHealth ImageProviderHealthProbe
	// VidRushProviderAvailability is required whenever a provider toggle is
	// enabled in MediaPlan. A missing probe is itself a fail-closed preflight
	// failure; disabled providers are never probed.
	MediaPlan                   mediadomain.MediaPlanSpec
	VidRushProviderAvailability VidRushProviderAvailabilityProbe
}

// RunMediaPreflight executes all independent asset checks concurrently and
// returns every failure. Fixed-section contract validation is performed before
// probes so malformed fixed media fails closed at the pre-generation gate.
func RunMediaPreflight(ctx context.Context, in MediaPreflightInput) PreflightResult {
	started := time.Now()
	collector := &preflightCollector{}

	// Validate the fixed-media contract synchronously before any asset probe.
	// A malformed intro/outro is a hard request failure and must not reach the
	// LLM, translator, or TTS phases.
	for _, failure := range validateFixedSectionContracts(in.FixedSections) {
		collector.fail(failure)
	}

	// Provider registration checks are synchronous and run before the asset
	// fan-out, so appending their failures cannot race with concurrent probes.
	for _, failure := range RunVidRushProviderAvailabilityPreflight(ctx, in.MediaPlan, in.VidRushProviderAvailability).Failures {
		collector.fail(failure)
	}

	probeClipExistence(ctx, in, collector)
	probeOriginalAudio(ctx, in, collector)

	// Resolve each canonical audio asset once. An effect may intentionally be
	// placed on many scenes, but concurrent materialization of the same Drive
	// asset races on the shared content-addressed `.part` file.
	probeResolvedAudioAssets(ctx, in, collector, canonicalAudioIDs(in.BGMIDs), "bgm", "BGM")
	probeResolvedAudioAssets(ctx, in, collector, canonicalAudioIDs(in.SFXIDs), "sfx", "SFX")

	probeRenderAsset(ctx, in, collector, "watermark", "watermark", in.WatermarkAssetID, in.WatermarkResolver)
	probeRenderAsset(ctx, in, collector, "background", "background", in.BackgroundAssetID, in.BackgroundResolver)

	// Run the foreign probe on the same concurrent fan-out as every other
	// check so a slow provider probe cannot serialise the preflight. Only a
	// probe error becomes a failure; one healthy provider is enough.
	if in.ImageProviderHealth != nil {
		collector.goRun(func() {
			if err := in.ImageProviderHealth.ProbeImageProviderHealth(ctx); err != nil {
				collector.fail(PreflightFailure{
					Category: "image_providers",
					Detail:   fmt.Sprintf("no usable image retrieval provider: %v", err),
				})
			}
		})
	}

	return PreflightResult{
		Failures: collector.wait(),
		WallMS:   time.Since(started).Milliseconds(),
	}
}

// preflightCollector serialises the concurrent probe fan-out. Every append goes
// through fail() under the mutex, and goRun() adds to the WaitGroup BEFORE
// spawning so wait() can never miss a probe.
type preflightCollector struct {
	mu       sync.Mutex
	wg       sync.WaitGroup
	failures []PreflightFailure
}

func (c *preflightCollector) fail(failure PreflightFailure) {
	c.mu.Lock()
	c.failures = append(c.failures, failure)
	c.mu.Unlock()
}

func (c *preflightCollector) goRun(probe func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		probe()
	}()
}

func (c *preflightCollector) wait() []PreflightFailure {
	c.wg.Wait()
	return c.failures
}

// validateFixedSectionContracts rejects a malformed intro/outro before any
// asset probe: a fixed section with no clip or an invalid playback window would
// otherwise reach the LLM/translator/TTS phases and fail there.
func validateFixedSectionContracts(sections []FixedSectionPreflight) []PreflightFailure {
	var failures []PreflightFailure
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		if name == "" {
			name = "fixed"
		}
		if len(section.ClipIDs) < 1 {
			failures = append(failures, PreflightFailure{
				Category: "fixed_media", AssetID: name,
				Detail: "fixed section must contain at least one clip_id",
			})
		}
		if !section.Playback.Valid() {
			failures = append(failures, PreflightFailure{
				Category: "fixed_media", AssetID: name,
				Detail: "playback must use audio_mode=original_clip or silence with a valid source window",
			})
		}
	}
	return failures
}

// probeClipExistence verifies every generated and fixed clip resolves in the
// catalog. A nil prober is a failure, not a skip: an unverified clip is exactly
// what the preflight exists to catch.
func probeClipExistence(ctx context.Context, in MediaPreflightInput, collector *preflightCollector) {
	allClipIDs := make([]string, 0, len(in.ClipIDs)+len(in.FixedClips))
	allClipIDs = append(allClipIDs, in.ClipIDs...)
	for _, fixed := range in.FixedClips {
		allClipIDs = append(allClipIDs, fixed.ClipID)
	}
	for _, id := range uniqueClipProbeIDs(allClipIDs) {
		id := id
		if in.ClipProber == nil {
			collector.fail(PreflightFailure{
				Category: "clip", AssetID: id,
				Detail: "clip prober not wired — cannot verify clip existence",
			})
			continue
		}
		collector.goRun(func() {
			if err := in.ClipProber.ProbeClip(ctx, id); err != nil {
				collector.fail(PreflightFailure{
					Category: "clip", AssetID: id,
					Detail: fmt.Sprintf("clip not reachable: %v", err),
				})
			}
		})
	}
}

// probeOriginalAudio verifies the authoritative original audio of fixed media
// (always required) and of generated clips under VOICEOVER_DUCKED_CLIP.
func probeOriginalAudio(ctx context.Context, in MediaPreflightInput, collector *preflightCollector) {
	fixedIDs := make(map[string]struct{}, len(in.FixedClips))
	for _, fixed := range in.FixedClips {
		fixedIDs[fixed.ClipID] = struct{}{}
		if in.ClipAudioSource == nil {
			collector.fail(PreflightFailure{
				Category: "fixed_clip_audio", AssetID: fixed.ClipID,
				Detail: "clip audio source not wired — cannot verify authoritative original audio",
			})
			continue
		}
		fixed := fixed
		collector.goRun(func() {
			resolved, err := in.ClipAudioSource.ResolveClipAudioAsset(ctx, fixed.ClipID)
			if err != nil {
				collector.fail(PreflightFailure{Category: "fixed_clip_audio", AssetID: fixed.ClipID, Detail: fmt.Sprintf("authoritative original audio unavailable: %v", err)})
				return
			}
			if err := validateFixedClipAudio(resolved, fixed); err != nil {
				collector.fail(PreflightFailure{Category: "fixed_clip_audio", AssetID: fixed.ClipID, Detail: err.Error()})
			}
		})
	}
	if in.MixPolicy.Normalize() != capabilityaudio.MixVoiceoverWithDuckedClip {
		return
	}
	for _, id := range in.ClipIDs {
		if _, isFixed := fixedIDs[id]; isFixed {
			continue
		}
		id := id
		if in.ClipAudioSource == nil {
			collector.fail(PreflightFailure{
				Category: "clip_audio", AssetID: id,
				Detail: "clip audio source not wired — cannot verify original audio stream for VOICEOVER_DUCKED_CLIP",
			})
			continue
		}
		collector.goRun(func() {
			resolved, err := in.ClipAudioSource.ResolveClipAudioAsset(ctx, id)
			if err != nil {
				collector.fail(PreflightFailure{
					Category: "clip_audio", AssetID: id,
					Detail: fmt.Sprintf("original audio stream unavailable: %v", err),
				})
				return
			}
			if _, statErr := os.Stat(resolved.Path); statErr != nil {
				collector.fail(PreflightFailure{
					Category: "clip_audio", AssetID: id,
					Detail: fmt.Sprintf("resolved audio path not readable: %s: %v", resolved.Path, statErr),
				})
			}
		})
	}
}

// probeResolvedAudioAssets resolves each BGM/SFX asset once and proves the file
// is readable. category/noun keep the failure taxonomy and the operator-facing
// wording identical to the pre-split body.
func probeResolvedAudioAssets(ctx context.Context, in MediaPreflightInput, collector *preflightCollector, ids []string, category, noun string) {
	for _, id := range ids {
		id := id
		if in.AudioAssetSource == nil {
			collector.fail(PreflightFailure{
				Category: category, AssetID: id,
				Detail: fmt.Sprintf("audio asset source not wired — cannot verify %s", noun),
			})
			continue
		}
		collector.goRun(func() {
			resolved, err := in.AudioAssetSource.ResolveAudioAsset(ctx, id)
			if err != nil {
				collector.fail(PreflightFailure{
					Category: category, AssetID: id,
					Detail: fmt.Sprintf("%s asset unavailable: %v", noun, err),
				})
				return
			}
			if _, statErr := os.Stat(resolved.Path); statErr != nil {
				collector.fail(PreflightFailure{
					Category: category, AssetID: id,
					Detail: fmt.Sprintf("%s file not readable: %s: %v", noun, resolved.Path, statErr),
				})
			}
		})
	}
}

// probeRenderAsset verifies a render-time asset (watermark, background in
// mode=asset) that the materialized render cannot proceed without.
func probeRenderAsset(ctx context.Context, in MediaPreflightInput, collector *preflightCollector, category, noun, assetID string, resolver ClipPreflighter) {
	if !in.RenderEnabled || strings.TrimSpace(assetID) == "" {
		return
	}
	id := assetID
	if resolver == nil {
		collector.fail(PreflightFailure{
			Category: category, AssetID: id,
			Detail: fmt.Sprintf("%s resolver not wired", noun),
		})
		return
	}
	collector.goRun(func() {
		if err := resolver.ProbeClip(ctx, id); err != nil {
			collector.fail(PreflightFailure{
				Category: category, AssetID: id,
				Detail: fmt.Sprintf("%s asset unavailable: %v", noun, err),
			})
		}
	})
}

// RunVidRushProviderAvailabilityPreflight checks every explicitly enabled
// VidRush provider against the frozen composition registry. It is shared by
// the full preflight and the durable-resume fast check.
func RunVidRushProviderAvailabilityPreflight(ctx context.Context, plan mediadomain.MediaPlanSpec, probe VidRushProviderAvailabilityProbe) PreflightResult {
	started := time.Now()
	providerChecks := []struct {
		name    string
		enabled bool
	}{
		{name: scriptpkg.VidRushProviderYouTube, enabled: plan.ProviderPolicy.YouTube.AsBool()},
		{name: scriptpkg.VidRushProviderInternetImages, enabled: plan.ProviderPolicy.InternetImages.AsBool()},
		{name: scriptpkg.VidRushProviderImageGeneration, enabled: plan.ProviderPolicy.ImageGeneration.AsBool()},
	}
	var failures []PreflightFailure
	for _, check := range providerChecks {
		if !check.enabled {
			continue
		}
		if probe == nil {
			failures = append(failures, PreflightFailure{
				Category: "vidrush_provider",
				AssetID:  check.name,
				Detail:   "provider availability probe not wired — cannot verify enabled provider",
			})
			continue
		}
		if err := probe.ProbeVidRushProvider(ctx, check.name); err != nil {
			failures = append(failures, PreflightFailure{
				Category: "vidrush_provider",
				AssetID:  check.name,
				Detail:   fmt.Sprintf("enabled provider unavailable: %v", err),
			})
		}
	}
	return PreflightResult{Failures: failures, WallMS: time.Since(started).Milliseconds()}
}

// uniqueClipProbeIDs removes repeated existence probes without normalizing IDs
// or dropping empty values: blank IDs must still reach the fail-closed prober.
func uniqueClipProbeIDs(ids []string) []string {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
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

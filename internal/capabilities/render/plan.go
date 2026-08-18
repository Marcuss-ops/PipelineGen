// Package render owns the implementation-independent render contract passed
// from generation to the media executor. It contains no FFmpeg or transport
// behavior; all timing is compiled to integer frames before execution.
package render

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// FileSystem is the minimal read-side filesystem port used by
// ValidateManifestFiles to re-hash manifest entries and the final audio
// asset. The adapter (internal/platform/filesystem.OS) injects os.Open +
// os.Stat; the capability never imports os directly.
type FileSystem interface {
	Open(path string) (io.ReadCloser, error)
	Size(path string) (int64, error)
}

const PlanVersion = "render-plan.v2"

var (
	ErrInvalidPlan    = errors.New("invalid render plan")
	ErrManifestDrift  = errors.New("render manifest hash mismatch")
	ErrTimelineDrift  = errors.New("render timeline hash mismatch")
	ErrPlanDrift      = errors.New("render plan hash mismatch")
	ErrAssetHashDrift = errors.New("render asset hash mismatch")
)

type FrameRange struct {
	StartFrame int64 `json:"start_frame"`
	FrameCount int64 `json:"frame_count"`
}

type RenderSource struct {
	InFrame    int64 `json:"start_frame"`
	FrameCount int64 `json:"frame_count"`
}

type VideoSegment struct {
	AssetID  string       `json:"asset_id"`
	Source   RenderSource `json:"source"`
	Timeline FrameRange   `json:"timeline"`
	ZIndex   int          `json:"z_index"`
	// Freeze marks a synthetic tail: Source holds one frame (the clip's final
	// frame) stretched across Timeline.FrameCount destination frames.
	Freeze bool `json:"freeze,omitempty"`
}

type VideoTrack struct {
	Index    int            `json:"index"`
	Segments []VideoSegment `json:"segments"`
}

type AssetManifestEntry struct {
	AssetID    string `json:"asset_id"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	FrameCount int64  `json:"frame_count"`
}

type FinalAudioAsset struct {
	AssetID              string `json:"asset_id"`
	AssetKind            string `json:"asset_kind,omitempty"`
	Strategy             string `json:"audio_strategy,omitempty"`
	Path                 string `json:"path"`
	SHA256               string `json:"sha256"`
	PlanSHA256           string `json:"audio_plan_sha256,omitempty"`
	AudioContractVersion string `json:"audio_contract_version,omitempty"`
	AudioPlanVersion     string `json:"audio_plan_version,omitempty"`
	Codec                string `json:"codec,omitempty"`
	Profile              string `json:"profile,omitempty"`
	SampleRate           int    `json:"sample_rate,omitempty"`
	Channels             int    `json:"channels,omitempty"`
	ChannelLayout        string `json:"channel_layout,omitempty"`
	DurationMS           int64  `json:"duration_ms,omitempty"`
	StartPTS             int64  `json:"start_pts,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	FinalMix             bool   `json:"final_mix,omitempty"`
	CopyEligible         bool   `json:"copy_eligible,omitempty"`
}

// RenderExecutionPolicy pins the deterministic execution identity of a
// render: whether stream copy is allowed and the canonical hashes of the
// target output profile, the renderer and the encoder policy. Every field
// contributes to PlanSHA256 (the policy is part of the sealed plan), so a
// policy change — e.g. encoder preset medium → fast — can never reuse a
// stale artifact. Nil policy on a plan means legacy execution: no cache
// identity and no stream copy.
type RenderExecutionPolicy struct {
	AllowStreamCopy   bool   `json:"allow_stream_copy"`
	TargetProfileHash string `json:"target_profile_hash"`
	RendererVersion   string `json:"renderer_version"`
	EncoderPolicyHash string `json:"encoder_policy_hash"`
}

type RenderPlan struct {
	Version        string                  `json:"version"`
	JobID          string                  `json:"job_id"`
	Revision       string                  `json:"revision"`
	OutputPath     string                  `json:"output_path"`
	FPS            int                     `json:"fps"` // legacy nominal integer for executor compatibility
	FPSNumerator   int64                   `json:"fps_numerator"`
	FPSDenominator int64                   `json:"fps_denominator"`
	DurationFrames int64                   `json:"duration_frames"`
	Timeline       audio.CanonicalTimeline `json:"timeline"`
	TimelineHash   string                  `json:"timeline_hash"`
	FinalAudio     *FinalAudioAsset        `json:"final_audio,omitempty"`
	VideoTracks    []VideoTrack            `json:"video_tracks"`
	Manifest       []AssetManifestEntry    `json:"manifest"`
	ManifestSHA256 string                  `json:"manifest_sha256"`
	// ExecutionPolicy is the sealed execution identity. It is a pointer with
	// omitempty so legacy plans (nil policy) keep their exact PlanSHA256;
	// when set, it participates in the plan hash and therefore in every
	// cache key and checkpoint identity derived from it.
	ExecutionPolicy *RenderExecutionPolicy `json:"execution_policy,omitempty"`
	PlanSHA256      string                 `json:"plan_sha256"`
}

type CompileInput struct {
	JobID      string
	Revision   string
	OutputPath string
	FPS        int
	FrameRate  audio.FrameRate
	Timeline   audio.CanonicalTimeline
	FinalAudio *FinalAudioAsset
	Manifest   []AssetManifestEntry
	// ExecutionPolicy is optional; nil keeps legacy behavior (no cache
	// identity, no stream copy) and leaves PlanSHA256 unchanged.
	ExecutionPolicy *RenderExecutionPolicy
}

func Compile(input CompileInput) (RenderPlan, error) {
	rate, err := compileFrameRate(input)
	if err != nil {
		return RenderPlan{}, err
	}
	resolver, err := audio.NewFrameResolver(rate)
	if err != nil {
		return RenderPlan{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := input.Timeline.Validate(); err != nil {
		return RenderPlan{}, err
	}
	nominalFPS, err := nominalFPSForRate(rate)
	if err != nil {
		return RenderPlan{}, err
	}
	durationFrames, err := resolver.FrameAt(input.Timeline.DurationUS)
	if err != nil {
		return RenderPlan{}, fmt.Errorf("%w: timeline duration frames: %v", ErrInvalidPlan, err)
	}
	plan := RenderPlan{
		Version:        PlanVersion,
		JobID:          strings.TrimSpace(input.JobID),
		Revision:       strings.TrimSpace(input.Revision),
		OutputPath:     strings.TrimSpace(input.OutputPath),
		FPS:            nominalFPS,
		FPSNumerator:   rate.Numerator,
		FPSDenominator: rate.Denominator,
		DurationFrames: durationFrames,
		Timeline:       input.Timeline,
		FinalAudio:     input.FinalAudio,
		Manifest:       append([]AssetManifestEntry(nil), input.Manifest...),
		VideoTracks:    []VideoTrack{{Index: 0}},
	}
	if input.ExecutionPolicy != nil {
		policy := *input.ExecutionPolicy
		plan.ExecutionPolicy = &policy
	}
	if plan.JobID == "" || plan.Revision == "" || plan.OutputPath == "" || plan.DurationFrames <= 0 {
		return RenderPlan{}, fmt.Errorf("%w: identity, output, or duration is missing", ErrInvalidPlan)
	}
	if err := validateExecutionPolicy(plan.ExecutionPolicy); err != nil {
		return RenderPlan{}, err
	}
	if err := validateManifestEntries(plan.Manifest); err != nil {
		return RenderPlan{}, err
	}
	for i, segment := range input.Timeline.Segments {
		for _, video := range segment.EffectiveVideoSegments() {
			if strings.TrimSpace(video.AssetID) == "" {
				continue
			}
			if video.TimelineDurationUS <= 0 {
				return RenderPlan{}, fmt.Errorf("%w: segment %s source duration is invalid", ErrInvalidPlan, segment.ID)
			}
			start, destinationCount, err := resolver.FrameRange(segment.TimelineStartUS+video.TimelineOffsetUS, video.TimelineDurationUS)
			if err != nil {
				return RenderPlan{}, fmt.Errorf("%w: segment %s timeline frames: %v", ErrInvalidPlan, segment.ID, err)
			}
			var sourceStart, sourceCount int64
			if video.Freeze {
				// A freeze tail holds the clip's final frame. SourceInUS is the
				// exclusive source end of the preceding real segment, so the
				// frozen frame is FrameAt(SourceInUS)-1, stretched across the
				// destination frames.
				endFrame, err := resolver.FrameAt(video.SourceInUS)
				if err != nil {
					return RenderPlan{}, fmt.Errorf("%w: segment %s freeze source frame: %v", ErrInvalidPlan, segment.ID, err)
				}
				if endFrame <= 0 {
					return RenderPlan{}, fmt.Errorf("%w: segment %s freeze requires a preceding source frame", ErrInvalidPlan, segment.ID)
				}
				sourceStart, sourceCount = endFrame-1, 1
			} else {
				if video.SourceDurationUS <= 0 {
					return RenderPlan{}, fmt.Errorf("%w: segment %s source duration is invalid", ErrInvalidPlan, segment.ID)
				}
				sourceStart, err = resolver.FrameAt(video.SourceInUS)
				if err != nil {
					return RenderPlan{}, fmt.Errorf("%w: segment %s source frame: %v", ErrInvalidPlan, segment.ID, err)
				}
				sourceCount = destinationCount
			}
			plan.VideoTracks[0].Segments = append(plan.VideoTracks[0].Segments, VideoSegment{
				AssetID:  video.AssetID,
				Source:   RenderSource{InFrame: sourceStart, FrameCount: sourceCount},
				Timeline: FrameRange{StartFrame: start, FrameCount: destinationCount},
				ZIndex:   i,
				Freeze:   video.Freeze,
			})
		}
	}
	if err := plan.Seal(); err != nil {
		return RenderPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return RenderPlan{}, err
	}
	return plan, nil
}

func nominalFPSForRate(rate audio.FrameRate) (int, error) {
	if err := rate.Validate(); err != nil {
		return 0, fmt.Errorf("%w: invalid frame rate: %v", ErrInvalidPlan, err)
	}
	nominalNumerator := rate.Numerator + rate.Denominator/2
	if nominalNumerator < rate.Numerator { // defensive overflow guard
		return 0, fmt.Errorf("%w: frame rate rounding overflows", ErrInvalidPlan)
	}
	nominal := nominalNumerator / rate.Denominator
	if nominal <= 0 || uint64(nominal) > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("%w: nominal frame rate overflows int", ErrInvalidPlan)
	}
	return int(nominal), nil
}

func compileFrameRate(input CompileInput) (audio.FrameRate, error) {
	if input.FrameRate.Numerator > 0 || input.FrameRate.Denominator > 0 {
		if err := input.FrameRate.Validate(); err != nil {
			return audio.FrameRate{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
		return input.FrameRate, nil
	}
	if input.FPS <= 0 {
		return audio.FrameRate{}, fmt.Errorf("%w: fps must be positive", ErrInvalidPlan)
	}
	return audio.IntegerFrameRate(input.FPS), nil
}

// FrameAtMS is retained only as a compatibility helper for callers outside
// the canonical timeline path. New code must use audio.FrameResolver with US.
func FrameAtMS(milliseconds int64, fps int) (int64, error) {
	if milliseconds < 0 {
		return 0, fmt.Errorf("%w: invalid milliseconds", ErrInvalidPlan)
	}
	microseconds, err := audio.MicrosecondsFromMilliseconds(milliseconds)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	resolver, err := audio.NewFrameResolver(audio.IntegerFrameRate(fps))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	return resolver.FrameAt(microseconds)
}

func (p RenderPlan) ManifestHash() (string, error) {
	entries := append([]AssetManifestEntry(nil), p.Manifest...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].AssetID < entries[j].AssetID })
	b, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("hash render manifest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (p RenderPlan) Hash() (string, error) {
	copyPlan := p
	copyPlan.Manifest = append([]AssetManifestEntry(nil), p.Manifest...)
	sort.Slice(copyPlan.Manifest, func(i, j int) bool { return copyPlan.Manifest[i].AssetID < copyPlan.Manifest[j].AssetID })
	copyPlan.ManifestSHA256 = ""
	copyPlan.PlanSHA256 = ""
	b, err := json.Marshal(copyPlan)
	if err != nil {
		return "", fmt.Errorf("hash render plan: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (p *RenderPlan) Seal() error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrInvalidPlan)
	}
	timelineHash, err := p.Timeline.Hash()
	if err != nil {
		return err
	}
	p.TimelineHash = timelineHash
	manifestHash, err := p.ManifestHash()
	if err != nil {
		return err
	}
	p.ManifestSHA256 = manifestHash
	planHash, err := p.Hash()
	if err != nil {
		return err
	}
	p.PlanSHA256 = planHash
	return nil
}

func (p RenderPlan) Validate() error {
	if p.Version != PlanVersion || p.JobID == "" || p.Revision == "" || p.OutputPath == "" || p.FPS <= 0 || p.FPSNumerator <= 0 || p.FPSDenominator <= 0 || p.DurationFrames <= 0 {
		return fmt.Errorf("%w: version, identity, output, fps, or duration", ErrInvalidPlan)
	}
	if err := p.Timeline.Validate(); err != nil {
		return err
	}
	rate := audio.FrameRate{Numerator: p.FPSNumerator, Denominator: p.FPSDenominator}
	nominalFPS, err := nominalFPSForRate(rate)
	if err != nil || p.FPS != nominalFPS {
		return fmt.Errorf("%w: legacy fps does not match rational frame rate", ErrInvalidPlan)
	}
	expectedTimeline, err := p.Timeline.Hash()
	if err != nil {
		return err
	}
	if p.TimelineHash != expectedTimeline {
		return fmt.Errorf("%w: got %q want %q", ErrTimelineDrift, p.TimelineHash, expectedTimeline)
	}
	if err := validateManifestEntries(p.Manifest); err != nil {
		return err
	}
	manifestIDs := make(map[string]AssetManifestEntry, len(p.Manifest))
	for _, entry := range p.Manifest {
		manifestIDs[entry.AssetID] = entry
	}
	for _, track := range p.VideoTracks {
		for _, segment := range track.Segments {
			entry, ok := manifestIDs[segment.AssetID]
			if !ok {
				return fmt.Errorf("%w: video segment references asset %q absent from manifest", ErrInvalidPlan, segment.AssetID)
			}
			if segment.Source.InFrame > math.MaxInt64-segment.Source.FrameCount || segment.Source.InFrame+segment.Source.FrameCount > entry.FrameCount {
				return fmt.Errorf("%w: source frame range exceeds asset %q", ErrInvalidPlan, segment.AssetID)
			}
		}
	}
	expectedManifest, err := p.ManifestHash()
	if err != nil {
		return err
	}
	if p.ManifestSHA256 != expectedManifest {
		return fmt.Errorf("%w: got %q want %q", ErrManifestDrift, p.ManifestSHA256, expectedManifest)
	}
	expectedPlan, err := p.Hash()
	if err != nil {
		return err
	}
	if p.PlanSHA256 != expectedPlan {
		return fmt.Errorf("%w: got %q want %q", ErrPlanDrift, p.PlanSHA256, expectedPlan)
	}
	if err := validateExecutionPolicy(p.ExecutionPolicy); err != nil {
		return err
	}
	resolver, err := audio.NewFrameResolver(rate)
	if err != nil {
		return fmt.Errorf("%w: invalid frame rate: %v", ErrInvalidPlan, err)
	}
	expectedDurationFrames, err := resolver.FrameAt(p.Timeline.DurationUS)
	if err != nil || p.DurationFrames != expectedDurationFrames {
		return fmt.Errorf("%w: duration frame count does not match timeline", ErrInvalidPlan)
	}
	for _, track := range p.VideoTracks {
		// A canonical timeline may begin with an audio-only intro. In that
		// case the first visual segment legitimately starts after frame 0;
		// the renderer fills the leading interval according to its track
		// policy. Once video starts, segments on the primary track must remain
		// contiguous and still cover the remainder of the output.
		expectedStart := int64(-1)
		if track.Index == 0 {
			timelineVideo := make([]audio.VideoSegment, 0, len(p.Timeline.Segments))
			for _, timelineSegment := range p.Timeline.Segments {
				timelineVideo = append(timelineVideo, timelineSegment.EffectiveVideoSegments()...)
			}
			if len(track.Segments) != len(timelineVideo) {
				return fmt.Errorf("%w: primary track does not match canonical video segments", ErrInvalidPlan)
			}
			for i, segment := range timelineVideo {
				if track.Segments[i].AssetID != segment.AssetID {
					return fmt.Errorf("%w: source duration or asset diverges from canonical timeline", ErrInvalidPlan)
				}
			}
		}
		for _, segment := range track.Segments {
			if expectedStart < 0 {
				expectedStart = segment.Timeline.StartFrame
			}
			// A freeze tail stretches one source frame across many timeline
			// frames; every other segment keeps the 1:1 source↔timeline map.
			sourceMatchesTimeline := segment.Source.FrameCount == segment.Timeline.FrameCount
			if segment.Freeze {
				sourceMatchesTimeline = segment.Source.FrameCount == 1
			}
			if segment.Source.InFrame < 0 || segment.Source.FrameCount <= 0 || segment.Timeline.StartFrame != expectedStart || segment.Timeline.StartFrame < 0 || segment.Timeline.FrameCount <= 0 || !sourceMatchesTimeline || segment.Timeline.StartFrame > math.MaxInt64-segment.Timeline.FrameCount || segment.Timeline.StartFrame+segment.Timeline.FrameCount > p.DurationFrames || expectedStart > math.MaxInt64-segment.Timeline.FrameCount {
				return fmt.Errorf("%w: invalid or non-contiguous integer frame segment", ErrInvalidPlan)
			}
			expectedStart += segment.Timeline.FrameCount
		}
		if track.Index == 0 && len(track.Segments) > 0 && expectedStart != p.DurationFrames {
			return fmt.Errorf("%w: primary video track does not cover duration_frames", ErrInvalidPlan)
		}
	}
	if p.FinalAudio != nil {
		if p.FinalAudio.AssetID == "" || p.FinalAudio.Path == "" || !isSHA256(p.FinalAudio.SHA256) {
			return fmt.Errorf("%w: final audio identity or SHA256 is invalid", ErrInvalidPlan)
		}
	}
	return nil
}

func (p RenderPlan) ValidateManifestFiles(fs FileSystem) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, entry := range p.Manifest {
		file, err := fs.Open(entry.Path)
		if err != nil {
			return fmt.Errorf("%w: open %s: %v", ErrAssetHashDrift, entry.AssetID, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("%w: asset %s", ErrAssetHashDrift, entry.AssetID)
		}
	}
	if p.FinalAudio != nil {
		file, err := fs.Open(p.FinalAudio.Path)
		if err != nil {
			return fmt.Errorf("%w: open final audio %s: %v", ErrAssetHashDrift, p.FinalAudio.AssetID, err)
		}
		size, statErr := fs.Size(p.FinalAudio.Path)
		if statErr != nil || size != p.FinalAudio.SizeBytes {
			_ = file.Close()
			return fmt.Errorf("%w: final audio size mismatch for %s", ErrAssetHashDrift, p.FinalAudio.AssetID)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != p.FinalAudio.SHA256 {
			return fmt.Errorf("%w: final audio %s", ErrAssetHashDrift, p.FinalAudio.AssetID)
		}
	}
	return nil
}

// validateExecutionPolicy fails closed on a structurally incomplete policy:
// a policy that cannot identify the execution (missing or malformed profile
// / encoder hashes, missing renderer version) must never be sealed into a
// plan. Nil policy is valid (legacy execution).
func validateExecutionPolicy(policy *RenderExecutionPolicy) error {
	if policy == nil {
		return nil
	}
	if !isSHA256(policy.TargetProfileHash) || !isSHA256(policy.EncoderPolicyHash) {
		return fmt.Errorf("%w: execution policy requires SHA256 target_profile_hash and encoder_policy_hash", ErrInvalidPlan)
	}
	if strings.TrimSpace(policy.RendererVersion) == "" {
		return fmt.Errorf("%w: execution policy requires renderer_version", ErrInvalidPlan)
	}
	return nil
}

func validateManifestEntries(entries []AssetManifestEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.AssetID == "" || entry.Path == "" || !isSHA256(entry.SHA256) || entry.FrameCount <= 0 {
			return fmt.Errorf("%w: manifest entry requires asset_id, path, SHA256, and positive frame_count", ErrInvalidPlan)
		}
		if _, ok := seen[entry.AssetID]; ok {
			return fmt.Errorf("%w: duplicate manifest asset %q", ErrInvalidPlan, entry.AssetID)
		}
		seen[entry.AssetID] = struct{}{}
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

package videocreate

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// VerifiedFacts are the durable §17 certification facts of the final
// video: the ffprobe gate outcome + the canonical SHA-256. They are
// what makes SUCCEEDED mean "a real, playable, audible MP4 exists and
// is published" instead of "ffmpeg exited 0".
type VerifiedFacts struct {
	SHA256       string  `json:"sha256"`
	SizeBytes    int64   `json:"size_bytes"`
	DurationMS   int64   `json:"duration_ms"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	FPS          float64 `json:"fps"`
	VideoCodec   string  `json:"video_codec"`
	AudioCodec   string  `json:"audio_codec"`
	VideoStreams int     `json:"video_streams"`
	AudioStreams int     `json:"audio_streams"`
}

// ── 10_verify ────────────────────────────────────────────────────────
//
// The §17 gate, fail-closed, in the documented order:
//
//	file exists · size > 0 · video_streams == 1 · audio_streams >= 1 ·
//	duration > 0 · width > 0 · height > 0 · fps > 0 · codecs valid ·
//	SHA-256
//
// A final MP4 WITHOUT an audio stream is NEVER a success: it fails
// with ErrFinalVideoAudioMissing (the historical silent-mux failure
// mode from the runbook) and video.create turns FAILED, not DONE.

func runVerifyStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	finalPath := run.finalVideoPath()
	if finalPath == "" {
		return out, fmt.Errorf("%w: nothing to verify (no assembled or muxed final video)", ErrVerificationFailed)
	}
	facts, err := run.Deps.Probe.Probe(ctx, finalPath)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	if err := verifyFinalVideo(facts); err != nil {
		return out, err
	}
	sha, size, err := digest.SHA256File(finalPath)
	if err != nil {
		return out, fmt.Errorf("%w: sha256: %v", ErrVerificationFailed, err)
	}
	if !digest.IsCanonicalSHA256(sha) {
		return out, fmt.Errorf("%w: non-canonical sha256", ErrVerificationFailed)
	}
	verified := &VerifiedFacts{
		SHA256:       sha,
		SizeBytes:    size,
		DurationMS:   facts.DurationMS,
		Width:        facts.Width,
		Height:       facts.Height,
		FPS:          facts.FPS,
		VideoCodec:   facts.VideoCodec,
		AudioCodec:   facts.AudioCodec,
		VideoStreams: facts.VideoStreamCount,
		AudioStreams: facts.AudioStreamCount,
	}
	out.Verified = verified
	out.Artifacts = []StageArtifactRef{{
		Kind:       string(job.ArtifactKindFinalVideo),
		ContentSHA: sha,
		DurationMS: facts.DurationMS,
		MediaType:  "video",
	}}
	run.Facts.Verified = verified
	return out, nil
}

// verifyFinalVideo is the §17 rule table as one fail-closed chain.
func verifyFinalVideo(facts ProbeFacts) error {
	if facts.SizeBytes <= 0 {
		return fmt.Errorf("%w: final video is empty", ErrVerificationFailed)
	}
	if facts.VideoStreamCount != 1 {
		return fmt.Errorf("%w: video_streams=%d, want exactly 1", ErrVerificationFailed, facts.VideoStreamCount)
	}
	if facts.AudioStreamCount < 1 {
		return fmt.Errorf("%w: audio_streams=%d, want >= 1", ErrFinalVideoAudioMissing, facts.AudioStreamCount)
	}
	if facts.DurationMS <= 0 {
		return fmt.Errorf("%w: duration=%d", ErrVerificationFailed, facts.DurationMS)
	}
	if facts.Width <= 0 || facts.Height <= 0 {
		return fmt.Errorf("%w: dimensions %dx%d", ErrVerificationFailed, facts.Width, facts.Height)
	}
	if facts.FPS <= 0 {
		return fmt.Errorf("%w: fps=%v", ErrVerificationFailed, facts.FPS)
	}
	// Codec validity means exactly ONE thing: the codecs the canonical
	// media contract owns (the render lane encodes with that policy, the
	// canonical assembler copy-concatenates it). No hardcoded encoder
	// vocabulary here — kernel/media is the sole owner of those values
	// (percheck_video_encoder_policy).
	contract := kernelmedia.DefaultAssemblyMediaContractV2()
	if !strings.EqualFold(strings.TrimSpace(facts.VideoCodec), contract.VideoCodec) {
		return fmt.Errorf("%w: video codec %q does not match the canonical media contract (%s)", ErrVerificationFailed, facts.VideoCodec, contract.VideoCodec)
	}
	if !strings.EqualFold(strings.TrimSpace(facts.AudioCodec), contract.AudioCodec) {
		return fmt.Errorf("%w: audio codec %q does not match the canonical media contract (%s)", ErrVerificationFailed, facts.AudioCodec, contract.AudioCodec)
	}
	return nil
}

// finalVideoPath is the §16 output when the mux ran, else the §15
// assembled master (voiceover=false runs keep the clips' own audio).
func (r *Run) finalVideoPath() string {
	if r.Facts.Muxed != nil && r.Facts.Muxed.Path != "" {
		return r.Facts.Muxed.Path
	}
	if r.Facts.Assembled != nil {
		return r.Facts.Assembled.Path
	}
	return ""
}

// ── 11_publish ───────────────────────────────────────────────────────
//
// The §18 stage: the final file goes through the normal artifact spine
// (canonical delivery publisher: Drive upload with size-match
// verification, media registry identity, content hash). The workflow's
// result stores ONLY the durable published identities — never a local
// path. The §19 option-B thumbnail context is recorded here as well:
// the cover stays owned by the caller side (InstaeditLogin on the 51).

func runPublishStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	verified := run.Facts.Verified
	if verified == nil {
		return out, fmt.Errorf("%w: publish requires verified final video facts", ErrPublishFailed)
	}
	finalPath := run.finalVideoPath()
	params := `{"kind":"video.create.final"}`
	assetID, err := digest.ArtifactKeyDigest(verified.SHA256, "video.create.final", params, "v1")
	if err != nil {
		return out, fmt.Errorf("%w: asset identity: %v", ErrPublishFailed, err)
	}
	published, err := run.Deps.Publish.Publish(ctx, PublishRequest{
		LocalRef:              LocalRef{LocalPath: finalPath},
		Filename:              "final_video.mp4",
		MIMEType:              "video/mp4",
		Kind:                  string(job.ArtifactKindFinalVideo),
		SHA256:                verified.SHA256,
		SizeBytes:             verified.SizeBytes,
		DurationMS:            verified.DurationMS,
		AssetID:               assetID,
		Project:               run.Job.Project,
		DeliveryDestinationID: run.Request.DeliveryDestinationID,
	})
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrPublishFailed, err)
	}
	if published.MediaURL == "" || published.DriveFileID == "" {
		return out, fmt.Errorf("%w: publisher returned no media_url/drive_file_id", ErrPublishFailed)
	}
	out.Published = &published
	out.Artifacts = []StageArtifactRef{{
		AssetID:    published.AssetID,
		Kind:       string(job.ArtifactKindFinalVideo),
		ContentSHA: verified.SHA256,
		DriveRef:   DriveRef{DriveFileID: published.DriveFileID},
		DurationMS: verified.DurationMS,
		MediaType:  "video",
	}}
	run.Facts.Published = &published
	return out, nil
}

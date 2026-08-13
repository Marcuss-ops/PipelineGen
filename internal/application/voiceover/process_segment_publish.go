// Package voiceover — Stage 3 publish extraction (PR-VO-USECASE-PROCESS-DRY
// decomposition, per YouTube DoD wave process_segment.go split precedent).
//
// publishStage owns the metadata-building + idempotency-key derivation +
// VoiceoverPublisher.Publish call. The orchestrator's Execute method
// delegates Stage 3 here and uses the returned publishStageResult to
// populate Stage 4's FinalizeCommand.
//
// godlike/06 SSOT: publishStageResult is the SINGLE canonical shape
// carrying Stage 3 output to Stage 4 input. Unexported (package-internal).
package voiceover

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// publishStageResult carries Stage 3 output to Stage 4 input.
type publishStageResult struct {
	MetaJSON []byte
	IdemKey  string
}

// publishStage executes Stage 3: metadata building + idempotency key
// derivation + VoiceoverPublisher.Publish (Drive upload).
//
// Returns publishStageResult carrying MetaJSON + IdemKey for Stage 4's
// FinalizeCommand. Metadata serialization failures are typed permanent
// errors and stop the pipeline before Drive publish; publisher failures
// retain their upload-stage classification.
func (u *ProcessSegmentUseCase) publishStage(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	tts *TTSOutput,
	post *AudioPostOutput,
	log *zap.Logger,
) (*publishStageResult, error) {
	// Build metadata JSON envelope.
	metaBuf := map[string]any{
		"text_hash":    cmd.TextHash,
		"text_preview": textutil.Truncate(cmd.Text, 100),
		"language":     cmd.Language,
		"voice":        out.Voice,
		"strategy":     cmd.Strategy,
		"cleaned_path": out.CleanedPath,
	}
	if cmd.Dest != nil && !cmd.Dest.StyleGroup.IsEmpty() {
		metaBuf["style_group"] = cmd.Dest.StyleGroup
	}
	mergeUserMetadata(metaBuf, cmd.Dest, cmd.Metadata, u.deps.Logger)

	// Semantic enrichment belongs to the canonical pipeline, immediately
	// before publish, so both batch and per-item callers persist the same
	// metadata without maintaining a second tagging path. Tagging is
	// intentionally best-effort to preserve the legacy batch contract:
	// a tagger outage must not turn valid synthesized audio into a failed
	// voiceover, but it is always visible in the structured logs.
	if u.deps.SemanticTagger != nil {
		semantic, err := u.deps.SemanticTagger(ctx, cmd.Text, "", "voiceover", "voiceover")
		if err != nil {
			u.deps.Logger.Warn("voiceover: semantic tagger failed; continuing without semantic enrichment", zap.Error(err))
		} else if semantic != nil {
			metaBuf["search_text"] = semantic.SearchText
			metaBuf["semantic_tags"] = semantic.Tags
			metaBuf["semantic_subjects"] = semantic.Subjects
			metaBuf["semantic_mood"] = semantic.Mood
			out.SearchText = semantic.SearchText
		}
	}

	metaJSON, err := json.Marshal(metaBuf)
	if err != nil {
		return nil, newPipelineErrorCode(
			StageMetadata,
			false,
			FailureMetadataSerialization,
			fmt.Errorf("voiceover metadata serialization: %w", err),
		)
	}

	// Derive deterministic idempotency key.
	var idemKey string
	if cmd.JobID != "" {
		idemKey = BuildVoiceoverIdempotencyKey(cmd.JobID, cmd.Language, cmd.TextHash)
	}

	// Publish to Drive.
	uploadPath := out.CleanedPath
	if uploadPath == "" {
		uploadPath = out.LocalPath
	}

	emitPublish := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "publish", string(cmd.Language))
	fileID, err := u.deps.Publisher.Publish(ctx, VoiceoverPublishCommand{
		ID:             cmd.ID,
		LocalPath:      uploadPath,
		Filename:       cmd.Filename,
		FolderID:       cmd.Dest.FolderID,
		Project:        cmd.Project,
		Language:       string(cmd.Language),
		IdempotencyKey: idemKey,
	})
	if err != nil {
		emitPublish("failed")
		return nil, err
	}
	emitPublish("completed")

	out.DriveFileID = fileID
	out.DriveLink = CanonicalDriveWebURL(fileID)
	out.DownloadLink = CanonicalDriveDownloadURL(fileID)

	// Timing bundle: publish timing.json + optional SRT/VTT projections
	// STRICTLY AFTER the audio upload. required failures fail the segment
	// with a typed PipelineError; best-effort failures degrade the timing
	// status while the audio stays completed; disabled preserves the
	// legacy behavior (no timing work at all).
	timingRes, err := u.publishTimingBundle(ctx, cmd, out, tts, post, uploadPath, log)
	if err != nil {
		return nil, err
	}
	out.Timing = timingRes

	return &publishStageResult{MetaJSON: metaJSON, IdemKey: idemKey}, nil
}

// publishTimingBundle builds and publishes the canonical timing bundle
// (timing.json SSOT + optional SRT/VTT projections) after the audio
// upload succeeded, applying the required/best-effort/disabled policy:
//
//	disabled    → no timing work; returns (nil, nil) — legacy behavior.
//	required    → ANY timing failure fails the segment with a typed
//	              PipelineError (never a fake success).
//	best_effort → timing failures degrade to TimingStatusUnavailable or
//	              TimingStatusFailed while the audio stays completed.
//
// The artifact binds to the FINAL audio (uploadPath — the exact bytes
// uploaded to Drive) via SHA-256 so downstream consumers can prove
// "this timing belongs to THIS mp3". All links come from the real
// Publisher file IDs — never hand-built.
func (u *ProcessSegmentUseCase) publishTimingBundle(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	tts *TTSOutput,
	post *AudioPostOutput,
	uploadPath string,
	log *zap.Logger,
) (*VoiceoverTimingResult, error) {
	policy := audio.DefaultTimingRequest()
	if cmd.Timing != nil {
		policy = cmd.Timing.Normalized()
	}
	if err := policy.Validate(); err != nil {
		return nil, newPipelineErrorCode(StageTiming, false, FailureTimingBuild,
			fmt.Errorf("voiceover timing policy: %w", err))
	}
	if policy.Mode == audio.TimingDisabled {
		return nil, nil
	}

	emitTiming := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "timing", string(cmd.Language))

	if len(tts.WordBoundaries) == 0 {
		if policy.Mode == audio.TimingRequired {
			emitTiming("failed")
			return nil, newPipelineErrorCode(StageTiming, false, FailureTimingUnavailable,
				fmt.Errorf("%s: TTS produced no word boundaries for required timing", FailureTimingUnavailable))
		}
		emitTiming("unavailable")
		return &VoiceoverTimingResult{Status: TimingStatusUnavailable}, nil
	}

	// Silence removal: raw provider boundaries describe the PRE-clean
	// timeline. With an edit map + final duration from the post-processor
	// the boundaries are remapped onto the cleaned timeline; without them
	// accurate timestamps are impossible, so required fails closed and
	// best-effort degrades explicitly (never fabricate timestamps).
	var postEditMap []audio.AudioEdit
	if cmd.RemoveSilence {
		if post == nil || len(post.EditMap) == 0 || post.DurationUS <= 0 {
			if policy.Mode == audio.TimingRequired {
				emitTiming("failed")
				return nil, newPipelineErrorCode(StageTiming, false, FailureTimingIncompatible,
					fmt.Errorf("timing required + remove_silence: the audio post-processor reported no edit map, so accurate timestamps are impossible"))
			}
			emitTiming("unavailable")
			return &VoiceoverTimingResult{Status: TimingStatusUnavailable}, nil
		}
		postEditMap = post.EditMap
	}

	// Build the canonical artifact bound to the FINAL audio bytes.
	words := make([]audio.SpeechWordTiming, 0, len(tts.WordBoundaries))
	for i, b := range tts.WordBoundaries {
		words = append(words, audio.SpeechWordTiming{
			Index:   i,
			Text:    b.Text,
			StartUS: b.StartUS,
			EndUS:   b.EndUS,
		})
	}
	durationUS := tts.Duration.Microseconds()
	if len(postEditMap) > 0 {
		remapped, err := audio.RemapSpeechTiming(words, postEditMap)
		if err != nil {
			return u.timingBuildFailure(policy, fmt.Errorf("remap speech timing after silence removal: %w", err))
		}
		words = remapped
		durationUS = post.DurationUS
	}
	audioSHA, err := files.HashFile(uploadPath, sha256.New())
	if err != nil {
		return u.timingBuildFailure(policy, fmt.Errorf("hash final audio %q: %w", uploadPath, err))
	}
	artifact, err := audio.BuildSpeechTimingArtifact(
		tts.Provider,
		string(cmd.Language),
		out.Voice,
		files.SHA256String(cmd.Text),
		audioSHA,
		durationUS,
		words,
	)
	if err != nil {
		return u.timingBuildFailure(policy, err)
	}

	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		return u.timingBuildFailure(policy, fmt.Errorf("marshal timing artifact: %w", err))
	}

	projections := map[audio.TimingFormat][]byte{}
	if policy.HasFormat(audio.TimingJSON) {
		projections[audio.TimingJSON] = artifactJSON
	}
	if policy.HasFormat(audio.TimingSRT) {
		srt, err := audio.RenderSRT(*artifact, audio.CueOptions{})
		if err != nil {
			return u.timingBuildFailure(policy, fmt.Errorf("render SRT: %w", err))
		}
		projections[audio.TimingSRT] = srt
	}
	if policy.HasFormat(audio.TimingVTT) {
		vtt, err := audio.RenderVTT(*artifact, audio.CueOptions{})
		if err != nil {
			return u.timingBuildFailure(policy, fmt.Errorf("render VTT: %w", err))
		}
		projections[audio.TimingVTT] = vtt
	}

	// Publish each projection through the canonical Publisher (real
	// verified file IDs → links), writing a local staging file first.
	res := &VoiceoverTimingResult{Status: TimingStatusCompleted}
	base := timingBaseName(cmd.Filename)
	dir := cmd.Dest.FolderPath
	var written []string
	defer func() {
		for _, p := range written {
			_ = os.Remove(p)
		}
	}()

	for format, data := range projections {
		filename := timingProjectionFilename(base, format)
		localPath := filepath.Join(dir, filename)
		if err := os.WriteFile(localPath, data, 0o644); err != nil {
			return u.timingPublishFailure(policy, fmt.Errorf("write timing projection %s: %w", filename, err))
		}
		written = append(written, localPath)

		fileID, err := u.deps.Publisher.Publish(ctx, VoiceoverPublishCommand{
			ID:             cmd.ID + "-timing-" + string(format),
			LocalPath:      localPath,
			Filename:       filename,
			FolderID:       cmd.Dest.FolderID,
			Project:        cmd.Project,
			Language:       string(cmd.Language),
			IdempotencyKey: BuildVoiceoverTimingIdempotencyKey(cmd.JobID, cmd.Language, cmd.TextHash, string(format)),
		})
		if err != nil {
			return u.timingPublishFailure(policy, fmt.Errorf("publish timing projection %s: %w", filename, err))
		}
		switch format {
		case audio.TimingJSON:
			res.JSONLink = CanonicalDriveWebURL(fileID)
		case audio.TimingSRT:
			res.SRTLink = CanonicalDriveWebURL(fileID)
		case audio.TimingVTT:
			res.VTTLink = CanonicalDriveWebURL(fileID)
		}
	}

	res.BoundaryMode = string(artifact.BoundaryMode)
	res.WordCount = len(artifact.Words)
	res.DurationUS = artifact.DurationUS
	res.TextSHA256 = artifact.TextSHA256
	res.AudioSHA256 = artifact.AudioSHA256
	emitTiming("completed")
	return res, nil
}

// timingBuildFailure converts a timing artifact build failure according
// to the policy: required fails the segment permanently with
// FailureTimingBuild; best-effort degrades the timing status to failed
// while the audio stays completed.
func (u *ProcessSegmentUseCase) timingBuildFailure(policy audio.TimingRequest, err error) (*VoiceoverTimingResult, error) {
	if policy.Mode == audio.TimingRequired {
		return nil, newPipelineErrorCode(StageTiming, false, FailureTimingBuild, err)
	}
	return &VoiceoverTimingResult{Status: TimingStatusFailed}, nil
}

// timingPublishFailure converts a timing projection upload failure
// according to the policy: required fails the segment permanently with
// FailureTimingPublish (the audio is already on Drive — the orphan
// cleanup path recovers it); best-effort degrades the timing status to
// failed while the audio stays completed.
func (u *ProcessSegmentUseCase) timingPublishFailure(policy audio.TimingRequest, err error) (*VoiceoverTimingResult, error) {
	if policy.Mode == audio.TimingRequired {
		return nil, newPipelineErrorCode(StageTiming, false, FailureTimingPublish, err)
	}
	return &VoiceoverTimingResult{Status: TimingStatusFailed}, nil
}

// timingBaseName strips the extension from the audio filename so the
// timing projections share the audio base (scene-0-it.mp3 → scene-0-it).
func timingBaseName(filename string) string {
	if i := strings.LastIndexByte(filename, '.'); i > 0 {
		return filename[:i]
	}
	return filename
}

// timingProjectionFilename derives the canonical Drive filename for a
// timing projection format.
func timingProjectionFilename(base string, format audio.TimingFormat) string {
	switch format {
	case audio.TimingJSON:
		return base + "-timing.json"
	case audio.TimingSRT:
		return base + ".srt"
	case audio.TimingVTT:
		return base + ".vtt"
	}
	return base + "-" + string(format)
}

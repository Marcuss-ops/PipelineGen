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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
	metaBuf, err := u.buildPublishMetadata(ctx, cmd, out)
	if err != nil {
		return nil, err
	}

	idemKey := u.voiceoverIdempotencyKey(cmd)

	// Delivery upload ONLY when a Publisher is wired. The video pipeline is
	// local-only (waste-cut 2026-10-24): per-scene audio + timing are
	// certified LOCAL artifacts (intermediates of the video composition),
	// never delivery products — so there is nothing to upload here.
	uploadPath := out.CleanedPath
	if uploadPath == "" {
		uploadPath = out.LocalPath
	}
	fileID, err := u.publishAudio(ctx, cmd, out, uploadPath, idemKey, log)
	if err != nil {
		return nil, err
	}

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
	injectTimingMetadata(metaBuf, timingRes)

	if fileID != "" {
		metaBuf["drive_file_id"] = fileID
		metaBuf["drive_link"] = out.DriveLink
	}

	metaJSON, err := marshalVoiceoverMetadata(metaBuf)
	if err != nil {
		return nil, err
	}

	return &publishStageResult{MetaJSON: metaJSON, IdemKey: idemKey}, nil
}

// buildPublishMetadata builds the metadata envelope for the voiceover row,
// merges the caller's metadata overlay, validates serializability BEFORE any
// external side effect, and applies best-effort semantic enrichment.
func (u *ProcessSegmentUseCase) buildPublishMetadata(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
) (map[string]any, error) {
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
	// Persist the silence-cleanup summary alongside the voiceover row so
	// the cleaned-duration proof survives beyond the in-memory result.
	if out.SilenceCleanup != nil {
		metaBuf["silence_cleanup"] = out.SilenceCleanup
	}
	mergeUserMetadata(metaBuf, cmd.Dest, cmd.Metadata, u.deps.Logger)
	// Validate caller metadata before any external side effect. The final
	// envelope is marshalled again after publish-time fields are added,
	// but unsupported values must fail at the metadata stage, before Drive.
	if _, err := marshalVoiceoverMetadata(metaBuf); err != nil {
		return nil, err
	}

	// Semantic enrichment belongs to the canonical pipeline, immediately
	// before publish, so both batch and per-item callers persist the same
	// metadata without maintaining a second tagging path. Tagging is
	// intentionally best-effort to preserve the legacy batch contract:
	// a tagger outage must not turn valid synthesized audio into a failed
	// voiceover, but it is always visible in the structured logs.
	if u.deps.Enrichment.SemanticTagger != nil {
		semantic, err := u.deps.Enrichment.SemanticTagger(ctx, cmd.Text, "", "voiceover", "voiceover")
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
	return metaBuf, nil
}

// voiceoverIdempotencyKey derives the deterministic audio idempotency key.
// Empty JobID preserves the legacy "no key" behavior (the gate is skipped).
func (u *ProcessSegmentUseCase) voiceoverIdempotencyKey(cmd *ProcessSegmentCommand) string {
	if cmd.JobID == "" {
		return ""
	}
	return BuildVoiceoverIdempotencyKey(
		cmd.JobID,
		cmd.Language,
		cmd.TextHash,
		idempotencyPolicyFingerprint(u.deps.TTSProvider, cmd.Language, cmd.Timing, cmd.RemoveSilence, voiceoverPostPolicy(cmd.TTSRate, cmd.SilencePolicy)),
	)
}

// publishAudio uploads the final audio when a Publisher is wired and stamps
// the verified file ID + canonical links on the result. Local-only
// compositions skip the upload and return an empty file ID.
func (u *ProcessSegmentUseCase) publishAudio(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	uploadPath, idemKey string,
	log *zap.Logger,
) (string, error) {
	emitPublish := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "publish", string(cmd.Language))
	if u.deps.Publisher == nil {
		emitPublish("skipped_local_only")
		return "", nil
	}
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
		return "", err
	}
	emitPublish("completed")
	out.DriveFileID = fileID
	out.DriveLink = CanonicalDriveWebURL(fileID)
	out.DownloadLink = CanonicalDriveDownloadURL(fileID)
	return fileID, nil
}

// injectTimingMetadata copies the timing summary into the voiceover metadata
// envelope. Links are recorded only when the projection was delivered; local
// paths ALWAYS record where the certified SSOT lives so the cross-run cache
// can rehydrate word timing without any network.
func injectTimingMetadata(metaBuf map[string]any, res *VoiceoverTimingResult) {
	if res == nil {
		return
	}
	if res.JSONLink != "" {
		metaBuf["timing_json_link"] = res.JSONLink
	}
	if res.SRTLink != "" {
		metaBuf["timing_srt_link"] = res.SRTLink
	}
	if res.VTTLink != "" {
		metaBuf["timing_vtt_link"] = res.VTTLink
	}
	if res.JSONPath != "" {
		metaBuf["timing_json_path"] = res.JSONPath
	}
	if res.SRTPath != "" {
		metaBuf["timing_srt_path"] = res.SRTPath
	}
	if res.VTTPath != "" {
		metaBuf["timing_vtt_path"] = res.VTTPath
	}
	if res.AudioSHA256 != "" {
		metaBuf["audio_sha256"] = res.AudioSHA256
	}
	metaBuf["timing_boundary_mode"] = res.BoundaryMode
	metaBuf["timing_word_count"] = res.WordCount
	metaBuf["timing_duration_us"] = res.DurationUS
	metaBuf["timing_status"] = res.Status
}

// marshalVoiceoverMetadata serializes the envelope and maps any failure to
// the typed permanent metadata error, so both the pre-publish validation and
// the final serialization keep identical failure semantics.
func marshalVoiceoverMetadata(metaBuf map[string]any) ([]byte, error) {
	metaJSON, err := json.Marshal(metaBuf)
	if err != nil {
		return nil, newPipelineErrorCode(
			StageMetadata,
			false,
			FailureMetadataSerialization,
			fmt.Errorf("voiceover metadata serialization: %w", err),
		)
	}
	return metaJSON, nil
}

// publishTimingBundle builds the canonical timing bundle (timing.json SSOT
// + optional SRT/VTT projections), ALWAYS persisting the certified local
// copy next to the audio, and publishing remote copies ONLY when a
// Publisher is wired (delivery surfaces). The video pipeline is local-only.
// It runs after the optional upload, applying the
// required/best-effort/disabled policy:
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
	policy := timingPolicyFor(cmd)
	if err := policy.Validate(); err != nil {
		return nil, newPipelineErrorCode(StageTiming, false, FailureTimingBuild,
			fmt.Errorf("voiceover timing policy: %w", err))
	}
	if policy.Mode == audio.TimingDisabled {
		return nil, nil
	}

	emitTiming := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "timing", string(cmd.Language))

	// Raw provider boundaries describe the PRE-clean timeline. With an edit
	// map + final duration from the post-processor the boundaries are remapped
	// onto the cleaned timeline; without them accurate timestamps are
	// impossible, so required fails closed and best-effort degrades explicitly
	// (never fabricate timestamps). An empty edit map is the identity
	// transform: the final duration alone still binds the artifact to the
	// post-processed audio.
	if blocked := timingInputBlocker(tts, post, cmd.RemoveSilence); blocked != nil {
		if policy.Mode == audio.TimingRequired {
			emitTiming("failed")
			timingEvent(log, "voiceover.timing.failed", cmd, tts.Provider, tts.BoundaryMode, len(tts.WordBoundaries), tts.Duration.Microseconds())
			return nil, newPipelineErrorCode(StageTiming, false, blocked.code, fmt.Errorf("%s", blocked.message))
		}
		emitTiming("unavailable")
		return &VoiceoverTimingResult{Status: TimingStatusUnavailable}, nil
	}

	// Build the canonical artifact bound to the FINAL audio bytes.
	timeline, err := buildTimingWordTimeline(tts, post, cmd.RemoveSilence)
	if err != nil {
		return u.timingBuildFailure(cmd, log, policy, fmt.Errorf("remap speech timing after silence removal: %w", err))
	}
	if timeline.DroppedWords > 0 {
		log.Warn("dropping TTS timing words beyond encoded audio duration",
			zap.String("voiceover_id", cmd.ID),
			zap.Int("dropped_words", timeline.DroppedWords),
			zap.Int64("audio_duration_us", timeline.DurationUS),
		)
	}
	if timeline.ClampedOverrunUS > 0 {
		log.Warn("clamping small TTS final-word timing overrun to encoded audio duration",
			zap.String("voiceover_id", cmd.ID),
			zap.Int64("overrun_us", timeline.ClampedOverrunUS),
			zap.Int64("audio_duration_us", timeline.DurationUS),
		)
	}
	artifact, err := buildVoiceoverTimingArtifact(cmd, out, tts, uploadPath, timeline)
	if err != nil {
		return u.timingBuildFailure(cmd, log, policy, err)
	}

	// Raw provider boundaries are now normalized into the canonical SSOT
	// artifact. Emit the summary event (never the per-word array).
	timingEvent(log, "voiceover.timing.normalized", cmd, artifact.Provider, artifact.BoundaryMode, len(artifact.Words), artifact.DurationUS)

	artifactJSON, err := json.Marshal(artifact)
	if err != nil {
		return u.timingBuildFailure(cmd, log, policy, fmt.Errorf("marshal timing artifact: %w", err))
	}

	projections, err := renderTimingProjections(artifact, artifactJSON, policy)
	if err != nil {
		return u.timingBuildFailure(cmd, log, policy, err)
	}

	base := timingBaseName(cmd.Filename)
	local, err := persistTimingProjections(cmd.Dest.FolderPath, base, artifactJSON, projections)
	if err != nil {
		return u.timingBuildFailure(cmd, log, policy, err)
	}

	// ── Remote delivery ONLY when a Publisher is wired ────────────
	// The video pipeline wires no publisher: local artifacts are the
	// product and there is nothing to upload (waste-cut 2026-10-24).
	// Delivery surfaces publish the SAME local files just persisted —
	// one write per artifact, no staging copies.
	links, err := u.publishTimingProjections(ctx, cmd, base, local)
	if err != nil {
		return u.timingPublishFailure(cmd, log, policy, err)
	}

	res := &VoiceoverTimingResult{
		Status:   TimingStatusCompleted,
		JSONPath: local.JSONPath,
		SRTPath:  local.SRTPath,
		VTTPath:  local.VTTPath,
		JSONLink: links.JSON,
		SRTLink:  links.SRT,
		VTTLink:  links.VTT,
	}

	res.BoundaryMode = string(artifact.BoundaryMode)
	res.WordCount = len(artifact.Words)
	res.DurationUS = artifact.DurationUS
	res.TextSHA256 = artifact.TextSHA256
	res.AudioSHA256 = artifact.AudioSHA256
	// Carry the full canonical artifact in-memory (json:"-") so consumers
	// that need the word-level timing receive the SSOT verbatim. The wire
	// result keeps only the summary fields above.
	res.Artifact = artifact

	moments, err := u.projectTimingMoments(ctx, cmd, artifact)
	if err != nil {
		return u.timingBuildFailure(cmd, log, policy, err)
	}
	res.Moments = moments

	timingEvent(log, "voiceover.timing.published", cmd, artifact.Provider, artifact.BoundaryMode, res.WordCount, res.DurationUS)

	emitTiming("completed")
	return res, nil
}

// timingInputBlock is a typed reason the TTS/post outputs cannot yield an
// accurate timeline. code is the failure the required policy surfaces.
type timingInputBlock struct {
	code    FailureCode
	message string
}

// timingInputBlocker returns a blocking reason when the outputs cannot yield
// accurate timing, or nil when they can. The messages are operator-facing and
// stable: tests and runbooks match on them.
func timingInputBlocker(tts *TTSOutput, post *AudioPostOutput, removeSilence bool) *timingInputBlock {
	if len(tts.WordBoundaries) == 0 {
		return &timingInputBlock{
			code:    FailureTimingUnavailable,
			message: fmt.Sprintf("%s: TTS produced no word boundaries for required timing", FailureTimingUnavailable),
		}
	}
	if removeSilence && (post == nil || post.DurationUS <= 0) {
		return &timingInputBlock{
			code:    FailureTimingIncompatible,
			message: "timing required + remove_silence: the audio post-processor reported no edit map, so accurate timestamps are impossible",
		}
	}
	return nil
}

// projectTimingMoments locates the requested moment annotations. The Rust
// worker owns the word/phrase match: Go sends every unique annotation surface
// once, with allow_missing so a value the speech never said is skipped, and
// joins the returned spans back to the queries. A missing aligner fails closed
// only when moments were requested; no Go matcher is a fallback.
func (u *ProcessSegmentUseCase) projectTimingMoments(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	artifact *audio.SpeechTimingArtifact,
) ([]audio.Moment, error) {
	if len(cmd.Moments) == 0 {
		return nil, nil
	}
	if u.deps.Enrichment.PhraseAligner == nil {
		return nil, fmt.Errorf("locate moments: phrase aligner is required")
	}
	alignments, err := u.deps.Enrichment.PhraseAligner.AlignPhrases(ctx, audio.PhraseAlignmentRequest{
		Timing: *artifact, Phrases: momentQueryValues(cmd.Moments), AllowMissing: true, AllOccurrences: true,
	})
	if err != nil {
		return nil, fmt.Errorf("locate moments: %w", err)
	}
	moments, err := audio.ProjectMoments(*artifact, cmd.Moments, alignments)
	if err != nil {
		return nil, fmt.Errorf("locate moments: %w", err)
	}
	return moments, nil
}

// timingWordTimeline is the canonical word timeline ready for artifact
// construction: provider boundaries (optionally remapped onto the cleaned
// timeline) normalized against the final audio duration.
type timingWordTimeline struct {
	Words            []audio.SpeechWordTiming
	DurationUS       int64
	DroppedWords     int
	ClampedOverrunUS int64
}

// buildTimingWordTimeline converts raw provider boundaries into the canonical
// timeline. When silence removal emitted an edit map, the words are remapped
// onto the cleaned timeline and the post-processed duration becomes
// authoritative; otherwise the TTS duration is used. Trailing words beyond
// the encoded audio are dropped and a small final-word overrun is clamped (the
// full rule lives in normalizeFinalWordTiming).
func buildTimingWordTimeline(tts *TTSOutput, post *AudioPostOutput, removeSilence bool) (timingWordTimeline, error) {
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
	if removeSilence && post != nil && len(post.EditMap) > 0 {
		remapped, err := audio.RemapSpeechTiming(words, post.EditMap)
		if err != nil {
			return timingWordTimeline{}, err
		}
		words = remapped
		durationUS = post.DurationUS
	}
	normalized, dropped, clamped := normalizeFinalWordTiming(words, durationUS)
	return timingWordTimeline{
		Words:            normalized,
		DurationUS:       durationUS,
		DroppedWords:     dropped,
		ClampedOverrunUS: clamped,
	}, nil
}

// buildVoiceoverTimingArtifact binds the canonical artifact to the FINAL audio
// bytes (the exact delivered file) and to the source text.
func buildVoiceoverTimingArtifact(
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	tts *TTSOutput,
	uploadPath string,
	timeline timingWordTimeline,
) (*audio.SpeechTimingArtifact, error) {
	audioSHA, _, err := digest.SHA256File(uploadPath)
	if err != nil {
		return nil, fmt.Errorf("hash final audio %q: %w", uploadPath, err)
	}
	return audio.BuildSpeechTimingArtifact(
		tts.Provider,
		string(cmd.Language),
		out.Voice,
		digest.SHA256String(cmd.Text),
		audioSHA,
		timeline.DurationUS,
		timeline.Words,
	)
}

// renderTimingProjections renders the requested delivery formats from the
// canonical artifact. timing.json is the SSOT bytes already marshalled by the
// caller, so it is never re-serialized here.
func renderTimingProjections(
	artifact *audio.SpeechTimingArtifact,
	artifactJSON []byte,
	policy audio.TimingRequest,
) (map[audio.TimingFormat][]byte, error) {
	projections := map[audio.TimingFormat][]byte{}
	if policy.HasFormat(audio.TimingJSON) {
		projections[audio.TimingJSON] = artifactJSON
	}
	if policy.HasFormat(audio.TimingSRT) {
		srt, err := audio.RenderSRT(*artifact, audio.CueOptions{})
		if err != nil {
			return nil, fmt.Errorf("render SRT: %w", err)
		}
		projections[audio.TimingSRT] = srt
	}
	if policy.HasFormat(audio.TimingVTT) {
		vtt, err := audio.RenderVTT(*artifact, audio.CueOptions{})
		if err != nil {
			return nil, fmt.Errorf("render VTT: %w", err)
		}
		projections[audio.TimingVTT] = vtt
	}
	return projections, nil
}

// timingLocalFiles records the durable local projections. timing.json is
// ALWAYS persisted; SRT/VTT only when the policy requested those formats.
type timingLocalFiles struct {
	JSONPath string
	SRTPath  string
	VTTPath  string
	Paths    map[audio.TimingFormat]string
}

// persistTimingProjections writes the certified projections next to the
// audio. These files are the durable SSOT (cross-run cache, retry, overlay
// timing): they are never deleted and never rebuilt from a remote link.
func persistTimingProjections(
	dir, base string,
	artifactJSON []byte,
	projections map[audio.TimingFormat][]byte,
) (timingLocalFiles, error) {
	local := timingLocalFiles{Paths: make(map[audio.TimingFormat]string, len(projections))}
	timingJSONPath := filepath.Join(dir, timingProjectionFilename(base, audio.TimingJSON))
	if err := os.WriteFile(timingJSONPath, artifactJSON, 0o644); err != nil {
		return timingLocalFiles{}, fmt.Errorf("write timing artifact %s: %w", timingJSONPath, err)
	}
	local.JSONPath = timingJSONPath
	// Paths mirrors the REQUESTED delivery set: the JSON SSOT is always
	// written locally, but it is only uploaded when the policy requested it.
	if _, requested := projections[audio.TimingJSON]; requested {
		local.Paths[audio.TimingJSON] = timingJSONPath
	}
	for format, data := range projections {
		if format == audio.TimingJSON {
			continue // SSOT JSON already persisted above
		}
		filename := timingProjectionFilename(base, format)
		localPath := filepath.Join(dir, filename)
		if err := os.WriteFile(localPath, data, 0o644); err != nil {
			return timingLocalFiles{}, fmt.Errorf("write timing projection %s: %w", filename, err)
		}
		local.Paths[format] = localPath
		switch format {
		case audio.TimingSRT:
			local.SRTPath = localPath
		case audio.TimingVTT:
			local.VTTPath = localPath
		}
	}
	return local, nil
}

// timingPublishedLinks carries the verified remote links, one per delivered
// projection. Empty means the format was not requested or not delivered.
type timingPublishedLinks struct {
	JSON string
	SRT  string
	VTT  string
}

// publishTimingProjections uploads the persisted projections ONLY when a
// Publisher is wired. JSON, SRT and VTT are independent Drive writes, so they
// run concurrently (bounded at 3) and the first failure wins. The video
// pipeline wires no publisher: local artifacts are the product.
func (u *ProcessSegmentUseCase) publishTimingProjections(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	base string,
	local timingLocalFiles,
) (timingPublishedLinks, error) {
	if u.deps.Publisher == nil || len(local.Paths) == 0 {
		return timingPublishedLinks{}, nil
	}
	const timingPublishConcurrency = 3
	type timingPubResult struct {
		format audio.TimingFormat
		fileID string
	}
	timingPubSem := make(chan struct{}, timingPublishConcurrency)
	var timingPubWg sync.WaitGroup
	timingPubResults := make([]timingPubResult, 0, len(local.Paths))
	var timingPubMu sync.Mutex
	var timingPubFirstErr error
	policyFingerprint := idempotencyPolicyFingerprint(
		u.deps.TTSProvider, cmd.Language, cmd.Timing, cmd.RemoveSilence,
		voiceoverPostPolicy(cmd.TTSRate, cmd.SilencePolicy),
	)

	for format := range local.Paths {
		format := format
		timingPubWg.Add(1)
		go func() {
			defer timingPubWg.Done()
			timingPubSem <- struct{}{}
			defer func() { <-timingPubSem }()

			filename := timingProjectionFilename(base, format)
			fileID, err := u.deps.Publisher.Publish(ctx, VoiceoverPublishCommand{
				ID:             cmd.ID + "-timing-" + string(format),
				LocalPath:      local.Paths[format],
				Filename:       filename,
				FolderID:       cmd.Dest.FolderID,
				Project:        cmd.Project,
				Language:       string(cmd.Language),
				IdempotencyKey: BuildVoiceoverTimingIdempotencyKey(cmd.JobID, cmd.Language, cmd.TextHash, policyFingerprint, string(format)),
			})
			if err != nil {
				timingPubMu.Lock()
				if timingPubFirstErr == nil {
					timingPubFirstErr = fmt.Errorf("publish timing projection %s: %w", filename, err)
				}
				timingPubMu.Unlock()
				return
			}
			timingPubMu.Lock()
			timingPubResults = append(timingPubResults, timingPubResult{format: format, fileID: fileID})
			timingPubMu.Unlock()
		}()
	}
	timingPubWg.Wait()

	if timingPubFirstErr != nil {
		return timingPublishedLinks{}, timingPubFirstErr
	}

	var links timingPublishedLinks
	for _, r := range timingPubResults {
		switch r.format {
		case audio.TimingJSON:
			links.JSON = CanonicalDriveWebURL(r.fileID)
		case audio.TimingSRT:
			links.SRT = CanonicalDriveWebURL(r.fileID)
		case audio.TimingVTT:
			links.VTT = CanonicalDriveWebURL(r.fileID)
		}
	}
	return links, nil
}

// momentQueryValues returns the unique, non-empty annotation surfaces to send
// to the Rust aligner in one batch. Query order is preserved; duplicates are
// removed by normalized text.
func momentQueryValues(queries []audio.MomentQuery) []string {
	seen := make(map[string]struct{}, len(queries))
	values := make([]string, 0, len(queries))
	for _, query := range queries {
		value := strings.TrimSpace(query.Value)
		if value == "" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, value)
	}
	return values
}

// timingBuildFailure converts a timing artifact build failure according
// to the policy: required fails the segment permanently with
// FailureTimingBuild; best-effort degrades the timing status to failed
// while the audio stays completed.
func (u *ProcessSegmentUseCase) timingBuildFailure(cmd *ProcessSegmentCommand, log *zap.Logger, policy audio.TimingRequest, err error) (*VoiceoverTimingResult, error) {
	timingEvent(log, "voiceover.timing.failed", cmd, "", "", 0, 0)
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
func (u *ProcessSegmentUseCase) timingPublishFailure(cmd *ProcessSegmentCommand, log *zap.Logger, policy audio.TimingRequest, err error) (*VoiceoverTimingResult, error) {
	timingEvent(log, "voiceover.timing.failed", cmd, "", "", 0, 0)
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

// timingEvent emits one structured voiceover.timing.* lifecycle event. It
// carries summary metadata only — scene id, language, provider, boundary
// mode, word count and duration — and NEVER the per-word array, so the log
// volume stays bounded regardless of scene length (godlike/07: do not log
// thousands of word boundaries).
func timingEvent(log *zap.Logger, event string, cmd *ProcessSegmentCommand, provider string, boundaryMode audio.BoundaryMode, wordCount int, durationUS int64) {
	if log == nil || cmd == nil {
		return
	}
	fields := []zap.Field{
		zap.String("scene_id", cmd.ID),
		zap.String("language", string(cmd.Language)),
	}
	if cmd.Project != "" {
		fields = append(fields, zap.String("project", cmd.Project))
	}
	if provider != "" {
		fields = append(fields, zap.String("provider", provider))
	}
	if boundaryMode != "" {
		fields = append(fields, zap.String("boundary_mode", string(boundaryMode)))
	}
	if wordCount > 0 {
		fields = append(fields, zap.Int("word_count", wordCount))
	}
	if durationUS > 0 {
		fields = append(fields, zap.Int64("duration_us", durationUS))
	}
	log.Info(event, fields...)
}

// silenceCleanupEvent emits the structured voiceover.silence_cleanup event
// when silence removal produced a cleanup report. It carries only the four
// summary durations (original, leading trim, trailing trim, clean) so
// operators can verify the timeline used the cleaned duration — never any
// audio content or per-edit detail.
func silenceCleanupEvent(log *zap.Logger, cmd *ProcessSegmentCommand, report *SilenceCleanupReport) {
	if log == nil || cmd == nil || report == nil {
		return
	}
	fields := []zap.Field{
		zap.String("scene_id", cmd.ID),
		zap.String("language", string(cmd.Language)),
		zap.Int64("original_duration_us", report.OriginalDurationUS),
		zap.Int64("trim_start_us", report.TrimStartUS),
		zap.Int64("trim_end_us", report.TrimEndUS),
		zap.Int64("clean_duration_us", report.CleanDurationUS),
	}
	if cmd.Project != "" {
		fields = append(fields, zap.String("project", cmd.Project))
	}
	log.Info("voiceover.silence_cleanup", fields...)
}

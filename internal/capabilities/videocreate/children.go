package videocreate

import (
	"encoding/json"
	"fmt"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	voicesvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Child-job identity (the §8 idempotency contract) ──────────────────
//
// Every child the workflow fans out to derives its idempotency key from
// ONE root key (the caller's idempotency key, e.g.
// "calendar:item-128:2026-09-24"). Re-running video.create — a retry, a
// broker redelivery, a restart mid-render — therefore converges on the
// SAME children:
//
//	calendar:128:2026-09-24:script
//	calendar:128:2026-09-24:youtube:scene:001
//	calendar:128:2026-09-24:stock:scene:003
//	calendar:128:2026-09-24:voiceover
//	calendar:128:2026-09-24:render:scene:001
//
// The broker's (client_id, idempotency_key) UNIQUE pair answers a
// duplicate enqueue with the EXISTING child, so a replayed workflow
// cannot create a second script, re-download media, re-generate the
// voiceover or produce two videos. This is what makes the 30-day
// calendar safe to retry.

// ChildClientID is the canonical client identity the workflow enqueues
// its children under. Children are internal M2M consumers of the job
// registry, and this constant is the dedup namespace that separates
// their (client_id, idempotency_key) pairs from external submitters.
const ChildClientID = "internal:video.create"

// RootKey returns the idempotency root for a job. The caller-controlled
// M2M idempotency key is the root when present (it is exactly the
// calendar item identity); an internal submit with no key falls back to
// the job id, which is equally stable across retries of the SAME job.
func RootKey(idempotencyKey, jobID string) string {
	if k := strings.TrimSpace(idempotencyKey); k != "" {
		return k
	}
	return strings.TrimSpace(jobID)
}

// ChildKey derives one child idempotency key from the workflow root.
// The suffixes are the §8 canonical vocabulary; scene indexes are
// 1-based and zero-padded to 3 so the keys sort in timeline order.
func ChildKey(root, suffix string) string {
	return strings.TrimSpace(root) + ":" + suffix
}

// SceneChildKey derives a per-scene child key ("…:youtube:scene:001").
func SceneChildKey(root, family string, sceneIndex int) string {
	return ChildKey(root, fmt.Sprintf("%s:scene:%03d", family, sceneIndex))
}

// ChildCorrelationID scopes one child's correlation id away from its
// parent's. The broker dedupes on (type, correlation_id): a child that
// inherited the parent's running correlation id would resolve to the
// PARENT and never be created (the clip.render submit→settle lesson, in
// continuation.go::SettleCorrelationID). The scope is deterministic so a
// redelivered parent addresses the identical child.
func ChildCorrelationID(parentCorrelationID, jobType, childKey string) string {
	base := strings.TrimSpace(parentCorrelationID)
	if base == "" {
		base = jobType
	}
	return base + ":vc:" + childKey
}

// ── Typed child payloads: the REAL handler wire contracts ─────────────
//
// godlike/06 SSOT: every builder below returns the child family's OWN
// canonical request type — the exact struct the registered handler
// decodes from job.Payload. The workflow's payload can therefore never
// drift from the contract the child speaks; children_contracts_test.go
// pins the round trip against the handler-side decoders.
//
// Infrastructure facts (worker addresses, engine URLs, local paths) are
// deliberately absent: they belong to the runtime executing the child.

// ScriptChildPayload drives the script.generate child (the §9 first
// stage) with its REAL wire contract: one GenerationEnvelopeV2 item
// (kernel/script). The item asks for the canonical timeline and — when
// the request wants voiceover — for the per-scene voiceovers plus the
// COMBINED_TIMELINE canonical audio master, which the §9 reuse rule
// then carries into 09_audio_mux without re-doing the work.
//
// Source is text/topic based (the workflow's request is a topic), and
// target_words is derived from the requested duration at the canonical
// narration pace (~2 words/second) so the script's length tracks the
// requested video length.
func ScriptChildPayload(req appjobs.VideoCreatePayload, project, videoName, itemID string) (scriptpkg.GenerationEnvelopeV2, error) {
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "en"
	}
	targetWords := req.DurationSeconds * 2
	if targetWords < 40 {
		targetWords = 40
	}
	item := scriptpkg.GenerationItemV2{
		ID:       itemID,
		Title:    req.Topic,
		Project:  project,
		Language: language,
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: req.Topic,
		},
		ScriptParams: scriptpkg.ScriptSpec{TargetWords: targetWords},
		Output: scriptpkg.OutputSpec{
			SaveToDB:         true,
			GenerateTimeline: true,
		},
	}
	if req.Voiceover {
		item.Output.VoiceoverEnabled = scriptpkg.ToggleEnabled
		item.Audio = scriptpkg.AudioOutputConfig{Mode: string(audio.AudioModeCombinedTimeline)}
		item.Output.Audio = item.Audio
	}
	env := scriptpkg.GenerationEnvelopeV2{
		Version:       scriptpkg.EnvelopeVersion,
		Preset:        scriptpkg.PresetCustom,
		CorrelationID: itemID,
		Items:         []scriptpkg.GenerationItemV2{item},
	}
	if err := env.Validate(); err != nil {
		return env, fmt.Errorf("videocreate: script child payload: %w", err)
	}
	return env, nil
}

// AcquireChildPayload drives ONE acquisition child with its REAL wire
// contract: youtube_clip.extract receives a youtubetypes.ExtractRequest,
// media.stock a stockpipeline.StockRunPayload. sceneIndex is 1-based
// timeline order; sceneSeconds is the per-scene budget derived from the
// requested duration and the scene count.
func AcquireChildPayload(cand MediaCandidate, req appjobs.VideoCreatePayload, project, videoName string, sceneSeconds int) (any, string, error) {
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "en"
	}
	if cand.Source == "youtube" {
		// Subtitle/transcript acquisition is a hard pre-commit
		// requirement: 07_render runs with transcript.mode=reuse and a
		// clip without a READY transcript would fail at render time.
		requireTranscript := boolPtr(true)
		if cand.SourceVideoID != "" && cand.EndSec > cand.StartSec {
			// Registry candidate with a KNOWN window: re-extract the EXACT
			// clip (explicit segments) so the scene plays the selected
			// content. The extraction pipeline's identity contract
			// (kernel/asset/detail.YouTubeClipAssetID) converges the
			// re-cut on the SAME asset id as the registry clip.
			name := strings.TrimSpace(cand.Title)
			if name == "" {
				name = videoName
			}
			return youtubetypes.ExtractRequest{
				URL: fmt.Sprintf("https://www.youtube.com/watch?v=%s", cand.SourceVideoID),
				Segments: []youtubetypes.Segment{{
					Start: hmsTimestamp(cand.StartSec),
					End:   hmsTimestamp(cand.EndSec),
					Name:  name,
				}},
				ForceKeyframes:         true,
				RequireTranscriptReady: requireTranscript,
			}, "youtube", nil
		}
		payload := youtubetypes.ExtractRequest{
			URL:                    cand.SourceURL,
			ForceKeyframes:         true,
			RequireTranscriptReady: requireTranscript,
			// No source window known: the canonical analyzer derives the
			// clip from the video transcript through the SAME extraction
			// pipeline explicit segments use.
			Selection: &youtubetypes.SegmentSelection{
				Mode:        string(youtubetypes.SegmentSelectionModeImportant),
				Language:    language,
				MaxSegments: 1,
			},
		}
		return payload, "youtube", nil
	}
	payload := stockpipeline.StockRunPayload{
		SearchQueries:                  []string{searchTermFor(cand, req.Topic)},
		TotalMinutes:                   minutesFor(sceneSeconds),
		TargetTotalDurationSeconds:     sceneSeconds,
		TargetDurationPerSourceSeconds: sceneSeconds,
		ClipsPerSource:                 1,
		ClipDurationSeconds:            sceneSeconds,
		MaxVideos:                      1,
		Subfolder:                      project,
		FolderName:                     videoName,
	}
	return payload, "stock", nil
}

// searchTermFor is the deterministic stock search term: the candidate's
// title when the search produced one, else the request topic.
func searchTermFor(cand MediaCandidate, topic string) string {
	if t := strings.TrimSpace(cand.Title); t != "" {
		return t
	}
	return strings.TrimSpace(topic)
}

// minutesFor rounds a scene's second budget up to whole minutes (the
// stock contract's unit), with a floor of one minute.
func minutesFor(sceneSeconds int) int {
	if sceneSeconds <= 0 {
		return 1
	}
	return (sceneSeconds + 59) / 60
}

// VoiceoverChildPayload drives the voiceover.generate child (§12: the
// EXTERNAL-SAFE parent, never the internal voiceover.generate_item
// child) with its REAL wire contract: one GenerateVoiceoversCommand
// whose items[] are the scene narration texts in the requested
// language. The parent fans out one generate_item child per item.
func VoiceoverChildPayload(texts []string, language, project string) (voicesvc.GenerateVoiceoversCommand, error) {
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = "en"
	}
	cmd := voicesvc.GenerateVoiceoversCommand{
		Project: project,
		Items:   make([]voicesvc.VoiceoverItem, 0, len(texts)),
	}
	for i, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		cmd.Items = append(cmd.Items, voicesvc.VoiceoverItem{
			Text:     text,
			Language: voicesvc.Language(lang),
			Filename: fmt.Sprintf("scene-%03d", i+1),
			Required: true,
		})
	}
	if len(cmd.Items) == 0 {
		return cmd, fmt.Errorf("videocreate: voiceover child payload: no speakable scene text")
	}
	if err := cmd.Validate(); err != nil {
		return cmd, fmt.Errorf("videocreate: voiceover child payload: %w", err)
	}
	return cmd, nil
}

// RenderChildPayload drives one clip.render child (§14: RenderingGen →
// Chronon is behind that boundary; video.create never talks to Chronon)
// with its REAL wire contract: a cliprender.RenderRequest over the
// scene's acquired source asset, normalized and validated fail-closed
// here so a request the clip.render worker would reject is never
// enqueued. Overlays become burned subtitles (the text-overlay family
// of the render plan) — cover/thumbnail stays owned by the caller side.
func RenderChildPayload(sourceAssetID, language, aspectRatio string, overlays bool) (cliprender.RenderRequest, error) {
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = cliprender.DefaultLanguage
	}
	width, height, err := aspectDimensions(aspectRatio)
	if err != nil {
		return cliprender.RenderRequest{}, err
	}
	payload := cliprender.RenderRequest{
		SourceAssetID: sourceAssetID,
		Transcript: &cliprender.TranscriptSpec{
			Mode:     cliprender.TranscriptModeReuse,
			Language: lang,
		},
		Output: &cliprender.OutputSpec{
			Contract: cliprender.OutputContractVeloxAssemblyReadyV1,
			Width:    width,
			Height:   height,
		},
	}
	if overlays {
		payload.Subtitles = &cliprender.SubtitlesSpec{
			Enabled: true,
			Mode:    cliprender.SubtitlesModeBurn,
		}
	}
	payload.Normalize()
	if err := payload.Validate(); err != nil {
		return payload, fmt.Errorf("videocreate: render child payload: %w", err)
	}
	return payload, nil
}

// aspectDimensions maps the requested aspect ratio onto the canonical
// render dimensions. An empty ratio is the canonical 16:9 YouTube
// output; a vertical request fails closed here because the clip.render
// output contract is horizontal-only (the worker rejects it too).
func aspectDimensions(aspectRatio string) (int, int, error) {
	switch strings.TrimSpace(aspectRatio) {
	case "", "16:9", "16x9":
		return 1920, 1080, nil
	default:
		return 0, 0, fmt.Errorf("%w: aspect_ratio %q is not the canonical 16:9 output", ErrInvalidPayload, aspectRatio)
	}
}

func boolPtr(v bool) *bool { return &v }

// hmsTimestamp renders whole seconds as the canonical "HH:MM:SS" segment
// timestamp (the exact shape the extraction DTO's ParseTimestamp reads and
// the production fixtures carry).
func hmsTimestamp(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
}

// marshalChild encodes one typed child payload for the broker. The
// payload travels with the canonical parent link so the child's
// terminal commit can report back to the workflow's ledger without
// process-local memory (kernel/job.ParentLink).
func marshalChild(payload any, parentJobID, parentRunID string) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("videocreate: encode child payload: %w", err)
	}
	return job.InjectParentLink(raw, job.ParentLink{
		ParentJobID: parentJobID,
		ParentRunID: parentRunID,
	}), nil
}

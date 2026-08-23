// Package scriptgeneration — document_html.go renders the canonical Google
// Doc body for generated scripts.
//
// The renderer is split into two passes so DocsPrepare can overlap the
// generative branches instead of waiting for their outputs:
//
//   - RenderDocumentSkeleton renders only what is available at SceneTextReady
//     (the title and each scene's final text) plus deterministic late-bound
//     markers. It depends on NO voiceover, entity, timing, or audio artifact.
//   - InjectDocumentLateBound fills those markers with the artifacts that only
//     exist after TTS/NLP/audio complete (voiceover links, entity images,
//     scene timing, phrase timing, full audio, overlay, and the machine JSON
//     blocks).
//
// RenderDocument is the one-shot convenience (skeleton + injection) and the
// byte-equivalent successor to the retired
// adapters.BuildSpecSceneDocumentHTML — the human surface stays identical.
//
// The document surface combines a caller-facing title with the human scene
// view (scene text + available Drive links) and one structured SpecScene JSON
// representation. Technical metadata remains excluded from the human surface;
// the links themselves are deliberately visible and clickable.
package scriptgeneration

import (
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"html"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// CanonicalDocumentRendererID is the observable identity of the only
// production renderer for SpecScene documents.
const CanonicalDocumentRendererID = "specscene-html-v2"

// SpecSceneSHA256 hashes the canonical JSON representation received by the
// renderer. It is used for runtime proof that the embedded JSON came from the
// same SpecScene that was rendered.
func SpecSceneSHA256(spec scriptpkg.SpecSceneOutput) string {
	raw, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	sum := digest.SHA256Bytes(raw)
	return sum
}

// DocumentSceneText is the minimal scene input available at SceneTextReady:
// identity and final text only. The early skeleton pass must never read
// bindings, voiceover, entities, timing, or audio from it.
type DocumentSceneText struct {
	ID    string
	Index int
	Text  string
}

// DocumentSkeletonInput carries everything the early skeleton pass may read.
type DocumentSkeletonInput struct {
	Title  string
	Scenes []DocumentSceneText
}

// Late-bound markers. They are deterministic and replaced exactly once by
// InjectDocumentLateBound; they never collide with scene text because they
// contain no spaces and live outside the escaped scene paragraphs.
const (
	documentSkeletonBeforeMarker = "<!--pipelinegen:document:before-scenes-->"
	documentSkeletonAfterMarker  = "<!--pipelinegen:document:after-scenes-->"
)

func documentSkeletonSceneMarker(i int) string {
	return fmt.Sprintf("<!--pipelinegen:document:scene-%d-->", i)
}

// RenderDocumentSkeleton renders the early, scene-text-only pass: title plus
// one <section> per scene with its heading, final text, and a late-bound
// marker. It is a pure function of DocumentSkeletonInput and has no I/O.
func RenderDocumentSkeleton(in DocumentSkeletonInput) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	if title := strings.TrimSpace(in.Title); title != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(title))
		b.WriteString("</h1>")
	}
	b.WriteString(documentSkeletonBeforeMarker)
	for i, scene := range in.Scenes {
		b.WriteString("<section>")
		fmt.Fprintf(&b, "<h2>Scene %d</h2>", i+1)
		if text := strings.TrimSpace(scene.Text); text != "" {
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(text))
			b.WriteString("</p>")
		}
		b.WriteString(documentSkeletonSceneMarker(i))
		b.WriteString("</section>")
	}
	b.WriteString(documentSkeletonAfterMarker)
	b.WriteString("</body></html>")
	return b.String()
}

// InjectDocumentLateBound fills a skeleton produced by RenderDocumentSkeleton
// with the late-bound artifacts: full audio + overlay (before scenes),
// per-scene timing/media/links/phrase timings (inside each scene section),
// and the summary + machine JSON blocks (after scenes). It is deterministic
// and returns the complete document HTML.
func InjectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) string {
	if model == nil {
		return skeleton
	}

	var before strings.Builder
	writeDocumentFullAudio(&before, opts)
	writeDocumentOverlay(&before, opts)
	skeleton = strings.Replace(skeleton, documentSkeletonBeforeMarker, before.String(), 1)

	for i := range model.SpecScene.Scenes {
		scene := &model.SpecScene.Scenes[i]
		var perScene strings.Builder
		writeDocumentSceneTiming(&perScene, scene, opts)
		writeDocumentSceneMediaDurations(&perScene, scene, opts)
		writeDocumentSceneLinks(&perScene, scene, opts)
		writeDocumentPhraseTimings(&perScene, scene, opts)
		skeleton = strings.Replace(skeleton, documentSkeletonSceneMarker(i), perScene.String(), 1)
	}

	var after strings.Builder
	writeDocumentAudioCertificationSummary(&after, model, opts)
	writeDocumentSpecSceneJSON(&after, model)
	writeDocumentTimelineJSON(&after, opts)
	writeDocumentFinalAudioJSON(&after, opts)
	writeDocumentOverlayJSON(&after, opts)
	skeleton = strings.Replace(skeleton, documentSkeletonAfterMarker, after.String(), 1)

	return skeleton
}

// RenderDocument is the one-shot renderer (skeleton + injection). It is the
// DocumentRenderer port implementation and the byte-equivalent successor to
// the retired adapters.BuildSpecSceneDocumentHTML.
func RenderDocument(model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) (string, error) {
	if model == nil {
		return "", nil
	}
	return InjectDocumentLateBound(RenderDocumentSkeleton(documentSkeletonInput(model, opts.Title)), model, opts), nil
}

// documentSkeletonInput projects the scene-text-only inputs from a full model.
func documentSkeletonInput(model *scriptpkg.ModelScriptOutputV1, title string) DocumentSkeletonInput {
	in := DocumentSkeletonInput{Title: title}
	if model == nil {
		return in
	}
	for i := range model.SpecScene.Scenes {
		scene := &model.SpecScene.Scenes[i]
		in.Scenes = append(in.Scenes, DocumentSceneText{ID: scene.ID, Index: scene.Index, Text: scene.Text})
	}
	return in
}

// ── Machine JSON blocks ─────────────────────────────────────────────

func writeDocumentSpecSceneJSON(b *strings.Builder, model *scriptpkg.ModelScriptOutputV1) {
	if model == nil {
		return
	}
	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err != nil {
		return
	}
	b.WriteString("<h2>SpecScene JSON</h2><pre><code>")
	b.WriteString(html.EscapeString(string(raw)))
	b.WriteString("</code></pre>")
}

func writeDocumentTimelineJSON(b *strings.Builder, opts DocumentRenderOptions) {
	if opts.AudioTimeline == nil {
		return
	}
	if raw, err := json.MarshalIndent(opts.AudioTimeline, "", "  "); err == nil {
		b.WriteString("<h2>Audio Timeline JSON</h2><pre><code>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</code></pre>")
	}
}

func writeDocumentFinalAudioJSON(b *strings.Builder, opts DocumentRenderOptions) {
	block := buildFinalAudioBlock(opts.FinalAudio, string(opts.Language))
	if block == nil {
		return
	}
	if raw, err := json.MarshalIndent(block, "", "  "); err == nil {
		b.WriteString("<h2>Final Audio JSON</h2><pre><code>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</code></pre>")
	}
}

func writeDocumentOverlayJSON(b *strings.Builder, opts DocumentRenderOptions) {
	if opts.Overlay == nil {
		return
	}
	if raw, err := json.MarshalIndent(opts.Overlay, "", "  "); err == nil {
		b.WriteString("<h2>Rendered Overlay JSON</h2><pre><code>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</code></pre>")
	}
}

// finalAudioDocumentBlock is the document projection of the certified master.
// It intentionally excludes the local path and projects the pre-resolved
// container and duration_us verbatim, so the same asset ID certified here is
// the one the video renderer must consume.
type finalAudioDocumentBlock struct {
	AudioAssetID     string `json:"audio_asset_id"`
	Language         string `json:"language"`
	DriveLink        string `json:"drive_link,omitempty"`
	Container        string `json:"container,omitempty"`
	Codec            string `json:"codec,omitempty"`
	Profile          string `json:"profile,omitempty"`
	SampleRate       int    `json:"sample_rate,omitempty"`
	Channels         int    `json:"channels,omitempty"`
	ChannelLayout    string `json:"channel_layout,omitempty"`
	DurationUS       int64  `json:"duration_us,omitempty"`
	AudioPlanSHA256  string `json:"audio_plan_sha256,omitempty"`
	FinalAudioSHA256 string `json:"final_audio_sha256,omitempty"`
	FinalMix         bool   `json:"final_mix,omitempty"`
	CopyEligible     bool   `json:"copy_eligible,omitempty"`
}

func buildFinalAudioBlock(ref *FinalAudioReference, language string) *finalAudioDocumentBlock {
	if ref == nil {
		return nil
	}
	// The renderer is purely projective: Container and DurationUS must already
	// be resolved at the boundary. It never derives the container from the
	// local path nor the duration from the legacy duration_ms field.
	return &finalAudioDocumentBlock{
		AudioAssetID:     ref.AssetID,
		Language:         language,
		DriveLink:        pureDriveURL(ref.DriveLink),
		Container:        ref.Container,
		Codec:            ref.Codec,
		Profile:          ref.Profile,
		SampleRate:       ref.SampleRate,
		Channels:         ref.Channels,
		ChannelLayout:    ref.ChannelLayout,
		DurationUS:       ref.DurationUS,
		AudioPlanSHA256:  ref.PlanSHA256,
		FinalAudioSHA256: ref.FinalAudioSHA256,
		FinalMix:         ref.FinalMix,
		CopyEligible:     ref.CopyEligible,
	}
}

func writeDocumentFullAudio(b *strings.Builder, opts DocumentRenderOptions) {
	if opts.FullAudio == nil || strings.TrimSpace(opts.FullAudio.DriveLink) == "" {
		return
	}
	b.WriteString("<section><h2>Full Audio</h2>")
	if language := strings.TrimSpace(opts.FullAudio.Language); language != "" {
		b.WriteString("<p><strong>Lang:</strong> ")
		b.WriteString(html.EscapeString(documentLanguageLabel(language)))
		b.WriteString("</p>")
	}
	link := strings.TrimSpace(opts.FullAudio.DriveLink)
	b.WriteString("<p>")
	b.WriteString(renderDocumentLink(link, link, link))
	b.WriteString("</p>")
	if opts.FullAudio.DurationMS > 0 {
		b.WriteString("<p><strong>Duration:</strong> ")
		b.WriteString(html.EscapeString(formatDurationMS(opts.FullAudio.DurationMS)))
		b.WriteString("</p>")
	}
	b.WriteString("</section>")
}

// writeDocumentOverlay projects the published render-overlay reference into
// the human document surface. Only public fields (artifact URL, job, profile,
// duration) are shown; the local artifact path never appears here.
func writeDocumentOverlay(b *strings.Builder, opts DocumentRenderOptions) {
	if opts.Overlay == nil {
		return
	}
	b.WriteString("<section><h2>Rendered Overlay</h2>")
	if link := strings.TrimSpace(opts.Overlay.URL); link != "" {
		b.WriteString("<p><strong>Artifact:</strong> ")
		b.WriteString(renderDocumentLink(link, link, link))
		b.WriteString("</p>")
	}
	if job := strings.TrimSpace(opts.Overlay.JobID); job != "" {
		b.WriteString("<p><strong>Rendering job:</strong> ")
		b.WriteString(html.EscapeString(job))
		b.WriteString("</p>")
	}
	if profile := strings.TrimSpace(opts.Overlay.ProfileID); profile != "" {
		b.WriteString("<p><strong>Profile:</strong> ")
		b.WriteString(html.EscapeString(profile))
		b.WriteString("</p>")
	}
	if opts.Overlay.DurationUS > 0 {
		b.WriteString("<p><strong>Duration:</strong> ")
		b.WriteString(html.EscapeString(formatTimelineTimestamp(opts.Overlay.DurationUS)))
		b.WriteString("</p>")
	}
	b.WriteString("</section>")
}

func documentLanguageLabel(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "it":
		return "Italiano"
	case "pt":
		return "Português"
	case "en":
		return "English"
	default:
		return language
	}
}

// writeDocumentSceneTiming projects the canonical timeline placement of a
// scene into the human surface. The end timestamp is always derived as
// start + duration and is never stored as a separate source-of-truth field:
// the canonical timeline keeps timeline_start_us + duration_us only.
func writeDocumentSceneTiming(b *strings.Builder, scene *scriptpkg.SpecScene, opts DocumentRenderOptions) {
	if opts.AudioTimeline == nil || scene == nil {
		return
	}
	segment := timelineSegmentForScene(opts.AudioTimeline, scene.Index)
	if segment == nil || segment.DurationUS <= 0 {
		return
	}
	startUS := segment.TimelineStartUS
	b.WriteString("<p><strong>Start:</strong> ")
	b.WriteString(html.EscapeString(formatTimelineTimestamp(startUS)))
	b.WriteString("</p>")
	b.WriteString("<p><strong>End:</strong> ")
	b.WriteString(html.EscapeString(formatTimelineTimestamp(startUS + segment.DurationUS)))
	b.WriteString("</p>")
	b.WriteString("<p><strong>Duration:</strong> ")
	b.WriteString(html.EscapeString(formatTimelineTimestamp(segment.DurationUS)))
	b.WriteString("</p>")
}

// writeDocumentSceneMediaDurations renders the three timing concepts that are
// easy to confuse when reading Audio Timeline JSON by hand:
//
//   - scene duration: the placement window on the master timeline;
//   - video source/timeline duration: the selected portion of a video clip;
//   - voiceover source/timeline duration: the audio file and its placement.
//
// The timeline is the source of truth. In particular, an empty video segment
// is rendered as "None" rather than borrowing the scene duration (which is
// valid for audio-only scenes but is not a clip duration).
func writeDocumentSceneMediaDurations(b *strings.Builder, scene *scriptpkg.SpecScene, opts DocumentRenderOptions) {
	if scene == nil || opts.AudioTimeline == nil {
		return
	}
	segment := timelineSegmentForScene(opts.AudioTimeline, scene.Index)
	if segment == nil || segment.DurationUS <= 0 {
		return
	}

	b.WriteString("<h3>Video Clip</h3>")
	videos := segment.EffectiveVideoSegments()
	if len(videos) == 0 {
		b.WriteString("<p>None</p>")
	} else {
		for i, video := range videos {
			if len(videos) > 1 {
				fmt.Fprintf(b, "<p><strong>Clip %d</strong></p>", i+1)
			}
			writeDocumentAssetRow(b, "Asset", video.AssetID)
			writeDocumentAssetDurationRow(b, "Total Duration", clipTotalDuration(opts.ClipMetadata, video.AssetID))
			writeDocumentDurationRow(b, "Source In", video.SourceInUS)
			writeDocumentOptionalDurationRow(b, "Source Duration", video.SourceDurationUS)
			writeDocumentOptionalDurationRow(b, "Timeline Duration", video.TimelineDurationUS)
		}
	}

	b.WriteString("<h3>Voiceover</h3>")
	voiceovers := segment.EffectiveAudioIntents()
	voiceoverFound := false
	for _, intent := range voiceovers {
		if intent.Mode != capabilityaudio.AudioVoiceover {
			continue
		}
		voiceoverFound = true
		writeDocumentAssetRow(b, "Asset", intent.VoiceoverAssetID)
		writeDocumentOptionalDurationRow(b, "Source Duration", intent.SourceDurationUS)
		writeDocumentOptionalDurationRow(b, "Timeline Duration", intent.TimelineDurationUS)
	}
	if !voiceoverFound {
		// Do not infer a voiceover from the scene duration. An absent intent
		// means that this scene has no canonical voiceover track.
		b.WriteString("<p>None</p>")
	}
}

// writeDocumentAudioCertificationSummary formats the pre-computed aggregate
// audio facts into the human document. It only projects opts.AudioSummary;
// the clip/voiceover totals and counts are resolved at the capability
// boundary, never summed here. A missing clip total stays Unknown; it is
// never reconstructed from the scene or voiceover duration.
func writeDocumentAudioCertificationSummary(b *strings.Builder, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) {
	if model == nil || opts.AudioTimeline == nil {
		return
	}
	summary := opts.AudioSummary
	b.WriteString("<section><h2>Summary</h2>")
	fmt.Fprintf(b, "<p><strong>Clips:</strong> %d</p>", summary.ClipCount)
	fmt.Fprintf(b, "<p><strong>Scenes:</strong> %d</p>", len(model.SpecScene.Scenes))
	fmt.Fprintf(b, "<p><strong>Voiceovers:</strong> %d</p>", summary.VoiceoverCount)
	writeDocumentSummaryDuration(b, "Total Source Clip Duration", summary.ClipTotalUS, summary.ClipTotalKnown)
	writeDocumentSummaryDuration(b, "Total Edge TTS Duration", summary.VoiceoverTotalUS, summary.VoiceoverCount > 0)
	writeDocumentSummaryDuration(b, "Canonical Timeline", opts.AudioTimeline.DurationUS, opts.AudioTimeline.DurationUS > 0)
	finalAudioUS := int64(0)
	if opts.FinalAudio != nil {
		finalAudioUS = opts.FinalAudio.DurationUS
	}
	writeDocumentSummaryDuration(b, "Final Audio", finalAudioUS, finalAudioUS > 0)
	b.WriteString("</section>")
}

func writeDocumentSummaryDuration(b *strings.Builder, label string, durationUS int64, known bool) {
	b.WriteString("<p><strong>")
	b.WriteString(html.EscapeString(label))
	b.WriteString(":</strong> ")
	if !known || durationUS <= 0 {
		b.WriteString("Unknown</p>")
		return
	}
	b.WriteString(html.EscapeString(formatTimelineTimestamp(durationUS)))
	b.WriteString("</p>")
}

// clipTotalDuration looks up the canonical total source duration of a clip
// asset from the pre-resolved ClipMetadata projection. A missing or unknown
// asset returns OptionalDuration{Known: false} — the renderer formats it as
// "Unknown", never reconstructed from another field and never confused with a
// genuine zero-length asset.
func clipTotalDuration(metadata []capabilityaudio.ClipAssetMetadata, assetID string) kernelasset.OptionalDuration {
	for _, m := range metadata {
		if m.AssetID == assetID {
			return kernelasset.OptionalDuration{Known: m.Duration.Known(), DurationUS: m.Duration.DurationUS}
		}
	}
	return kernelasset.NoDuration()
}

func writeDocumentAssetRow(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("<p><strong>")
	b.WriteString(html.EscapeString(label))
	b.WriteString(":</strong> ")
	b.WriteString(html.EscapeString(value))
	b.WriteString("</p>")
}

func writeDocumentDurationRow(b *strings.Builder, label string, durationUS int64) {
	b.WriteString("<p><strong>")
	b.WriteString(html.EscapeString(label))
	b.WriteString(":</strong> ")
	b.WriteString(html.EscapeString(formatTimelineTimestamp(durationUS)))
	b.WriteString("</p>")
}

func writeDocumentOptionalDurationRow(b *strings.Builder, label string, durationUS int64) {
	if durationUS <= 0 {
		b.WriteString("<p><strong>")
		b.WriteString(html.EscapeString(label))
		b.WriteString(":</strong> Unknown</p>")
		return
	}
	writeDocumentDurationRow(b, label, durationUS)
}

// writeDocumentAssetDurationRow formats the asset total duration with
// provenance. An explicit unknown (Known=false) is formatted as "Unknown" —
// never as a fabricated 0 and never borrowed from a window field.
func writeDocumentAssetDurationRow(b *strings.Builder, label string, d kernelasset.OptionalDuration) {
	if !d.Known || d.DurationUS <= 0 {
		b.WriteString("<p><strong>")
		b.WriteString(html.EscapeString(label))
		b.WriteString(":</strong> Unknown</p>")
		return
	}
	writeDocumentDurationRow(b, label, d.DurationUS)
}

// writeDocumentPhraseTimings projects the scene's phrase timings from the
// canonical SceneSpeechTiming projection. Only the phrase text and its
// local/master spans are shown — word-level boundaries stay in the machine
// JSON / published timing.json, so the human surface stays readable.
func writeDocumentPhraseTimings(b *strings.Builder, scene *scriptpkg.SpecScene, opts DocumentRenderOptions) {
	if scene == nil || len(opts.SceneSpeechTimings) == 0 {
		return
	}
	var speech *capabilityaudio.SceneSpeechTiming
	for i := range opts.SceneSpeechTimings {
		if opts.SceneSpeechTimings[i].SceneID == scene.ID {
			speech = &opts.SceneSpeechTimings[i]
			break
		}
	}
	if speech == nil || len(speech.Phrases) == 0 {
		return
	}
	b.WriteString("<h3>Phrase Timing</h3>")
	for _, p := range speech.Phrases {
		fmt.Fprintf(b, "<p><strong>Phrase %d:</strong> %s</p>", p.PhraseIndex+1, html.EscapeString(p.Text))
		b.WriteString("<p>Local: ")
		b.WriteString(html.EscapeString(formatTimelineTimestamp(p.LocalStartUS)))
		b.WriteString(" → ")
		b.WriteString(html.EscapeString(formatTimelineTimestamp(p.LocalEndUS)))
		b.WriteString("</p>")
		b.WriteString("<p>Master: ")
		b.WriteString(html.EscapeString(formatTimelineTimestamp(p.GlobalStartUS)))
		b.WriteString(" → ")
		b.WriteString(html.EscapeString(formatTimelineTimestamp(p.GlobalEndUS)))
		b.WriteString("</p>")
	}
}

// timelineSegmentForScene returns the canonical segment matching the scene's
// zero-based index, or nil when the timeline does not cover that scene.
func timelineSegmentForScene(timeline *capabilityaudio.CanonicalTimeline, index int) *capabilityaudio.TimelineSegment {
	if timeline == nil {
		return nil
	}
	for i := range timeline.Segments {
		if timeline.Segments[i].Index == index {
			return &timeline.Segments[i]
		}
	}
	return nil
}

// formatTimelineTimestamp renders microsecond precision as MM:SS.mmm.
func formatTimelineTimestamp(us int64) string {
	if us < 0 {
		us = 0
	}
	totalMs := us / 1000
	ms := totalMs % 1000
	totalSec := totalMs / 1000
	sec := totalSec % 60
	min := totalSec / 60
	return fmt.Sprintf("%02d:%02d.%03d", min, sec, ms)
}

// formatDurationMS renders a millisecond duration as MM:SS.mmm so the human
// document surface, the canonical timeline and the certified master all show
// the same precision (e.g. 02:14.832 instead of 02:14).
func formatDurationMS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return formatTimelineTimestamp(ms * 1000)
}

// pureDriveURL normalizes a Drive link to a bare URL for the machine JSON
// surface. Some upstream producers wrap the link in markdown ("[label](url)");
// the Final Audio JSON must always carry the plain URL, never markdown.
func pureDriveURL(link string) string {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "[") {
		return link
	}
	start := strings.Index(link, "](")
	if start < 0 {
		return link
	}
	rest := link[start+2:]
	if end := strings.Index(rest, ")"); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return link
}

// writeDocumentSceneLinks renders only usable external links. Labels are
// human-facing, while IDs, paths, durations and statuses stay in the JSON.
func writeDocumentSceneLinks(b *strings.Builder, scene *scriptpkg.SpecScene, opts DocumentRenderOptions) {
	if scene == nil {
		return
	}

	seen := make(map[string]struct{})
	write := func(label, link string) {
		link = strings.TrimSpace(link)
		if link == "" {
			return
		}
		key := label + "\x00" + link
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		b.WriteString("<p><strong>")
		b.WriteString(html.EscapeString(label))
		b.WriteString(":</strong> ")
		b.WriteString(renderDocumentLink(link, link, link))
		b.WriteString("</p>")
	}

	// Clip is a legacy alias for the first Clips entry. Render each resource
	// once even when both compatibility fields are populated.
	for _, clip := range scene.Bindings.Clips {
		write("Clip", clip.DriveLink)
		write("Subtitles", clip.SubtitleLink)
	}
	if clip := scene.Bindings.Clip; clip != nil {
		write("Clip", clip.DriveLink)
		write("Subtitles", clip.SubtitleLink)
	}

	if stock := scene.Bindings.Stock; stock != nil {
		write("Stock", stock.DriveLink)
		write("Stock folder", stock.FolderLink)
	}

	if image := scene.Bindings.Image; image != nil {
		write("Image", image.URL)
	}

	for _, media := range scene.Bindings.Media {
		label := "Media"
		if slot := strings.TrimSpace(media.Slot); slot != "" {
			label += " " + slot
		}
		write(label, media.DriveLink)
	}

	if annotations := scene.Annotations; annotations != nil {
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity{}, annotations.PrimaryEntities...), annotations.SecondaryEntities...) {
			if entity.Image != nil && strings.TrimSpace(entity.Image.DriveLink) != "" {
				writeDocumentEntityImage(b, entity)
			}
		}
	}

	writeDocumentVoiceover(b, scene.Bindings.Voiceover, opts, write)
	writeDocumentTimingLinks(b, scene.Bindings.Voiceover, opts, write)
}

// writeDocumentEntityImage renders one entity image read-only from the binding
// surface. When a direct image URL is present it is inlined as an <img> (IDEAL
// PASS); the canonical Drive link always follows. It never recomputes NLP or
// bindings — the entity-image SSOT is the projection produced upstream.
func writeDocumentEntityImage(b *strings.Builder, entity scriptpkg.AnnotatedEntity) {
	if entity.Image == nil {
		return
	}
	name := strings.TrimSpace(entity.CanonicalName)
	if name == "" {
		name = strings.TrimSpace(entity.Text)
	}
	preview := strings.TrimSpace(entity.Image.PreviewURL)
	drive := strings.TrimSpace(entity.Image.DriveLink)
	if preview == "" && drive == "" {
		return // not_found or no usable link — never fabricate an image
	}
	if preview != "" {
		b.WriteString(`<p><img src="`)
		b.WriteString(html.EscapeString(preview))
		b.WriteString(`" alt="`)
		b.WriteString(html.EscapeString(name))
		b.WriteString(`" style="max-width:320px;max-height:240px;" /></p>`)
	}
	if drive != "" {
		b.WriteString("<p><strong>Entity image:</strong> ")
		b.WriteString(renderDocumentLink(drive, drive, drive))
		b.WriteString("</p>")
	}
}

func writeDocumentVoiceover(b *strings.Builder, voiceover *scriptpkg.VoiceoverBinding, opts DocumentRenderOptions, write func(string, string)) {
	link := resolveDocumentVoiceoverLink(voiceover, string(opts.Language), string(opts.DefaultLanguage))
	if link == "" {
		return
	}
	write("Voiceover", link)
}

func renderDocumentLink(url, label, fallback string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		if fallback == "" {
			return "(no link)"
		}
		return html.EscapeString(fallback)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = url
	}
	return "<a href=\"" + html.EscapeString(url) + "\">" + html.EscapeString(label) + "</a>"
}

// writeDocumentTimingLinks renders the published timing bundle links
// (timing.json SSOT + optional SRT/VTT projections) for the resolved
// language. Word-level boundaries are never inlined — they stay in the
// published timing.json the links point to.
func writeDocumentTimingLinks(b *strings.Builder, voiceover *scriptpkg.VoiceoverBinding, opts DocumentRenderOptions, write func(string, string)) {
	timing, ok := resolveDocumentTimingBinding(voiceover, string(opts.Language), string(opts.DefaultLanguage))
	if !ok {
		return
	}
	write("Timing JSON", timing.JSONLink)
	write("Timing SRT", timing.SRTLink)
	write("Timing VTT", timing.VTTLink)
}

// resolveDocumentTimingBinding picks the timing bundle binding for the
// requested language using the same resolution order as the voiceover audio
// link: canonical language-specific entry first, then the default/legacy
// surface, then a single available entry when no language was requested.
func resolveDocumentTimingBinding(voiceover *scriptpkg.VoiceoverBinding, language, defaultLanguage string) (scriptpkg.VoiceoverTimingBinding, bool) {
	if voiceover == nil || voiceover.Timing == nil {
		return scriptpkg.VoiceoverTimingBinding{}, false
	}
	language = strings.TrimSpace(language)
	defaultLanguage = strings.TrimSpace(defaultLanguage)
	if language != "" {
		if timing, ok := voiceover.Timing[language]; ok {
			return timing, true
		}
	}
	if language == "" || language == defaultLanguage {
		if timing, ok := voiceover.Timing[""]; ok {
			return timing, true
		}
	}
	if language == "" && len(voiceover.Timing) == 1 {
		for _, timing := range voiceover.Timing {
			return timing, true
		}
	}
	return scriptpkg.VoiceoverTimingBinding{}, false
}

// resolveDocumentVoiceoverLink picks the single Drive link to surface in the
// human document section for a scene's voiceover binding.
//
// Resolution order:
//  1. The canonical language-specific link in Links[language].
//  2. The legacy/default-language surface (Link) only when no language is
//     requested or when it matches the job's default language.
//  3. A single available link when no language was requested and exactly one
//     link exists.
//
// It deliberately never falls back to a wrong-language link: a document built
// for language X must not show the voiceover of language Y just because it is
// the only one present.
func resolveDocumentVoiceoverLink(
	voiceover *scriptpkg.VoiceoverBinding,
	language string,
	defaultLanguage string,
) string {
	if voiceover == nil {
		return ""
	}

	language = strings.TrimSpace(language)
	defaultLanguage = strings.TrimSpace(defaultLanguage)

	// 1. Canonical language-specific link.
	if language != "" && voiceover.Links != nil {
		if link := strings.TrimSpace(voiceover.Links[language]); link != "" {
			return link
		}
	}

	// 2. Legacy/default-language compatibility.
	if language == "" || language == defaultLanguage {
		if link := strings.TrimSpace(voiceover.Link); link != "" {
			return link
		}
	}

	// 3. No language requested + exactly one available link.
	if language == "" && len(voiceover.Links) == 1 {
		for _, raw := range voiceover.Links {
			if link := strings.TrimSpace(raw); link != "" {
				return link
			}
		}
	}

	return ""
}

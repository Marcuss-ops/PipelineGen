// Package scriptgeneration — document_audio_blocks.go: the audio/timing
// blocks of the canonical document renderer.
//
// Extracted from document_html.go (2026-09-12) to keep each renderer half
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap):
// this file owns the Final Audio / overlay / scene-timing human surface
// (finalAudioDocumentBlock and the writeDocument{FullAudio,Overlay,
// SceneTiming,SceneMediaDurations,PhraseTimings} family) plus the shared
// timestamp formatters.
package scriptgeneration

import (
	"fmt"
	"html"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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

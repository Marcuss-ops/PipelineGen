// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The document surface combines a caller-facing title with the human scene
// view (scene text + available Drive links) and one structured SpecScene JSON
// representation. Technical metadata remains excluded from the human
// surface; the links themselves are deliberately visible and clickable.
package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"path/filepath"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SpecSceneDocumentOptions carries the caller-facing inputs the document
// renderer needs. The renderer is deterministic: it renders only the title,
// the human scene surface, and the byte-faithful SpecScene JSON snapshot. It
// never mutates SpecScene and never resolves technical bindings itself.
type SpecSceneDocumentOptions struct {
	Title           string
	Language        string
	DefaultLanguage string
	FullAudio       *scriptpkg.DocumentAudioRef
	// FinalAudio is the full master certification. When present the renderer
	// embeds it verbatim (minus the local path) so the video renderer and the
	// document both reference the same asset by ID.
	FinalAudio    *scriptpkg.FinalAudioArtifact
	AudioTimeline *capabilityaudio.CanonicalTimeline
}

// BuildSpecSceneDocumentHTML renders the canonical production Google Doc.
//
// Visible sections include the optional title, followed by the human scene
// view ("Scene N" + scene text + available resource links) and the complete
// SpecScene JSON snapshot. The JSON block is the canonical machine-consumable
// surface and must remain byte-faithful to model.SpecScene.
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	opts SpecSceneDocumentOptions,
) string {
	if model == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	if title := strings.TrimSpace(opts.Title); title != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(title))
		b.WriteString("</h1>")
	}

	writeDocumentFullAudio(&b, opts)

	for i := range model.SpecScene.Scenes {
		scene := &model.SpecScene.Scenes[i]

		b.WriteString("<section>")
		fmt.Fprintf(&b, "<h2>Scene %d</h2>", i+1)

		if text := strings.TrimSpace(scene.Text); text != "" {
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(text))
			b.WriteString("</p>")
		}

		writeDocumentSceneTiming(&b, scene, opts)

		writeDocumentSceneLinks(&b, scene, opts)

		b.WriteString("</section>")
	}

	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre><code>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</code></pre>")
	}
	if opts.AudioTimeline != nil {
		if timeline, timelineErr := json.MarshalIndent(opts.AudioTimeline, "", "  "); timelineErr == nil {
			b.WriteString("<h2>Audio Timeline JSON</h2><pre><code>")
			b.WriteString(html.EscapeString(string(timeline)))
			b.WriteString("</code></pre>")
		}
	}
	if block := buildFinalAudioBlock(opts.FinalAudio, opts.Language); block != nil {
		if finalAudio, finalAudioErr := json.MarshalIndent(block, "", "  "); finalAudioErr == nil {
			b.WriteString("<h2>Final Audio JSON</h2><pre><code>")
			b.WriteString(html.EscapeString(string(finalAudio)))
			b.WriteString("</code></pre>")
		}
	}

	b.WriteString("</body></html>")
	return b.String()
}

// finalAudioDocumentBlock is the document projection of the certified master.
// It intentionally excludes the local path and derives duration_us from the
// canonical duration_ms, so the same asset ID certified here is the one the
// video renderer must consume.
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

func buildFinalAudioBlock(ref *scriptpkg.FinalAudioArtifact, language string) *finalAudioDocumentBlock {
	if ref == nil {
		return nil
	}
	return &finalAudioDocumentBlock{
		AudioAssetID:     ref.AssetID,
		Language:         language,
		DriveLink:        ref.DriveLink,
		Container:        mediaContainerFromPath(ref.Path),
		Codec:            ref.Codec,
		Profile:          ref.Profile,
		SampleRate:       ref.SampleRate,
		Channels:         ref.Channels,
		ChannelLayout:    ref.ChannelLayout,
		DurationUS:       ref.DurationMS * 1000,
		AudioPlanSHA256:  ref.AudioPlanSHA256,
		FinalAudioSHA256: ref.FinalAudioSHA256,
		FinalMix:         ref.FinalMix,
		CopyEligible:     ref.CopyEligible,
	}
}

// mediaContainerFromPath derives the media container (e.g. "m4a") from the
// local artifact path without ever exposing the path itself in the document.
func mediaContainerFromPath(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

func writeDocumentFullAudio(b *strings.Builder, opts SpecSceneDocumentOptions) {
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
		seconds := opts.FullAudio.DurationMS / 1000
		b.WriteString(fmt.Sprintf("<p><strong>Duration:</strong> %02d:%02d</p>", seconds/60, seconds%60))
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
func writeDocumentSceneTiming(b *strings.Builder, scene *scriptpkg.SpecScene, opts SpecSceneDocumentOptions) {
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

// writeDocumentSceneLinks renders only usable external links. Labels are
// human-facing, while IDs, paths, durations and statuses stay in the JSON.
func writeDocumentSceneLinks(b *strings.Builder, scene *scriptpkg.SpecScene, opts SpecSceneDocumentOptions) {
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
			if entity.Image != nil {
				write("Entity image", entity.Image.DriveLink)
			}
		}
	}

	writeDocumentVoiceover(b, scene.Bindings.Voiceover, opts, write)
}

func writeDocumentVoiceover(b *strings.Builder, voiceover *scriptpkg.VoiceoverBinding, opts SpecSceneDocumentOptions, write func(string, string)) {
	link := resolveDocumentVoiceoverLink(voiceover, opts.Language, opts.DefaultLanguage)
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

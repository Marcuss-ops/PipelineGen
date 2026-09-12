// Package scriptgeneration — document_html.go renders the canonical Google
// Doc body for generated scripts. The human-facing Drive/voiceover link
// surface lives in document_links.go (split 2026-09-12 under the
// max_lines_per_file_strict=600 forward-prevention cap, godlike/08).
//
// The renderer is split into two passes so DocsPrepare can overlap the
// generative branches instead of waiting for their outputs:
//
//   - RenderDocumentSkeleton renders only what is available at SceneTextReady
//     (the title and each scene's final text) plus deterministic late-bound
//     markers. It depends on NO voiceover, entity, timing, or audio artifact.
//   - InjectDocumentLateBound fills those markers with the artifacts that only
//     exist after TTS/NLP/audio complete (voiceover links, entity images,
//     scene timing, phrase timing, full audio and overlay references).
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

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	Kind  scriptpkg.SceneKind
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
		heading := fmt.Sprintf("Scene %d", i+1)
		switch scene.Kind {
		case scriptpkg.SceneIntro:
			heading = "Intro"
		case scriptpkg.SceneOutro:
			heading = "Outro"
		}
		fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(heading))
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
// and the human semantic summary plus certified artifact references (after
// scenes). It is deterministic
// and returns the complete document HTML.
func InjectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) string {
	if model == nil {
		return skeleton
	}

	var before strings.Builder
	if !opts.PayloadOnly {
		writeDocumentFullAudio(&before, opts)
		writeDocumentOverlay(&before, opts)
		writeDocumentSemanticSummary(&before, model)
		writeDocumentSemanticOverlay(&before, opts)
	}

	perScene := make([]string, len(model.SpecScene.Scenes))
	for i := range model.SpecScene.Scenes {
		scene := &model.SpecScene.Scenes[i]
		var b strings.Builder
		if !opts.PayloadOnly {
			writeDocumentSceneTiming(&b, scene, opts)
			writeDocumentSceneMediaDurations(&b, scene, opts)
			writeDocumentPhraseTimings(&b, scene, opts)
		}
		// Clip links remain visible even in payload-only documents. The payload
		// mode hides technical timing/spec JSON, not the operator's clip inputs.
		writeDocumentSceneLinks(&b, scene, opts)
		perScene[i] = b.String()
	}

	var after strings.Builder
	if !opts.PayloadOnly {
		writeDocumentAudioCertificationSummary(&after, model, opts)
		writeDocumentSpecSceneJSON(&after, model)
		writeDocumentTimelineJSON(&after, opts)
		writeDocumentFinalAudioJSON(&after, opts)
		writeDocumentOverlayPlanJSON(&after, opts)
		writeDocumentOverlayJSON(&after, opts)
	}

	// Single-pass splice. The legacy implementation ran one
	// strings.Replace over the WHOLE (growing) document per scene, copying the
	// full document O(N) times for N scenes; the splice below walks the
	// skeleton once and produces byte-identical output:
	//   head BEFORE_MARKER [section_i … SCENE_MARKER(i) …]* AFTER_MARKER tail
	// The scene markers are unique, ordered and HTML-escaped on input, so a
	// marker string can never appear inside injected content.
	splice := func() string {
		headEnd := strings.Index(skeleton, documentSkeletonBeforeMarker)
		if headEnd < 0 {
			return ""
		}
		afterIdx := strings.Index(skeleton[headEnd+len(documentSkeletonBeforeMarker):], documentSkeletonAfterMarker)
		if afterIdx < 0 {
			return ""
		}
		afterIdx += headEnd + len(documentSkeletonBeforeMarker)
		middle := skeleton[headEnd+len(documentSkeletonBeforeMarker) : afterIdx]
		var out strings.Builder
		out.Grow(len(skeleton) + before.Len() + after.Len())
		out.WriteString(skeleton[:headEnd])
		if !opts.PayloadOnly {
			out.WriteString(before.String())
		}
		cur := 0
		for i := range perScene {
			marker := documentSkeletonSceneMarker(i)
			idx := strings.Index(middle[cur:], marker)
			if idx < 0 {
				return ""
			}
			out.WriteString(middle[cur : cur+idx])
			out.WriteString(perScene[i])
			cur += idx + len(marker)
		}
		out.WriteString(middle[cur:])
		out.WriteString(after.String())
		out.WriteString(skeleton[afterIdx+len(documentSkeletonAfterMarker):])
		return out.String()
	}
	if out := splice(); out != "" {
		return out
	}
	// Defensive fallback (marker contract violated): keep the legacy
	// replace-based assembly so output stays deterministic on any unexpected
	// skeleton shape. In practice the markers are always present because the
	// skeleton is produced by RenderDocumentSkeleton.
	return injectDocumentLateBoundLegacy(skeleton, opts, before.String(), perScene, after.String())
}

// injectDocumentLateBoundLegacy is the byte-equivalent replace-based assembly
// kept as the defensive fallback of InjectDocumentLateBound (see above).
func injectDocumentLateBoundLegacy(skeleton string, opts DocumentRenderOptions, before string, perScene []string, after string) string {
	if opts.PayloadOnly {
		skeleton = strings.Replace(skeleton, documentSkeletonBeforeMarker, "", 1)
	} else {
		skeleton = strings.Replace(skeleton, documentSkeletonBeforeMarker, before, 1)
	}
	for i := range perScene {
		skeleton = strings.Replace(skeleton, documentSkeletonSceneMarker(i), perScene[i], 1)
	}
	skeleton = strings.Replace(skeleton, documentSkeletonAfterMarker, after, 1)
	return skeleton
}

// writeDocumentSemanticSummary renders the operator-facing aggregate view of
// the NLP output. Scene annotations remain the source of truth; this section
// only groups the already-resolved entities and phrases so a reviewer can
// inspect the result without opening the embedded JSON.
func writeDocumentSemanticSummary(b *strings.Builder, model *scriptpkg.ModelScriptOutputV1) {
	if model == nil {
		return
	}

	type entityGroup struct {
		label    string
		entities []scriptpkg.AnnotatedEntity
	}
	groups := []entityGroup{
		{label: "PERSON"},
		{label: "ORG"},
		{label: "GPE"},
		{label: "CONCEPT"},
	}
	groupIndex := map[string]int{"PERSON": 0, "ORG": 1, "ORGANIZATION": 1, "GPE": 2, "LOCATION": 2, "PLACE": 2, "COUNTRY": 2, "CITY": 2, "CONCEPT": 3}
	seenEntities := make(map[string]struct{})
	var phrases []string
	seenPhrases := make(map[string]struct{})

	for _, scene := range model.SpecScene.Scenes {
		if scene.Annotations == nil {
			continue
		}
		for _, phrase := range scene.Annotations.ImportantPhrases {
			value := strings.TrimSpace(phrase.Text)
			key := strings.ToLower(value)
			if value != "" && key != "" {
				if _, ok := seenPhrases[key]; !ok {
					seenPhrases[key] = struct{}{}
					phrases = append(phrases, value)
				}
			}
		}
		entities := append(append([]scriptpkg.AnnotatedEntity{}, scene.Annotations.PrimaryEntities...), scene.Annotations.SecondaryEntities...)
		for _, entity := range entities {
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				name = strings.TrimSpace(entity.Text)
			}
			kind := strings.ToUpper(strings.TrimSpace(entity.Type))
			idx, ok := groupIndex[kind]
			if !ok {
				idx = 3
			}
			key := kind + "\x00" + strings.ToLower(name)
			if name == "" || key == "\x00" {
				continue
			}
			if _, exists := seenEntities[key]; exists {
				continue
			}
			seenEntities[key] = struct{}{}
			groups[idx].entities = append(groups[idx].entities, entity)
		}
	}

	if len(phrases) == 0 && len(seenEntities) == 0 {
		return
	}
	if len(seenEntities) > 0 {
		b.WriteString("<section><h2>Entities</h2>")
		for _, group := range groups {
			for _, entity := range group.entities {
				writeDocumentEntityLine(b, entity)
			}
		}
		b.WriteString("</section>")
	}
	if len(phrases) > 0 {
		b.WriteString("<section><h2>Important Phrases</h2>")
		for _, phrase := range phrases {
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(phrase))
			b.WriteString("</p>")
		}
		b.WriteString("</section>")
	}
}

// writeDocumentEntityLine is deliberately compact: the human-facing document
// shows only the resolved type, name and canonical Drive link. Asset/cache,
// source and license metadata remain available in the machine JSON blocks.
func writeDocumentEntityLine(b *strings.Builder, entity scriptpkg.AnnotatedEntity) {
	name := strings.TrimSpace(entity.CanonicalName)
	if name == "" {
		name = strings.TrimSpace(entity.Text)
	}
	if name == "" {
		return
	}

	b.WriteString("<p><strong>")
	b.WriteString(html.EscapeString(documentEntityTypeLabel(entity.Type)))
	b.WriteString(":</strong> ")
	b.WriteString(html.EscapeString(name))
	if entity.Image != nil {
		if drive := strings.TrimSpace(entity.Image.DriveLink); drive != "" {
			b.WriteString(" — ")
			b.WriteString(renderDocumentLink(drive, "Drive", drive))
		}
	}
	b.WriteString("</p>")
}

func documentEntityTypeLabel(kind string) string {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "PERSON":
		return "Person"
	case "ORG", "ORGANIZATION":
		return "Organization"
	case "GPE", "LOCATION", "PLACE", "COUNTRY", "CITY":
		return "Location"
	case "CONCEPT":
		return "Concept"
	default:
		if label := strings.TrimSpace(kind); label != "" {
			return label
		}
		return "Entity"
	}
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
		in.Scenes = append(in.Scenes, DocumentSceneText{ID: scene.ID, Index: scene.Index, Kind: scene.Kind, Text: scene.Text})
	}
	return in
}

// ── Machine JSON blocks ─────────────────────────────────────────────

func writeDocumentSpecSceneJSON(b *strings.Builder, model *scriptpkg.ModelScriptOutputV1) {
	if model == nil {
		return
	}
	raw, err := marshalDocumentSpecScene(model.SpecScene)
	if err != nil {
		return
	}
	b.WriteString("<h2>SpecScene JSON</h2><pre><code>")
	b.WriteString(html.EscapeString(string(raw)))
	b.WriteString("</code></pre>")
}

// marshalDocumentSpecScene strips producer-local paths from the operator
// document while preserving the complete semantic/timing JSON shape. Drive
// links, entity names and timing references remain visible; local filesystem
// paths never belong in a Google Doc.
func marshalDocumentSpecScene(spec scriptpkg.SpecSceneOutput) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	stripDocumentLocalPaths(value)
	return json.MarshalIndent(value, "", "  ")
}

func stripDocumentLocalPaths(value any) {
	switch node := value.(type) {
	case map[string]any:
		delete(node, "local_path")
		for _, child := range node {
			stripDocumentLocalPaths(child)
		}
	case []any:
		for _, child := range node {
			stripDocumentLocalPaths(child)
		}
	}
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

// writeDocumentSemanticOverlay renders the exact semantic items that were
// handed to RenderingGen. It is intentionally separate from the rendered
// artifact reference: one proves what Chronon rendered, the other proves
// which entity/phrase/timing inputs produced it.
func writeDocumentSemanticOverlay(b *strings.Builder, opts DocumentRenderOptions) {
	plan := opts.OverlayPlan
	if plan == nil || len(plan.Items) == 0 {
		return
	}
	b.WriteString("<section><h2>Semantic Overlay</h2>")
	if planID := strings.TrimSpace(plan.PlanID); planID != "" {
		b.WriteString("<p><strong>Plan:</strong> ")
		b.WriteString(html.EscapeString(planID))
		b.WriteString("</p>")
	}
	b.WriteString("<ul>")
	for _, item := range plan.Items {
		b.WriteString("<li>")
		if item.EntityRef != nil {
			name := item.EntityRef.Name
			if name == "" {
				name = item.Text
			}
			b.WriteString("<strong>Entity:</strong> ")
			b.WriteString(html.EscapeString(name))
			if item.EntityRef.Type != "" {
				b.WriteString(" <em>(")
				b.WriteString(html.EscapeString(item.EntityRef.Type))
				b.WriteString(")</em>")
			}
			if item.EntityRef.CanonicalEntityID != "" {
				b.WriteString(" <small>[canonical=")
				b.WriteString(html.EscapeString(item.EntityRef.CanonicalEntityID))
				b.WriteString("]</small>")
			}
		} else if item.Text != "" {
			b.WriteString("<strong>Phrase:</strong> ")
			b.WriteString(html.EscapeString(item.Text))
		} else {
			b.WriteString("<strong>Overlay:</strong> ")
			b.WriteString(html.EscapeString(item.Kind))
		}
		fmt.Fprintf(b, " — Scene %s — %s → %s", html.EscapeString(item.SceneID),
			formatTimelineTimestamp(item.StartUSValue()), formatTimelineTimestamp(item.EndUSValue()))
		if item.TemplateID != "" {
			b.WriteString(" — template=")
			b.WriteString(html.EscapeString(item.TemplateID))
		}
		for _, asset := range item.AssetRefs {
			if link := strings.TrimSpace(asset.URL); link != "" {
				b.WriteString(" — ")
				b.WriteString(renderDocumentLink("image asset", link, link))
				break
			}
		}
		b.WriteString("</li>")
	}
	b.WriteString("</ul></section>")
}

func writeDocumentOverlayPlanJSON(b *strings.Builder, opts DocumentRenderOptions) {
	if opts.OverlayPlan == nil {
		return
	}
	if raw, err := json.MarshalIndent(opts.OverlayPlan, "", "  "); err == nil {
		b.WriteString("<h2>Semantic Overlay JSON</h2><pre><code>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</code></pre>")
	}
}

// finalAudioDocumentBlock is the document projection of the certified master.

package adapters

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Deprecated HTML helpers ──────────────────────────────────────────
//
// BuildClipSpecSceneDocumentHTML and BuildSectionDocHTML are retained
// for backward-compat (PR 7 byte-level diff tests + legacy batch
// pipeline). The canonical production renderer is
// BuildGenerationDocumentHTML (generation_html.go).

// BuildClipSpecSceneDocumentHTML renders the clip-aware document body
// used by generate-from-clips. It emits the canonical SpecScene JSON
// structure (SpecSceneOutput) instead of a legacy format with
// description/drive_links arrays. Every scene uses the canonical
// Bindings.Clip.DriveLink and Bindings.Clip.ClipTitle fields.
//
// DEPRECATED (FASE-document-canonical, July 2026): the canonical
// production renderer is BuildGenerationDocumentHTML (called by
// DocumentProcessor). The SpecScene JSON debug block is available via
// BuildGenerationDocumentHTML's `includeSpecSceneBlock=true` parameter
// — production callers MUST go through that path. This function is
// RETAINED as a debug-only standalone helper used by:
//
//  1. The PR 7 byte-level diff test
//     (`TestClipBindings_DocBuilderByteStream_Equals_JSONWire_PR7`),
//     which compares the doc-rendered SpecScene JSON with the
//     response-writer wire shape — a load-bearing regression pin
//     for the slice-header-sharing discipline.
//
//  2. Tooling that only wants the SpecScene JSON dump, not the full
//     script body.
//
// It accepts an `evidence *scriptpkg.ClipEvidence` parameter for
// backward-compat with the PR 7 test fixture (the field is unused
// here — the canonical SpecScene is read from model.SpecScene so the
// evidence never leaks into the rendered JSON).
func BuildClipSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	evidence *scriptpkg.ClipEvidence,
) string {
	if model == nil {
		return ""
	}

	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	if t := strings.TrimSpace(title); t != "" {
		b.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(t)))
	}
	b.WriteString("<h2>SpecScene JSON</h2><pre>")
	b.WriteString(html.EscapeString(string(raw)))
	b.WriteString("</pre></body></html>")
	return b.String()
}

// BuildSectionDocHTML renders a sectioned HTML document from a flat
// (titles, contents) list. Canonical impl lives here in adapters/ after
// PR-G mega-package split (the previous scripts/usecase/section_regen.go
// shim was hoisted into this package when the scripts/ package was
// renamed to scripts/adapters/). Returns a minimal but real HTML body
// so callers don't observe empty Google Docs in production.
//
// noChapters=true skips the <h1> chapter heading (caller controls
// whether each section title renders as h2 or h3).
func BuildSectionDocHTML(title string, sectionTitles, sectionContents []string, noChapters bool, language string) string {
	var b strings.Builder
	htmlEscape := func(s string) string {
		s = strings.ReplaceAll(s, "&", "&amp;")
		s = strings.ReplaceAll(s, "<", "&lt;")
		s = strings.ReplaceAll(s, ">", "&gt;")
		s = strings.ReplaceAll(s, "\"", "&quot;")
		return s
	}
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>")
	b.WriteString(htmlEscape(title))
	b.WriteString("</title></head><body>")
	if !noChapters {
		b.WriteString("<h1>")
		b.WriteString(htmlEscape(title))
		b.WriteString("</h1>")
	}
	if language != "" {
		b.WriteString("<p><em>")
		b.WriteString(htmlEscape(language))
		b.WriteString("</em></p>")
	}
	n := len(sectionTitles)
	if n < len(sectionContents) {
		n = len(sectionContents)
	}
	for i := 0; i < n; i++ {
		var t, c string
		if i < len(sectionTitles) {
			t = sectionTitles[i]
		}
		if i < len(sectionContents) {
			c = sectionContents[i]
		}
		if strings.TrimSpace(t) != "" {
			b.WriteString("<h2>")
			b.WriteString(htmlEscape(t))
			b.WriteString("</h2>")
		}
		if strings.TrimSpace(c) != "" {
			for _, para := range strings.Split(c, "\n\n") {
				b.WriteString("<p>")
				b.WriteString(htmlEscape(strings.TrimSpace(para)))
				b.WriteString("</p>")
			}
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}

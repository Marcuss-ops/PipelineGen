// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The visible document surface is deliberately reduced to one structured
// representation: SpecScene JSON. The legacy prose and human-readable scene
// sections remain internal pipeline data, but are not duplicated in the
// generated document. Generation provenance is retained only as a hidden HTML
// comment for operator traceability.
package adapters

import (
	"encoding/json"
	"html"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildSpecSceneDocumentHTML renders the canonical production Google Doc.
//
// Visible sections:
//
//	<h1>title</h1>
//	<h2>SpecScene JSON</h2><pre>...</pre>
//
// The renderer intentionally does not emit the duplicate Script, Scenes,
// Entities, Video Metadata, or visible Technical Provenance sections.
// Provenance remains embedded as a hidden PIPELINEGEN-PROVENANCE comment so
// operational traceability is preserved without creating a second visible
// source of truth.
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	provenance ...*scriptpkg.GenerationProvenance,
) string {
	if model == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	if t := strings.TrimSpace(title); t != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(t))
		b.WriteString("</h1>")
	}

	if len(provenance) > 0 && provenance[0] != nil {
		raw, err := json.MarshalIndent(provenance[0], "", "  ")
		if err == nil {
			b.WriteString("<!-- PIPELINEGEN-PROVENANCE: ")
			b.WriteString(html.EscapeString(string(raw)))
			b.WriteString(" -->")
		}
	}

	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</pre>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

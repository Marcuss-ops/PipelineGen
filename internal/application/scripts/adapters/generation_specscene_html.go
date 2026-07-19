// Package scripts: generation_specscene_html.go renders the canonical
// Google Doc body for generated scripts.
//
// The document surface is exactly one structured representation: SpecScene
// JSON. Titles, prose, duplicate scene listings, and technical provenance are
// intentionally excluded from the generated document.
package adapters

import (
	"encoding/json"
	"html"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildSpecSceneDocumentHTML renders the canonical production Google Doc.
//
// Visible section:
//
//	<h2>SpecScene JSON</h2><pre>...</pre>
func BuildSpecSceneDocumentHTML(
	model *scriptpkg.ModelScriptOutputV1,
	title string,
	provenance ...*scriptpkg.GenerationProvenance,
) string {
	if model == nil {
		return ""
	}
	// Keep the narrow compatibility signature for existing callers. These
	// values are pipeline metadata, not document content.
	_ = title
	_ = provenance

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")

	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre>")
		b.WriteString(html.EscapeString(string(raw)))
		b.WriteString("</pre>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

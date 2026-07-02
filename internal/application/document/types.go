// Package document — types.go (P0 Commit 10, July 2026).
//
// Request and result types for the document.generate use case.
// Both are deliberately minimal: NO Filename, NO Path, NO OutputDir
// in DocumentRequest — those are provider-side concerns owned by
// the ArtifactManifest sidecar (Filename/MIMEType/SizeBytes) and
// the workspace manager (Path). Killing the path hardcoding is the
// C10 motivation for this refactor.
package document

// Format represents the output document format. Currently only
// PDF is supported; future formats (DOCX, EPUB) would join here.
type Format string

const (
	// FormatPDF = "pdf" is the canonical first adopter (P0 Commit 10).
	FormatPDF Format = "pdf"
)

// DocumentRequest is the JSON body for POST /api/document/generate.
//
// CRITICAL (path-hardcoding kill): there is NO OutputPath, NO
// Filename, NO OutputDir field on this request. The output path is
// the workspace manager's responsibility (under
// /tmp/pipelinegen/jobs/<jobID>/), and the upload-side Filename is
// derived from the Title via pkg/textutil.Slugify + the format
// extension. This is the C10 invariant: every document flow
// generates its filename from the request + format, never from a
// caller-supplied path string. Path traversal or filename mismatch
// bugs at the boundary are now structurally impossible.
type DocumentRequest struct {
	// Title is the document heading + the source of the canonical
	// filename (e.g. Title "Hello World!" + Format "pdf" →
	// "hello-world.pdf"). Required. Trimmed by Validate.
	Title string `json:"title"`

	// Body is the document body content (plain text, rendered into
	// the PDF). Required.
	Body string `json:"body"`

	// Format selects the output format. Defaults to "pdf" when
	// empty (omitempty). Validated to one of the Format* constants.
	Format Format `json:"format,omitempty"`
}

// DocumentResult is the typed payload carried in
// job.ExecutionResult[DocumentResult].Data by the use case and
// echoed to the API caller via the wire envelope.
//
// NO file paths here. The ArtifactManifest sidecar (carried in the
// envelope's Artifacts field) is the Sender-safe source of truth
// for the produced file's URL + metadata. The typed payload only
// carries caller-visible statistics (counts) and the title.
type DocumentResult struct {
	// Title is the document's heading (echoes DocumentRequest.Title).
	Title string `json:"title"`

	// Format is the produced format (always FormatPDF for now).
	Format Format `json:"format"`

	// PageCount is the number of pages in the generated PDF. Useful
	// for dashboards + operator audit.
	PageCount int `json:"page_count"`

	// BodyChars is the body character count. Tracks whether the
	// caller supplied the body that was actually rendered.
	BodyChars int `json:"body_chars"`

	// Slug is the slugified filename stem (matches the Artifact
	// Filename minus its `.pdf` extension). Echoed in the result
	// so callers can show "the title slug became hello-world.pdf"
	// diagnostics without re-running Slugify client-side.
	Slug string `json:"slug"`
}

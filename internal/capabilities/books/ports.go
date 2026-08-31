// Package books — port (Pattern 0) interfaces for the two
// canonical book-pipeline territories (Fase 7 Spina Dorsale,
// July 2026). Mirrors godlike/06 §"One owner per fact": each port
// has exactly one canonical surface and exactly one ownership
// boundary. The engines behind each port can change (Python
// subprocess today, in-process LLM tomorrow, or a third-party
// REST adapter next) without leaking through the apply layer.
//
// Two territories:
//
//	BookSourceReader  — read source bytes (local file / Drive
//	                   download / Google Doc URL resolution).
//	Pre-Fase-7 the
//	                   responsibility lived inline in
//	                   books/service.go::ProcessBookFromDrive;
//	                   the port is reserved for the future
//	                   wave to provide a real adapter. EXPAND
//	                   window keeps the legacy inline download
//	                   path alive.
//
//	BookTransformer   — turn a source + request shape into a
//	                   TransformResult. The Fase 7 concrete is
//	                   internal/platform/books/
//	                   pythontransformer.SubprocessTransformer
//	                   (wraps scripts/bridges/book_summarizer.py).
//	                   The transformer is the ONLY Python-aware
//	                   surface in the books apply layer —
//	                   books.Service does not import os/exec.
//
// Composition-root wiring lives in
// internal/app/build_bundles_core.go::buildBooksService.
package books

import (
	"context"
)

// BookSourceDescription tags the canonical book source types.
// The transformer + consumer paths branch on the populated fields
// (LocalPath or GoogleDocID, mutually exclusive). DriveFileURL is
// resolved by SourceReaders to a LocalPath before reaching the
// transformer.
type BookSourceDescription struct {
	// LocalPath is the on-disk path of a local file (the ECP
	// for the current inline path-resolution sites — ProcessBook's
	// FilePath arg, and the temp file written by
	// ProcessBookFromDrive before it delegates to ProcessBook).
	LocalPath string

	// GoogleDocID is the bare document ID extracted from a
	// Google Docs URL. Populated when the source is a Google Doc;
	// the transformer passes --google-doc-id <id> to the Python
	// script which fetches the doc via its own Drive client.
	GoogleDocID string

	// DriveFileURL is the public Google Drive URL of a source
	// file. EXPAND-phase: drive.go::ProcessBookFromDrive
	// resolves this to a LocalPath via drive.Reader.DownloadFile
	// before handing off to the transformer; future BookSourceReader
	// adapter implementations can implement this differently.
	DriveFileURL string
}

// TransformRequest is the canonical input surface for the book
// transformer (Python script invocation, today). The request
// shape intentionally DRIVES the existing --file / --google-doc-id
// / etc. CLI flags of book_summarizer.py; future transformer
// concretes (e.g. an in-process LLM-backed extraction pipeline)
// consume the same shape.
type TransformRequest struct {
	// Source is the resolved book bytes descriptor. Must be
	// either LocalPath OR GoogleDocID populated (not both, not
	// neither).
	Source BookSourceDescription

	// Instruction is the user-provided summarisation
	// directive.
	Instruction string

	// Model is the Ollama model name for the rewrite step.
	// Empty → transformer defaults to "gemma4:e4b".
	Model string

	// PagesPerChunk is the per-chunk page count (PDF path only).
	PagesPerChunk int

	// ChunkSize is the chunk character budget for the
	// summarise pipeline. Zero → 12000 (transformer default).
	ChunkSize int

	// OverlapSize is the chunk overlap size. Zero → 2000
	// (transformer default).
	OverlapSize int

	// MaxChunks is the upper bound on chunks processed.
	// Zero → unbounded.
	MaxChunks int

	// OllamaURL is the Ollama server URL. Empty →
	// http://127.0.0.1:11434 (transformer default).
	OllamaURL string

	// DriveFolderID is the destination Drive folder ID.
	// Empty → process.driveFolder fallback (composition root
	// field).
	DriveFolderID string

	// Language is the BCP-47 language tag for translation.
	Language string

	// TranslateOnly, when true, skips the summarisation step
	// and only emits the translated text.
	TranslateOnly bool

	// GeneratePDF, when true, generates a PDF alongside the
	// summary. EXPAND-phase: PDF gen happens inside the Python
	// subprocess; a future export split must move PDF generation
	// out of the transformer.
	GeneratePDF bool

	// PDFStyle is the optional PDF style preset name.
	PDFStyle string

	// OutputPath is the suggested local output path (the
	// Python script's --output flag; honoured natively by
	// book_summarizer.py). Empty → the transformer lets the
	// script pick the canonical path under cwd.
	OutputPath string
}

// TransformResult is the canonical output surface mirroring the
// legacy ProcessResult fields. The apply-layer wrapper
// ProcessResult is preserved (URL shape unchanged for the EXPAND
// window); the transformer is the only Python-aware surface.
type TransformResult struct {
	// OutputPath is the local path to the translated/summarised
	// text output.
	OutputPath string

	// PDFPath is the local path to the generated PDF (empty if
	// PDF generation wasn't requested).
	PDFPath string

	// DriveFolderURL is the public Drive folder URL where the
	// book artefacts landed.
	DriveFolderURL string

	// DriveDocURL is the public Drive document URL of the
	// generated Doc (Google Docs surface).
	DriveDocURL string

	// DrivePDFURL is the public Drive document URL of the
	// generated PDF (Google Docs surface).
	DrivePDFURL string

	// WordCount is the post-rewrite token count.
	WordCount int

	// ChunksProcessed is the chunk count that the transformer
	// emitted (PDF path only).
	ChunksProcessed int

	// Language is the post-translate language tag (echoes the
	// request field for clarity).
	Language string
}

// BookTransformer turns a TransformRequest into a TransformResult.
//
// EXPAND-phase concrete: pythontransformer.SubprocessTransformer
// (the bridge from the books apply layer to
// scripts/bridges/book_summarizer.py). The transformer is the
// SINGLE Python-aware surface in the apply layer — books.Service
// does NOT import os/exec anymore.
type BookTransformer interface {
	// Transform runs the book-summarisation pipeline against
	// the resolved source + request shape. Returns the
	// canonical TransformResult on success or a wrapped
	// subprocess error on failure. The implementation MUST
	// respect ctx cancellation (the subprocess is spawned
	// with exec.CommandContext(ctx, ...)).
	Transform(ctx context.Context, req *TransformRequest) (*TransformResult, error)

	// TransformWithProgress is the streaming variant: emits
	// progress via the onProgress callback as the Python
	// subprocess prints [PROGRESS] %d %s lines to stdout.
	// Returns the same canonical TransformResult on success.
	// Pure-pipeline concretes (no subprocess) can implement
	// this method by translating onProgress callbacks to
	// internal phase markers.
	TransformWithProgress(ctx context.Context, req *TransformRequest, onProgress func(int, string)) (*TransformResult, error)
}

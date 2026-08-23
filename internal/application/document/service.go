// Package document — service.go (P0 Commit 10, July 2026).
//
// Service is the application-layer renderer that turns a
// DocumentRequest into a PDF on disk under the per-job workspace.
//
// Path policy (C10 invariant):
//
//	The output PDF is written at
//	  <WorkspaceRoot>/job-<jobID>/attempt-<n>/<slug>.<format>
//	where WorkspaceRoot comes from the WorkspaceManager (C9).
//	This is the canonical per-job isolated workspace — the same
//	invariant the §5.4 path-containment check enforces in C9.
//
// Filename derivation:
//
//	Filename is derived from request.Title via textutil.Slugify +
//	the format extension. Hardcoding ".pdf" or any path-relative
//	suffix in the service is forbidden; the caller MUST pass a
//	format-driven service so the ArtifactManifest's Filename
//	matches the on-disk path's basename exactly.
package document

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/jung-kurt/gofpdf"
	"go.uber.org/zap"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job/workspace"
)

// Service is the canonical document-rendering service. It composes
// the per-job WorkspaceManager (C9) with the gofpdf renderer.
//
// Composition: NewService requires a non-nil workspace.WorkspaceManager.
// Fail-closed at composition time so a wired-up use case cannot
// silently bypass the §5.4 path-containment contract by running
// without an isolated workspace.
type Service struct {
	wm  workspace.WorkspaceManager
	log *zap.Logger
}

// NewService constructs the document Service. wm MUST be non-nil;
// composition-time fail-fast (per AGENTS.md Composition-Root Fail-
// Fast Patterns) prevents a future contributor from constructing a
// document service that bypasses the workspace isolation contract.
func NewService(wm workspace.WorkspaceManager, log *zap.Logger) (*Service, error) {
	if wm == nil {
		return nil, fmt.Errorf("document.NewService: workspace.WorkspaceManager is required (C10 fail-fast)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{wm: wm, log: log}, nil
}

// PDFInfo is the typed output of GeneratePDF. Carries everything
// the use case needs to build a Sender-safe ArtifactManifest sidecar
// (Path, Filename, SizeBytes, SHA256, PageCount).
type PDFInfo struct {
	// JobID is the canonical job identifier (derived from the
	// workspace.ManagedWorkspace).
	JobID string

	// Path is the on-disk absolute path of the produced PDF.
	// Lives under the per-job workspace; the §5.4 assertContained
	// check guarantees it cannot escape the workspace root.
	Path string

	// Filename is the leaf name of the produced file. Echoes
	// the Artifact.Filename in the manifest sidecar so the
	// Sender-side upload cycle's drive filename matches the
	// on-disk basename (diff-by-leaf is the canonical mismatch
	// failure mode pre-C10).
	Filename string

	// Slug is the slugified title stem (Filename minus `.pdf`).
	// Echoed in DocumentResult.Slug for caller diagnostics.
	Slug string

	// SizeBytes is the on-disk size in bytes.
	SizeBytes int64

	// SHA256 is the hex-encoded SHA-256 digest of the file contents.
	SHA256 string

	// PageCount is the number of pages the renderer emitted.
	PageCount int
}

// GeneratePDF is the canonical render entry point. It allocates a
// per-job workspace (via the C9 WorkspaceManager), renders the
// body into a PDF using gofpdf, computes the SHA-256 digest of the
// final file, and returns a typed PDFInfo. The use case is then
// responsible for materialising the Sender-safe ArtifactManifest
// sidecar from the PDFInfo.
//
// C10 invariants:
//
//  1. NO hardcoded paths in this service. The output path comes
//     from the WorkspaceManager (job-attempt-scoped per C9).
//  2. Filename is derived from request.Title + request.Format
//     (never from a caller-supplied string). Slug + extension.
//  3. SHA-256 + SizeBytes are computed AFTER the file is fully
//     written, so the sidecar values reflect the on-disk truth.
//  4. Service holds no global state; concurrent calls each get
//     their own ManagedWorkspace via Prepare.
func (s *Service) GeneratePDF(ctx context.Context, req DocumentRequest, jobID string, attempt int) (PDFInfo, error) {
	if strings.TrimSpace(req.Title) == "" {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: title is required")
	}
	if req.Body == "" {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: body is required")
	}
	format := req.Format
	if format == "" {
		format = FormatPDF
	}
	if format != FormatPDF {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: unsupported format %q (only %q is implemented in C10)", format, FormatPDF)
	}

	// Slug + filename derivation (C10 invariant #2).
	rawSlug := textutil.Slugify(strings.TrimSpace(req.Title))
	if rawSlug == "" {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: title %q slugifies to empty (use a non-empty title with safe characters)", req.Title)
	}
	filename := rawSlug + ".pdf"

	// Allocate the per-job workspace (C9).
	ws, err := s.wm.Prepare(ctx, jobID, attempt)
	if err != nil {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: workspace.Prepare: %w", err)
	}

	// Compose target path INSIDE the workspace (the §5.4 contract
	// forbids every part of the path from escaping ws.Root).
	targetPath := filepath.Join(ws.Root, filename)

	// Render the PDF using gofpdf.
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(req.Title, false)
	pdf.SetAuthor("PipelineGen", false)
	pdf.SetCreator("PipelineGen document.generate", false)

	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 24)
	pdf.CellFormat(0, 14, req.Title, "", 1, "L", false, 0, "")
	pdf.Ln(8)

	pdf.SetFont("Helvetica", "", 11)
	// Split on blank lines to honour multi-paragraph bodies. Reuse
	// the loop variable directly (Go 1.22+ per-iteration var semantics)
	// so a slugified-trim rename isn't needed.
	for _, para := range strings.Split(req.Body, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		pdf.MultiCell(0, 6, para, "", "L", false)
		pdf.Ln(3)
	}

	pageCount := pdf.PageNo()
	if pageCount <= 0 {
		pageCount = 1
	}

	if err := pdf.OutputFileAndClose(targetPath); err != nil {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: write %q: %w", targetPath, err)
	}

	// Post-write: compute SizeBytes + SHA256 (C10 invariant #3).
	size, statErr := os.Stat(targetPath)
	if statErr != nil {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: stat %q: %w", targetPath, statErr)
	}

	sha, shaErr := sha256File(targetPath)
	if shaErr != nil {
		return PDFInfo{}, fmt.Errorf("document.GeneratePDF: sha256 %q: %w", targetPath, shaErr)
	}

	s.log.Info("document generated",
		zap.String("job_id", jobID),
		zap.String("filename", filename),
		zap.String("path", targetPath),
		zap.Int64("size_bytes", size.Size()),
		zap.Int("page_count", pageCount),
	)

	return PDFInfo{
		JobID:     jobID,
		Path:      targetPath,
		Filename:  filename,
		Slug:      rawSlug,
		SizeBytes: size.Size(),
		SHA256:    sha,
		PageCount: pageCount,
	}, nil
}

// sha256File computes the hex-encoded SHA-256 digest of the file at
// path. Uses stdlib io.Copy so the on-disk content deterministically
// drives the digest (no in-memory buffer needed for arbitrary sizes).
// Kept package-internal — the sidecar / Sender uses job.ComputeSHA256
// (the cross-package canonical helper) at upload-cycle time.
func sha256File(path string) (string, error) {
	h, _, err := digest.SHA256File(path)
	if err != nil {
		return "", fmt.Errorf("sha256File: %q: %w", path, err)
	}
	return h, nil
}

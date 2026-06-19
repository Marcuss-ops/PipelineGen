package lessons

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/jung-kurt/gofpdf"
)

// GenerateLessonPDF creates a PDF from the lesson result using gofpdf.
// Returns the full path to the generated PDF file.
// The PDF includes: title page, table of contents, chapters with headings,
// body text, and embedded images (from local paths).
// This component can be reused independently for any LessonResult.
func (s *Service) GenerateLessonPDF(result *LessonResult, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	slug := textutil.Slugify(result.Title)
	pdfPath := filepath.Join(outputDir, slug+".pdf")

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(result.Title, true)
	pdf.SetAuthor("PipelineGen Lessons", true)
	pdf.SetCreator("PipelineGen", true)

	// --- Title page ---
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 28)
	pdf.Ln(60)
	pdf.CellFormat(190, 20, result.Title, "", 1, "C", false, 0, "")
	pdf.Ln(10)

	pdf.SetFont("Helvetica", "", 14)
	langName := result.Language
	if langName == "it" {
		langName = "Italiano"
	} else if langName == "en" {
		langName = "English"
	}
	pdf.CellFormat(190, 10, fmt.Sprintf("Lingua: %s", langName), "", 1, "C", false, 0, "")
	pdf.CellFormat(190, 10, fmt.Sprintf("Capitoli: %d", len(result.Chapters)), "", 1, "C", false, 0, "")
	pdf.CellFormat(190, 10, fmt.Sprintf("Parole totali: %d", result.TotalWords), "", 1, "C", false, 0, "")
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(190, 10, fmt.Sprintf("Generato il: %s", result.GeneratedAt), "", 1, "C", false, 0, "")

	// --- Table of contents ---
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(190, 15, "Indice", "", 1, "L", false, 0, "")
	pdf.Ln(5)

	for _, ch := range result.Chapters {
		if ch.Error != "" {
			continue
		}
		pdf.SetFont("Helvetica", "", 12)
		tocLine := fmt.Sprintf("Capitolo %d: %s", ch.Index+1, ch.Title)
		pdf.CellFormat(190, 8, tocLine, "", 1, "L", false, 0, "")
	}

	// --- Chapters ---
	for _, ch := range result.Chapters {
		pdf.AddPage()

		// Chapter title
		pdf.SetFont("Helvetica", "B", 20)
		pdf.CellFormat(190, 15, fmt.Sprintf("Capitolo %d", ch.Index+1), "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 16)
		pdf.CellFormat(190, 12, ch.Title, "", 1, "L", false, 0, "")
		pdf.Ln(5)

		if ch.Error != "" {
			pdf.SetFont("Helvetica", "I", 11)
			pdf.MultiCell(190, 6, ch.Error, "", "L", false)
			continue
		}

		// Image if available locally
		if ch.Image != nil {
			imgPath := resolveLocalImagePath(ch.Image.PathRel)
			if imgPath != "" && fileExists(imgPath) {
				pdf.ImageOptions(imgPath, 15, pdf.GetY(), 180, 0, false, gofpdf.ImageOptions{ImageType: "", ReadDpi: true}, 0, "")
				pdf.Ln(5)
			}
		}

		// Body text — split into paragraphs for proper formatting
		pdf.SetFont("Helvetica", "", 11)
		paragraphs := strings.Split(ch.Content, "\n\n")
		for _, para := range paragraphs {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			pdf.MultiCell(190, 6, para, "", "J", false)
			pdf.Ln(3)
		}

		// Word count
		pdf.Ln(5)
		pdf.SetFont("Helvetica", "I", 9)
		pdf.CellFormat(190, 5, fmt.Sprintf("Parole: %d", ch.WordCount), "", 1, "R", false, 0, "")
	}

	// Save PDF
	if err := pdf.OutputFileAndClose(pdfPath); err != nil {
		return "", fmt.Errorf("failed to write PDF file: %w", err)
	}

	s.log.Info("lesson PDF generated",
		zap.String("path", pdfPath),
		zap.Int("chapters", len(result.Chapters)),
	)

	return pdfPath, nil
}

// GeneratePDFFromMarkdown is a future-ready hook for MD→PDF conversion.
// Currently generates PDF from the structured LessonResult for richer output.
// When pandoc/wkhtmltopdf becomes available, switch to markdown-based generation.
func (s *Service) GeneratePDFFromMarkdown(mdPath string, result *LessonResult, outputDir string) (string, error) {
	return s.GenerateLessonPDF(result, outputDir)
}

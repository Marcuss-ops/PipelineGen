package script

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// createDocumentFromRequest handles the core document creation logic
func (h *ScriptPipelineHandler) createDocumentFromRequest(ctx context.Context, req *CreateDocumentRequest) (*CreateDocumentResponse, error) {
	h.normalizeCreateDocumentRequest(req)

	topic := req.Topic
	if topic == "" {
		topic = req.Title
	}

	h.enrichCreateDocumentRequest(ctx, req, topic)

	stockFolderID := normalizeDriveFolderID(req.StockFolderURL)
	scriptBody := req.Script
	if strings.TrimSpace(scriptBody) == "" {
		scriptBody = req.SourceText
	}
	content := h.BuildDocumentContent(
		req.Title,
		topic,
		req.Duration,
		req.Language,
		scriptBody,
		req.Segments,
		req.ArtlistAssocs,
		stockFolderID,
		req.StockFolder,
		req.DriveAssocs,
		req.FrasiImportanti,
		req.NomiSpeciali,
		req.ParoleImportanti,
		req.EntitaConImmagine,
		req.Translations,
	)

	if req.PreviewOnly {
		previewPath, err := savePreviewDocument(req.Title, content)
		if err != nil {
			return nil, err
		}
		return &CreateDocumentResponse{
			Ok:          true,
			DocID:       "local_file",
			DocURL:      previewPath,
			PreviewPath: previewPath,
			Mode:        "preview",
		}, nil
	}

	if h.docClient == nil {
		return nil, fmt.Errorf("Docs client not initialized")
	}

	publishCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	doc, err := h.docClient.CreateDoc(publishCtx, req.Title, content, "")
	if err != nil {
		return nil, err
	}

	return &CreateDocumentResponse{
		Ok:     true,
		DocID:  doc.ID,
		DocURL: doc.URL,
		Mode:   "publish",
	}, nil
}

// normalizeCreateDocumentRequest ensures Script field is populated from SourceText if empty
func (h *ScriptPipelineHandler) normalizeCreateDocumentRequest(req *CreateDocumentRequest) {
	if strings.TrimSpace(req.Script) == "" && strings.TrimSpace(req.SourceText) != "" {
		req.Script = req.SourceText
	}
}

// savePreviewDocument saves a preview document as a local markdown file
func savePreviewDocument(title, content string) (string, error) {
	base := strings.TrimSpace(title)
	if base == "" {
		base = "script_doc"
	}
	base = strings.NewReplacer(" ", "_", ":", "", "/", "_", "\\", "_", "\n", "_", "\r", "_").Replace(base)
	if len([]rune(base)) > 50 {
		runes := []rune(base)
		base = string(runes[:50])
	}
	filename := fmt.Sprintf("/tmp/%s_%d.md", base, time.Now().Unix())
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to save preview file: %w", err)
	}
	return fmt.Sprintf("file://%s", filename), nil
}

// BuildDocumentContent builds the document content from all components
func (h *ScriptPipelineHandler) BuildDocumentContent(
	title, topic string,
	duration int,
	language string,
	scriptBody string,
	segments []Segment,
	artlistAssocs []ArtlistAssoc,
	stockFolderID string,
	stockFolder string,
	driveAssocs []DriveFolderAssoc,
	frasiImportanti []string,
	nomiSpeciali []string,
	paroleImportanti []string,
	entitaConImmagine []EntityImage,
	translations []Translation,
) string {
	var b strings.Builder

	// 1. Metadata & Title
	mins := duration / 60
	secs := duration % 60
	b.WriteString(fmt.Sprintf("📝 %s\n", title))
	b.WriteString(fmt.Sprintf("Topic: %s | Durata: %d:%02d | Lingua: %s | %s\n", topic, mins, secs, language, time.Now().Format("02/01/2006")))
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	// 2. Main Script
	b.WriteString("📖 SCRIPT COMPLETO\n")
	b.WriteString(strings.Repeat("-", 30) + "\n")
	b.WriteString(scriptBody + "\n\n")
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	// 3. Entities & Highlights
	b.WriteString("🔍 HIGHLIGHTS ED ENTITÀ\n")
	b.WriteString(strings.Repeat("-", 30) + "\n")

	if len(frasiImportanti) > 0 {
		b.WriteString("📌 FRASI IMPORTANTI:\n")
		for i, f := range frasiImportanti {
			b.WriteString(fmt.Sprintf("   %d. %s\n", i+1, f))
		}
		b.WriteString("\n")
	}

	if len(nomiSpeciali) > 0 {
		b.WriteString("👤 NOMI SPECIALI:\n")
		b.WriteString("   " + strings.Join(nomiSpeciali, ", ") + "\n\n")
	}

	if len(paroleImportanti) > 0 {
		b.WriteString("🔑 PAROLE IMPORTANTI:\n")
		b.WriteString("   " + strings.Join(paroleImportanti, ", ") + "\n\n")
	}

	if len(entitaConImmagine) > 0 {
		b.WriteString("🖼️ ENTITÀ CON IMMAGINE:\n")
		for _, ei := range entitaConImmagine {
			b.WriteString(fmt.Sprintf("   🖼 %s → %s\n", ei.Entity, ei.ImageURL))
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	// 4. Stock & Drive Associations
	b.WriteString("📁 ARCHIVIO CLIPS\n")
	b.WriteString(strings.Repeat("-", 30) + "\n")

	if stockFolder != "" || stockFolderID != "" {
		b.WriteString(fmt.Sprintf("📦 CARTELLA STOCK PRINCIPALE: %s\n", stockFolder))
		if stockFolderID != "" {
			b.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/drive/folders/%s\n", stockFolderID))
		}
		b.WriteString("\n")
	}

	if len(driveAssocs) > 0 {
		b.WriteString("📎 COLLEGAMENTI DRIVE SPECIFICI:\n")
		for _, da := range driveAssocs {
			b.WriteString(fmt.Sprintf("   • Phrase: %s\n", da.Phrase))
			b.WriteString(fmt.Sprintf("     Cartella: %s\n", da.FolderName))
			b.WriteString(fmt.Sprintf("     Link: %s\n\n", da.FolderURL))
		}
	}
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	// 5. Artlist Clips
	if len(artlistAssocs) > 0 {
		b.WriteString("🎶 CLIPS ARTLIST\n")
		b.WriteString(strings.Repeat("-", 30) + "\n")
		for _, aa := range artlistAssocs {
			b.WriteString(fmt.Sprintf("   • Phrase: %s\n", aa.Phrase))
			for _, clip := range aa.Clips {
				b.WriteString(fmt.Sprintf("     - %s (%s)\n", clip.Name, clip.URL))
			}
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	// 6. Translations
	if len(translations) > 0 {
		b.WriteString("🌍 TRADUZIONI\n")
		b.WriteString(strings.Repeat("-", 30) + "\n")
		for _, t := range translations {
			b.WriteString(fmt.Sprintf("📍 LINGUA: %s\n", t.Language))
			b.WriteString(t.Text + "\n\n")
		}
		b.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	// 7. Segments Breakdown
	if len(segments) > 0 {
		b.WriteString("⏱️ SEGMENTAZIONE SCRIPT\n")
		b.WriteString(strings.Repeat("-", 30) + "\n")
		for _, s := range segments {
			smins := s.StartTime / 60
			ssecs := s.StartTime % 60
			emins := s.EndTime / 60
			esecs := s.EndTime % 60
			b.WriteString(fmt.Sprintf("[%d:%02d - %d:%02d] %s\n", smins, ssecs, emins, esecs, s.Text))
		}
		b.WriteString(strings.Repeat("=", 100) + "\n")
	}

	return b.String()
}
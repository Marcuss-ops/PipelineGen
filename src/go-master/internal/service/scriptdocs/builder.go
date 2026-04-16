package scriptdocs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"
)

func (s *ScriptDocService) createDocWithFallback(ctx context.Context, title string, content string) (docID string, docURL string, err error) {
	if s.docClient == nil {
		return s.saveToLocalFile(title, content)
	}
	doc, err := s.docClient.CreateDoc(ctx, title, content, "")
	if err != nil {
		logger.Warn("Google Docs creation failed, falling back to local file", zap.Error(err))
		return s.saveToLocalFile(title, content)
	}
	return doc.ID, doc.URL, nil
}

func (s *ScriptDocService) saveToLocalFile(title string, content string) (string, string, error) {
	filename := strings.ReplaceAll(title, " ", "_")
	filename = strings.ReplaceAll(filename, ":", "")
	filename = fmt.Sprintf("/tmp/%s_%d.txt", filename[:util.Min(50, len(filename))], time.Now().Unix())
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed to save local file: %w", err)
	}
	return "local_file", fmt.Sprintf("file://%s", filename), nil
}

func (s *ScriptDocService) buildMultilingualDocument(topic string, duration int, stockFolder StockFolder, langResults []LanguageResult) string {
	var b strings.Builder
	caser := cases.Title(language.Und)

	mins := duration / 60
	secs := duration % 60
	b.WriteString(fmt.Sprintf("📝 %s\n", topic))
	b.WriteString(fmt.Sprintf("Topic: %s | Durata: %d:%02d | %s\n", topic, mins, secs, time.Now().Format("02/01/2006")))
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	b.WriteString("📦 STOCK DRIVE\n\n")
	b.WriteString(fmt.Sprintf("📁 %s\n", stockFolder.Name))
	b.WriteString(fmt.Sprintf("🔗 %s\n\n", stockFolder.URL))
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	for _, lr := range langResults {
		info, ok := LanguageInfo[lr.Language]
		if !ok { info.Name = lr.Language }

		b.WriteString(fmt.Sprintf("🌍 %s\n\n", info.Name))
		b.WriteString(strings.Repeat("-", 80) + "\n\n")
		b.WriteString(lr.FullText + "\n\n")
		b.WriteString(strings.Repeat("-", 80) + "\n\n")

		b.WriteString(fmt.Sprintf("🔍 ENTITÀ ESTRATTE (%s)\n\n", info.Name))
		b.WriteString(fmt.Sprintf("📌 FRASI IMPORTANTI (%d)\n", len(lr.FrasiImportanti)))
		for i, f := range lr.FrasiImportanti {
			b.WriteString(fmt.Sprintf("   %d. %s\n", i+1, f))
		}
		b.WriteString("\n")

		b.WriteString(fmt.Sprintf("👤 NOMI SPECIALI (%d)\n", len(lr.NomiSpeciali)))
		b.WriteString("   " + strings.Join(lr.NomiSpeciali, ", ") + "\n\n")

		b.WriteString(fmt.Sprintf("🔑 PAROLE IMPORTANTI (%d)\n", len(lr.ParoleImportant)))
		b.WriteString("   " + strings.Join(lr.ParoleImportant, ", ") + "\n\n")

		b.WriteString(fmt.Sprintf("🖼️ ENTITÀ CON IMMAGINE (%d)\n", len(lr.EntitaConImmagine)))
		for entity, imageURL := range lr.EntitaConImmagine {
			b.WriteString(fmt.Sprintf("   🖼 %s → %s\n", entity, imageURL))
		}
		b.WriteString("\n")
		b.WriteString(strings.Repeat("-", 80) + "\n\n")

		if len(lr.Associations) > 0 {
			b.WriteString("🎬 ASSOCIAZIONI CLIP\n\n")
			for i, assoc := range lr.Associations {
				b.WriteString(fmt.Sprintf("%d. 💬 \"%s\"\n", i+1, truncate(assoc.Phrase, 160)))
				switch assoc.Type {
				case "STOCK_FOLDER":
					if assoc.StockFolder != nil {
						b.WriteString(fmt.Sprintf("   ✅ Fonte primaria: DRIVE FOLDER\n"))
						b.WriteString(fmt.Sprintf("   📁 %s\n", assoc.StockFolder.Name))
						b.WriteString(fmt.Sprintf("   🔗 %s\n", assoc.StockFolder.URL))
					}
				case "DYNAMIC":
					if assoc.DynamicClip != nil {
						b.WriteString("   ✅ Fonte primaria: DYNAMIC SEARCH\n")
						b.WriteString(fmt.Sprintf("   📁 %s\n", assoc.DynamicClip.Folder))
						b.WriteString(fmt.Sprintf("   📹 %s\n", assoc.DynamicClip.Filename))
						b.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/file/d/%s/view\n", assoc.DynamicClip.DriveID))
					}
				case "STOCK_DB", "STOCK":
					if assoc.ClipDB != nil {
						b.WriteString("   ✅ Fonte primaria: STOCK DB\n")
						b.WriteString(fmt.Sprintf("   📹 %s\n", assoc.ClipDB.Filename))
						b.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/file/d/%s/view\n", assoc.ClipDB.ClipID))
						if assoc.ClipDB.FolderID != "" {
							b.WriteString(fmt.Sprintf("   📁 %s\n", assoc.ClipDB.FolderID))
						}
					}
				case "ARTLIST":
					if assoc.Clip != nil {
						b.WriteString("   ✅ Fonte primaria: ARTLIST\n")
						b.WriteString(fmt.Sprintf("   🟢 Artlist: %s\n", assoc.Clip.Name))
						b.WriteString(fmt.Sprintf("   📁 Stock/Artlist/%s\n", caser.String(strings.ToLower(assoc.Clip.Term))))
						b.WriteString(fmt.Sprintf("   🔗 %s\n", assoc.Clip.URL))
					}
				default:
					b.WriteString("   ⚠️ Nessuna associazione affidabile\n")
				}
				if assoc.MatchedKeyword != "" {
					b.WriteString(fmt.Sprintf("   🔍 Match: %s\n", assoc.MatchedKeyword))
				}
				b.WriteString(fmt.Sprintf("   📊 Confidenza: %.2f\n\n", assoc.Confidence))
			}
			b.WriteString(strings.Repeat("-", 80) + "\n\n")
		}

		b.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	return b.String()
}

func langNames(results []LanguageResult) string {
	var names []string
	for _, r := range results {
		if info, ok := LanguageInfo[r.Language]; ok {
			names = append(names, info.Name)
		} else {
			names = append(names, r.Language)
		}
	}
	return strings.Join(names, ", ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

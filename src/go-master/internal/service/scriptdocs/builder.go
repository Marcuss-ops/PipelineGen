package scriptdocs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"velox/go-master/internal/stockdb"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"

	"go.uber.org/zap"
)

// createDocWithFallback tries to create a Google Doc, falls back to local file if it fails.
func (s *ScriptDocService) createDocWithFallback(ctx context.Context, title string, content string) (docID string, docURL string, err error) {
	if s.docClient == nil {
		// No doc client available, save to local file as fallback
		return s.saveToLocalFile(title, content)
	}

	doc, err := s.docClient.CreateDoc(ctx, title, content, "")
	if err != nil {
		logger.Warn("Google Docs creation failed, falling back to local file",
			zap.Error(err),
		)
		return s.saveToLocalFile(title, content)
	}

	return doc.ID, doc.URL, nil
}

// saveToLocalFile saves the document content to a local file.
func (s *ScriptDocService) saveToLocalFile(title string, content string) (string, string, error) {
	// Sanitize filename
	filename := strings.ReplaceAll(title, " ", "_")
	filename = strings.ReplaceAll(filename, ":", "")
	filename = fmt.Sprintf("/tmp/%s_%d.txt", filename[:util.Min(50, len(filename))], time.Now().Unix())

	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed to save local file: %w", err)
	}

	return "local_file", fmt.Sprintf("file://%s", filename), nil
}

// buildMultilingualDocument formats the full document text with stock folder,
// each language's script, entities, and clip associations.
func (s *ScriptDocService) buildMultilingualDocument(topic string, duration int, stockFolder StockFolder, langResults []LanguageResult) string {
	var b strings.Builder
	caser := cases.Title(language.Und)

	// Header
	mins := duration / 60
	secs := duration % 60
	b.WriteString(fmt.Sprintf("📝 %s\n", topic))
	b.WriteString(fmt.Sprintf("Topic: %s | Durata: %d:%02d | %s\n", topic, mins, secs, time.Now().Format("02/01/2006")))
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	// Stock folder section
	b.WriteString("📦 STOCK DRIVE\n\n")
	b.WriteString(fmt.Sprintf("📁 %s\n", stockFolder.Name))
	b.WriteString(fmt.Sprintf("🔗 %s\n\n", stockFolder.URL))

	// List actual clips from the Stock folder if available
	if s.stockDB != nil && stockFolder.ID != "" {
		clips, err := s.stockDB.GetClipsForFolder(stockFolder.ID)
		if err == nil && len(clips) > 0 {
			maxClips := 10
			if len(clips) < maxClips {
				maxClips = len(clips)
			}
			b.WriteString(fmt.Sprintf("🎬 %d clip disponibili:\n\n", len(clips)))
			for i := 0; i < maxClips; i++ {
				clip := clips[i]
				b.WriteString(fmt.Sprintf("  %d. 📹 %s\n", i+1, clip.Filename))
				b.WriteString(fmt.Sprintf("     🔗 https://drive.google.com/file/d/%s/view\n", clip.ClipID))
				if clip.Duration > 0 {
					durSec := clip.Duration / 1000
					b.WriteString(fmt.Sprintf("     ⏱ %d:%02d\n", durSec/60, durSec%60))
				}
			}
			if len(clips) > 10 {
				b.WriteString(fmt.Sprintf("  ... e altri %d clip\n", len(clips)-10))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString(strings.Repeat("=", 100) + "\n\n")

	// Each language section
	for _, lr := range langResults {
		info, ok := LanguageInfo[lr.Language]
		if !ok {
			info.Name = lr.Language
		}

		b.WriteString(fmt.Sprintf("🌍 %s\n\n", info.Name))
		b.WriteString(strings.Repeat("-", 80) + "\n\n")

		// Full script
		b.WriteString(lr.FullText + "\n\n")
		b.WriteString(strings.Repeat("-", 80) + "\n\n")

		// Entities
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

		// Separate Dynamic, Stock and Artlist
		var dynamicAssocs, stockAssocs, artlistAssocs []ClipAssociation
		for _, assoc := range lr.Associations {
			if assoc.Type == "DYNAMIC" {
				dynamicAssocs = append(dynamicAssocs, assoc)
			} else if assoc.Type == "STOCK_DB" || assoc.Type == "STOCK" {
				stockAssocs = append(stockAssocs, assoc)
			} else if assoc.Type == "ARTLIST" {
				artlistAssocs = append(artlistAssocs, assoc)
			}
		}

		if len(dynamicAssocs) > 0 {
			b.WriteString("🎬 CLIP TROVATE DINAMICAMENTE\n\n")
			for i, assoc := range dynamicAssocs {
				if assoc.DynamicClip != nil {
					b.WriteString(fmt.Sprintf("%d. 💬 \"%s...\"\n", i+1, truncate(assoc.Phrase, 150)))
					b.WriteString(fmt.Sprintf("   📁 %s\n", assoc.DynamicClip.Folder))
					b.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/file/d/%s/view\n", assoc.DynamicClip.DriveID))
					b.WriteString(fmt.Sprintf("   🔍 Keyword: '%s'\n", assoc.DynamicClip.Keyword))
					b.WriteString("\n")
				}
			}
			b.WriteString(strings.Repeat("-", 80) + "\n\n")
		}

		if len(stockAssocs) > 0 {
			b.WriteString("📦 ASSOCIAZIONI STOCK\n\n")
			// Get clips from this folder
			var folderClips []stockdb.StockClipEntry
			if s.stockDB != nil && stockFolder.ID != "" {
				folderClips, _ = s.stockDB.GetClipsForFolder(stockFolder.ID)
			}

			for i, assoc := range stockAssocs {
				b.WriteString(fmt.Sprintf("%d. 💬 \"%s...\"\n", i+1, truncate(assoc.Phrase, 150)))
				b.WriteString(fmt.Sprintf("   📁 %s\n", stockFolder.Name))
				b.WriteString(fmt.Sprintf("   🔗 %s\n", stockFolder.URL))
				// Show available clips from folder
				if len(folderClips) > 0 {
					clipIdx := i % len(folderClips)
					clip := folderClips[clipIdx]
					b.WriteString(fmt.Sprintf("   📹 Clip: %s\n", clip.Filename))
					b.WriteString(fmt.Sprintf("   🔗 https://drive.google.com/file/d/%s/view\n", clip.ClipID))
				}
				b.WriteString("\n")
			}
			b.WriteString(strings.Repeat("-", 80) + "\n\n")
		}

		if len(artlistAssocs) > 0 {
			b.WriteString("🎬 ASSOCIAZIONI ARTLIST\n\n")
			for i, assoc := range artlistAssocs {
				if assoc.Clip != nil {
					b.WriteString(fmt.Sprintf("%d. 💬 \"%s...\"\n", i+1, truncate(assoc.Phrase, 150)))
					b.WriteString(fmt.Sprintf("   🟢 Artlist: %s\n", assoc.Clip.Name))
					b.WriteString(fmt.Sprintf("   📁 Stock/Artlist/%s\n", caser.String(strings.ToLower(assoc.Clip.Term))))
					b.WriteString(fmt.Sprintf("   🔗 %s\n", assoc.Clip.URL))
					b.WriteString(fmt.Sprintf("   🔍 Concept: '%s'\n", assoc.Clip.Term))
					b.WriteString("\n")
				}
			}
			b.WriteString(strings.Repeat("-", 80) + "\n\n")
		}

		b.WriteString(strings.Repeat("=", 100) + "\n\n")
	}

	return b.String()
}

// langNames joins language display names for the document header.
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

// truncate truncates strings to maxLen with "..." suffix.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

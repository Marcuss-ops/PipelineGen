package script

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/stockdb"
)

// normalizeDriveFolderID extracts the ID from a Google Drive folder URL or returns the ID as is.
func normalizeDriveFolderID(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "drive.google.com/drive/folders/") {
		parts := strings.Split(v, "/folders/")
		if len(parts) > 1 {
			id := parts[1]
			if i := strings.Index(id, "?"); i >= 0 {
				id = id[:i]
			}
			if i := strings.Index(id, "/"); i >= 0 {
				id = id[:i]
			}
			return strings.TrimSpace(id)
		}
	}
	return v
}

// resolveDriveFolderName returns the folder name for a Drive folder ID when available.
func (h *ScriptPipelineHandler) resolveDriveFolderName(folderID string) string {
	if h.driveClient == nil || strings.TrimSpace(folderID) == "" {
		return ""
	}
	f, err := h.driveClient.GetFile(context.Background(), folderID)
	if err != nil || f == nil {
		return ""
	}
	return strings.TrimSpace(f.Name)
}

// resolveStockFolderForDocument searches the stock database for a folder matching the topic.
func (h *ScriptPipelineHandler) resolveStockFolderForDocument(topic string) (folderID, folderName string) {
	if h.stockDB == nil || strings.TrimSpace(topic) == "" {
		return "", ""
	}

	tryFolder := func(folder *stockdb.StockFolderEntry) (string, string, bool) {
		if folder == nil || strings.TrimSpace(folder.DriveID) == "" {
			return "", "", false
		}
		name := strings.TrimSpace(folder.FullPath)
		if name == "" {
			name = strings.TrimSpace(folder.TopicSlug)
		}
		return folder.DriveID, name, true
	}

	if folder, _ := h.stockDB.FindFolderByTopicInSection(topic, "stock"); folder != nil {
		if id, name, ok := tryFolder(folder); ok {
			return id, name
		}
	}
	if folder, _ := h.stockDB.FindFolderByTopic(topic); folder != nil {
		if id, name, ok := tryFolder(folder); ok {
			return id, name
		}
	}

	return "", ""
}

// resolveClipFolderForDocument searches the local clip DB for a folder matching the query.
func (h *ScriptPipelineHandler) resolveClipFolderForDocument(query string) (folderID, folderName string) {
	if h.clipDB == nil || strings.TrimSpace(query) == "" {
		return "", ""
	}

	folders := h.clipDB.SearchFolders(query)
	if len(folders) == 0 {
		return "", ""
	}

	folder := folders[0]
	name := strings.TrimSpace(folder.FullPath)
	if name == "" {
		name = strings.TrimSpace(folder.TopicSlug)
	}
	if name == "" {
		return "", ""
	}
	return folder.DriveID, name
}

// formatClipFolderDisplayPath renders a stable, human-readable folder path for docs.
func formatClipFolderDisplayPath(folder clip.IndexedFolder) string {
	parts := make([]string, 0, 4)
	parts = append(parts, "Clip")

	if group := strings.TrimSpace(folder.Group); group != "" {
		parts = append(parts, clipGroupDisplayName(group))
	}

	path := strings.TrimSpace(folder.Path)
	if path != "" {
		for _, part := range strings.Split(path, "/") {
			part = strings.TrimSpace(part)
			if part != "" {
				parts = append(parts, part)
			}
		}
	}

	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(cleaned) > 0 && strings.EqualFold(cleaned[len(cleaned)-1], part) {
			continue
		}
		cleaned = append(cleaned, part)
	}

	return strings.Join(cleaned, " -> ")
}

func clipGroupDisplayName(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return ""
	}
	for _, g := range clip.ClipGroups {
		if strings.EqualFold(g.ID, group) || strings.EqualFold(g.Name, group) {
			return g.Name
		}
	}
	return strings.Title(strings.ToLower(group))
}

// normalizeCreateDocumentRequest ensures required fields are set in a CreateDocumentRequest.
func (h *ScriptPipelineHandler) normalizeCreateDocumentRequest(req *CreateDocumentRequest) {
	if strings.TrimSpace(req.Script) == "" && strings.TrimSpace(req.SourceText) != "" {
		req.Script = req.SourceText
	}
}

// savePreviewDocument saves content to a temporary Markdown file for local preview.
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

// extractPhrases extracts initial and final phrases from text for display.
func extractPhrases(text string) (string, string) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return "", ""
	}
	if len(words) <= 3 {
		return text, text
	}
	initial := strings.Join(words[:3], " ")
	final := strings.Join(words[len(words)-3:], " ")
	return initial, final
}

func shortPhrase(text string, maxWords int) string {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) == 0 {
		return ""
	}
	if maxWords <= 0 || len(words) <= maxWords {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:maxWords], " ")
}

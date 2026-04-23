package script

import (
	"fmt"
	"os"
	"strings"
	"time"

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

	tokens := make([]string, 0, 4)
	for _, raw := range strings.FieldsFunc(strings.ToLower(topic), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '/' || r == ':' || r == ',' || r == '.'
	}) {
		token := strings.TrimSpace(raw)
		if len(token) < 3 {
			continue
		}
		tokens = append(tokens, token)
	}

	if len(tokens) == 0 {
		return "", ""
	}

	if folders, err := h.stockDB.GetFoldersBySection("stock"); err == nil {
		for _, folder := range folders {
			candidate := strings.ToLower(folder.FullPath + " " + folder.TopicSlug)
			for _, token := range tokens {
				if strings.Contains(candidate, token) {
					if id, name, ok := tryFolder(&folder); ok {
						return id, name
					}
				}
			}
		}
	}

	return "", ""
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

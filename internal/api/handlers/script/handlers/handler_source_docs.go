package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/upload/drive"
)

func (h *ScriptFlowHandler) createGeneratedGoogleDoc(ctx context.Context, pkg GeneratedScriptPackage, videoScenes []VideoScene) (*drive.Doc, error) {
	if h.docClient == nil {
		return nil, fmt.Errorf("google docs client not initialized")
	}

	title := strings.TrimSpace(pkg.Title)
	if title == "" {
		title = strings.TrimSpace(pkg.OutputName)
	}
	if title == "" {
		title = "Generated Script"
	}

	return h.docClient.CreateDoc(ctx, title, buildGeneratedDocContent(pkg, videoScenes), h.googleDocsFolderID())
}

func (h *ScriptFlowHandler) googleDocsFolderID() string {
	if h.cfg == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.Drive.RootFolder())
}

func buildGeneratedDocContent(pkg GeneratedScriptPackage, videoScenes []VideoScene) string {
	var b strings.Builder
	if strings.TrimSpace(pkg.Title) != "" {
		b.WriteString("Title:\n")
		b.WriteString(strings.TrimSpace(pkg.Title))
		b.WriteString("\n\n")
	}
	
	if pkg.Voiceover != nil && pkg.Voiceover.DriveLink != "" {
		b.WriteString("Unified Voiceover Link (Base):\n")
		b.WriteString(pkg.Voiceover.DriveLink)
		b.WriteString("\n\n")
	}

	b.WriteString("Script (Base):\n")
	b.WriteString(strings.TrimSpace(pkg.RewrittenScript))
	b.WriteString("\n\n")

	for lang, trans := range pkg.Translations {
		if trans.Voiceover != nil && trans.Voiceover.DriveLink != "" {
			b.WriteString(fmt.Sprintf("Unified Voiceover Link (%s):\n", strings.ToUpper(lang)))
			b.WriteString(trans.Voiceover.DriveLink)
			b.WriteString("\n\n")
		}
		b.WriteString(fmt.Sprintf("Script (%s):\n", strings.ToUpper(lang)))
		b.WriteString(strings.TrimSpace(trans.RewrittenScript))
		b.WriteString("\n\n")
	}

	b.WriteString("Scenes JSON:\n")
	b.WriteString(renderGeneratedJSONBlock(videoScenes))
	b.WriteString("\n")
	return b.String()
}

func renderGeneratedJSONBlock(videoScenes []VideoScene) string {
	jsonData := renderGeneratedJSON(videoScenes)
	var b strings.Builder
	b.WriteString("```json\n")
	b.WriteString(jsonData)
	if !strings.HasSuffix(jsonData, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```")
	return b.String()
}

func renderGeneratedJSON(videoScenes []VideoScene) string {
	data, err := json.MarshalIndent(videoScenes, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(data)
}

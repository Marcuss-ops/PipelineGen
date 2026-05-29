package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/upload/drive"
)

func (h *ScriptFlowHandler) createGeneratedGoogleDoc(ctx context.Context, pkg GeneratedScriptPackage) (*drive.Doc, error) {
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

	return h.docClient.CreateDoc(ctx, title, buildGeneratedDocContent(pkg), h.googleDocsFolderID())
}

func (h *ScriptFlowHandler) googleDocsFolderID() string {
	if h.cfg == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.Drive.RootFolder())
}

func buildGeneratedDocContent(pkg GeneratedScriptPackage) string {
	var b strings.Builder
	if strings.TrimSpace(pkg.Title) != "" {
		b.WriteString("Title:\n")
		b.WriteString(strings.TrimSpace(pkg.Title))
		b.WriteString("\n\n")
	}
	b.WriteString("Full Script:\n")
	b.WriteString(strings.TrimSpace(pkg.RewrittenScript))
	b.WriteString("\n\n")
	b.WriteString("Scenes JSON:\n")
	b.WriteString(renderGeneratedJSONBlock(pkg))
	b.WriteString("\n")
	return b.String()
}

func renderGeneratedJSONBlock(pkg GeneratedScriptPackage) string {
	jsonData := renderGeneratedJSON(pkg)
	var b strings.Builder
	b.WriteString("```json\n")
	b.WriteString(jsonData)
	if !strings.HasSuffix(jsonData, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```")
	return b.String()
}

func renderGeneratedJSON(pkg GeneratedScriptPackage) string {
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

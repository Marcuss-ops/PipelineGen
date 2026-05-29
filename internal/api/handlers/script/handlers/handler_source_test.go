package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"velox/go-master/internal/config"
	"velox/go-master/internal/upload/drive"
)

func TestWriteGeneratedScriptFilesPlacesScriptBeforeJson(t *testing.T) {
	tmpDir := t.TempDir()
	h := &ScriptFlowHandler{
		cfg: &config.Config{
			Storage: config.StorageConfig{DataDir: tmpDir},
		},
	}

	pkg := GeneratedScriptPackage{
		SourceText:      "original source text",
		RewrittenScript: "First sentence. Second sentence.",
		Language:        "en",
		Style:           "deep_dive",
		VisualStyle:     "anime",
		Title:           "Castle Story",
		OutputName:      "castle-story",
		Scenes: []GeneratedScene{
			{
				ID:    "scene_001",
				Index: 0,
				Text:  "First sentence.",
				Query: "First sentence. | Castle Story | anime | en",
				Image: &GeneratedImage{
					DriveFileID: "abc123",
					DriveLink:   "https://drive.google.com/file/d/abc123/view",
				},
			},
		},
		GeneratedAt: time.Date(2026, 5, 29, 13, 39, 46, 0, time.UTC),
	}

	outDir, written, err := h.writeGeneratedScriptFiles(pkg)
	if err != nil {
		t.Fatalf("writeGeneratedScriptFiles failed: %v", err)
	}

	if !strings.Contains(outDir, filepath.Join("docs", "generated")) {
		t.Fatalf("expected output dir under docs/generated, got %q", outDir)
	}

	mdPath := written.Files.Markdown
	jsonPath := written.Files.JSON
	if mdPath == "" || jsonPath == "" {
		t.Fatalf("expected files to be populated, got %#v", written.Files)
	}

	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read markdown failed: %v", err)
	}
	md := string(mdBytes)
	if !strings.Contains(md, "## Full Script") {
		t.Fatalf("markdown missing full script section: %s", md)
	}
	if !strings.Contains(md, "First sentence. Second sentence.") {
		t.Fatalf("markdown missing rewritten script: %s", md)
	}
	if !strings.Contains(md, "## Scenes JSON") {
		t.Fatalf("markdown missing scenes json section: %s", md)
	}
	if !strings.Contains(md, "\"drive_link\": \"https://drive.google.com/file/d/abc123/view\"") {
		t.Fatalf("markdown json block missing drive link: %s", md)
	}
	if strings.Index(md, "First sentence. Second sentence.") > strings.Index(md, "## Scenes JSON") {
		t.Fatalf("expected script text before json block: %s", md)
	}

	jsonBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json failed: %v", err)
	}
	var decoded GeneratedScriptPackage
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("unmarshal json failed: %v", err)
	}
	if decoded.Files.Markdown != mdPath || decoded.Files.JSON != jsonPath {
		t.Fatalf("json file paths not persisted, got %#v", decoded.Files)
	}
	if len(decoded.Scenes) != 1 || decoded.Scenes[0].Image == nil {
		t.Fatalf("expected one scene with image, got %#v", decoded.Scenes)
	}
	if decoded.Scenes[0].Image.DriveLink != "https://drive.google.com/file/d/abc123/view" {
		t.Fatalf("unexpected drive link in json: %#v", decoded.Scenes[0].Image)
	}
}

func TestWriteGeneratedScriptFilesWritesRepoDocsDir(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../../../../../"))
	dataDir := filepath.Join(repoRoot, "data")

	h := &ScriptFlowHandler{
		cfg: &config.Config{
			Storage: config.StorageConfig{DataDir: dataDir},
		},
	}

	pkg := GeneratedScriptPackage{
		SourceText:      "In a moonlit kingdom, a knight rode through the forest to seek the lost crown.",
		RewrittenScript: "In a moonlit kingdom, a knight rode through the forest to seek the lost crown. The old castle waited beyond the river.",
		Language:        "en",
		Style:           "storytelling",
		VisualStyle:     "medieval",
		Title:           "The Lost Crown",
		OutputName:      "the-lost-crown",
		Scenes: []GeneratedScene{
			{
				ID:    "scene_001",
				Index: 0,
				Text:  "In a moonlit kingdom, a knight rode through the forest to seek the lost crown.",
				Query: "In a moonlit kingdom, a knight rode through the forest to seek the lost crown. | The Lost Crown | medieval | en",
			},
			{
				ID:    "scene_002",
				Index: 1,
				Text:  "The old castle waited beyond the river.",
				Query: "The old castle waited beyond the river. | The Lost Crown | medieval | en",
				Image: &GeneratedImage{
					DriveFileID: "testdrive123",
					DriveLink:   "https://drive.google.com/file/d/testdrive123/view",
				},
			},
		},
		GeneratedAt: time.Date(2026, 5, 29, 13, 39, 46, 0, time.UTC),
	}

	outDir, written, err := h.writeGeneratedScriptFiles(pkg)
	if err != nil {
		t.Fatalf("writeGeneratedScriptFiles failed: %v", err)
	}

	if !strings.HasPrefix(outDir, filepath.Join(repoRoot, "docs", "generated")) {
		t.Fatalf("expected output dir under repo docs/generated, got %q", outDir)
	}

	if _, err := os.Stat(written.Files.Markdown); err != nil {
		t.Fatalf("markdown file missing: %v", err)
	}
	if _, err := os.Stat(written.Files.JSON); err != nil {
		t.Fatalf("json file missing: %v", err)
	}
}

type mockDocClient struct {
	title   string
	content string
	folder  string
}

func (m *mockDocClient) CreateDoc(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
	m.title = title
	m.content = content
	m.folder = folderID
	return &drive.Doc{
		ID:    "doc-123",
		Title: title,
		URL:   "https://docs.google.com/document/d/doc-123/edit",
	}, nil
}

func TestCreateGeneratedGoogleDocBuildsFullDocContent(t *testing.T) {
	client := &mockDocClient{}
	h := &ScriptFlowHandler{
		docClient: client,
		cfg: &config.Config{
			Drive: config.DriveConfig{MediaRootFolder: "drive-root-folder"},
		},
	}

	pkg := GeneratedScriptPackage{
		Title:           "The Lost Crown",
		RewrittenScript: "In a moonlit kingdom, a knight rode through the forest.",
		SourceText:      "In a moonlit kingdom, a knight rode through the forest.",
		Language:        "en",
		VisualStyle:     "medieval",
		OutputName:      "the-lost-crown",
		GeneratedAt:     time.Date(2026, 5, 29, 13, 39, 46, 0, time.UTC),
		Scenes: []GeneratedScene{
			{ID: "scene_001", Index: 0, Text: "In a moonlit kingdom, a knight rode through the forest."},
		},
	}

	doc, err := h.createGeneratedGoogleDoc(context.Background(), pkg)
	if err != nil {
		t.Fatalf("createGeneratedGoogleDoc failed: %v", err)
	}
	if doc.ID != "doc-123" {
		t.Fatalf("unexpected doc id: %#v", doc)
	}
	if client.title != "The Lost Crown" {
		t.Fatalf("unexpected title sent to doc client: %q", client.title)
	}
	if client.folder != "drive-root-folder" {
		t.Fatalf("unexpected folder sent to doc client: %q", client.folder)
	}
	if !strings.Contains(client.content, "Full Script:") || !strings.Contains(client.content, "Scenes JSON:") {
		t.Fatalf("doc content missing sections: %s", client.content)
	}
	if !strings.Contains(client.content, "In a moonlit kingdom, a knight rode through the forest.") {
		t.Fatalf("doc content missing full script: %s", client.content)
	}
	if !strings.Contains(client.content, "\"scene_001\"") {
		t.Fatalf("doc content missing scene json: %s", client.content)
	}
}

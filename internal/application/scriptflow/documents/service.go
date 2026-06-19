// Package documents provides Google Doc content building and creation for
// the script pipeline. Lives in the application layer so the HTTP transport
// remains thin.
package documents

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"reflect"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

// DocClient mirrors the upload/drive.DocClient interface that the documents
// package needs. This avoids importing the full drive package in callers.
type DocClient = drive.DocClient

// Service builds and creates Google Docs for generated scripts.
type Service struct {
	docClient     DocClient
	log           *zap.Logger
	driveFolderID string
}

// NewService creates a new documents Service.
func NewService(docClient DocClient, log *zap.Logger, driveFolderID string) *Service {
	return &Service{
		docClient:     docClient,
		log:           log,
		driveFolderID: driveFolderID,
	}
}

// BuildContent builds the HTML content for a Google Doc including script text,
// entities, insights, metadata, and scenes.
func BuildContent(
	title, script string,
	targetWords int,
	videoMetadata []VideoMetadata,
	entitiesJSON string,
	insights ScriptInsights,
	scenes []SceneRef,
) string {
	var b strings.Builder

	if strings.TrimSpace(title) != "" {
		b.WriteString("<h1>")
		b.WriteString(html.EscapeString(strings.TrimSpace(title)))
		b.WriteString("</h1>\n")
	}

	if strings.TrimSpace(script) != "" {
		b.WriteString("<h2>Script</h2>\n")
		for _, p := range strings.Split(strings.TrimSpace(script), "\n\n") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			b.WriteString("<p>")
			b.WriteString(html.EscapeString(p))
			b.WriteString("</p>\n")
		}
	}

	if entitiesJSON != "" {
		if sliceLen(insights.ImportantWords) > 0 {
			b.WriteString("<h2>Important Words</h2>\n")
			writeJSONBlock(&b, insights.ImportantWords)
		}
		if sliceLen(insights.ImportantPhrases) > 0 {
			b.WriteString("<h2>Important Phrases</h2>\n")
			writeJSONBlock(&b, insights.ImportantPhrases)
		}
		if sliceLen(insights.SpecialNames) > 0 {
			b.WriteString("<h2>Special Names</h2>\n")
			writeJSONBlock(&b, insights.SpecialNames)
		}
		if sliceLen(insights.ArtlistPhrases) > 0 {
			b.WriteString("<h2>Artlist Phrases</h2>\n")
			writeJSONBlock(&b, insights.ArtlistPhrases)
		}
		if reflectSliceLen(insights.EntityImages) > 0 {
			b.WriteString("<h2>Entity Images</h2>\n")
			writeJSONBlock(&b, insights.EntityImages)
		}
		if reflectSliceLen(insights.ArtlistClipSuggestions) > 0 {
			b.WriteString("<h2>Artlist Clip Suggestions</h2>\n")
			writeJSONBlock(&b, insights.ArtlistClipSuggestions)
		}
		if reflectSliceLen(insights.PhraseClipSuggestions) > 0 {
			b.WriteString("<h2>Phrase Clip Suggestions</h2>\n")
			writeJSONBlock(&b, insights.PhraseClipSuggestions)
		}
		if reflectSliceLen(insights.IntroClips) > 0 {
			b.WriteString("<h2>Intro Clips</h2>\n")
			writeJSONBlock(&b, insights.IntroClips)
		}
		if insights.RecommendedDriveFolder != nil {
			b.WriteString("<h2>Recommended Drive Folder</h2>\n")
			writeJSONBlock(&b, insights.RecommendedDriveFolder)
		}

		b.WriteString("<h2>Entities JSON (Full Analysis)</h2>\n")
		b.WriteString("<pre style=\"" + preStyle + "\">")
		var entitiesPretty bytes.Buffer
		if err := json.Indent(&entitiesPretty, []byte(entitiesJSON), "", "  "); err == nil {
			b.WriteString(html.EscapeString(entitiesPretty.String()))
		} else {
			b.WriteString(html.EscapeString(entitiesJSON))
		}
		b.WriteString("</pre>\n")
	}

	for _, m := range videoMetadata {
		lang := strings.TrimSpace(m.Language)
		if lang == "" {
			continue
		}
		b.WriteString("<h2>Metadata (")
		b.WriteString(html.EscapeString(lang))
		b.WriteString(")</h2>\n")
		b.WriteString("<pre style=\"" + preStyle + "\">")

		type langMeta struct {
			Language    string   `json:"language"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		}
		lm := langMeta{
			Language:    m.Language,
			Title:       m.Title,
			Description: m.Description,
			Tags:        m.Tags,
		}
		langBytes, _ := json.MarshalIndent(lm, "", "  ")
		b.WriteString(html.EscapeString(string(langBytes)))
		b.WriteString("</pre>\n")
	}

	if entitiesJSON != "" {
		jsonBlock := buildInsightsJSONBlock(title, insights)
		if jsonBlock != "" {
			b.WriteString("<h2>Common Metadata</h2>\n")
			b.WriteString("<pre style=\"" + preStyle + "\">")
			b.WriteString(html.EscapeString(jsonBlock))
			b.WriteString("</pre>\n")
		}
	}

	if len(scenes) > 0 {
		b.WriteString("<h2>Scenes JSON</h2>\n")
		b.WriteString("<pre style=\"" + preStyle + "\">")
		scenesBytes, _ := json.MarshalIndent(scenes, "", "  ")
		b.WriteString(html.EscapeString(string(scenesBytes)))
		b.WriteString("</pre>\n")
	}

	return b.String()
}

// CreateDoc resolves the target folder and creates a Google Doc.
// Returns the doc URL and ID, or empty strings on failure (non-fatal).
func (s *Service) CreateDoc(
	ctx context.Context,
	title, content string,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	driveFolderID string,
) (docLink, docID string) {
	if s.docClient == nil {
		return "", ""
	}

	resolvedFolderID, err := resolveFolder(ctx, driveFolderID, "1qfTiJNqnce18MmeDrV4ORh6n5jLW4dR5")
	if err != nil {
		if s.log != nil {
			s.log.Warn("failed to resolve custom drive folder name/path, using default root", zap.Error(err))
		}
		resolvedFolderID = "1qfTiJNqnce18MmeDrV4ORh6n5jLW4dR5"
	}

	return s.maybeCreateGoogleDoc(ctx, title, content, resolvedFolderID)
}

// maybeCreateGoogleDoc creates a Google Doc via the doc client.
func (s *Service) maybeCreateGoogleDoc(ctx context.Context, title, content, folderID string) (string, string) {
	if s.docClient == nil {
		return "", ""
	}
	effectiveFolderID := strings.TrimSpace(folderID)
	if effectiveFolderID == "" {
		effectiveFolderID = strings.TrimSpace(s.driveFolderID)
	}
	effectiveTitle := strings.TrimSpace(title)
	if effectiveTitle == "" {
		effectiveTitle = "Generated Script"
	}
	if s.log != nil {
		s.log.Info("creating Google Doc for generated script",
			zap.String("title", effectiveTitle),
			zap.String("folder_id", effectiveFolderID),
			zap.Int("content_chars", len(content)),
		)
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	doc, docErr := s.docClient.CreateDoc(saveCtx, effectiveTitle, content, effectiveFolderID)
	if docErr != nil {
		if s.log != nil {
			s.log.Warn("failed to create Google Doc",
				zap.String("title", effectiveTitle),
				zap.String("folder_id", effectiveFolderID),
				zap.Error(docErr),
			)
		}
		return "", ""
	}
	if s.log != nil {
		s.log.Info("Google Doc created",
			zap.String("title", effectiveTitle),
			zap.String("doc_id", doc.ID),
			zap.String("url", doc.URL),
			zap.String("folder_id", effectiveFolderID),
		)
	}
	return doc.URL, doc.ID
}

// ── Shared types (canonical in application layer) ──────────────────────────

// VideoMetadata holds per-language video metadata.
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// ScriptInsights bundles all entity and insight data extracted from a script.
type ScriptInsights struct {
	ImportantWords         []string      `json:"important_words,omitempty"`
	ImportantPhrases       []string      `json:"important_phrases,omitempty"`
	SpecialNames           []string      `json:"special_names,omitempty"`
	ArtlistPhrases         []string      `json:"artlist_phrases,omitempty"`
	ArtlistClipSuggestions any           `json:"artlist_clip_suggestions,omitempty"`
	RecommendedDriveFolder any           `json:"recommended_drive_folder,omitempty"`
	PhraseClipSuggestions  any           `json:"phrase_clip_suggestions,omitempty"`
	IntroClips             any           `json:"intro_clips,omitempty"`
	EntityImages           any           `json:"entity_images,omitempty"`
}

// SceneRef is a lightweight reference to a scene for doc content building.
type SceneRef struct {
	Text          string   `json:"text"`
	Image         string   `json:"image,omitempty"`
	Images        []string `json:"images,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	NarrationRole string   `json:"narration_role,omitempty"`
}

// ── Helpers ─────────────────────────────────────────────────────────────────

const preStyle = "background:#f5f5f5;padding:12px;border-radius:4px;font-size:13px;overflow-x:auto"

func writeJSONBlock(b *strings.Builder, v any) {
	b.WriteString("<pre style=\"" + preStyle + "\">")
	data, err := json.MarshalIndent(v, "", "  ")
	if err == nil && len(data) > 0 {
		b.WriteString(html.EscapeString(string(data)))
	}
	b.WriteString("</pre>\n")
}

// buildInsightsJSONBlock builds a JSON metadata block for the top of the Google Doc.
// (Moved from handler_script_handlers_flow_doc_metadata.go)
func buildInsightsJSONBlock(title string, insights ScriptInsights) string {
	var block struct {
		Title              string   `json:"title,omitempty"`
		WordCount          int      `json:"word_count,omitempty"`
		Topic              string   `json:"topic,omitempty"`
		ImportantWords     []string `json:"important_words,omitempty"`
		ImportantPhrases   []string `json:"important_phrases,omitempty"`
		SpecialNames       []string `json:"special_names,omitempty"`
		ArtlistPhrases     []string `json:"artlist_phrases,omitempty"`
		VoiceoverLanguages []string `json:"voiceover_languages,omitempty"`
	}
	block.Title = strings.TrimSpace(title)
	block.ImportantWords = insights.ImportantWords
	block.ImportantPhrases = insights.ImportantPhrases
	block.SpecialNames = insights.SpecialNames
	block.ArtlistPhrases = insights.ArtlistPhrases

	data, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

// sliceLen returns the length of a string slice.
func sliceLen(s []string) int { return len(s) }

// reflectSliceLen returns the length of an any value if it is a slice,
// or 0 otherwise.
func reflectSliceLen(v any) int {
	if v == nil {
		return 0
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice {
		return rv.Len()
	}
	return 0
}

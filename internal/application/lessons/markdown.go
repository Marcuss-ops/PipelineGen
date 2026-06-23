package lessons

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// AssembleLesson produces the complete Markdown content for a lesson result.
// Includes YAML front matter, table of contents, and all chapters with images.
// This is a pure formatting function — no side effects, easily testable.
func (s *Service) AssembleLesson(result *LessonResult) string {
	var b strings.Builder

	// YAML front matter
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: \"%s\"\n", escapeYAMLString(result.Title))
	fmt.Fprintf(&b, "language: %s\n", result.Language)
	fmt.Fprintf(&b, "generated_at: %s\n", result.GeneratedAt)
	fmt.Fprintf(&b, "chapters: %d\n", len(result.Chapters))
	fmt.Fprintf(&b, "total_words: %d\n", result.TotalWords)
	b.WriteString("---\n\n")

	// Title
	b.WriteString(fmt.Sprintf("# %s\n\n", result.Title))

	// Table of contents
	b.WriteString("## Indice\n\n")
	for _, ch := range result.Chapters {
		if ch.Error != "" {
			continue
		}
		anchor := chapterAnchor(ch.Index+1, ch.Title)
		fmt.Fprintf(&b, "1. [%s](#%s)\n", ch.Title, anchor)
	}
	b.WriteString("\n---\n\n")

	// Chapters
	for _, ch := range result.Chapters {
		if ch.Error != "" {
			b.WriteString(fmt.Sprintf("## Capitolo %d: %s\n\n", ch.Index+1, ch.Title))
			b.WriteString(fmt.Sprintf("_%s_\n\n", ch.Error))
			b.WriteString("\n---\n\n")
			continue
		}

		fmt.Fprintf(&b, "## Capitolo %d: %s\n\n", ch.Index+1, ch.Title)

		// Image if present
		if ch.Image != nil && ch.Image.URL != "" {
			fmt.Fprintf(&b, "![%s](%s)\n\n", ch.Title, ch.Image.URL)
		}

		// Chapter content
		b.WriteString(ch.Content)
		b.WriteString("\n\n")

		// Chapter word count
		fmt.Fprintf(&b, "_{Parole: %d}_\n\n", ch.WordCount)

		b.WriteString("---\n\n")
	}

	return b.String()
}

// SaveLessonMarkdown saves the lesson Markdown content to a file.
// Returns the full path to the saved file.
// Can be reused by other components that need to persist lesson output.
func (s *Service) SaveLessonMarkdown(result *LessonResult, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	slug := textutil.Slugify(result.Title)
	mdPath := filepath.Join(outputDir, slug+".md")

	content := s.AssembleLesson(result)
	if err := os.WriteFile(mdPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write markdown file: %w", err)
	}

	s.log.Info("lesson markdown saved",
		zap.String("path", mdPath),
		zap.Int("size", len(content)),
	)

	return mdPath, nil
}

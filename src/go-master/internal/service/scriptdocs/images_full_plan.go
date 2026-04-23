package scriptdocs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *ScriptDocService) buildImagePlan(topic string, duration int, mode string, langResults []LanguageResult) *ImagePlan {
	plan := &ImagePlan{
		Topic:           topic,
		Duration:        duration,
		AssociationMode: normalizeAssociationMode(mode),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	for _, lr := range langResults {
		lplan := ImagePlanLang{
			Language:          lr.Language,
			Associations:      append([]ImageAssociation(nil), lr.ImageAssociations...),
			TotalAssociations: len(lr.ImageAssociations),
		}
		for _, ch := range lr.Chapters {
			lplan.Chapters = append(lplan.Chapters, ImagePlanChapter{
				Index:            ch.Index,
				Title:            ch.Title,
				StartTime:        ch.StartTime,
				EndTime:          ch.EndTime,
				Confidence:       ch.Confidence,
				SourceText:       compactSnippet(ch.SourceText, 260),
				DominantEntities: append([]string(nil), ch.DominantEntities...),
			})
		}
		for _, assoc := range lr.ImageAssociations {
			if assoc.Cached {
				lplan.CachedAssociations++
				plan.TotalCached++
			}
			if strings.TrimSpace(assoc.LocalPath) != "" {
				lplan.Downloaded++
				plan.TotalDownloaded++
			}
		}
		plan.TotalAssociations += len(lr.ImageAssociations)
		plan.Languages = append(plan.Languages, lplan)
	}
	return plan
}

func saveImagePlanJSON(topic string, plan *ImagePlan) (string, error) {
	if plan == nil {
		return "", nil
	}
	dir := filepath.Join(os.TempDir(), "velox-image-plans")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	name := safeFileName(topic)
	path := filepath.Join(dir, fmt.Sprintf("%s_%d.json", name, time.Now().Unix()))
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func safeFileName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "image_plan"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "image_plan"
	}
	return out
}

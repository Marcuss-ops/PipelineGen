package handlers

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/repository/scripts"
)

// ── Phase: Merge Results ─────────────────────────────────────────────────────

// mergeBatchResults reconstructs the ordered generatedParts and mergedScript
// from the parallel chapter generation results.
func mergeBatchResults(docTitle string, results []*genChapterResult, noChapters bool, language string) ([]generatedPart, string, []chapterTiming, []scripts.ScriptSectionRecord) {
	var generatedParts []generatedPart
	var mergedScript strings.Builder
	mergedScript.WriteString(fmt.Sprintf("# %s\n\n", docTitle))

	for idx, res := range results {
		if res == nil {
			continue
		}
		generatedParts = append(generatedParts, res.part)
		if res.scriptContent != "" {
			chapterLabel := chapterLabelForLang(language)
			if noChapters {
				mergedScript.WriteString(fmt.Sprintf("%s\n\n", res.scriptContent))
			} else {
				mergedScript.WriteString(fmt.Sprintf("## %s %d: %s\n\n%s\n\n---\n\n", chapterLabel, idx+1, res.part.topic, res.scriptContent))
			}
		}
	}

	timings := make([]chapterTiming, 0, len(generatedParts))
	sections := make([]scripts.ScriptSectionRecord, 0, len(generatedParts))
	for idx, part := range generatedParts {
		timings = append(timings, part.timing)
		status := "completed"
		if part.timing.Status == "failed" {
			status = "failed"
		}
		sections = append(sections, scripts.ScriptSectionRecord{
			SectionType:  "item",
			SectionTitle: part.topic,
			Content:      part.content,
			SortOrder:    idx + 1,
			WordCount:    part.timing.WordCount,
			Status:       status,
		})
	}

	return generatedParts, mergedScript.String(), timings, sections
}

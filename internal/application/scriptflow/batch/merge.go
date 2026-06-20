package batch

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
)

// ── Phase: Merge Results ─────────────────────────────────────────────────────

// mergeBatchResults reconstructs the ordered GeneratedParts and mergedScript
// from the parallel chapter generation results.
func mergeBatchResults(docTitle string, results []*genChapterResult, noChapters bool, language string) ([]GeneratedPart, string, []chapterTiming, []scripts.ScriptSectionRecord) {
	var GeneratedParts []GeneratedPart
	var mergedScript strings.Builder
	mergedScript.WriteString(fmt.Sprintf("# %s\n\n", docTitle))

	for idx, res := range results {
		if res == nil {
			continue
		}
		GeneratedParts = append(GeneratedParts, res.part)
		if res.scriptContent != "" {
			chapterLabel := chapterLabelForLang(language)
			if noChapters {
				mergedScript.WriteString(fmt.Sprintf("%s\n\n", res.scriptContent))
			} else {
				mergedScript.WriteString(fmt.Sprintf("## %s %d: %s\n\n%s\n\n---\n\n", chapterLabel, idx+1, res.part.topic, res.scriptContent))
			}
		}
	}

	timings := make([]chapterTiming, 0, len(GeneratedParts))
	sections := make([]scripts.ScriptSectionRecord, 0, len(GeneratedParts))
	for idx, part := range GeneratedParts {
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

	return GeneratedParts, mergedScript.String(), timings, sections
}

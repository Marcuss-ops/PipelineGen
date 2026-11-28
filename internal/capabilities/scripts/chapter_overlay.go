// chapter_overlay.go — producer validation for the chapter directive.
//
// The rules live in the shared contract (`editorial_composition.v1`,
// `chapter`): modes, grounds, style families, card duration, authored titles
// and unique chapter numbers. This file only maps the payload shape onto that
// one validator, so the generation ingress cannot drift from the composition
// boundary.
package scriptgeneration

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan/editorialcomposition"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// validateChapterOverlay fails closed on a payload the contract rejects. A
// nil directive is valid: it keeps the automatic chapter extraction.
func validateChapterOverlay(in *scriptpkg.ChapterOverlaySpec) error {
	if in == nil {
		return nil
	}
	chapter := &editorialcomposition.Chapter{
		Mode:        in.Mode,
		StyleFamily: in.StyleFamily,
		DurationMS:  in.DurationMS,
		Grounding:   in.Grounding,
	}
	if len(in.Titles) > 0 {
		chapter.Titles = make([]editorialcomposition.ChapterTitle, 0, len(in.Titles))
		for _, title := range in.Titles {
			chapter.Titles = append(chapter.Titles, editorialcomposition.ChapterTitle{
				SceneID:       title.SceneID,
				Title:         title.Title,
				ChapterNumber: title.ChapterNumber,
			})
		}
	}
	if err := editorialcomposition.ValidateChapterPolicy(chapter); err != nil {
		return fmt.Errorf("chapter_overlay: %w", err)
	}
	return nil
}

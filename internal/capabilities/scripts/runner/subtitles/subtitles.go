// Package subtitles owns the final-job fixed-media subtitle policy: which
// protected intro/mid/outro clip needs a localized render, in which language,
// and with which burned caption. It reads a narrow Source and depends on a
// narrow Port; it never sees the whole GenerateResult or the Runner.
package subtitles

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderunits"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Source is the read-only projection the subtitle policy needs.
type Source struct {
	Scenes           []contract.Scene
	LocalizedRenders []contract.LocalizedRenderResult
	Subtitles        *scriptpkg.VideoSubtitlesSpec
}

// Port is the narrow seam the policy reads through. Implementations live at
// the run boundary; the policy itself stays pure and testable.
type Port interface {
	FixedMediaNeedsRender(req contract.GenerateRequest, scene contract.Scene) bool
	CaptionText(req contract.GenerateRequest, scene contract.Scene) string
	CertifiedLocalRender(sceneID, clipID, language string) bool
}

// Job is one fixed-media subtitle render the caller must execute.
type Job struct {
	Scene          contract.Scene
	Language       contract.Language
	Text           string
	SourceText     string
	ClipID         string
	ClipAssetID    string
	ClipSHA256     string
	ClipDurationMS int64
}

// BuildPlan resolves the language and enumerates the uncertified fixed-media
// units. A disabled subtitle request or a run without a protected clip is a
// no-op, not an error.
func BuildPlan(req contract.GenerateRequest, source Source, port Port) ([]Job, error) {
	if source.Subtitles == nil || !source.Subtitles.Enabled || port == nil {
		return nil, nil
	}
	needsRender := false
	for i := range source.Scenes {
		scene := source.Scenes[i]
		if !port.FixedMediaNeedsRender(req, scene) || port.CaptionText(req, scene) == "" {
			continue
		}
		if len(renderunits.RenderUnitsForScene(scene)) > 0 {
			needsRender = true
			break
		}
	}
	if !needsRender {
		return nil, nil
	}
	language, err := renderLanguage(req, source.LocalizedRenders)
	if err != nil {
		return nil, fmt.Errorf("resolve render language: %w", err)
	}
	if language == "" {
		language = strings.TrimSpace(string(req.SourceLanguage))
	}
	if language == "" {
		for _, scene := range source.Scenes {
			if !port.FixedMediaNeedsRender(req, scene) {
				continue
			}
			for lang := range scene.Text {
				if strings.TrimSpace(scene.Text[lang]) != "" {
					language = strings.TrimSpace(string(lang))
					break
				}
			}
			if language != "" {
				break
			}
		}
	}
	if language == "" {
		return nil, nil
	}
	lang := contract.Language(language)
	jobs := make([]Job, 0, len(source.Scenes))
	for _, scene := range source.Scenes {
		if !port.FixedMediaNeedsRender(req, scene) || port.CaptionText(req, scene) == "" {
			continue
		}
		for _, unit := range renderunits.RenderUnitsForScene(scene) {
			if unit.Clip == nil || strings.TrimSpace(unit.Clip.ID) == "" {
				continue
			}
			if port.CertifiedLocalRender(scene.ID, unit.Clip.ID, language) {
				continue
			}
			clipID, clipAssetID, clipSHA256, clipDurationMS := renderunits.LocalizedRenderUnitClipFields(unit)
			jobs = append(jobs, Job{
				Scene:          scene,
				Language:       lang,
				Text:           renderunits.FixedCaptionText(scene, req.SourceLanguage, lang),
				SourceText:     strings.TrimSpace(scene.Text[req.SourceLanguage]),
				ClipID:         clipID,
				ClipAssetID:    clipAssetID,
				ClipSHA256:     clipSHA256,
				ClipDurationMS: clipDurationMS,
			})
		}
	}
	return jobs, nil
}

// renderLanguage mirrors FinalJobRenderLanguage: an existing target render
// wins, a singleton target is authoritative before any render exists, then the
// source language, then the only rendered language. An ambiguous matrix fails
// closed instead of burning the wrong language into an intro/outro.
func renderLanguage(req contract.GenerateRequest, renders []contract.LocalizedRenderResult) (string, error) {
	for _, target := range req.Languages {
		preferred := strings.TrimSpace(string(target))
		if preferred == "" {
			continue
		}
		for _, rendered := range renders {
			if strings.EqualFold(strings.TrimSpace(string(rendered.Language)), preferred) {
				return strings.TrimSpace(string(rendered.Language)), nil
			}
		}
	}
	if len(req.Languages) == 1 {
		if target := strings.TrimSpace(string(req.Languages[0])); target != "" {
			return target, nil
		}
	}
	if len(renders) == 0 {
		return "", nil
	}
	preferred := strings.TrimSpace(string(req.SourceLanguage))
	if preferred != "" {
		for _, rendered := range renders {
			if strings.EqualFold(strings.TrimSpace(string(rendered.Language)), preferred) {
				return strings.TrimSpace(string(rendered.Language)), nil
			}
		}
	}
	languages := make(map[string]struct{}, len(renders))
	for _, rendered := range renders {
		if language := strings.TrimSpace(string(rendered.Language)); language != "" {
			languages[language] = struct{}{}
		}
	}
	if len(languages) == 1 {
		for language := range languages {
			return language, nil
		}
	}
	available := make([]string, 0, len(languages))
	for language := range languages {
		available = append(available, language)
	}
	sort.Strings(available)
	return "", fmt.Errorf("no certified render for source language %q and the run produced %d render languages (%s)", preferred, len(available), strings.Join(available, ", "))
}

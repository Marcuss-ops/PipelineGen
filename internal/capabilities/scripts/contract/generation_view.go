package contract

import (
	generation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

// Generation returns the generation-phase projection of the request. It is a
// read-only view: source acquisition, submission identity, audio
// configuration, publication and final-job opt-in stay in their own phases and
// are deliberately absent, so this view cannot become a second god model.
func (r GenerateRequest) Generation() generation.Request {
	return generation.Request{
		TranslationProvider:           r.TranslationProvider,
		TranslationModel:              r.TranslationModel,
		TranslationConcurrency:        r.TranslationConcurrency,
		Model:                         r.Model,
		Tone:                          r.Tone,
		Style:                         r.Style,
		Render:                        r.Render,
		ChannelID:                     r.ChannelID,
		ShortPhraseMotion:             r.ShortPhraseMotion,
		PhraseMotionFamily:            r.PhraseMotionFamily,
		ChapterOverlay:                r.ChapterOverlay,
		EditorialBulletStyles:         r.EditorialBulletStyles,
		DateMotionFamily:              r.DateMotionFamily,
		MotionSeed:                    r.MotionSeed,
		ImageMotions:                  r.ImageMotions,
		ImageWithTextMotions:          r.ImageWithTextMotions,
		EntityImageMotions:            r.EntityImageMotions,
		EntityCaptionMotions:          r.EntityCaptionMotions,
		RandomLightLeakOnClipStart:    r.RandomLightLeakOnClipStart,
		RandomLightLeakOnOverlayStart: r.RandomLightLeakOnOverlayStart,
		RandomImageEffects:            r.RandomImageEffects,
		OverlayCategories:             r.OverlayCategories,
		OverlayImages:                 r.OverlayImages,
		MapType:                       r.MapType,
		MapCameraAnimation:            r.MapCameraAnimation,
		MapLabelAnimation:             r.MapLabelAnimation,
		MapAnimation:                  r.MapAnimation,
		MapLevel:                      r.MapLevel,
		MapPresentation:               r.MapPresentation,
		MaxPhraseOverlays:             r.MaxPhraseOverlays,
		MaxOverlaysPerScene:           r.MaxOverlaysPerScene,
		MapsOnly:                      r.MapsOnly,
		MaxMapOverlays:                r.MaxMapOverlays,
		MaxImageOverlays:              r.MaxImageOverlays,
		EditorialDensity:              r.EditorialDensity,
		AnimationCounts:               r.AnimationCounts,
		OverlayGroupLimits:            r.OverlayGroupLimits,
		EntityStyleID:                 r.EntityStyleID,
		EntityBadgeColor:              r.EntityBadgeColor,
		DisableNumberOverlays:         r.DisableNumberOverlays,
		DisableRecapOverlays:          r.DisableRecapOverlays,
		DisableChartOverlays:          r.DisableChartOverlays,
		DisableEditorialOverlays:      r.DisableEditorialOverlays,
		EnableCharts:                  r.EnableCharts,
		HeavyPhrasePriority:           r.HeavyPhrasePriority,
		OverlayBackground:             r.OverlayBackground,
		OverlayStyle:                  r.OverlayStyle,
		ScriptParams:                  r.ScriptParams,
		SourceLanguage:                r.SourceLanguage,
		Languages:                     r.Languages,
		GenerateTimeline:              r.GenerateTimeline,
		EnforceClipIdentity:           r.EnforceClipIdentity,
		Title:                         r.Title,
		Project:                       r.Project,
		OutputName:                    r.OutputName,
		Intro:                         r.Intro,
		Outro:                         r.Outro,
	}
}

// Generation returns the generation-phase projection of the result. It keeps
// the narration, editorial takeaways and build provenance; timing, audio,
// overlay and final-job receipts stay in their own phases.
func (r GenerateResult) Generation() generation.Result {
	var translation generation.TranslationMetrics
	if r.TranslationMetrics != nil {
		translation = generation.TranslationMetrics{
			Calls:       r.TranslationMetrics.Calls,
			Concurrency: r.TranslationMetrics.Concurrency,
			WallMS:      r.TranslationMetrics.WallMS,
		}
	}
	return generation.Result{
		Output: generation.Output{
			Text:                       r.Output.Text,
			WordCount:                  r.Output.WordCount,
			SourceLanguageFallbackUsed: r.Output.SourceLanguageFallbackUsed,
		},
		Title:              r.Title,
		SourceLanguage:     r.SourceLanguage,
		OutputName:         r.OutputName,
		WordCount:          r.WordCount,
		VoiceoverGroup:     r.VoiceoverGroup,
		Summary:            r.Summary,
		BulletPoints:       r.BulletPoints,
		HeavySentences:     r.HeavySentences,
		ChapterManifest:    r.ChapterManifest,
		SceneHighlights:    r.SceneHighlights,
		Editorial:          r.Editorial,
		BinarySHA256:       r.BinarySHA256,
		RustRelease:        r.RustRelease,
		RustGitSHA:         r.RustGitSHA,
		RustBinarySHA256:   r.RustBinarySHA256,
		TranslationMetrics: translation,
	}
}

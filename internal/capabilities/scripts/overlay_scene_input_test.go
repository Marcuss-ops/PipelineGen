package scriptgeneration

import (
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// overlaySceneInput is the retired Go planner projection. It survives only as
// a test oracle for the focused unit tests; no production file calls it. The
// phrase match is delegated to the test aligner, so this file never becomes a
// second production matcher.
// overlaySceneInput projects grounded annotations and certified occurrences
// into planner candidates. Optional unspoken annotations never receive timing.
func overlaySceneInput(scene Scene, language, sourceLanguage Language, timing capabilityaudio.SpeechTimingArtifact, timelineStartUS int64, occurrences []capabilityentities.EntityOccurrence, plates capabilityoverlay.PlateResolver) (*capabilityoverlay.SceneInput, error) {
	ann := annotationsForLanguage(scene, language, sourceLanguage)
	if ann == nil {
		return nil, nil
	}
	out := capabilityoverlay.SceneInput{ID: scene.ID}
	locate := func(phrase string) (*capabilityaudio.PhraseTiming, error) {
		located, err := testLocatePhrase(timing, phrase)
		if err != nil {
			return nil, err
		}
		first := located[0]
		return &capabilityaudio.PhraseTiming{
			SceneIndex: scene.Index, PhraseIndex: 0, Text: first.Text,
			WordStart: first.WordStart, WordEnd: first.WordEnd,
			LocalStartUS: first.StartUS, LocalEndUS: first.EndUS,
			TimelineStartUS: timelineStartUS,
			GlobalStartUS:   timelineStartUS + first.StartUS,
			GlobalEndUS:     timelineStartUS + first.EndUS,
		}, nil
	}
	timed := func(p *capabilityaudio.PhraseTiming, score float64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{Text: p.Text, StartMs: p.GlobalStartUS / 1000, EndMs: (p.GlobalEndUS + 999) / 1000, StartUS: p.GlobalStartUS, DurationUS: p.GlobalEndUS - p.GlobalStartUS, Score: score}
	}
	for _, span := range ann.ImportantPhrases {
		phrase := strings.TrimSpace(span.Text)
		// A two-word phrase can be a complete, grounded callout; single words
		// remain too fragmentary for this overlay lane.
		if len(strings.Fields(phrase)) < 2 {
			continue
		}
		if p, err := locate(phrase); err == nil {
			out.Phrases = append(out.Phrases, timed(p, span.Score))
		}
	}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
		kind := capmodel.EntityTypeToKind(entity.Type)
		if kind == capmodel.KindBrandText {
			if p, err := locate(entity.CanonicalName); err == nil {
				score := entity.Confidence
				if score <= 0 {
					score = 0.9
				}
				out.BrandTexts = append(out.BrandTexts, timed(p, score))
			}
			continue
		}
		occ := occurrenceFor(occurrences, entity)
		if occ == nil {
			continue
		}
		score := entity.Confidence
		if score <= 0 {
			score = 0.9
		}
		switch kind {
		case capmodel.KindNumber:
			// Entity extraction occasionally wraps the number in prose on
			// either side ("8 ottobre 2026 per", live C1 "si attesta
			// sull'18"). Clean to the biggest valid numeric/date surface,
			// then re-attach a %/currency suffix the NLP value dropped
			// ("sull'18" from "sull'18%").
			value := recoverNumericSuffix(strings.TrimSpace(scene.Text[language]),
				cleanNumericOverlayValue(entity.CanonicalName), entity.Mentions)
			if value == "" || !numericEntityGroundedForLanguage(scene, language, sourceLanguage, value) {
				continue
			}
			startUS, endUS := occ.AudioStartUS, occ.AudioEndUS
			if value != entity.CanonicalName {
				if p, err := locate(value); err == nil {
					startUS, endUS = p.GlobalStartUS, p.GlobalEndUS
				}
				// locate fails when TTS speaks digits as words ("42%" →
				// "quarantadue"): keep the certified occurrence window — it
				// provably contains the cleaned number (same word span) —
				// instead of dropping a real, grounded value.
			}
			out.Numbers = append(out.Numbers, capabilityoverlay.TimedAnnotation{Text: value, Type: entity.Type, StartMs: startUS / 1000, EndMs: (endUS + 999) / 1000, StartUS: startUS, DurationUS: endUS - startUS, Score: score})
		case capmodel.KindQuote:
			out.Quotes = append(out.Quotes, capabilityoverlay.TimedAnnotation{Text: entity.CanonicalName, Type: entity.Type, StartMs: occ.AudioStartUS / 1000, EndMs: (occ.AudioEndUS + 999) / 1000, StartUS: occ.AudioStartUS, DurationUS: occ.AudioEndUS - occ.AudioStartUS, Score: score})
		case capmodel.KindProduct:
			if entity.Image != nil {
				out.Products = append(out.Products, imageCandidate(entity.Image, occ, score))
			}
		case capmodel.KindLogo:
			if entity.Image == nil {
				if p, err := locate(entity.CanonicalName); err == nil {
					out.BrandTexts = append(out.BrandTexts, timed(p, score))
				}
				continue
			}
			out.Logos = append(out.Logos, imageCandidate(entity.Image, occ, score))
		case capmodel.KindLocation:
			if entity.Geo == nil {
				continue
			}
			candidate, ok := capabilityoverlay.NewMapCandidate(occ.EntityID, entity.CanonicalName, entity.Geo.Latitude, entity.Geo.Longitude, occ.AudioStartUS, occ.AudioEndUS-occ.AudioStartUS, score, entity.Geo.Scope)
			if ok {
				out.Maps = append(out.Maps, candidate)
			}
		default:
			// Entity cards are resolved independently through their media
			// index; adding the same image here would duplicate rendering.
			continue
		}
	}
	// Recover complete, explicitly spoken dates that the entity annotator
	// missed (notably year-only references). These are still source-grounded
	// and receive timing only when the exact surface is found in word timing.
	if scene.OverlayCategories[scriptpkg.OverlayCategoryNumbers].AsBool() {
		out.Numbers = dedupeOverlappingGroundedDates(out.Numbers)
		for _, date := range explicitDateSurfaces(scene.Text[language]) {
			duplicate := false
			for i, existing := range out.Numbers {
				if strings.EqualFold(existing.Text, date) {
					duplicate = true
					break
				}
				// Entity extraction can return only "13 agosto" while the
				// source explicitly says "13 agosto 2007". Keep one card and
				// prefer the complete, exactly timed source surface.
				if sameGroundedDate(existing.Text, date) {
					if dateHasYear(date) && !dateHasYear(existing.Text) {
						p, err := locate(date)
						if err == nil {
							full := timed(p, scoreForDate(existing.Score))
							full.Type = "DATE"
							out.Numbers[i] = full
						}
					}
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			p, err := locate(date)
			if err != nil {
				continue
			}
			number := timed(p, 0.8)
			number.Type = "DATE"
			out.Numbers = append(out.Numbers, number)
		}
	}
	if len(out.Phrases)+len(out.Keywords)+len(out.Images)+len(out.Numbers)+len(out.BrandTexts)+len(out.Quotes)+len(out.Products)+len(out.Logos)+len(out.Maps) == 0 {
		return nil, nil
	}
	return &out, nil
}

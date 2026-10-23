// Package scriptgeneration — overlay_plan_golden_mix_test.go certifies the
// golden throughput mix end to end through CompileOverlayPlan: with the
// payload's requested overlay configuration and a full candidate supply the
// sealed plan materializes 100 items (25 short + 25 long phrases, 30 images
// of which 10 entity cards, 10 dates, 10 maps) instead of the 36–41 the old
// run-level clamps produced (TICKET-OVERLAY-THROUGHPUT-100-MIN).
package scriptgeneration

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const goldenMixSceneDurationUS = int64(20_000_000)

// goldenMixCanvas mirrors the golden throughput payload's overlay
// configuration: 50 grounded phrases (25 short + 25 long), 30 images,
// 10 maps, 10 overlay slots per scene and disabled chains.
func goldenMixCanvas() OverlayCanvasSpec {
	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = 50
	canvas.MaxImageOverlays = 30
	canvas.MaxMapOverlays = 10
	canvas.MaxOverlaysPerScene = 10
	canvas.OverlayGroupLimits = map[string]int{
		"maps": 10, "short_phrases": 25, "long_phrases": 25,
		"entities_with_text": 10, "images": 30, "locations": 0,
	}
	// The golden payload's animation_counts, mirrored verbatim: the maps
	// window (3) is what selects the scene-scoped map branch instead of the
	// merged journey, and chain_disabled keeps composed groups unbundled.
	canvas.AnimationCounts = map[string]int{
		"maps": 3, "images": 13, "images_with_text": 13,
		"important_phrase": 20, "short_important_phrase": 10,
		"entities": 5, "entity_caption": 5, "dates": 4, "chain_disabled": 1,
	}
	canvas.DisableRecapOverlays = true
	canvas.DisableEditorialOverlays = true
	canvas.DisableChartOverlays = true
	return canvas
}

// goldenMixPlate builds a certified plate centred on the grounded place,
// backed by a real PNG on disk, for the map lane.
func goldenMixPlate(t *testing.T, id string, lat, lon float64) capabilityoverlay.MapPlate {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, GoldenOverlayCanvas.Width, GoldenOverlayCanvas.Height))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	path := filepath.Join(t.TempDir(), id+".png")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return capabilityoverlay.MapPlate{
		ID: id, License: "operator-supplied", Attribution: "© OpenStreetMap contributors",
		Center: capabilityoverlay.MapCenter{Latitude: lat, Longitude: lon}, Zoom: 10,
		Width: GoldenOverlayCanvas.Width, Height: GoldenOverlayCanvas.Height,
		Window: geodesy.CenteredOn(lat, lon, 10, GoldenOverlayCanvas.Width, GoldenOverlayCanvas.Height),
		Asset: capabilityoverlay.OverlayAssetRef{
			AssetID: id, SHA256: digest.SHA256Bytes(buf.Bytes()),
			MediaType: "image/png", LocalPath: path,
		},
	}
}

// goldenMixResult builds a 20-scene fixture with a full candidate supply:
// every scene speaks two three-word and two eight-word grounded phrases (80
// candidates for the 25+25 lanes), scenes 01–10 speak one documented date
// each and carry two certified internet stills, scenes 11–20 speak one
// geocoded place and one bound person each.
func goldenMixResult(t *testing.T) (*GenerateResult, capabilityoverlay.PlateResolver) {
	t.Helper()
	result := &GenerateResult{SourceLanguage: "en"}
	plates := make([]capabilityoverlay.MapPlate, 0, 10)
	for i := 0; i < 20; i++ {
		sceneID := fmt.Sprintf("scene-%02d", i+1)
		startUS := int64(i) * goldenMixSceneDurationUS

		shortA := []string{fmt.Sprintf("alpha%02d", i), "bravo", "charlie"}
		shortB := []string{fmt.Sprintf("delta%02d", i), "echo", "foxtrot"}
		longA := []string{fmt.Sprintf("golf%02d", i), "hotel", "india", "juliet", "kilo", "lima", "mike", "november"}
		longB := []string{fmt.Sprintf("oscar%02d", i), "papa", "quebec", "romeo", "sierra", "tango", "uniform", "victor"}

		words := []string{}
		words = append(words, shortA...)
		words = append(words, shortB...)
		entities := []scriptpkg.AnnotatedEntity{}
		occs := []capabilityentities.EntityOccurrence{}
		addOccurrence := func(entityType, name string, wordAt int, confidence float64) {
			text := strings.Join(words, " ")
			at := strings.Index(text, name)
			if at < 0 {
				t.Fatalf("fixture: %q not in scene text %q", name, text)
			}
			occs = append(occs, capabilityentities.EntityOccurrence{
				EntityID: capabilityentities.StableEntityID(entityType, name),
				Name:     name, Type: entityType,
				SceneID: sceneID, SceneIndex: i,
				TextStart: at, TextEnd: at + len(name),
				WordStart: wordAt, WordEnd: wordAt,
				LocalStartUS: int64(wordAt) * 100_000, LocalEndUS: int64(wordAt+1) * 100_000,
				TimelineStartUS: startUS,
				AudioStartUS:    startUS + int64(wordAt)*100_000,
				AudioEndUS:      startUS + int64(wordAt+1)*100_000,
				Confidence:      confidence,
			})
		}

		if i < 10 {
			// One documented date per scene: distinct numeric surfaces keep
			// the number lane's text dedupe honest.
			date := fmt.Sprintf("20%02d0101", i)
			words = append(words, date)
			entities = append(entities, scriptpkg.AnnotatedEntity{
				ID: "e-date-" + sceneID, CanonicalName: date, Type: "DATE", Confidence: 0.92,
			})
			addOccurrence("DATE", date, len(words)-1, 0.92)
		} else {
			// One geocoded place and one bound person per scene.
			place := fmt.Sprintf("City%02d", i)
			person := fmt.Sprintf("Person%02d", i)
			words = append(words, place, person)
			lat, lon := 10.0+float64(i), 10.0+float64(i)
			entities = append(entities,
				scriptpkg.AnnotatedEntity{
					ID: "e-place-" + sceneID, CanonicalName: place, Type: "GPE", Confidence: 0.93,
					Geo: &scriptpkg.GeoCoordinate{Latitude: lat, Longitude: lon, DisplayName: place},
				},
				scriptpkg.AnnotatedEntity{
					ID: "e-person-" + sceneID, CanonicalName: person, Type: "PERSON", Confidence: 0.95,
					Image: &scriptpkg.EntityImageBinding{
						Status: "bound", AssetID: "person-image-" + sceneID,
						SHA256:     digest.SHA256String("person-image-" + sceneID),
						PreviewURL: "https://images.example/" + person + ".jpg",
						MediaType:  "image/jpeg",
					},
				},
			)
			addOccurrence("GPE", place, len(words)-2, 0.93)
			addOccurrence("PERSON", person, len(words)-1, 0.95)
			plates = append(plates, goldenMixPlate(t, "plate-"+place, lat, lon))
		}
		words = append(words, longA...)
		words = append(words, longB...)

		timing := speechTimingForWords(words)
		text := strings.Join(words, " ")
		phrases := []scriptpkg.AnnotationSpan{
			{Text: strings.Join(shortA, " "), Score: 0.99},
			{Text: strings.Join(shortB, " "), Score: 0.98},
			{Text: strings.Join(longA, " "), Score: 0.97},
			{Text: strings.Join(longB, " "), Score: 0.96},
		}
		result.Scenes = append(result.Scenes, Scene{
			ID: sceneID, Index: i,
			Text: map[Language]string{"en": text},
			Voiceover: map[Language]AudioReference{
				"en": {ID: "vo-" + sceneID, Duration: float64(len(words)) * 0.1, Timing: &timing},
			},
			Annotations: &scriptpkg.SceneAnnotations{
				Version: 1, Language: "en", Status: "completed",
				ImportantPhrases: phrases, PrimaryEntities: entities,
			},
		})
		result.ResolvedScenes = append(result.ResolvedScenes, ResolvedScene{
			ID: sceneID, Index: i, TimelineStartUS: startUS, DurationUS: goldenMixSceneDurationUS,
		})
		result.EntityTimeline = appendEntityTimelineScene(result.EntityTimeline, sceneID, i, startUS, occs)

		if i < 10 {
			// Two certified internet stills per scene (20 generic images).
			candidates := make([]scriptpkg.SegmentAssetCandidate, 0, 2)
			for j := 0; j < 2; j++ {
				candidates = append(candidates, scriptpkg.SegmentAssetCandidate{
					SegmentID: sceneID, AssetID: fmt.Sprintf("still-%02d-%d", i, j),
					Provider:           scriptpkg.VidRushProviderInternetImages,
					AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
					VerificationStatus: scriptpkg.VidRushStatusVerified,
					PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
					IndexStatus:        scriptpkg.VidRushStatusIndexed,
					LegacyFileMD5:      fmt.Sprintf("%064x", i*2+j+1),
					SourceURL:          fmt.Sprintf("https://images.example/still-%02d-%d.jpg", i, j),
					MIMEType:           "image/jpeg",
				})
			}
			result.Segments = append(result.Segments, scriptpkg.VidRushSegmentResult{
				SegmentID: sceneID, SceneID: sceneID,
				Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: candidates},
			})
		}
	}
	return result, plateStub{plates: plates}
}

// appendEntityTimelineScene grows the canonical entity timeline with one
// scene's grounded occurrences.
func appendEntityTimelineScene(timeline *capabilityentities.EntityTimeline, sceneID string, index int, startUS int64, occs []capabilityentities.EntityOccurrence) *capabilityentities.EntityTimeline {
	if timeline == nil {
		timeline = &capabilityentities.EntityTimeline{
			Version: capabilityentities.EntityTimelineVersion, Language: "en",
		}
	}
	timeline.DurationUS = 20 * goldenMixSceneDurationUS
	timeline.Scenes = append(timeline.Scenes, capabilityentities.SceneEntityTimeline{
		SceneID: sceneID, SceneIndex: index, TimelineStartUS: startUS, Entities: occs,
	})
	return timeline
}

// TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items is the golden
// acceptance for the planner fix: the requested 100-item mix is materialized
// when the candidate supply exists — 25 short + 25 long phrases, 20 context
// images + 10 entity cards, 10 dates and 10 maps.
func TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items(t *testing.T) {
	result, resolver := goldenMixResult(t)
	plan, err := CompileOverlayPlanWithPlates(result, "en", goldenMixCanvas(),
		"plan-golden-mix", "video-golden-mix", "project-golden-mix", resolver, true, true)
	require.NoError(t, err)
	require.NotNil(t, plan)

	short, long := 0, 0
	for _, item := range plan.Items {
		if item.Kind == "text_phrase" {
			if words := len(strings.Fields(item.Text)); words <= 5 {
				short++
			} else {
				long++
			}
		}
	}
	require.Equal(t, 25, short, "the short phrase lane must materialize 25 items")
	require.Equal(t, 25, long, "the long phrase lane must materialize 25 items")
	require.Equal(t, 10, countKind(plan.Items, "number"), "the date lane must materialize 10 items")
	require.Equal(t, 10, countKind(plan.Items, "map"), "the map lane must materialize 10 items")
	require.Equal(t, 20, countKind(plan.Items, "image"), "the context image lane must materialize 20 items")
	require.Equal(t, 10, countKind(plan.Items, "entity_image"), "the entity card lane must materialize 10 items")
	require.Equal(t, 100, len(plan.Items), "the golden mix must materialize 100 items, not 36–41")
}

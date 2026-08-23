package main

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func measureCompile(plan capabilityoverlay.OverlayPlan) (meanUS, minUS float64) {
	var sum time.Duration
	var min time.Duration = 1<<63 - 1
	for i := 0; i < iterations; i++ {
		start := time.Now()
		if _, err := capabilityoverlay.CompileChrononPlan(plan); err != nil {
			fmt.Fprintf(os.Stderr, "compile: %v\n", err)
			os.Exit(1)
		}
		d := time.Since(start)
		sum += d
		if d < min {
			min = d
		}
	}
	return durUS(sum) / iterations, durUS(min)
}

// measureAutoSelect times CompileOverlayPlan over `iterations` runs.
func measureAutoSelect(result *scriptgen.GenerateResult) (meanUS, minUS float64) {
	var sum time.Duration
	var min time.Duration = 1<<63 - 1
	for i := 0; i < iterations; i++ {
		start := time.Now()
		if _, err := scriptgen.CompileOverlayPlan(result, "en", scriptgen.GoldenOverlayCanvas, "golden-content-06-full-scene", "video-golden-content-06-full-scene", "golden-content"); err != nil {
			fmt.Fprintf(os.Stderr, "auto-select: %v\n", err)
			os.Exit(1)
		}
		d := time.Since(start)
		sum += d
		if d < min {
			min = d
		}
	}
	return durUS(sum) / iterations, durUS(min)
}

func durUS(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1000.0 }

// content04Plan reconstructs the GOLDEN 04 light-leak semantic plan
// (background + two composited light leaks) — the twin of the light-leak
// golden fixture.
func content04Plan() capabilityoverlay.OverlayPlan {
	return capabilityoverlay.OverlayPlan{
		SchemaVersion:   capabilityoverlay.SchemaVersionPlan,
		PlanID:          "golden-content-04-light-leak",
		VideoID:         "video-golden-content-04-light-leak",
		ProjectID:       "golden-content",
		Width:           1280,
		Height:          720,
		FPSNum:          30,
		FPSDen:          1,
		RendererVersion: "chronon",
		Items: []capabilityoverlay.OverlayItem{
			{
				ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 5000,
				AssetRefs: []capabilityoverlay.OverlayAssetRef{{
					AssetID: "background", URL: "assets/background.jpg",
					SHA256: capabilityoverlay.GoldenBackgroundHash, MediaType: "image/jpeg",
				}},
			},
			{
				ID: "light_leak_1", TemplateID: "LIGHT_LEAK", StartMs: 1200, EndMs: 3000,
				Params: map[string]any{"blend_mode": "screen", "opacity": 0.65, "loop": false},
				AssetRefs: []capabilityoverlay.OverlayAssetRef{{
					AssetID: "leak-1", URL: "assets/light_leak_01.mp4",
					SHA256: capabilityoverlay.GoldenLightLeak01Hash, MediaType: "video/mp4",
				}},
			},
			{
				ID: "light_leak_2", TemplateID: "LIGHT_LEAK", StartMs: 3200, EndMs: 4400,
				Params: map[string]any{"blend_mode": "add", "opacity": 0.4, "loop": true},
				AssetRefs: []capabilityoverlay.OverlayAssetRef{{
					AssetID: "leak-2", URL: "assets/light_leak_02.mp4",
					SHA256: capabilityoverlay.GoldenLightLeak02Hash, MediaType: "video/mp4",
				}},
			},
		},
	}
}

// ── GOLDEN 06 ──────────────────────────────────────────────────────────────

const golden06Script = "Apple shocked investors today after announcing a ten billion dollar investment in artificial intelligence. Tim Cook described the move as one of the company's most important decisions in years."

var golden06Words = strings.Fields(golden06Script)

// content06Plan derives the GOLDEN 06 semantic plan exactly like cmd/golden06:
// script → auto-selection → structural layers (background + light leak) → real
// asset hashes. Returns the finalized plan ready for CompileChrononPlan.
func content06Plan() capabilityoverlay.OverlayPlan {
	plan, err := scriptgen.CompileOverlayPlan(golden06Result(), "en", scriptgen.GoldenOverlayCanvas, "golden-content-06-full-scene", "video-golden-content-06-full-scene", "golden-content")
	if err != nil || plan == nil {
		fmt.Fprintf(os.Stderr, "golden06 plan: %v\n", err)
		os.Exit(1)
	}
	finalize06(plan)
	return *plan
}

func finalize06(plan *capabilityoverlay.OverlayPlan) {
	hashByFile := map[string]string{
		"background.jpg":    capabilityoverlay.GoldenBackgroundHash,
		"apple.png":         capabilityoverlay.GoldenAppleHash,
		"overlay_globe.png": capabilityoverlay.GoldenGlobeHash,
		"overlay_chart.png": capabilityoverlay.GoldenChartHash,
		"logo_pulse.png":    capabilityoverlay.GoldenLogoHash,
		"light_leak_01.mp4": capabilityoverlay.GoldenLightLeak01Hash,
		"light_leak_02.mp4": capabilityoverlay.GoldenLightLeak02Hash,
	}
	for i := range plan.Items {
		for j := range plan.Items[i].AssetRefs {
			ref := &plan.Items[i].AssetRefs[j]
			if h, ok := hashByFile[path.Base(strings.TrimSpace(ref.URL))]; ok {
				ref.SHA256 = h
			}
		}
	}
	background := capabilityoverlay.OverlayItem{
		ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 5000,
		AssetRefs: []capabilityoverlay.OverlayAssetRef{{
			AssetID: "background", URL: "assets/background.jpg",
			SHA256: capabilityoverlay.GoldenBackgroundHash, MediaType: "image/jpeg",
		}},
	}
	lightLeak := capabilityoverlay.OverlayItem{
		ID: "light_leak", TemplateID: "LIGHT_LEAK", StartMs: 2000, EndMs: 2400,
		Params: map[string]any{"blend_mode": "screen", "opacity": 0.6},
		AssetRefs: []capabilityoverlay.OverlayAssetRef{{
			AssetID: "leak-1", URL: "assets/light_leak_01.mp4",
			SHA256: capabilityoverlay.GoldenLightLeak01Hash, MediaType: "video/mp4",
		}},
	}
	var images, text []capabilityoverlay.OverlayItem
	for _, item := range plan.Items {
		switch item.TemplateID {
		case "IMAGE_OVERLAY", "PRODUCT", "LOGO":
			images = append(images, item)
		default:
			text = append(text, item)
		}
	}
	ordered := make([]capabilityoverlay.OverlayItem, 0, len(plan.Items)+2)
	ordered = append(ordered, background)
	ordered = append(ordered, images...)
	ordered = append(ordered, lightLeak)
	ordered = append(ordered, text...)
	plan.Items = ordered
	for i := range plan.Items {
		plan.Items[i].RenderKey = ""
	}
	plan.Fingerprint = ""
	if err := plan.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "golden06 seal: %v\n", err)
		os.Exit(1)
	}
}

func golden06Result() *scriptgen.GenerateResult {
	wordCount := len(golden06Words)
	words := make([]capabilityaudio.SpeechWordTiming, wordCount)
	for i, w := range golden06Words {
		words[i] = capabilityaudio.SpeechWordTiming{Index: i, Text: w, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000}
	}
	timing, err := capabilityaudio.BuildSpeechTimingArtifact(
		"edge_tts", "en", "", "golden-06-text", "golden-06-audio",
		int64(wordCount)*100_000, words,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "golden06 timing: %v\n", err)
		os.Exit(1)
	}
	return &scriptgen.GenerateResult{
		Scenes: []scriptgen.Scene{{
			ID:    "scene-0",
			Index: 0,
			Text:  map[scriptgen.Language]string{"en": golden06Script},
			Voiceover: map[scriptgen.Language]scriptgen.AudioReference{
				"en": {ID: "vo-scene-0-en", Duration: 2.9, Timing: timing},
			},
			Annotations: golden06Annotations(),
		}},
		EntityTimeline: golden06Timeline(),
		ResolvedScenes: []scriptgen.ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: int64(wordCount) * 100_000}},
	}
}

func golden06Annotations() *scriptpkg.SceneAnnotations {
	return &scriptpkg.SceneAnnotations{
		Version: 1, Language: "en", Status: "completed",
		ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "most important decisions", Score: 0.85}},
		ImportantWords: []scriptpkg.AnnotationSpan{
			{Text: "Apple", Score: 1.0},
			{Text: "artificial intelligence", Score: 0.9},
			{Text: "ten billion", Score: 0.8},
		},
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-apple", CanonicalName: "Apple", Type: "LOGO", Confidence: 0.98,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "apple-logo", PreviewURL: "assets/apple.png"}},
			{ID: "entity-tim-cook", CanonicalName: "Tim Cook", Type: "PERSON", Confidence: 0.97,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "tim-cook-photo", PreviewURL: "assets/overlay_globe.png"}},
		},
		SecondaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-ten-billion", CanonicalName: "ten billion", Type: "MONEY", Confidence: 0.9},
			{ID: "entity-artificial-intelligence", CanonicalName: "artificial intelligence", Type: "CONCEPT", Confidence: 0.85},
		},
	}
}

func golden06Timeline() *capabilityentities.EntityTimeline {
	occ := func(name, typ string, start, end int) capabilityentities.EntityOccurrence {
		return capabilityentities.EntityOccurrence{
			EntityID: capabilityentities.StableEntityID(typ, name), Name: name, Type: typ,
			SceneID: "scene-0", SceneIndex: 0,
			TextStart: start, TextEnd: start + 1, WordStart: start, WordEnd: end,
			LocalStartUS: int64(start) * 100_000, LocalEndUS: int64(end+1) * 100_000,
			TimelineStartUS: 0, AudioStartUS: int64(start) * 100_000, AudioEndUS: int64(end+1) * 100_000,
			Confidence: 0.9,
		}
	}
	return &capabilityentities.EntityTimeline{
		Version:    capabilityentities.EntityTimelineVersion,
		DurationUS: int64(len(golden06Words)) * 100_000,
		Scenes: []capabilityentities.SceneEntityTimeline{{
			SceneID: "scene-0", SceneIndex: 0, TimelineStartUS: 0,
			Entities: []capabilityentities.EntityOccurrence{
				occ("Apple", "LOGO", 0, 0),
				occ("ten billion", "MONEY", 7, 8),
				occ("artificial intelligence", "CONCEPT", 12, 13),
				occ("Tim Cook", "PERSON", 14, 15),
			},
		}},
	}
}

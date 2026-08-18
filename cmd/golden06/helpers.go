package main

import (
	"fmt"
	"os"
	"path"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func layerLabel(l capabilityoverlay.ChrononLayer) string {
	switch l.Type {
	case "text":
		return fmt.Sprintf("text=%q font=%s", l.Text, l.Font)
	case "image":
		return "asset=" + l.Asset
	case "video":
		return fmt.Sprintf("source=%s blend=%s opacity=%.2f", l.Source, l.BlendMode, l.Opacity)
	default:
		return ""
	}
}

// finalize injects the structural layers the planner does not own (the
// full-canvas background and the light-leak emphasis before the headline),
// then resolves the real content hashes for every image/video asset so the
// worker can materialize them from the object store.
func finalize(plan *capabilityoverlay.OverlayPlan) {
	hashByFile := map[string]string{
		"background.jpg":    capabilityoverlay.GoldenBackgroundHash,
		"apple.png":         capabilityoverlay.GoldenAppleHash,
		"overlay_globe.png": capabilityoverlay.GoldenGlobeHash,
		"overlay_chart.png": capabilityoverlay.GoldenChartHash,
		"logo_pulse.png":    capabilityoverlay.GoldenLogoHash,
		"light_leak_01.mp4": capabilityoverlay.GoldenLightLeak01Hash,
		"light_leak_02.mp4": capabilityoverlay.GoldenLightLeak02Hash,
	}
	resolve := func(item *capabilityoverlay.OverlayItem) {
		for i := range item.AssetRefs {
			ref := &item.AssetRefs[i]
			if hash, ok := hashByFile[path.Base(strings.TrimSpace(ref.URL))]; ok {
				ref.SHA256 = hash
			}
		}
	}

	// 1. Inject real hashes into the auto-selected asset refs.
	for i := range plan.Items {
		resolve(&plan.Items[i])
	}

	// 2. Build the final layer order (bottom → top): background → images →
	//    light leak → text overlays.
	const total = 5000 // 5s @ 30fps = 150 frames, matching the golden canvas.
	background := capabilityoverlay.OverlayItem{
		ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: total,
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
	resolve(&lightLeak)

	imageLayer := func(t string) bool {
		switch t {
		case "IMAGE_OVERLAY", "PRODUCT", "LOGO":
			return true
		}
		return false
	}
	var images, text []capabilityoverlay.OverlayItem
	for _, item := range plan.Items {
		if imageLayer(item.TemplateID) {
			images = append(images, item)
		} else {
			text = append(text, item)
		}
	}

	ordered := make([]capabilityoverlay.OverlayItem, 0, len(plan.Items)+2)
	ordered = append(ordered, background)
	ordered = append(ordered, images...)
	ordered = append(ordered, lightLeak)
	ordered = append(ordered, text...)
	plan.Items = ordered

	// 3. Re-seal: clear render keys + fingerprint so Validate recomputes them
	//    over the finalized items.
	for i := range plan.Items {
		plan.Items[i].RenderKey = ""
	}
	plan.Fingerprint = ""
	if err := plan.Validate(); err != nil {
		fatalf("seal finalized plan: %v", err)
	}
}

func buildResult() *scriptgen.GenerateResult {
	wordCount := len(golden06Words)
	words := make([]capabilityaudio.SpeechWordTiming, wordCount)
	for i, w := range golden06Words {
		words[i] = capabilityaudio.SpeechWordTiming{
			Index: i, Text: w, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000,
		}
	}
	timing, err := capabilityaudio.BuildSpeechTimingArtifact(
		"edge_tts", "en", "", "golden-06-text", "golden-06-audio",
		int64(wordCount)*100_000, words,
	)
	if err != nil {
		fatalf("build speech timing artifact: %v", err)
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
		Version:  1,
		Language: "en",
		Status:   "completed",
		ImportantPhrases: []scriptpkg.AnnotationSpan{
			{Text: "most important decisions", Score: 0.85},
		},
		ImportantWords: []scriptpkg.AnnotationSpan{
			{Text: "Apple", Score: 1.0},
			{Text: "artificial intelligence", Score: 0.9},
			{Text: "ten billion", Score: 0.8},
		},
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{
				ID: "entity-apple", CanonicalName: "Apple", Type: "LOGO", Confidence: 0.98,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "apple-logo", PreviewURL: "assets/apple.png"},
			},
			{
				ID: "entity-tim-cook", CanonicalName: "Tim Cook", Type: "PERSON", Confidence: 0.97,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "tim-cook-photo", PreviewURL: "assets/overlay_globe.png"},
			},
		},
		SecondaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-ten-billion", CanonicalName: "ten billion", Type: "MONEY", Confidence: 0.9},
			{ID: "entity-artificial-intelligence", CanonicalName: "artificial intelligence", Type: "CONCEPT", Confidence: 0.85},
		},
	}
}

// golden06Timeline anchors every entity to its verbatim word window.
func golden06Timeline() *capabilityentities.EntityTimeline {
	occ := func(name, typ string, start, end int) capabilityentities.EntityOccurrence {
		return capabilityentities.EntityOccurrence{
			EntityID:        capabilityentities.StableEntityID(typ, name),
			Name:            name,
			Type:            typ,
			SceneID:         "scene-0",
			SceneIndex:      0,
			TextStart:       start,
			TextEnd:         start + 1,
			WordStart:       start,
			WordEnd:         end,
			LocalStartUS:    int64(start) * 100_000,
			LocalEndUS:      int64(end+1) * 100_000,
			TimelineStartUS: 0,
			AudioStartUS:    int64(start) * 100_000,
			AudioEndUS:      int64(end+1) * 100_000,
			Confidence:      0.9,
		}
	}
	return &capabilityentities.EntityTimeline{
		Version:    capabilityentities.EntityTimelineVersion,
		DurationUS: int64(len(golden06Words)) * 100_000,
		Scenes: []capabilityentities.SceneEntityTimeline{{
			SceneID:         "scene-0",
			SceneIndex:      0,
			TimelineStartUS: 0,
			Entities: []capabilityentities.EntityOccurrence{
				occ("Apple", "LOGO", 0, 0),
				occ("ten billion", "MONEY", 7, 8),
				occ("artificial intelligence", "CONCEPT", 12, 13),
				occ("Tim Cook", "PERSON", 14, 15),
			},
		}},
	}
}

type queueJob struct {
	ID         string                        `json:"id"`
	Schema     string                        `json:"schema"`
	Version    int                           `json:"version"`
	RenderPlan capabilityoverlay.ChrononPlan `json:"render_plan"`
	Assets     []queueAsset                  `json:"assets"`
}

type queueAsset struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "golden06: "+format+"\n", args...)
	os.Exit(1)
}

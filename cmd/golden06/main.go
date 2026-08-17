// Command golden06 executes the GOLDEN 06 chain at the PipelineGen boundary:
//
//	script → auto content selection (CompileOverlayPlan) → OverlayPlan
//	       → chronon.render-plan.v1 (CompileChrononPlan) → queue job fixture
//
// It is the executable definition of the "Full Script Scene" golden: it takes
// only the script scene (plus the deterministic semantic surfaces the LLM/NER/
// TTS steps would have produced — annotations, entity timeline, word timing)
// and derives the concrete render plan, then writes the RenderingGen queue job
// fixture (render_plan + content-addressed assets) that the worker executes
// (MP4 → SHA256 → DB → Drive).
//
// Usage:
//
//	go run ./cmd/golden06 -out ../RenderingGen/testdata/golden/golden-content-06-full-scene-job-v1.json
package main

import (
	"encoding/json"
	"flag"
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

const (
	jobID     = "golden-content-06-full-scene"
	videoID   = "video-golden-content-06-full-scene"
	projectID = "golden-content"
)

// golden06Script is the GOLDEN 06 source text (one scene, 29 words).
const golden06Script = "Apple shocked investors today after announcing a ten billion dollar investment in artificial intelligence. Tim Cook described the move as one of the company's most important decisions in years."

// golden06Words is the canonical word list of the scene text, 100ms per word
// (deterministic stand-in for the real TTS word-boundary timing).
var golden06Words = strings.Fields(golden06Script)

func main() {
	outPath := flag.String("out", "../RenderingGen/testdata/golden/golden-content-06-full-scene-job-v1.json", "output fixture path")
	flag.Parse()

	result := buildResult()
	plan, err := scriptgen.CompileOverlayPlan(result, "en", scriptgen.DefaultOverlayCanvas, jobID, videoID, projectID)
	if err != nil {
		fatalf("compile overlay plan: %v", err)
	}
	if plan == nil {
		fatalf("script produced no derivable overlay surface")
	}

	fmt.Printf("== GOLDEN 06 auto content selection ==\n")
	fmt.Printf("script (%d words): %s\n\n", len(golden06Words), golden06Script)
	for i, item := range plan.Items {
		fmt.Printf("  [%02d] %-22s %-24q %5d→%5d ms\n", i, item.TemplateID, item.Text, item.StartMs, item.EndMs)
	}

	finalize(plan)

	compiled, err := capabilityoverlay.CompileChrononPlan(*plan)
	if err != nil {
		fatalf("compile chronon plan: %v", err)
	}

	job := queueJob{
		ID:         jobID,
		Schema:     "renderinggen.job",
		Version:    1,
		RenderPlan: compiled.Plan,
		Assets:     make([]queueAsset, 0, len(compiled.Assets)),
	}
	for _, a := range compiled.Assets {
		job.Assets = append(job.Assets, queueAsset{Hash: a.Hash, LogicalPath: a.LogicalPath})
	}

	raw, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		fatalf("marshal job: %v", err)
	}
	if err := os.WriteFile(*outPath, append(raw, '\n'), 0o644); err != nil {
		fatalf("write fixture: %v", err)
	}

	fmt.Printf("\n== compiled chronon.render-plan.v1 ==\n")
	fmt.Printf("layers: %d, duration_frames: %d, assets: %d\n", len(compiled.Plan.Layers), compiled.Plan.Canvas.DurationFrames, len(compiled.Assets))
	for i, layer := range compiled.Plan.Layers {
		fmt.Printf("  [%02d] %-6s %-8s f%3d..%3d  %s\n", i, layer.Type, layer.Preset, layer.StartFrame, layer.StartFrame+layer.DurationFrames, layerLabel(layer))
	}
	fmt.Printf("\nfixture written: %s\n", *outPath)
}

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
	timing := capabilityaudio.SpeechTimingArtifact{
		Version:      capabilityaudio.SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "golden-06-text",
		AudioSHA256:  "golden-06-audio",
		DurationUS:   int64(wordCount) * 100_000,
		Words:        make([]capabilityaudio.SpeechWordTiming, wordCount),
	}
	for i, w := range golden06Words {
		timing.Words[i] = capabilityaudio.SpeechWordTiming{
			Index: i, Text: w, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000,
		}
	}

	return &scriptgen.GenerateResult{
		Scenes: []scriptgen.Scene{{
			ID:    "scene-0",
			Index: 0,
			Text:  map[scriptgen.Language]string{"en": golden06Script},
			Voiceover: map[scriptgen.Language]scriptgen.AudioReference{
				"en": {ID: "vo-scene-0-en", Duration: 2.9, Timing: &timing},
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
			EntityID:        capabilityentities.SafeEntityID(name),
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

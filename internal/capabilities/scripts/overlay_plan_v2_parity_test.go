// Package scriptgeneration — Go ↔ Rust sealed-plan parity harness.
//
// The harness compiles the same checked-in `editorial_plan.v2` fixture through
// two independent paths:
//
//  1. the current Go planner (`compileOverlayPlanWithMotionOffset`);
//  2. the Rust single-call planner, represented by the checked-in golden
//     produced by `overlay-compiler --example compile_fixture` and enforced by
//     `overlay-compiler/tests/overlay_plan_contract.rs`.
//
// It compares the decision surface, not renderer metadata: kind, template,
// editorial role, scene, text and the absolute microsecond window. Style,
// motion, effects, animation and opaque params are intentionally excluded
// until those phases are ported; comparing them now would produce noise, not
// a parity signal.
//
// The differential test is opt-in because the cutover is not complete:
//
//	OVERLAY_PLAN_PARITY=1 go test ./internal/capabilities/scripts \
//	  -run TestOverlayPlanV2ParityMinimalPhraseOnly -count=1 -v
//
// The always-on shape test only proves that the fixture and golden are
// readable and internally consistent, so normal CI stays green while the
// cutover is in progress.
package scriptgeneration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	overlayParityFixtureDir = "../../../../BrainRust/overlay-compiler/tests/fixtures/overlay_plan"
	overlayParityFixture    = "minimal_phrase_only"
)

type overlayParityRequest struct {
	SchemaVersion  string `json:"schema_version"`
	Language       string `json:"language"`
	SourceLanguage string `json:"source_language"`
	Plan           struct {
		PlanID     string `json:"plan_id"`
		VideoID    string `json:"video_id"`
		ProjectID  string `json:"project_id"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		FPSNum     int    `json:"fps_num"`
		FPSDen     int    `json:"fps_den"`
		MotionSeed string `json:"motion_seed"`
	} `json:"plan"`
	Seal struct {
		AudioDurationUS int64  `json:"audio_duration_us"`
		OutputProfileID string `json:"output_profile_id"`
	} `json:"seal"`
	Canvas struct {
		MaxPhraseOverlays        int               `json:"max_phrase_overlays"`
		MaxImageOverlays         int               `json:"max_image_overlays"`
		MaxMapOverlays           int               `json:"max_map_overlays"`
		MaxOverlaysPerScene      int               `json:"max_overlays_per_scene"`
		OverlayCategories        map[string]string `json:"overlay_categories"`
		AnimationCounts          map[string]int    `json:"animation_counts"`
		AnimationContractVersion int               `json:"animation_contract_version"`
		AnimationCatalogDigest   string            `json:"animation_catalog_digest"`
		EntityStyleID            string            `json:"entity_style_id"`
	} `json:"canvas"`
	Editorial      *scriptpkg.EditorialManifest       `json:"editorial"`
	Chapters       json.RawMessage                    `json:"chapters"`
	Scenes         []overlayParityScene               `json:"scenes"`
	EntityTimeline *capabilityentities.EntityTimeline `json:"entity_timeline"`
	Media          *overlayParityMedia                `json:"media"`
	Budgets        *overlayParityBudgets              `json:"budgets"`
}

type overlayParityScene struct {
	ID                 string            `json:"id"`
	Text               string            `json:"text"`
	DurationMS         int64             `json:"duration_ms"`
	ExecutionMode      string            `json:"execution_mode"`
	ShortPhraseOverlay bool              `json:"short_phrase_overlay"`
	TimelineStartUS    int64             `json:"timeline_start_us"`
	DurationUS         int64             `json:"duration_us"`
	OverlayCategories  map[string]string `json:"overlay_categories"`
	Voiceover          *struct {
		Timing *capabilityaudio.SpeechTimingArtifact `json:"timing"`
	} `json:"voiceover"`
	Annotations *overlayParityAnnotations `json:"annotations"`
}

type overlayParityAnnotations struct {
	Phrases  []overlayParityAnnotation `json:"phrases"`
	Keywords []overlayParityAnnotation `json:"keywords"`
	Images   []overlayParityImage      `json:"images"`
}

type overlayParityAnnotation struct {
	Text       string  `json:"text"`
	StartUS    int64   `json:"start_us"`
	DurationUS int64   `json:"duration_us"`
	Score      float64 `json:"score"`
}

type overlayParityImage struct {
	AssetID    string  `json:"asset_id"`
	URL        string  `json:"url"`
	SHA256     string  `json:"sha256"`
	MediaType  string  `json:"media_type"`
	StartUS    int64   `json:"start_us"`
	DurationUS int64   `json:"duration_us"`
	Score      float64 `json:"score"`
}

type overlayParityMedia struct {
	MaterializedAssets []struct {
		AssetID   string `json:"asset_id"`
		URL       string `json:"url"`
		SHA256    string `json:"sha256"`
		MediaType string `json:"media_type"`
	} `json:"materialized_assets"`
	VerifiedEntityAssets []struct {
		EntityID string `json:"entity_id"`
		Assets   []struct {
			AssetID   string `json:"asset_id"`
			URL       string `json:"url"`
			SHA256    string `json:"sha256"`
			MediaType string `json:"media_type"`
		} `json:"assets"`
	} `json:"verified_entity_assets"`
}

type overlayParityBudgets struct {
	PhraseOverlays int `json:"phrase_overlays"`
	ImageOverlays  int `json:"image_overlays"`
	MapOverlays    int `json:"map_overlays"`
	MaxPerScene    int `json:"max_per_scene"`
}

type overlayParityRustPlan struct {
	OK            bool   `json:"ok"`
	SchemaVersion string `json:"schema_version"`
	Plan          struct {
		SchemaVersion string `json:"schema_version"`
		PlanID        string `json:"plan_id"`
		VideoID       string `json:"video_id"`
		DurationMS    int64  `json:"duration_ms"`
		Items         []struct {
			ID         string         `json:"id"`
			Kind       string         `json:"kind"`
			TemplateID string         `json:"template_id"`
			SceneID    string         `json:"scene_id"`
			Text       string         `json:"text"`
			StartUS    int64          `json:"start_us"`
			DurationUS int64          `json:"duration_us"`
			Params     map[string]any `json:"params"`
		} `json:"items"`
	} `json:"plan"`
}

type overlayParityItem struct {
	Kind       string `json:"kind"`
	TemplateID string `json:"template_id"`
	Role       string `json:"editorial_role"`
	SceneID    string `json:"scene_id"`
	Text       string `json:"text"`
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
}

func loadOverlayParityFixture(t *testing.T) (overlayParityRequest, overlayParityRustPlan) {
	t.Helper()
	// PipelineGen CI checks out without submodules, so the sibling BrainRust
	// fixture may be absent. The differential cannot run without it; skip
	// explicitly instead of turning a missing cross-repo checkout into a red
	// package.
	if _, err := os.Stat(overlayParityFixtureDir); err != nil {
		t.Skipf("cross-repo BrainRust fixture unavailable: %v", err)
	}
	requestPath := filepath.Join(overlayParityFixtureDir, "requests", overlayParityFixture+".request.json")
	planPath := filepath.Join(overlayParityFixtureDir, "plans", overlayParityFixture+".plan.json")
	requestBytes, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read parity request %s: %v", requestPath, err)
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read parity golden %s: %v", planPath, err)
	}
	var request overlayParityRequest
	if err := json.Unmarshal(requestBytes, &request); err != nil {
		t.Fatalf("decode parity request: %v", err)
	}
	var plan overlayParityRustPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("decode parity golden: %v", err)
	}
	return request, plan
}

// TestOverlayPlanV2ParityFixtureShape is always-on. It validates the fixture
// pair the differential test consumes, without asserting cross-planner
// equality that the cutover has not reached yet.
func TestOverlayPlanV2ParityFixtureShape(t *testing.T) {
	request, golden := loadOverlayParityFixture(t)
	if request.SchemaVersion != "editorial_plan.v2" {
		t.Fatalf("schema_version=%q, want editorial_plan.v2", request.SchemaVersion)
	}
	if !golden.OK || golden.SchemaVersion != "editorial_plan.v2" || golden.Plan.SchemaVersion != "editorial_plan.v2" {
		t.Fatalf("golden envelope is not a valid editorial_plan.v2 response: %+v", golden)
	}
	if request.Plan.PlanID != golden.Plan.PlanID || request.Plan.VideoID != golden.Plan.VideoID {
		t.Fatalf("golden identity %q/%q does not match request %q/%q",
			golden.Plan.PlanID, golden.Plan.VideoID, request.Plan.PlanID, request.Plan.VideoID)
	}
	if len(request.Scenes) != 1 || len(golden.Plan.Items) == 0 {
		t.Fatalf("parity fixture must carry one scene and a non-empty golden: scenes=%d items=%d",
			len(request.Scenes), len(golden.Plan.Items))
	}
	if request.Scenes[0].ID == "" || request.Scenes[0].DurationUS <= 0 {
		t.Fatalf("parity scene must carry id and positive duration: %+v", request.Scenes[0])
	}
	for _, item := range golden.Plan.Items {
		if item.ID == "" || item.SceneID == "" || item.Kind == "" || item.DurationUS <= 0 {
			t.Fatalf("golden item is not a sealed decision: %+v", item)
		}
	}
}

// TestOverlayPlanV2ParityMinimalPhraseOnly is the differential gate. It is
// opt-in until the remaining planner phases reach parity; once they do, remove
// the environment guard so the comparison runs on every CI pass.
func TestOverlayPlanV2ParityMinimalPhraseOnly(t *testing.T) {
	if os.Getenv("OVERLAY_PLAN_PARITY") != "1" {
		t.Skip("set OVERLAY_PLAN_PARITY=1 to run the Go↔Rust sealed-plan parity harness")
	}
	request, golden := loadOverlayParityFixture(t)
	goPlan, err := compileOverlayParityGoPlan(request)
	if err != nil {
		t.Fatalf("compile parity fixture with the Go planner: %v", err)
	}
	goItems := overlayParityItemsFromGo(goPlan)
	rustItems := overlayParityItemsFromRust(golden)
	diffs := diffOverlayParityItems(goItems, rustItems)
	if len(diffs) == 0 {
		return
	}
	report := map[string]any{
		"fixture": overlayParityFixture,
		"diffs":   diffs,
		"go":      goItems,
		"rust":    rustItems,
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	t.Errorf("Go↔Rust parity differs on %s:\n%s", overlayParityFixture, encoded)
}

func compileOverlayParityGoPlan(request overlayParityRequest) (*capmodel.OverlayPlan, error) {
	if len(request.Scenes) == 0 {
		return nil, fmt.Errorf("parity request carries no scenes")
	}
	scene := request.Scenes[0]
	timing := (*capabilityaudio.SpeechTimingArtifact)(nil)
	if scene.Voiceover != nil {
		timing = scene.Voiceover.Timing
	}
	if timing != nil {
		// The v2 fixture carries only the authoritative word boundaries; the
		// legacy Go locator additionally validates the artifact envelope. Fill
		// the envelope metadata without touching any timestamp, so both
		// planners consume the same certified timing.
		timing.Version = capabilityaudio.SpeechTimingVersion
		if timing.BoundaryMode == "" {
			timing.BoundaryMode = capabilityaudio.BoundaryWord
		}
		if timing.Language == "" {
			timing.Language = request.Language
		}
		if timing.Provider == "" {
			timing.Provider = "parity-fixture"
		}
		if timing.TextSHA256 == "" {
			timing.TextSHA256 = strings.Repeat("d", 64)
		}
		if timing.AudioSHA256 == "" {
			timing.AudioSHA256 = strings.Repeat("e", 64)
		}
	}
	var annotations *scriptpkg.SceneAnnotations
	if scene.Annotations != nil {
		annotations = &scriptpkg.SceneAnnotations{
			Version:  1,
			Language: request.Language,
			Status:   "completed",
		}
		for _, phrase := range scene.Annotations.Phrases {
			annotations.ImportantPhrases = append(annotations.ImportantPhrases, scriptpkg.AnnotationSpan{
				Text: phrase.Text, Score: phrase.Score,
			})
		}
		for _, keyword := range scene.Annotations.Keywords {
			annotations.ImportantWords = append(annotations.ImportantWords, scriptpkg.AnnotationSpan{
				Text: keyword.Text, Score: keyword.Score,
			})
		}
		if entity := parityPrimaryEntity(request); entity != nil {
			annotations.PrimaryEntities = append(annotations.PrimaryEntities, *entity)
		}
	}
	voiceover := map[Language]AudioReference{}
	if timing != nil {
		voiceover[Language(request.Language)] = AudioReference{
			ID:       "vo-" + scene.ID,
			Duration: float64(timing.DurationUS) / 1_000_000,
			Timing:   timing,
		}
	}
	// The v2 fixture omits a per-scene category map, which the Rust planner
	// reads as "use the global toggle" (empty = enabled). The legacy Go scene
	// contract stores the resolved toggles explicitly and AsBool() treats the
	// zero value as disabled, so the parity fixture must spell out the same
	// resolved default instead of silently disabling phrases/editorial.
	sceneCategories := map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryPhrases:   scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryImages:    scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryNumbers:   scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryQuotes:    scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryBrandText: scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryMaps:      scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryCharts:    scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryRecaps:    scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryTitles:    scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryBullets:   scriptpkg.ToggleEnabled,
	}
	result := &GenerateResult{SourceLanguage: Language(request.SourceLanguage),
		Editorial:      request.Editorial,
		EntityTimeline: request.EntityTimeline,
		Scenes: []Scene{{
			ID:                 scene.ID,
			Index:              0,
			DurationMS:         scene.DurationMS,
			DurationUS:         scene.DurationUS,
			ExecutionMode:      scriptpkg.SceneExecutionMode(scene.ExecutionMode),
			ShortPhraseOverlay: scene.ShortPhraseOverlay,
			OverlayCategories:  sceneCategories,
			Text:               map[Language]string{Language(request.Language): scene.Text},
			Voiceover:          voiceover,
			Annotations:        annotations,
		}},
		ResolvedScenes: []ResolvedScene{{
			ID:              scene.ID,
			Index:           0,
			TimelineStartUS: scene.TimelineStartUS,
			DurationUS:      scene.DurationUS,
		}},
	}
	canvas := OverlayCanvasSpec{
		Width:                    request.Plan.Width,
		Height:                   request.Plan.Height,
		FPSNum:                   request.Plan.FPSNum,
		FPSDen:                   request.Plan.FPSDen,
		OutputProfileID:          request.Seal.OutputProfileID,
		MaxPhraseOverlays:        request.Canvas.MaxPhraseOverlays,
		MaxImageOverlays:         request.Canvas.MaxImageOverlays,
		MaxMapOverlays:           request.Canvas.MaxMapOverlays,
		MaxOverlaysPerScene:      request.Canvas.MaxOverlaysPerScene,
		AnimationCounts:          request.Canvas.AnimationCounts,
		AnimationContractVersion: request.Canvas.AnimationContractVersion,
		AnimationCatalogDigest:   request.Canvas.AnimationCatalogDigest,
		EntityStyleID:            request.Canvas.EntityStyleID,
		OverlayCategories:        sceneCategories,
	}
	// The fixture has no maps, so the plate resolver is deliberately nil. The
	// passthrough resolver keeps the non-map decision surfaces (trim, ranking,
	// cascade) pure for the comparison.
	return compileMapPlanForTest(result, Language(request.Language), canvas,
		request.Plan.PlanID, request.Plan.VideoID, request.Plan.ProjectID, nil)
}

func parityPrimaryEntity(request overlayParityRequest) *scriptpkg.AnnotatedEntity {
	if request.EntityTimeline == nil {
		return nil
	}
	for _, scene := range request.EntityTimeline.Scenes {
		for _, occurrence := range scene.Entities {
			entity := &scriptpkg.AnnotatedEntity{
				ID:            "parity-" + occurrence.EntityID,
				CanonicalName: occurrence.Name,
				Type:          occurrence.Type,
				Confidence:    occurrence.Confidence,
			}
			if request.Media != nil {
				for _, verified := range request.Media.VerifiedEntityAssets {
					if verified.EntityID != occurrence.EntityID || len(verified.Assets) == 0 {
						continue
					}
					asset := verified.Assets[0]
					entity.Image = &scriptpkg.EntityImageBinding{
						Status:     "bound",
						AssetID:    asset.AssetID,
						PreviewURL: asset.URL,
						SHA256:     asset.SHA256,
						MediaType:  asset.MediaType,
					}
					break
				}
			}
			return entity
		}
	}
	return nil
}

func overlayParityItemsFromGo(plan *capmodel.OverlayPlan) []overlayParityItem {
	items := make([]overlayParityItem, 0, len(plan.Items))
	for _, item := range plan.Items {
		role := ""
		if item.Params != nil {
			role, _ = item.Params["editorial_role"].(string)
		}
		startUS, durationUS := parityGoWindow(item)
		items = append(items, overlayParityItem{
			Kind:       item.Kind,
			TemplateID: item.TemplateID,
			Role:       role,
			SceneID:    item.SceneID,
			Text:       item.Text,
			StartUS:    startUS,
			DurationUS: durationUS,
		})
	}
	sortOverlayParityItems(items)
	return items
}

func parityGoWindow(item capmodel.OverlayItem) (int64, int64) {
	if item.DurationUS > 0 {
		return item.StartUS, item.DurationUS
	}
	if item.EndMs > item.StartMs {
		return item.StartMs * 1000, (item.EndMs - item.StartMs) * 1000
	}
	return 0, 0
}

func overlayParityItemsFromRust(plan overlayParityRustPlan) []overlayParityItem {
	items := make([]overlayParityItem, 0, len(plan.Plan.Items))
	for _, item := range plan.Plan.Items {
		role := ""
		if item.Params != nil {
			role, _ = item.Params["editorial_role"].(string)
		}
		items = append(items, overlayParityItem{
			Kind:       item.Kind,
			TemplateID: item.TemplateID,
			Role:       role,
			SceneID:    item.SceneID,
			Text:       item.Text,
			StartUS:    item.StartUS,
			DurationUS: item.DurationUS,
		})
	}
	sortOverlayParityItems(items)
	return items
}

func sortOverlayParityItems(items []overlayParityItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartUS != items[j].StartUS {
			return items[i].StartUS < items[j].StartUS
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Role != items[j].Role {
			return items[i].Role < items[j].Role
		}
		if items[i].Text != items[j].Text {
			return items[i].Text < items[j].Text
		}
		return items[i].DurationUS < items[j].DurationUS
	})
}

func diffOverlayParityItems(goItems, rustItems []overlayParityItem) []string {
	var diffs []string
	if len(goItems) != len(rustItems) {
		diffs = append(diffs, fmt.Sprintf("item count: go=%d rust=%d", len(goItems), len(rustItems)))
	}
	limit := len(goItems)
	if len(rustItems) < limit {
		limit = len(rustItems)
	}
	for i := 0; i < limit; i++ {
		goItem, rustItem := goItems[i], rustItems[i]
		if goItem.Kind != rustItem.Kind {
			diffs = append(diffs, fmt.Sprintf("item %d kind: go=%q rust=%q", i, goItem.Kind, rustItem.Kind))
		}
		if goItem.TemplateID != rustItem.TemplateID {
			diffs = append(diffs, fmt.Sprintf("item %d template: go=%q rust=%q", i, goItem.TemplateID, rustItem.TemplateID))
		}
		if goItem.Role != rustItem.Role {
			diffs = append(diffs, fmt.Sprintf("item %d role: go=%q rust=%q", i, goItem.Role, rustItem.Role))
		}
		if goItem.SceneID != rustItem.SceneID {
			diffs = append(diffs, fmt.Sprintf("item %d scene: go=%q rust=%q", i, goItem.SceneID, rustItem.SceneID))
		}
		if strings.TrimSpace(goItem.Text) != strings.TrimSpace(rustItem.Text) {
			diffs = append(diffs, fmt.Sprintf("item %d text: go=%q rust=%q", i, goItem.Text, rustItem.Text))
		}
		if goItem.StartUS != rustItem.StartUS || goItem.DurationUS != rustItem.DurationUS {
			diffs = append(diffs, fmt.Sprintf("item %d window: go=[%d,%d] rust=[%d,%d]",
				i, goItem.StartUS, goItem.StartUS+goItem.DurationUS,
				rustItem.StartUS, rustItem.StartUS+rustItem.DurationUS))
		}
	}
	for i := limit; i < len(goItems); i++ {
		diffs = append(diffs, fmt.Sprintf("item %d only in Go: %s", i, overlayParityItemString(goItems[i])))
	}
	for i := limit; i < len(rustItems); i++ {
		diffs = append(diffs, fmt.Sprintf("item %d only in Rust: %s", i, overlayParityItemString(rustItems[i])))
	}
	return diffs
}

func overlayParityItemString(item overlayParityItem) string {
	encoded, _ := json.Marshal(item)
	return string(encoded)
}

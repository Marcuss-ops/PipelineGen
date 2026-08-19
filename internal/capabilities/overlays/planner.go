package overlays

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// PlannerConfig contains the conservative editorial limits for one scene.
// The planner is deterministic: ties preserve the order supplied by the
// caller, while higher scores win within each category.
type PlannerConfig struct {
	MaxPhrases     int
	MaxKeywords    int
	MaxImages      int
	MaxPhraseWords int
	// Extended semantic entity limits (NUMBER / QUOTE / PRODUCT / LOGO).
	MaxNumbers  int
	MaxQuotes   int
	MaxProducts int
	MaxLogos    int
	// MaxOverlap caps how many content items may overlap at any single
	// moment; beyond it the planner drops the lowest-priority overlapping
	// items (see DegradeOverlaps). Default: DefaultOverlapBudget (3).
	MaxOverlap int
}

func (c PlannerConfig) withDefaults() PlannerConfig {
	if c.MaxPhrases <= 0 {
		c.MaxPhrases = 1
	}
	if c.MaxKeywords <= 0 {
		c.MaxKeywords = 3
	}
	if c.MaxImages <= 0 {
		c.MaxImages = 1
	}
	if c.MaxPhraseWords <= 0 {
		c.MaxPhraseWords = 8
	}
	if c.MaxNumbers <= 0 {
		c.MaxNumbers = 1
	}
	if c.MaxQuotes <= 0 {
		c.MaxQuotes = 1
	}
	if c.MaxProducts <= 0 {
		c.MaxProducts = 1
	}
	if c.MaxLogos <= 0 {
		c.MaxLogos = 1
	}
	if c.MaxOverlap <= 0 {
		c.MaxOverlap = DefaultOverlapBudget
	}
	return c
}

// TimedAnnotation is a semantic annotation already projected onto the final
// timeline. StartMs/EndMs must come from certified speech timing; the planner
// never estimates them from text length or scene duration. StartUS/DurationUS
// are the integer-microsecond canonical timing (authoritative); StartMs/EndMs
// are their millisecond projection.
type TimedAnnotation struct {
	Text       string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
}

type ImageCandidate struct {
	AssetID    string
	URL        string
	SHA256     string
	MediaType  string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
}

type SceneInput struct {
	ID       string
	Phrases  []TimedAnnotation
	Keywords []TimedAnnotation
	Images   []ImageCandidate
	// Extended semantic entity annotations: numbers (stat highlights),
	// quotes, product images and logos. They terminate in the same
	// canonical primitives as the base set (Text / Image).
	Numbers  []TimedAnnotation
	Quotes   []TimedAnnotation
	Products []ImageCandidate
	Logos    []ImageCandidate
}

// PlanInput is the minimal upstream projection required by the overlay
// planner. It deliberately does not import the script domain package.
type PlanInput struct {
	PlanID    string
	VideoID   string
	ProjectID string
	Width     int
	Height    int
	FPS       int
	// RendererVersion is intentionally optional. PipelineGen emits semantic
	// overlay instructions; RenderingGen owns the concrete renderer selection.
	// Callers should leave this empty unless the queue contract explicitly
	// requires a renderer capability/version constraint.
	RendererVersion string
	Scenes          []SceneInput
}

// BuildPlan selects bounded overlays from scene annotations. Candidates with
// invalid or missing certified timing are ignored, never given guessed timing.
// An image must carry an explicit window and a content identity because it is
// later materialized by RenderingGen from the asset manifest.
func BuildPlan(input PlanInput, config PlannerConfig) (OverlayPlan, error) {
	config = config.withDefaults()
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        input.PlanID, VideoID: input.VideoID, ProjectID: input.ProjectID,
		Width: input.Width, Height: input.Height, FPS: input.FPS,
		RendererVersion: input.RendererVersion,
	}
	// Items are appended in the canonical z-index order (bottom → top), so the
	// compiled layer order IS the stacking order — defined and deterministic,
	// never dependent on map/array iteration order:
	//
	//	images / products / logos  z=20
	//	numbers / quotes           z=50
	//	important words            z=80
	//	important phrases          z=100
	for _, scene := range input.Scenes {
		if strings.TrimSpace(scene.ID) == "" {
			return OverlayPlan{}, fmt.Errorf("overlay planner: scene id is required")
		}
		images := rankedImages(scene.Images)
		if len(images) > config.MaxImages {
			images = images[:config.MaxImages]
		}
		for _, image := range images {
			id := itemID(scene.ID, "image", image.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectImagePreset(input.PlanID, scene.ID, id),
				Kind: "image", TemplateID: "IMAGE_OVERLAY",
				StartMs: image.StartMs, EndMs: image.EndMs, StartUS: image.StartUS, DurationUS: image.DurationUS,
				AssetRefs: []OverlayAssetRef{{AssetID: image.AssetID, URL: image.URL, SHA256: image.SHA256, MediaType: image.MediaType}},
				Params:    map[string]any{"position": "right", "style": "popup", "priority": image.Score},
			})
		}

		products := rankedImages(scene.Products)
		if len(products) > config.MaxProducts {
			products = products[:config.MaxProducts]
		}
		for _, product := range products {
			plan.Items = append(plan.Items, OverlayItem{
				ID: itemID(scene.ID, "product", product.AssetID), SceneID: scene.ID,
				Kind: "product", TemplateID: "PRODUCT",
				StartMs: product.StartMs, EndMs: product.EndMs, StartUS: product.StartUS, DurationUS: product.DurationUS,
				AssetRefs: []OverlayAssetRef{{AssetID: product.AssetID, URL: product.URL, SHA256: product.SHA256, MediaType: product.MediaType}},
				Params:    map[string]any{"position": "right", "style": "popup", "priority": product.Score},
			})
		}

		logos := rankedImages(scene.Logos)
		if len(logos) > config.MaxLogos {
			logos = logos[:config.MaxLogos]
		}
		for _, logo := range logos {
			plan.Items = append(plan.Items, OverlayItem{
				ID: itemID(scene.ID, "logo", logo.AssetID), SceneID: scene.ID,
				Kind: "logo", TemplateID: "LOGO",
				StartMs: logo.StartMs, EndMs: logo.EndMs, StartUS: logo.StartUS, DurationUS: logo.DurationUS,
				AssetRefs: []OverlayAssetRef{{AssetID: logo.AssetID, URL: logo.URL, SHA256: logo.SHA256, MediaType: logo.MediaType}},
				Params:    map[string]any{"position": "corner", "style": "logo", "priority": logo.Score},
			})
		}

		numbers := rankedValid(scene.Numbers, 0)
		if len(numbers) > config.MaxNumbers {
			numbers = numbers[:config.MaxNumbers]
		}
		for _, number := range numbers {
			plan.Items = append(plan.Items, OverlayItem{
				ID: itemID(scene.ID, "number", number.Text), SceneID: scene.ID,
				Kind: "number", TemplateID: "NUMBER", Text: number.Text,
				StartMs: number.StartMs, EndMs: number.EndMs, StartUS: number.StartUS, DurationUS: number.DurationUS,
				Params: map[string]any{"position": "center", "style": "stat", "priority": number.Score},
			})
		}

		quotes := rankedValid(scene.Quotes, 0)
		if len(quotes) > config.MaxQuotes {
			quotes = quotes[:config.MaxQuotes]
		}
		for _, quote := range quotes {
			plan.Items = append(plan.Items, OverlayItem{
				ID: itemID(scene.ID, "quote", quote.Text), SceneID: scene.ID,
				Kind: "quote", TemplateID: "QUOTE", Text: quote.Text,
				StartMs: quote.StartMs, EndMs: quote.EndMs, StartUS: quote.StartUS, DurationUS: quote.DurationUS,
				Params: map[string]any{"position": "center", "style": "quote", "priority": quote.Score},
			})
		}

		keywords := rankedValid(scene.Keywords, 0)
		if len(keywords) > config.MaxKeywords {
			keywords = keywords[:config.MaxKeywords]
		}
		for _, candidate := range keywords {
			id := itemID(scene.ID, "keyword", candidate.Text)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectWordPreset(input.PlanID, scene.ID, id),
				Kind: "keyword", TemplateID: "IMPORTANT_WORD", Text: candidate.Text,
				StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
				Params: map[string]any{"position": "top", "style": "alert", "priority": candidate.Score},
			})
		}

		phrases := rankedValid(scene.Phrases, config.MaxPhraseWords)
		if len(phrases) > config.MaxPhrases {
			phrases = phrases[:config.MaxPhrases]
		}
		for _, candidate := range phrases {
			id := itemID(scene.ID, "phrase", candidate.Text)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectPhrasePreset(input.PlanID, scene.ID, id),
				Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: candidate.Text,
				StartMs: candidate.StartMs, EndMs: candidate.EndMs, StartUS: candidate.StartUS, DurationUS: candidate.DurationUS,
				Params: map[string]any{"position": "center", "style": "headline", "priority": candidate.Score},
			})
		}
	}
	// Degrade overlaps deterministically: no more than MaxOverlap content
	// items may pile up at any moment (lowest editorial priority drops first;
	// structural layers are never counted nor dropped).
	plan.Items = DegradeOverlaps(plan.Items, config.MaxOverlap)
	if err := plan.Validate(); err != nil {
		return OverlayPlan{}, err
	}
	return plan, nil
}

func rankedValid(in []TimedAnnotation, maxWords int) []TimedAnnotation {
	valid := make([]TimedAnnotation, 0, len(in))
	for _, candidate := range in {
		if strings.TrimSpace(candidate.Text) == "" || candidate.StartMs < 0 || candidate.EndMs <= candidate.StartMs {
			continue
		}
		if maxWords > 0 && len(strings.Fields(candidate.Text)) > maxWords {
			continue
		}
		candidate.Text = strings.TrimSpace(candidate.Text)
		valid = append(valid, candidate)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Score > valid[j].Score })
	// Dedupe identical text (case/whitespace-insensitive): the same spoken
	// phrase/word/number/quote must never become two overlay items — item
	// IDs derive from the text, so a duplicate would otherwise collide and
	// fail plan sealing. The first occurrence after the score sort wins, so
	// the highest-scoring candidate is the one kept.
	seen := make(map[string]struct{}, len(valid))
	out := make([]TimedAnnotation, 0, len(valid))
	for _, candidate := range valid {
		key := strings.ToLower(strings.Join(strings.Fields(candidate.Text), " "))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func rankedImages(in []ImageCandidate) []ImageCandidate {
	valid := make([]ImageCandidate, 0, len(in))
	for _, candidate := range in {
		if strings.TrimSpace(candidate.AssetID) == "" || candidate.StartMs < 0 || candidate.EndMs <= candidate.StartMs {
			continue
		}
		valid = append(valid, candidate)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Score > valid[j].Score })
	return valid
}

func itemID(sceneID, kind, value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(sceneID + "-" + kind + "-" + value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

package overlays

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
	// RunLevelEditorialBudget applies the production 5-image + 5-grounded-
	// phrase contract and drops other content overlay kinds. Structural
	// background layers are not represented as OverlayItems and are unaffected.
	RunLevelEditorialBudget bool
}

// AllCandidatesPlannerConfig is the production generation policy: every
// valid, uniquely identified image or phrase candidate received from the
// certified semantic surfaces is kept in the plan, subject to the run-level
// 5+5 budget. Other content overlay kinds are excluded. Timing validation,
// duplicate removal and the hard image-duration ceiling remain active. This is
// intentionally explicit instead of changing the conservative defaults used
// by the standalone planner/certification tests.
func AllCandidatesPlannerConfig(scenes []SceneInput) PlannerConfig {
	maxPerScene := func(count func(SceneInput) int) int {
		max := 0
		for _, scene := range scenes {
			if n := count(scene); n > max {
				max = n
			}
		}
		return max
	}
	maxPhraseWords := 0
	totalCandidates := 0
	for _, scene := range scenes {
		for _, phrase := range scene.Phrases {
			if n := len(strings.Fields(phrase.Text)); n > maxPhraseWords {
				maxPhraseWords = n
			}
		}
		totalCandidates += len(scene.Phrases) + len(scene.Keywords) + len(scene.Images) +
			len(scene.Numbers) + len(scene.Quotes) + len(scene.Products) + len(scene.Logos)
	}
	// A positive value is required to avoid withDefaults restoring a cap. The
	// extra slot makes the overlap budget strictly larger than every possible
	// planner-owned candidate in this input.
	if totalCandidates < 1 {
		totalCandidates = 1
	}
	if maxPhraseWords < 1 {
		maxPhraseWords = 1
	}
	return PlannerConfig{
		MaxPhrases:              maxPerScene(func(s SceneInput) int { return len(s.Phrases) }),
		MaxKeywords:             maxPerScene(func(s SceneInput) int { return len(s.Keywords) }),
		MaxImages:               maxPerScene(func(s SceneInput) int { return len(s.Images) }),
		MaxPhraseWords:          maxPhraseWords,
		MaxNumbers:              maxPerScene(func(s SceneInput) int { return len(s.Numbers) }),
		MaxQuotes:               maxPerScene(func(s SceneInput) int { return len(s.Quotes) }),
		MaxProducts:             maxPerScene(func(s SceneInput) int { return len(s.Products) }),
		MaxLogos:                maxPerScene(func(s SceneInput) int { return len(s.Logos) }),
		MaxOverlap:              totalCandidates + 1,
		RunLevelEditorialBudget: true,
	}
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
	LocalPath  string
	SHA256     string
	MediaType  string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
}

// MaxImageOverlayDurationMS is the hard editorial ceiling for every image,
// product and logo overlay.
const MaxImageOverlayDurationMS int64 = 5_000

func clampImageWindow(candidate ImageCandidate) ImageCandidate {
	if candidate.StartUS > 0 || candidate.DurationUS > 0 {
		if candidate.DurationUS > MaxImageOverlayDurationMS*1000 {
			candidate.DurationUS = MaxImageOverlayDurationMS * 1000
			candidate.EndMs = (candidate.StartUS + candidate.DurationUS + 999) / 1000
		}
		return candidate
	}
	if candidate.EndMs-candidate.StartMs > MaxImageOverlayDurationMS {
		candidate.EndMs = candidate.StartMs + MaxImageOverlayDurationMS
	}
	return candidate
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
	FPSNum    int
	FPSDen    int
	// RendererVersion is intentionally optional. PipelineGen emits semantic
	// overlay instructions; RenderingGen owns the concrete renderer selection.
	// Callers should leave this empty unless the queue contract explicitly
	// requires a renderer capability/version constraint.
	RendererVersion string
	// Background is copied verbatim into the sealed overlay plan when the
	// script/render payload explicitly requests one.
	Background *OverlayBackground
	// PhraseMotions optionally REPLACES the certified phrase-motion rotation
	// pool for this run (the animated IMPORTANT_PHRASE overlays). It carries a
	// channel profile's motion choice — PipelineGen resolves it at request
	// build; the planner only rotates within it, deterministically, exactly as
	// it does with the certified default pool. Empty keeps the default pool,
	// and an id outside CertifiedPhraseMotions() is a compile failure: the
	// rotation must never hand the renderer a motion it cannot run.
	PhraseMotions []string
	// PhraseMotionFamily optionally limits automatic phrase-motion selection
	// to a certified family and applies the one sampled motion to every phrase.
	PhraseMotionFamily string
	// ImageMotions optionally narrows the certified layer-only image motion pool.
	ImageMotions []string
	Scenes       []SceneInput
}

// BuildPlan selects bounded overlays from scene annotations. Candidates with
// invalid or missing certified timing are ignored, never given guessed timing.
// An image must carry an explicit window and a content identity because it is
// later materialized by RenderingGen from the asset manifest.
func BuildPlan(input PlanInput, config PlannerConfig) (OverlayPlan, error) {
	config = config.withDefaults()
	if err := validatePhraseMotionPool(input.PhraseMotions); err != nil {
		return OverlayPlan{}, err
	}
	if err := validateImageMotionPool(input.ImageMotions); err != nil {
		return OverlayPlan{}, err
	}
	if input.PhraseMotionFamily != "" {
		familyPool := certifiedPhraseFamily(input.PhraseMotionFamily)
		if len(familyPool) == 0 {
			return OverlayPlan{}, fmt.Errorf("overlay planner: phrase motion family %q has no certified motions", input.PhraseMotionFamily)
		}
		if len(input.PhraseMotions) > 0 {
			allowed := make(map[string]bool, len(familyPool))
			for _, id := range familyPool {
				allowed[id] = true
			}
			for _, id := range input.PhraseMotions {
				if !allowed[id] {
					return OverlayPlan{}, fmt.Errorf("overlay planner: phrase motion %q is outside family %q", id, input.PhraseMotionFamily)
				}
			}
			familyPool = input.PhraseMotions
		}
		input.PhraseMotions = familyPool
	}
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        input.PlanID, VideoID: input.VideoID, ProjectID: input.ProjectID,
		Width: input.Width, Height: input.Height, FPSNum: input.FPSNum, FPSDen: input.FPSDen,
		RendererVersion: input.RendererVersion,
		Background:      input.Background,
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
			image = clampImageWindow(image)
			id := itemID(scene.ID, "image", image.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectImagePreset(input.PlanID, scene.ID, id),
				Kind: "image", TemplateID: "IMAGE_OVERLAY",
				StartMs: image.StartMs, EndMs: image.EndMs, StartUS: image.StartUS, DurationUS: image.DurationUS,
				AssetRefs: []OverlayAssetRef{NewOverlayAssetRef(asset.New(image.AssetID, image.SHA256, image.MediaType, 0), image.URL, image.LocalPath)},
				Params:    map[string]any{"position": "right", "style": "popup", "priority": image.Score},
			})
		}

		products := rankedImages(scene.Products)
		if len(products) > config.MaxProducts {
			products = products[:config.MaxProducts]
		}
		for _, product := range products {
			product = clampImageWindow(product)
			id := itemID(scene.ID, "product", product.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID,
				Kind: "product", TemplateID: "PRODUCT",
				StartMs: product.StartMs, EndMs: product.EndMs, StartUS: product.StartUS, DurationUS: product.DurationUS,
				AssetRefs: []OverlayAssetRef{NewOverlayAssetRef(asset.New(product.AssetID, product.SHA256, product.MediaType, 0), product.URL, product.LocalPath)},
				Params:    map[string]any{"position": "right", "style": "popup", "priority": product.Score},
			})
		}

		logos := rankedImages(scene.Logos)
		if len(logos) > config.MaxLogos {
			logos = logos[:config.MaxLogos]
		}
		for _, logo := range logos {
			logo = clampImageWindow(logo)
			id := itemID(scene.ID, "logo", logo.AssetID)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID,
				Kind: "logo", TemplateID: "LOGO",
				StartMs: logo.StartMs, EndMs: logo.EndMs, StartUS: logo.StartUS, DurationUS: logo.DurationUS,
				AssetRefs: []OverlayAssetRef{NewOverlayAssetRef(asset.New(logo.AssetID, logo.SHA256, logo.MediaType, 0), logo.URL, logo.LocalPath)},
				Params:    map[string]any{"position": "corner", "style": "logo", "priority": logo.Score},
			})
		}

		numbers := rankedValid(scene.Numbers, 0)
		if len(numbers) > config.MaxNumbers {
			numbers = numbers[:config.MaxNumbers]
		}
		for _, number := range numbers {
			id := itemID(scene.ID, "number", number.Text)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectWordPreset(input.PlanID, scene.ID, id),
				MotionID: SelectTextMotion(input.PlanID, scene.ID, id),
				Kind:     "number", TemplateID: "NUMBER", Text: number.Text,
				StartMs: number.StartMs, EndMs: number.EndMs, StartUS: number.StartUS, DurationUS: number.DurationUS,
				Params: map[string]any{"position": "center", "style": "stat", "priority": number.Score},
			})
		}

		quotes := rankedValid(scene.Quotes, 0)
		if len(quotes) > config.MaxQuotes {
			quotes = quotes[:config.MaxQuotes]
		}
		for _, quote := range quotes {
			id := itemID(scene.ID, "quote", quote.Text)
			plan.Items = append(plan.Items, OverlayItem{
				ID: id, SceneID: scene.ID, PresetID: selectPhrasePreset(input.PlanID, scene.ID, id),
				MotionID: SelectTextMotion(input.PlanID, scene.ID, id),
				Kind:     "quote", TemplateID: "QUOTE", Text: quote.Text,
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
				MotionID: SelectTextMotion(input.PlanID, scene.ID, id),
				Kind:     "keyword", TemplateID: "IMPORTANT_WORD", Text: candidate.Text,
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
				MotionParams: phraseMotionParams(candidate, input.FPSNum, input.FPSDen),
				Params:       map[string]any{"position": "center", "style": "headline", "priority": candidate.Score},
			})
		}
	}
	// Degrade overlaps deterministically: no more than MaxOverlap content
	// items may pile up at any moment (lowest editorial priority drops first;
	// structural layers are never counted nor dropped).
	plan.Items = DegradeOverlaps(plan.Items, config.MaxOverlap)
	if config.RunLevelEditorialBudget {
		plan.Items, _ = ApplyEditorialOverlayBudget(plan.Items)
	} else {
		plan.Items, _ = ApplyPhraseOverlayBudget(plan.Items)
	}
	// Assign the phrase motion sequence AFTER run-level ranking and dedupe.
	// Every admitted phrase gets a distinct, visible catalog motion, even when
	// phrases span different scenes or the winning candidates were not the
	// first annotations supplied by NLP.
	phraseOrdinal := 0
	imageOrdinal := 0
	for i := range plan.Items {
		switch plan.Items[i].Kind {
		case "text_phrase":
			plan.Items[i].MotionID = selectPhraseMotion(input.PlanID, "run", phraseOrdinal, input.PhraseMotions)
			phraseOrdinal++
		case "image", "entity_image", "product", "logo":
			plan.Items[i].MotionID = selectImageMotion(input.PlanID, "run", imageOrdinal, input.ImageMotions)
			imageOrdinal++
		}
	}
	if err := plan.Validate(); err != nil {
		return OverlayPlan{}, err
	}
	return plan, nil
}

// phraseMotionParams gives the entrance half of the phrase's on-screen
// duration. Motion catalog windows are frame counts, so convert that duration
// at the output frame rate before sending the plan.
func phraseMotionParams(phrase TimedAnnotation, fpsNum, fpsDen int) map[string]any {
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil
	}
	phraseDurationUS := phrase.DurationUS
	if phraseDurationUS <= 0 && phrase.EndMs > phrase.StartMs {
		phraseDurationUS = (phrase.EndMs - phrase.StartMs) * 1_000
	}
	entranceDurationUS := phraseDurationUS / 2
	framesNumerator := entranceDurationUS * int64(fpsNum)
	framesDenominator := 1_000_000 * int64(fpsDen)
	frames := (framesNumerator + framesDenominator - 1) / framesDenominator
	if frames < 1 {
		frames = 1
	}
	return map[string]any{"enter_frames": int(frames)}
}

// validatePhraseMotionPool fails closed on a caller-supplied motion pool that
// names an id this build cannot render, or that would make the rotation
// ambiguous (duplicates). An empty pool is the certified default and is
// always valid.
func validatePhraseMotionPool(pool []string) error {
	if len(pool) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range CertifiedPhraseMotions() {
		certified[id] = true
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("overlay: phrase motion pool carries an empty id")
		}
		if !certified[id] {
			return fmt.Errorf("overlay: phrase motion %q is not a certified motion", id)
		}
		if seen[id] {
			return fmt.Errorf("overlay: phrase motion pool repeats %q", id)
		}
		seen[id] = true
	}
	return nil
}

func validateImageMotionPool(pool []string) error {
	if len(pool) == 0 {
		return nil
	}
	certified := make(map[string]bool)
	for _, id := range CertifiedImageMotions() {
		certified[id] = true
	}
	seen := make(map[string]bool, len(pool))
	for _, id := range pool {
		if !certified[id] {
			return fmt.Errorf("overlay planner: image motion %q is not in the certified render-safe pool", id)
		}
		if seen[id] {
			return fmt.Errorf("overlay planner: image motion pool repeats %q", id)
		}
		seen[id] = true
	}
	return nil
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

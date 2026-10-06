package overlays

import "strings"

// EditorialSectionID is an editor-facing grouping, not a renderer kind. A
// section is derived from an OverlayItem and is never serialized onto the
// rendering contract, so editorial organization cannot fork the wire schema.
type EditorialSectionID string

const (
	EditorialSectionEntityTextImage EditorialSectionID = "entity_text_image"
	EditorialSectionSingleImage     EditorialSectionID = "single_image"
	EditorialSectionImageStack      EditorialSectionID = "image_stack"
	EditorialSectionImportantPhrase EditorialSectionID = "important_phrase"
	EditorialSectionShortPhrase     EditorialSectionID = "short_important_phrase"
	EditorialSectionNumbersAndDates EditorialSectionID = "numbers_and_dates"
	EditorialSectionMap             EditorialSectionID = "map"
	EditorialSectionOther           EditorialSectionID = "other"
)

// EditorialSection describes one stable editor navigation section.
type EditorialSection struct {
	ID    EditorialSectionID `json:"id"`
	Title string             `json:"title"`
	Order int                `json:"order"`
}

var editorialSectionCatalog = [...]EditorialSection{
	{ID: EditorialSectionEntityTextImage, Title: "Entità con testo · 1 immagine", Order: 10},
	{ID: EditorialSectionSingleImage, Title: "Immagini singole", Order: 20},
	{ID: EditorialSectionImageStack, Title: "Immagini x2, x3, x4, x5…", Order: 30},
	{ID: EditorialSectionImportantPhrase, Title: "Frasi importanti", Order: 40},
	{ID: EditorialSectionShortPhrase, Title: "Frasi importanti brevi", Order: 50},
	{ID: EditorialSectionNumbersAndDates, Title: "Numeri & date", Order: 60},
	{ID: EditorialSectionMap, Title: "Mappe", Order: 70},
	{ID: EditorialSectionOther, Title: "Altri elementi", Order: 80},
}

// EditorialSections returns the canonical editor ordering. The returned slice
// is a copy; callers cannot mutate the shared catalog.
func EditorialSections() []EditorialSection {
	return append([]EditorialSection(nil), editorialSectionCatalog[:]...)
}

// EditorialPlanSection is one section of an organized plan. Items retain their
// original plan order and are not copied into multiple sections.
type EditorialPlanSection struct {
	EditorialSection
	Items []OverlayItem `json:"items"`
}

// EditorialSectionForItem classifies an item using the existing kind,
// template, caption and layer fields. It is the single owner of the mapping
// from render-plan items to editor sections. Frases brevi have 1–5 words,
// matching BuildPlan's existing short-phrase motion threshold.
func EditorialSectionForItem(item OverlayItem) EditorialSectionID {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	template := strings.ToUpper(strings.TrimSpace(item.TemplateID))

	if kind == string(KindMap) || template == "MAP" {
		return EditorialSectionMap
	}
	if len(item.ImageLayers) > 1 || len(item.AssetRefs) > 1 {
		return EditorialSectionImageStack
	}
	if len(item.ImageLayers) == 1 {
		return EditorialSectionSingleImage
	}
	if len(item.AssetRefs) == 1 {
		isEntity := kind == string(KindEntityImage) || kind == string(KindEntityCard) ||
			kind == string(KindOrganization) || kind == string(KindLocation) || kind == string(KindConcept)
		if isEntity && (strings.TrimSpace(item.EntityCaption) != "" || strings.TrimSpace(item.Text) != "") {
			return EditorialSectionEntityTextImage
		}
	}
	if template == "IMPORTANT_PHRASE" || kind == "text_phrase" || kind == string(KindImportantPhrase) {
		if words := len(strings.Fields(item.Text)); words > 0 && words < 6 {
			return EditorialSectionShortPhrase
		}
		return EditorialSectionImportantPhrase
	}
	switch template {
	case "NUMBER", "MONEY", "PERCENT", "PERCENTAGE", "DATE", "TIME", "METRIC_STAT_CARD", "TIMELINE_DATE_CARD":
		return EditorialSectionNumbersAndDates
	}
	switch kind {
	case "number", "metric_stat", "timeline_date", "date", "time":
		return EditorialSectionNumbersAndDates
	case "image", string(KindEntityImage), string(KindImagePopup), string(KindProduct), string(KindLogo):
		return EditorialSectionSingleImage
	}
	switch template {
	case "IMAGE_OVERLAY", "IMAGE_ENTITY", "IMAGE_POPUP", "PRODUCT", "LOGO":
		return EditorialSectionSingleImage
	}
	return EditorialSectionOther
}

// OrganizeEditorialPlan presents every plan item in exactly one editor
// section. Empty sections are retained so the editor has a stable navigation
// structure as content appears and disappears between plans.
func OrganizeEditorialPlan(plan OverlayPlan) []EditorialPlanSection {
	sections := EditorialSections()
	byID := make(map[EditorialSectionID]int, len(sections))
	organized := make([]EditorialPlanSection, len(sections))
	for i, section := range sections {
		organized[i].EditorialSection = section
		byID[section.ID] = i
	}
	for _, item := range plan.Items {
		sectionIndex, ok := byID[EditorialSectionForItem(item)]
		if !ok {
			sectionIndex = byID[EditorialSectionOther]
		}
		organized[sectionIndex].Items = append(organized[sectionIndex].Items, item)
	}
	return organized
}

// contentPriorityForItem keeps overlap degradation aligned with the editor
// classification while retaining the established priority for other known
// content kinds. The section is derived (not stored as a second item field).
func contentPriorityForItem(item OverlayItem) int {
	switch EditorialSectionForItem(item) {
	case EditorialSectionEntityTextImage, EditorialSectionSingleImage, EditorialSectionImageStack, EditorialSectionMap:
		return PriorityImage
	case EditorialSectionImportantPhrase, EditorialSectionShortPhrase:
		return PriorityPhrase
	case EditorialSectionNumbersAndDates:
		return PriorityWord
	}
	if priority := contentPriorityForTemplate(item.TemplateID); priority != PriorityStructural {
		return priority
	}
	switch strings.ToLower(strings.TrimSpace(item.Kind)) {
	case string(KindEntityCard), string(KindOrganization), string(KindLocation), string(KindConcept), string(KindLowerThird), string(KindQuote), string(KindImportantWord), string(KindNumber):
		return PriorityWord
	case string(KindProduct), string(KindLogo), string(KindImagePopup), string(KindEntityImage):
		return PriorityImage
	case "brand_text":
		return PriorityWord
	default:
		return PriorityStructural
	}
}

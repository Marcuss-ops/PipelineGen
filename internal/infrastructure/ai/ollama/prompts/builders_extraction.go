package prompts

import (
	"fmt"
	"strings"
)

// BuildEntityExtractionBatchPrompt keeps the per-scene contract intact while
// allowing one bounded model call to serve several scenes. The explicit start
// and end markers make scene association deterministic for small models.
func BuildEntityExtractionBatchPrompt(segments []string, entityCount int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Extract metadata independently for each numbered documentary segment.
Never combine information between segments. Return one block per segment using
exactly these markers and the five labeled sections:

### SEGMENT_INDEX: N
## frasi_importanti
## entity_senza_testo
## nomi_speciali
## parole_importanti
## artlist_phrases
### END_SEGMENT

Rules: extract at most %d named entities per segment, use only evidence in that
segment. Add bullet items only when supported by the segment; never copy
section labels, examples, placeholders, or instructions into the output. Do
not output JSON, markdown fences, commentary, or missing segment blocks.
`, entityCount)
	for i, segment := range segments {
		fmt.Fprintf(&b, "\nSEGMENT_INPUT_%d:\n%s\n", i, segment)
	}
	return b.String()
}

// BuildEntityExtractionPrompt builds the canonical per-segment extraction prompt.
func BuildEntityExtractionPrompt(text string, entityCount int) string {
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderEntityExtraction(text, entityCount)
		if err == nil {
			return rendered
		}
	}
	return buildEntityExtractionFallback(text, entityCount)
}

func buildEntityExtractionFallback(text string, entityCount int) string {
	return fmt.Sprintf(`You are extracting structured metadata from ONE documentary script segment for a visual production pipeline.

DO NOT output JSON, markdown fences, code blocks, scene IDs, indexes, or bindings.
Output EXACTLY five labeled sections. Every item must start with "- ".

## frasi_importanti
- [evocative verbatim fragment from the segment]

## entity_senza_testo
- VisualSubject: precise visual search description

## nomi_speciali
- PERSON: [full person name]
- PLACE: [specific location]
- ORGANIZATION: [specific organization]

## parole_importanti
- [specific concrete keyword]

## artlist_phrases
- [short visual concept phrase]

ENTITY RULES:
1. Extract at most %d real named entities in nomi_speciali.
2. Every named entity MUST use "TYPE: Value".
3. Allowed TYPE values: PERSON, PLACE, ORGANIZATION, EVENT, WORK, PRODUCT, OTHER.
4. Never type generic nouns, adjectives, verbs, titles, or sentence fragments as entities.
5. Omit uncertain names instead of guessing.
6. Extract at most %d concrete, segment-specific keywords in parole_importanti.

VISUAL SEARCH RULES:
1. entity_senza_testo uses "Subject: Description" and must describe a filmable visual.
2. artlist_phrases must contain EXACTLY 3 to 5 natural visual concepts, ideally 2-4 words each.
3. Every phrase must be concrete, camera-recordable, specific to this segment, and useful in Artlist search.
4. Reject adjacent word pairs copied mechanically from the text, dangling articles, isolated dates, apostrophe fragments, generic abstractions, and verb-only sequences.

GOOD: "Vesuvio erupting", "Roman ruins excavation", "Ancient city streets".
BAD: "pompei prosperò", "sua posizione", "79 d".

Output ONLY the five labeled sections.

TEXT:
"%s"`, entityCount, entityCount, text)
}

// BuildTimelineAssetRoutingPrompt asks the model to choose the best asset source for a timeline segment.
func BuildTimelineAssetRoutingPrompt(topic, openingSentence, closingSentence string, keywords, entities []string, stockFoldersBlock, artlistFoldersBlock string) string {
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderTimelineRouting(
			topic, openingSentence, closingSentence,
			keywords, entities,
			stockFoldersBlock, artlistFoldersBlock,
		)
		if err == nil {
			return rendered
		}
	}
	return buildTimelineRoutingFallback(topic, openingSentence, closingSentence, keywords, entities, stockFoldersBlock, artlistFoldersBlock)
}

func buildTimelineRoutingFallback(topic, openingSentence, closingSentence string, keywords, entities []string, stockFoldersBlock, artlistFoldersBlock string) string {
	return fmt.Sprintf(`You are choosing the best asset source for one documentary timeline segment.

Pick exactly one source:
- "stock_drive" only when a stock folder directly matches the segment topic
- "artlist_folder" only when an Artlist folder directly matches the segment topic
- "none" when no folder is relevant

Keep the smallest possible number of timeline blocks. Add a timestamp only for a clear argument, scene, subject, location, or time shift.
Prefer "none" over an unrelated folder. Do not invent folders.

Return only valid JSON:
{"source":"stock_drive","folder":"Exact folder name from the list","reason":"why this folder directly matches the segment topic"}

TOPIC: %s
OPENING: %s
CLOSING: %s
KEYWORDS: %s
ENTITIES: %s
AVAILABLE STOCK FOLDERS:
%s

AVAILABLE ARTLIST FOLDERS:
%s

JSON:`,
		topic,
		openingSentence,
		closingSentence,
		strings.Join(keywords, ", "),
		strings.Join(entities, ", "),
		stockFoldersBlock,
		artlistFoldersBlock,
	)
}

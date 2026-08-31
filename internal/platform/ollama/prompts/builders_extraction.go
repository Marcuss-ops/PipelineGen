package prompts

import (
	"fmt"
	"strings"
)

// BuildEntityExtractionBatchPrompt keeps the per-scene contract intact while
// allowing one bounded model call to serve several scenes. The explicit start
// and end markers make scene association deterministic for small models.
func BuildEntityExtractionBatchPrompt(segments []string, entityCount int) string {
	return BuildEntityExtractionBatchPromptForLanguage(segments, entityCount, "")
}

// BuildEntityExtractionBatchPromptForLanguage builds the canonical batch
// prompt with the source language explicitly declared for every segment.
func BuildEntityExtractionBatchPromptForLanguage(segments []string, entityCount int, language string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Extract metadata independently for each numbered documentary segment.
Never combine information between segments. Return one block per segment using
exactly these markers and the six labeled sections:

### SEGMENT_INDEX: N
## frasi_importanti
## entity_senza_testo
## nomi_speciali
## parole_importanti
## artlist_phrases
## noun_chunks
### END_SEGMENT

Rules: extract at most %d named entities per segment, use only evidence in that
segment. Add bullet items only when supported by the segment; never copy
section labels, examples, placeholders, or instructions into the output. Do
not output JSON, markdown fences, commentary, or missing segment blocks.
`, entityCount)
	for i, segment := range segments {
		fmt.Fprintf(&b, "\nSEGMENT_INPUT_%d:\n%s\n", i, segment)
	}
	b.WriteString(GroundedNounChunkContract(language))
	return b.String()
}

// BuildEntityExtractionPrompt builds the canonical per-segment extraction prompt.
func BuildEntityExtractionPrompt(text string, entityCount int) string {
	return BuildEntityExtractionPromptForLanguage(text, entityCount, "")
}

// BuildEntityExtractionPromptForLanguage builds the single-segment prompt
// using the same grounded noun-chunk contract as the batch path.
func BuildEntityExtractionPromptForLanguage(text string, entityCount int, language string) string {
	if cfg := Get(); cfg != nil {
		rendered, err := cfg.RenderEntityExtraction(text, entityCount)
		if err == nil {
			return rendered + GroundedNounChunkContract(language)
		}
	}
	return buildEntityExtractionFallback(text, entityCount) + GroundedNounChunkContract(language)
}

// GroundedNounChunkContract is the single source of truth for the
// noun_chunks field. Both single-segment and batch prompts append this exact
// contract; format-specific instructions remain owned by their callers.
func GroundedNounChunkContract(language string) string {
	if strings.TrimSpace(language) == "" {
		language = "infer from the source text; never translate it"
	}
	return fmt.Sprintf(`

NOUN_CHUNK GROUNDING CONTRACT (MANDATORY FOR EVERY LANGUAGE):
- SOURCE_LANGUAGE: %s
- Every value in noun_chunks MUST be copied VERBATIM from the corresponding source segment.
- Do not translate, paraphrase, lowercase, lemmatize, normalize, correct, or rewrite any value.
- Preserve the source language, spelling, accents, diacritics, apostrophes, particles, articles when part of the source span, adjectives, modifiers, possessives, inflections, and grammatical case.
- The returned value must be an exact contiguous source span; if a candidate cannot be copied exactly, omit it.
- Emit one noun expression per array item. Do not concatenate separate referents or append surrounding clauses.
- Prefer the complete visually meaningful noun expression explicitly present in the source. Do not shorten away explicit visual adjectives or modifiers merely to make a smaller phrase.
- Scan the entire source from left to right before deciding. Include every distinct explicit visual referent, including coordinated objects separated by commas or conjunctions; never stop after the first usable noun phrase.
- When a visual referent has an explicit adjective, participle, possessive, location, or other modifier, preserve that complete contiguous expression instead of returning only its head noun.
- For Japanese, Chinese, Korean, and any language without reliable whitespace boundaries, identify the complete visual expression from the original characters and copy that exact character span. Do not segment it using English or Western whitespace assumptions.
- For languages without reliable whitespace boundaries, stop the copied span at the end of the noun expression, before any predicate, auxiliary, or verbal clause; never return the whole sentence as one noun chunk.
- It is better to omit a doubtful candidate than to invent, translate, or enrich it. Do not omit a clearly explicit visual noun expression only because it is morphologically inflected.
- Never include verbs, verbal clauses, adverbs, narrative commentary, or details that are not literally present in the source.
`, language)
}

func buildEntityExtractionFallback(text string, entityCount int) string {
	return fmt.Sprintf(`You are extracting structured metadata from ONE documentary script segment for a visual production pipeline.

DO NOT output JSON, markdown fences, code blocks, scene IDs, indexes, or bindings.
Output EXACTLY six labeled sections. Every item must start with "- ".

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

## noun_chunks
- [verbatim grounded noun phrase copied from the segment]

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

Output ONLY the six labeled sections.

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

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
Important phrases are editorial fragments from the source, not entity names:
do not return a person, place, organization, or partial name as a phrase; use
the meaningful action, claim, event, or description around it instead.
	`, entityCount)
	b.WriteString(ImportantPhraseQualityContract())
	b.WriteString(NamedEntityLanguageContract(language))
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
			return rendered + ImportantPhraseQualityContract() + NamedEntityLanguageContract(language) + GroundedNounChunkContract(language)
		}
	}
	return buildEntityExtractionFallback(text, entityCount) + ImportantPhraseQualityContract() + NamedEntityLanguageContract(language) + GroundedNounChunkContract(language)
}

// NamedEntityLanguageContract keeps multilingual entity extraction anchored to
// the translated text's surface forms. In particular, it prevents a model from
// treating a capitalized phrase or a case-inflected location as a person name.
func NamedEntityLanguageContract(language string) string {
	if strings.TrimSpace(language) == "" {
		language = "infer from the source text"
	}
	return fmt.Sprintf(`

NAMED ENTITY CONTRACT (MANDATORY FOR EVERY LANGUAGE):
- SOURCE_LANGUAGE: %s
- Scan the entire source segment for explicit people, places, organizations, events, works, and products.
- Copy each entity's exact contiguous surface form from the source text, including its script, spelling, accents, and grammatical inflection. Never translate an entity or substitute its English name.
- Classify a person as PERSON only when the span names a person. Classify a city, region, or country as PLACE, even when the localized name has a grammatical case ending.
- Do not emit sentence fragments, titles, roles, sentence-initial words, or descriptive phrases as named entities.
- Omit uncertain entities rather than assigning a guessed name or type.
`, language)
}

// ImportantPhraseQualityContract keeps phrase extraction useful for editorial
// overlays across both the built-in and configured extraction prompts. Phrase
// candidates remain verbatim and grounded; the model ranks their editorial
// usefulness but never invents or rewrites them.
func ImportantPhraseQualityContract() string {
	return `

IMPORTANT PHRASE QUALITY CONTRACT:
- Return up to three candidates, strongest and most screen-worthy first.
- Prefer a short, self-contained mini-clause (usually 4–9 words) with a clear subject and action or result. It must make sense by itself on screen and identify a concrete, distinctive beat from this segment.
- Prefer a named subject, concrete noun, or explicit number plus an active verb. Keep the key outcome when it is stated in the source.
- Reject incomplete noun phrases, dangling gerunds, adjective/adverb fragments, generic abstractions, filler, and weak wording such as "significantly shaped", "superstardom was significantly shaped", "role in shaping", "heavyweight title", or "securing a world heavyweight".
- Reject generic transitions, topic labels, names alone, pronouns without an antecedent, and sentence scraps that do not make sense on screen.
- Copy every candidate as an exact contiguous span from the source. Never invent a slogan, paraphrase, or add words.
`
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

IMPORTANT PHRASE RULES:
1. Extract short, meaningful source fragments, not names or partial names.
2. A person/place/organization name belongs in nomi_speciali, never in frasi_importanti.
3. Prefer the action, claim, event, or description that makes the segment important.

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

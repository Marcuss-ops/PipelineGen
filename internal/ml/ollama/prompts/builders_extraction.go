package prompts

import (
	"fmt"
	"strings"
)

// BuildEntityExtractionPrompt builds the prompt for entity extraction.
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
	return fmt.Sprintf(`You are extracting structured metadata from a documentary script fragment for a visual production pipeline.

Return ONLY valid JSON with exactly this shape:
{
  "frasi_importanti": ["..."],
  "entity_senza_testo": {"VisualSubject": "Description of search term"},
  "nomi_speciali": ["..."],
  "parole_importanti": ["..."],
  "artlist_phrases": ["visual phrase 1", "visual phrase 2"]
}

RULES FOR VISUAL ENTITIES:
1. "nomi_speciali": Extract a MAXIMUM of %d specific names of people, places, organizations, works, or clearly identifiable unique things (e.g., "Vesuvio", "San Marzano", "Pompei").
   - STRICT LIMIT: Do NOT extract more than %d names under any circumstance. Only take the most important ones.
   - ONLY include real named entities, not descriptions, scene labels, titles, dialogue, sentence fragments, or discourse markers.
   - AVOID abstract or generic nouns, common nouns, adjectives, verbs, and category labels.
   - AVOID ambiguous common words unless qualified (e.g., AVOID "Campana" alone, PREFER "Mozzarella di bufala campana").
   - PREFER concrete entities that have a dedicated Wikipedia page.
   - Do not include the topic itself unless it is a real named entity that needs a visual reference.
2. "parole_importanti": Extract a MAXIMUM of %d key technical terms or specific ingredients (e.g., "mozzarella di bufala", "forno a legna").
3. "frasi_importanti": Extract up to 5 most evocative verbatim sentences or sentence fragments that appear in the TEXT.
4. "entity_senza_testo": Map identifiable subjects to a short descriptive search query.
5. "artlist_phrases": Extract EXACTLY 3 to 5 SHORT VISUAL CONCEPT PHRASES (2-3 words each) suitable for stock footage search.

CRITICAL — What artlist_phrases ARE:
  ✅ Concrete, filmable visual concepts: "Roman forum ruins", "Lava flowing down mountain", "Archaeological dig site"
  ✅ Noun phrases that describe a thing, place, action, or scene a CAMERA can record
  ✅ Paraphrased concepts — you can rephrase the text to describe what would appear on screen
  ✅ Topic-specific: "Pompeii volcanic eruption" not generic "volcanic eruption"

CRITICAL — What artlist_phrases are NOT:
  ❌ NOT sequential word-pairs from the text: "pompei prosperò", "secoli diventando", "città più", "sua posizione"
  ❌ NOT sliding-window bigrams: do NOT take word 1+2 from the text, then word 2+3, then word 3+4
  ❌ NOT sentence fragments or verb phrases: "l eruzione", "vulcano distrusse", "alimentavano contribuì"
  ❌ NOT numbered dates: "79 d", "1748"
  ❌ NOT phrases with apostrophe fragments: "l'eruzione", "dell'epoca"
  ❌ NOT phrases starting with function words: "sua", "questo", "della", "degli"
  ❌ NOT single-letter abbreviations: "d" in "79 d.C."
  ❌ NOT verb-only sequences: "prosperò diventando", "lasciando un"

SELF-CHECK — Before writing each phrase, verify:
  1. Is this a VISUAL concept that a camera operator could film? → If NO, discard.
  2. Is this phrase just 2 adjacent words copied from the text? → If YES, discard and find a real visual concept.
  3. Would searching Artlist for this phrase return relevant stock footage? → If NO, discard.
  4. Is this phrase SPECIFIC to THIS script topic? → If it could match any random video, make it more specific.

GOOD examples for a script about Pompeii:
  ✅ "Vesuvio erupting" — visual, topic-specific, filmable
  ✅ "Roman ruins excavation" — visual, concrete
  ✅ "Ancient city streets" — visual, specific to Pompeii
  ✅ "Volcanic ash layer" — visual, topic-specific

BAD examples for the SAME script:
  ❌ "pompei prosperò" — NOT visual, just sequential text words
  ❌ "sua posizione" — NOT visual, starts with function word
  ❌ "alimentavano contribuì" — NOT visual, two verbs, nonsense
  ❌ "79 d" — NOT visual, truncated date
  ❌ "città lasciando" — NOT visual, verb form

STRICT CONSTRAINTS:
- Output EXACTLY 3 to 5 artlist_phrases. Never more than 5.
- Each phrase MUST pass ALL 4 self-check questions above.
- Return ONLY the JSON object.

TEXT:
"%s"

JSON:`, entityCount, entityCount, entityCount, text)
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
- "stock_drive" (only if a stock folder name clearly and directly matches the segment topic)
- "artlist_folder" (only if an Artlist folder name clearly and directly matches the segment topic)
- "none" (if NO folder is relevant to the segment - this is the correct choice when folders don't match)

TIMESTAMP POLICY:
- Keep the smallest possible number of timeline blocks.
- Add a new timestamp only when the segment introduces a clearly different argument, scene, subject, location, or time shift.
- If the content stays on the same subject, keep using the current block.

STRICT RULES:
- ONLY pick a folder if its name is DIRECTLY about the same topic as the segment.
- "none" is PREFERRED over a poor or unrelated match.
- Do not invent new folders.

Return only valid JSON with this exact shape:
  {"source":"stock_drive","folder":"Exact folder name from the list","reason":"why this folder directly matches the segment topic"}

CONTEXT:
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

package prompts

import (
	"fmt"
	"strings"
	"velox/go-master/internal/ml/ollama/types"
)

// BuildChatMessages builds the message list for the chat API.
func BuildChatMessages(req *types.TextGenerationRequest) []types.Message {
	durationMinutes := req.Duration / 60
	if durationMinutes == 0 {
		durationMinutes = 1
	}
	targetWords := (req.Duration * types.WordsPerMinute) / 60

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	return []types.Message{
		{Role: "system", Content: BuildSystemPrompt(req.Language, req.Tone)},
		{Role: "user", Content: fmt.Sprintf(`TASK: Write a true NARRATIVE DOCUMENTARY of %d seconds (about %d minutes).

VIDEO TITLE: %s
LANGUAGE: %s
NARRATIVE STYLE: %s

REFERENCE INPUT / INSTRUCTIONS:
"%s"

STRICT QUALITY REQUIREMENTS (FAILURE IS NOT AN OPTION):
1. LENGTH: This video lasts %d minutes. You MUST write at least %d words.
2. STYLE: Cinematic and immersive.
3. FORMAT: Write as straight continuous prose only.
4. NO META-TEXT: Write ONLY the spoken script.
5. NO TIMESTAMPS: Do not include ANY time markers like [0:00], (0:15), [INIZIO], or ranges.
6. NO SPEAKER LABELS: Do NOT write "Narrator:", "Narratore:", "Voice:", "Voce:", or any other label. Start directly with the story.
7. NO STAGE DIRECTIONS: Do not include descriptions of shots, music, or tone in brackets.

SCRIPT:`, req.Duration, durationMinutes, sanitizedTitle, req.Language, req.Tone, sanitizedSource, durationMinutes, targetWords)},
	}
}

// BuildRegenerationChatMessages builds the message list for script regeneration via chat API.
func BuildRegenerationChatMessages(req *types.RegenerationRequest) []types.Message {
	sanitizedScript := types.SanitizeInput(req.OriginalScript)
	sanitizedTitle := types.SanitizeInput(req.Title)

	return []types.Message{
		{Role: "system", Content: BuildSystemPrompt(req.Language, req.Tone)},
		{Role: "user", Content: fmt.Sprintf(`Rewrite the following documentary script in a cleaner, more compelling form.

VIDEO TITLE: %s
LANGUAGE: %s
NARRATIVE STYLE: %s

SCRIPT TO REWRITE:
"%s"

STRICT RULES:
1. Return ONLY the rewritten spoken script.
2. Keep it as straight continuous prose.
3. Do not add timestamps, headings, labels, or stage directions.
4. Preserve the original subject and factual content unless the rewrite improves clarity or flow.

SCRIPT:`, sanitizedTitle, req.Language, req.Tone, sanitizedScript)},
	}
}

// BuildTextPrompt builds the prompt for text generation.
func BuildTextPrompt(req *types.TextGenerationRequest) string {
	durationMinutes := req.Duration / 60
	if durationMinutes == 0 {
		durationMinutes = 1
	}
	targetWords := (req.Duration * types.WordsPerMinute) / 60

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	return fmt.Sprintf(`%s

TASK: Write a true NARRATIVE DOCUMENTARY of %d seconds (about %d minutes).

VIDEO TITLE: %s
LANGUAGE: %s
NARRATIVE STYLE: %s

REFERENCE INPUT / INSTRUCTIONS:
"%s"

STRICT QUALITY REQUIREMENTS (FAILURE IS NOT AN OPTION):
1. LENGTH: This video lasts %d minutes. You MUST write at least %d words.
2. STYLE: Cinematic and immersive.
3. FORMAT: Write as straight continuous prose only.
4. NO META-TEXT: Write ONLY the spoken script. 
5. NO TIMESTAMPS: Do not include ANY time markers like [0:00], (0:15), [INIZIO], or ranges.
6. NO SPEAKER LABELS: Do NOT write "Narrator:", "Narratore:", "Voice:", "Voce:", or any other label. Start directly with the story.
7. NO STAGE DIRECTIONS: Do not include descriptions of shots, music, or tone in brackets.

SCRIPT:`,
		BuildSystemPrompt(req.Language, req.Tone),
		req.Duration,
		durationMinutes,
		sanitizedTitle,
		req.Language,
		req.Tone,
		sanitizedSource,
		durationMinutes,
		targetWords,
	)
}

// BuildEntityExtractionPrompt builds the prompt for entity extraction.
func BuildEntityExtractionPrompt(text string, entityCount int) string {
	return fmt.Sprintf(`You are extracting structured entities from a documentary script fragment.

Return ONLY valid JSON with exactly this shape:
{
  "frasi_importanti": ["..."],
  "entity_senza_testo": {"Name": ""},
  "nomi_speciali": ["..."],
  "parole_importanti": ["..."]
}

Rules:
- Extract up to %d items per array.
- Prefer meaningful names, places, organizations, concepts, and visual cues.
- If a category has no items, return an empty array or empty object.
- Do not add markdown, commentary, code fences, or extra keys.

TEXT:
"%s"

JSON:`, entityCount, text)
}

// BuildTimelineAssetRoutingPrompt asks the model to choose the best asset source and folder for a timeline segment.
func BuildTimelineAssetRoutingPrompt(topic, openingSentence, closingSentence string, keywords, entities []string, stockFoldersBlock, artlistFoldersBlock string) string {
	return fmt.Sprintf(`You are choosing the best asset source for one documentary timeline segment.

Pick exactly one source:
- "stock_drive" (only if a stock folder name clearly and directly matches the segment topic)
- "artlist_folder" (only if an Artlist folder name clearly and directly matches the segment topic)
- "none" (if NO folder is relevant to the segment - this is the correct choice when folders don't match)

TIMESTAMP POLICY:
- Keep the smallest possible number of timeline blocks.
- Add a new timestamp only when the segment introduces a clearly different argument, scene, subject, location, or time shift.
- If the content stays on the same subject, keep using the current block.
- Do not create extra timestamps for every sentence.

STRICT RULES:
- ONLY pick a folder if its name is DIRECTLY about the same topic as the segment.
- Example: if the segment is about "Amish life" and you have a folder named "Amish", pick "artlist_folder" with "Amish".
- Example: if the segment is about "Amish life" and the only folders are "Spring, Blossom, Leaves, Trees", pick "none" - do NOT force a bad match.
- "none" is PREFERRED over a poor or unrelated match.
- Do not invent new folders.
- Do not match based on weak keyword associations (e.g., "dust" matching "Dust" folder when the topic is Amish people).

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

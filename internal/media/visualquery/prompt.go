package visualquery

import (
	"encoding/json"
	"fmt"
)

// buildVisualQueryPrompt creates the prompt for visual query generation
// Returns enriched JSON with visual_subject, visual_caption, queries, entity_queries, and visual_prompts
func buildVisualQueryPrompt(topic, subject, narrative string) string {
	return fmt.Sprintf(`You are generating search queries for stock video platforms.

Given a documentary segment, create a JSON object with visual details and search queries.

Rules:
- visual_subject: 2-4 words summarizing the visual theme
- visual_caption: 5-15 words describing what should be shown visually
- queries: array of 3 short search queries in ENGLISH (2-4 words each). MUST BE GENERIC STOCK FOOTAGE CONCEPTS describing the action/environment. DO NOT use proper names of famous people in this field.
- entity_queries: array of 1-2 specific names or unique entities if present.
- visual_prompts: array of 1-2 long, descriptive visual prompts for AI image generation (describe the action, lighting, composition)
- Use concrete visual concepts, not abstract ideas
- Avoid filler words
- Return only valid JSON object

Sentence: "%s"
Context topic: "%s"
Segment subject: "%s"

JSON:`, narrative, topic, subject)
}

// buildBatchVisualQueryPrompt creates a batch prompt for multiple segments
func buildBatchVisualQueryPrompt(topic string, segments []BatchSegmentInput, maxQueries int) string {
	segmentsJSON, _ := json.Marshal(segments)
	return fmt.Sprintf(`You are generating search queries for stock video platforms.

Given an array of documentary segments, create a JSON array where each element has visual details and search queries.

Rules:
- visual_subject: 2-4 words summarizing the visual theme
- visual_caption: 5-15 words describing what should be shown visually
- queries: array of up to %d short search queries in ENGLISH (2-4 words each). MUST BE GENERIC STOCK FOOTAGE CONCEPTS. DO NOT use proper names of famous people.
- entity_queries: array of names/unique entities
- visual_prompts: array of long descriptive visual prompts
- Use concrete visual concepts, not abstract ideas
- Avoid filler words
- Return only valid JSON array

Segments: %s

Output JSON array:`, maxQueries, string(segmentsJSON))
}

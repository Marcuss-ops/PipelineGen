package visualquery

import (
	"encoding/json"
	"fmt"
)

// buildVisualQueryPrompt creates the prompt for visual query generation
// Returns enriched JSON with visual_subject, visual_caption, and queries
func buildVisualQueryPrompt(topic, subject, narrative string) string {
	return fmt.Sprintf(`You are generating search queries for stock video platforms like Artlist.

Given a documentary sentence, create a JSON object with visual subject, visual caption, and %d short visual search queries.

Rules:
- visual_subject: 2-4 words summarizing the visual theme
- visual_caption: 5-15 words describing what should be shown visually
- queries: array of %d short search queries in ENGLISH (2-4 words each)
- Use concrete visual concepts, not abstract ideas
- Avoid filler words (the, a, an, is, was, were, are, been, have, has, had, but, and, or)
- Avoid full sentences in queries
- Prefer scenes, objects, environments, actions, historical period, scientific setting
- Return only valid JSON object

Examples:

Input: "Further excavation is planned, and with each new layer of rock exposed..."
Output: {"visual_subject": "archaeological excavation", "visual_caption": "Archaeologists carefully uncovering ancient rock layers and fossils", "queries": ["archaeological excavation", "ancient cave discovery", "rock layer excavation"]}

Input: "Scientists mix chemicals in a lab to test new battery technology..."
Output: {"visual_subject": "science laboratory", "visual_caption": "Researchers conducting experiments with beakers and microscopes", "queries": ["science lab experiment", "battery research", "chemistry laboratory"]}

Input: "The election campaign trails through small towns, shaking hands with voters..."
Output: {"visual_subject": "political campaign", "visual_caption": "Politician greeting crowds at a local rally", "queries": ["political campaign", "election rally", "voter handshake"]}

Input: "The race car speeds around the track, tires screeching on the asphalt..."
Output: {"visual_subject": "motor racing", "visual_caption": "Formula 1 car racing on a circuit track", "queries": ["race car driving", "motorsport circuit", "racing tire screech"]}

Sentence: "%s"
Context topic: "%s"
Segment subject: "%s"

JSON:`, DefaultMaxQueries, DefaultMaxQueries, narrative, topic, subject)
}

// buildBatchVisualQueryPrompt creates a batch prompt for multiple segments
func buildBatchVisualQueryPrompt(topic string, segments []BatchSegmentInput, maxQueries int) string {
	segmentsJSON, _ := json.Marshal(segments)
	return fmt.Sprintf(`You are generating search queries for stock video platforms like Artlist.

Given an array of documentary segments, create a JSON array where each element has visual_subject, visual_caption, and queries for the corresponding segment.

Rules:
- visual_subject: 2-4 words summarizing the visual theme
- visual_caption: 5-15 words describing what should be shown visually
- queries: array of up to %d short search queries in ENGLISH (2-4 words each)
- Use concrete visual concepts, not abstract ideas
- Avoid filler words
- Return only valid JSON array

Segments: %s

Output JSON array:`, maxQueries, string(segmentsJSON))
}

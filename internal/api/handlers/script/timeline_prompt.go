package script

import (
	"fmt"

	"velox/go-master/internal/pkg/textutil"
)

// buildTimelinePlanningPrompt creates the prompt for LLM timeline planning
func buildTimelinePlanningPrompt(topic string, duration int, narrative string, sourceText string) string {
	return fmt.Sprintf(`You are a documentary timeline editor.

Split the script into the most natural topical segments.

Rules:
- The overall topic is: %s
- Every segment subject must stay close to the topic and its immediate subtopic.
- STRICT NAMING POLICY FOR 'subject': You MUST use the REAL, FORMAL, FULL NAME of the primary person, place, or entity being discussed in the segment. No nicknames, no aliases, no abstract concepts.
- The subject MUST be a concrete entity (a person, place, or thing). No abstract phrases.
- Never output file names, path fragments, or unrelated people/places.
- Keep each subject short and human-readable, ideally 2 to 6 words.
- Divide by argument or topic shifts, not by a fixed number of segments.
- Create as many segments as the narrative needs, but RESPECT THE MINIMUM SEGMENT REQUIREMENTS:
  * For duration <= 60 seconds: minimum 2 segments
  * For duration 60-180 seconds: minimum 4 segments
  * For duration 300 seconds: minimum 8 segments
  * For other durations: minimum 1 segment per 45 seconds of video
- If the narrative is short, you can exceed the minimum, but never go below it.
- Keep segments in the same order as the script.
- Each segment must be contiguous and represent one coherent idea.
- If the source material contains obvious section headings or subject transitions, use them to preserve the natural chapter structure.
- For a script built from clearly separated subjects, return one segment per subject block.
- For topics with strong focal subjects, you can use larger segments if the visuals remain consistent.
- Use timestamps in seconds from 0 to %d.
- The first segment must start at 0.
- The last segment must end exactly at %d.
- Segments must not overlap.
- The 'narrative_text' of all segments combined MUST exactly match the provided SCRIPT. Do not omit or truncate any part of the script.
- The 'search_suggestions' field MUST contain at least 3 specific, descriptive keywords or short phrases (e.g., "pizza dough kneading", "wood fired oven", "italian chef cooking") that would be perfect to search on Artlist for this segment.
- Even for short videos (e.g. 30s), if the script is long, you must include the full script text across the segments.
- "entities" MUST contain MAXIMUM 1 entity per segment. Only extract the single most important name, place or specific object. Do not extract more than one.
- Return ONLY valid JSON with this shape:
{
  "primary_focus": "short title of the main subject",
  "segments": [
    {
      "index": 1,
      "start_time": 0,
      "end_time": 18.5,
      "subject": "Main person or specific topic of this block",
      "narrative_text": "exact excerpt from the script for this segment",
      "opening_sentence": "opening sentence or excerpt",
      "closing_sentence": "closing sentence or excerpt",
      "keywords": ["optional", "keywords"],
      "entities": ["MAX 1 entity"]
    }
  ]
}

SCRIPT:
%s

SOURCE MATERIAL:
%s

JSON:`, textutil.Truncate(topic, 200), duration, duration, textutil.Truncate(narrative, 6000), textutil.Truncate(sourceText, 6000))
}

package script

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/media/association"
	clipresolver "velox/go-master/internal/media/clipresolver"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/sources/artlist"
)

// DirectorsBrief contains the global visual style guidelines extracted from the script.
type DirectorsBrief struct {
	Mood        string   `json:"mood"`
	Colors      string   `json:"colors"`
	KeySubjects []string `json:"key_subjects"`
	Location    string   `json:"location"`
}

// HybridTimelineSegment is a timeline chunk mapped to local/remote video assets.
type HybridTimelineSegment struct {
	Index         int                      `json:"index"`
	StartSec      float64                  `json:"start_sec"`
	EndSec        float64                  `json:"end_sec"`
	Duration      float64                  `json:"duration"`
	Text          string                   `json:"text"`
	VisualQuery   string                   `json:"visual_query"`
	Keywords      []string                 `json:"keywords"`
	SFXTriggers   []string                 `json:"sfx_triggers,omitempty"`
	SelectedClip  *association.ScoredMatch `json:"selected_clip,omitempty"`
	NeedsHarvest  bool                     `json:"needs_harvest"`
	HarvestQuery  string                   `json:"harvest_query,omitempty"`
}

// HybridTimelinePlan contains the complete generated plan.
type HybridTimelinePlan struct {
	Topic          string                  `json:"topic"`
	Brief          DirectorsBrief          `json:"brief"`
	TotalDuration  float64                 `json:"total_duration"`
	Segments       []HybridTimelineSegment `json:"segments"`
}

// GenerateDirectorsBrief calls Ollama once to extract macro aesthetic guidelines from the entire script.
func GenerateDirectorsBrief(ctx context.Context, gen *ollama.Generator, topic, script string) (*DirectorsBrief, error) {
	if gen == nil || gen.GetClient() == nil {
		return &DirectorsBrief{
			Mood:        "cinematic documentary",
			Colors:      "natural grading",
			KeySubjects: []string{topic},
			Location:    topic,
		}, nil
	}

	prompt := fmt.Sprintf(`You are a documentary director.
Analyze the following script and extract the global visual style guidelines (Director's Brief).
Return ONLY a valid JSON object matching this structure:
{
  "mood": "overall aesthetic mood (e.g. cinematic documentary, gritty realism, mysterious, high-energy)",
  "colors": "color palette description (e.g. cold tones, warm sepia, high contrast, vibrant)",
  "key_subjects": ["list of 2-3 primary subjects or visual motifs mentioned across the script"],
  "location": "primary geographical setting or environment (e.g. Alaska winter, ancient Roman ruins)"
}

SCRIPT:
%s

JSON:`, script)

	messages := []types.Message{
		{Role: "user", Content: prompt},
	}

	resp, err := gen.GetClient().Chat(ctx, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("directors brief generation failed: %w", err)
	}

	// Extract JSON
	jsonStr := resp
	if startIdx := strings.Index(resp, "{"); startIdx != -1 {
		if endIdx := strings.LastIndex(resp, "}"); endIdx != -1 && endIdx > startIdx {
			jsonStr = resp[startIdx : endIdx+1]
		}
	}

	var brief DirectorsBrief
	if err := json.Unmarshal([]byte(jsonStr), &brief); err != nil {
		zap.L().Warn("failed to parse directors brief JSON, using defaults", zap.Error(err), zap.String("raw", resp))
		return &DirectorsBrief{
			Mood:        "cinematic documentary",
			Colors:      "natural grading",
			KeySubjects: []string{topic},
			Location:    topic,
		}, nil
	}

	return &brief, nil
}

// SplitScriptIntoSentences splits a script into raw sentences.
func SplitScriptIntoSentences(script string) []string {
	// Clean text and standardize punctuation
	script = strings.ReplaceAll(script, "\r\n", " ")
	script = strings.ReplaceAll(script, "\n", " ")
	
	reg := regexp.MustCompile(`[^.!?]+[.!?]?`)
	rawMatches := reg.FindAllString(script, -1)
	
	var sentences []string
	for _, s := range rawMatches {
		trimmed := strings.TrimSpace(s)
		if len(trimmed) > 5 {
			sentences = append(sentences, trimmed)
		}
	}
	
	if len(sentences) == 0 && len(strings.TrimSpace(script)) > 0 {
		sentences = append(sentences, strings.TrimSpace(script))
	}
	
	return sentences
}

// SegmentScriptLocally groups sentences into chunks that last at least minDuration seconds based on reading speed.
func SegmentScriptLocally(sentences []string, wpm float64, minDuration float64) []HybridTimelineSegment {
	if wpm <= 0 {
		wpm = 135 // Default: 135 words per minute (~2.25 words per second)
	}
	wordsPerSecond := wpm / 60.0

	var segments []HybridTimelineSegment
	var currentSentences []string
	var currentWords int
	var currentTime float64
	
	segmentIdx := 1
	startSec := 0.0

	for _, sentence := range sentences {
		words := len(strings.Fields(sentence))
		duration := float64(words) / wordsPerSecond

		currentSentences = append(currentSentences, sentence)
		currentWords += words
		currentTime += duration

		if currentTime >= minDuration {
			text := strings.Join(currentSentences, " ")
			segments = append(segments, HybridTimelineSegment{
				Index:       segmentIdx,
				StartSec:    startSec,
				EndSec:      startSec + currentTime,
				Duration:    currentTime,
				Text:        text,
				Keywords:    ExtractLocalKeywords(text),
				SFXTriggers: DetectAudioCues(text),
			})
			startSec += currentTime
			segmentIdx++
			currentSentences = nil
			currentWords = 0
			currentTime = 0
		}
	}

	// Append any remaining text to the last segment or create one final segment
	if len(currentSentences) > 0 {
		text := strings.Join(currentSentences, " ")
		if len(segments) > 0 {
			// Append to the last segment to avoid a tiny final clip
			lastIdx := len(segments) - 1
			segments[lastIdx].Text += " " + text
			segments[lastIdx].Duration += currentTime
			segments[lastIdx].EndSec += currentTime
			segments[lastIdx].Keywords = ExtractLocalKeywords(segments[lastIdx].Text)
			segments[lastIdx].SFXTriggers = append(segments[lastIdx].SFXTriggers, DetectAudioCues(text)...)
		} else {
			segments = append(segments, HybridTimelineSegment{
				Index:       segmentIdx,
				StartSec:    startSec,
				EndSec:      startSec + currentTime,
				Duration:    currentTime,
				Text:        text,
				Keywords:    ExtractLocalKeywords(text),
				SFXTriggers: DetectAudioCues(text),
			})
		}
	}

	return segments
}

// ExtractLocalKeywords extracts relevant keyword nouns, filtering common stopwords in English and Italian.
func ExtractLocalKeywords(text string) []string {
	text = strings.ToLower(text)
	// Replace punctuation with spaces
	reg := regexp.MustCompile(`[^a-z0-9àèìòùáéíóú']`)
	cleanText := reg.ReplaceAllString(text, " ")
	
	words := strings.Fields(cleanText)
	stopwords := map[string]bool{
		// English
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"is": true, "are": true, "was": true, "were": true, "of": true, "to": true,
		"for": true, "in": true, "on": true, "at": true, "by": true, "with": true,
		"from": true, "that": true, "this": true, "these": true, "those": true,
		"it": true, "its": true, "they": true, "them": true, "their": true,
		"he": true, "him": true, "his": true, "she": true, "her": true, "hers": true,
		"you": true, "your": true, "yours": true, "we": true, "us": true, "our": true, "ours": true,
		// Italian (without repeating "a" and "in")
		"il": true, "la": true, "i": true, "gli": true, "le": true, "un": true, "una": true, "uno": true,
		"di": true, "da": true, "con": true, "su": true, "per": true, "tra": true, "fra": true,
		"e": true, "o": true, "ma": true, "se": true, "perché": true, "che": true, "chi": true, "cui": true,
		"questo": true, "questa": true, "questi": true, "queste": true, "quello": true, "quella": true, "quelli": true, "quelle": true,
		"io": true, "tu": true, "lui": true, "lei": true, "noi": true, "voi": true, "loro": true,
		"mio": true, "tuo": true, "suo": true, "nostro": true, "vostro": true,
	}

	var keywords []string
	seen := make(map[string]bool)
	for _, w := range words {
		if len(w) > 3 && !stopwords[w] && !seen[w] {
			seen[w] = true
			keywords = append(keywords, w)
		}
	}
	
	// Limit keywords to top 5 to keep search terms concise
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}
	return keywords
}

// DetectAudioCues detects audio cues in the text to trigger SFX sound layering.
func DetectAudioCues(text string) []string {
	text = strings.ToLower(text)
	var cues []string
	
	sfxDictionary := map[string]string{
		"vento":      "wind_howl_sfx",
		"ulula":      "wind_howl_sfx",
		"ghiaccio":   "ice_cracking_sfx",
		"spacca":     "ice_cracking_sfx",
		"ticchettio": "ice_cracking_sfx",
		"lenza":      "fishing_reel_sfx",
		"srotola":    "fishing_reel_sfx",
		"passi":      "footsteps_snow_sfx",
		"neve":       "footsteps_snow_sfx",
	}

	seen := make(map[string]bool)
	for word, sfxTag := range sfxDictionary {
		if strings.Contains(text, word) && !seen[sfxTag] {
			seen[sfxTag] = true
			cues = append(cues, sfxTag)
		}
	}
	return cues
}

// IsAbstractMetaphor returns true if the text segment appears abstract/metaphorical and lacks concrete nouns.
func IsAbstractMetaphor(text string, keywords []string) bool {
	// Simple heuristic: if we have very few keywords, or keywords belong to a dictionary of abstract concepts.
	abstractTerms := map[string]bool{
		"battaglia": true, "nemico": true, "speranza": true, "futuro": true, "tempo": true,
		"anima": true, "cuore": true, "mente": true, "destino": true, "sogno": true, "verità": true,
		"life": true, "hope": true, "future": true, "battle": true, "enemy": true, "dream": true,
	}
	
	abstractCount := 0
	for _, kw := range keywords {
		if abstractTerms[kw] {
			abstractCount++
		}
	}

	// If more than half of the keywords are abstract, or we have no concrete keywords, it's metaphorical.
	return len(keywords) == 0 || (float64(abstractCount)/float64(len(keywords)) >= 0.5)
}

// TranslateMetaphorMicro calls Ollama (Gemma 3) as a micro-request to translate an abstract metaphor into a concrete visual query.
func TranslateMetaphorMicro(ctx context.Context, gen *ollama.Generator, sentence string, brief DirectorsBrief) (string, error) {
	if gen == nil || gen.GetClient() == nil {
		return sentence, nil
	}

	prompt := fmt.Sprintf(`You are a cinematographer.
Translate the following metaphorical or abstract sentence into a single concrete, search-engine-ready visual scene description (2-5 words).
Include style elements from the brief: Location: %s, Mood: %s.
Return ONLY the query string, nothing else.

Sentence: "%s"

Query:`, brief.Location, brief.Mood, sentence)

	messages := []types.Message{
		{Role: "user", Content: prompt},
	}

	resp, err := gen.GetClient().Chat(ctx, messages, nil)
	if err != nil {
		return sentence, err
	}

	query := strings.TrimSpace(resp)
	query = strings.ReplaceAll(query, "\"", "")
	query = strings.ReplaceAll(query, "'", "")
	return query, nil
}

// BuildHybridTimelinePlan orchestrates the macro + micro timeline matching.
func BuildHybridTimelinePlan(ctx context.Context, gen *ollama.Generator, topic, script string, minScore float64, assocService *association.Service, artlistService *artlistSvc.Service, clipResolver *clipresolver.Service, stockRepo *clips.Repository) (*HybridTimelinePlan, error) {
	startedAt := time.Now()
	zap.L().Info("Building hybrid timeline plan", zap.String("topic", topic), zap.Int("script_len", len(script)))

	if minScore <= 0 {
		minScore = 55.0
	}

	// 1. Generate Directors Brief (Single macro call)
	brief, err := GenerateDirectorsBrief(ctx, gen, topic, script)
	if err != nil {
		zap.L().Warn("failed to generate directors brief, using defaults", zap.Error(err))
		brief = &DirectorsBrief{
			Mood:        "cinematic documentary",
			Colors:      "natural grading",
			KeySubjects: []string{topic},
			Location:    topic,
		}
	}
	zap.L().Info("generated directors brief",
		zap.String("mood", brief.Mood),
		zap.String("location", brief.Location),
	)

	// 2. Local sentence splitting and grouping
	sentences := SplitScriptIntoSentences(script)
	segments := SegmentScriptLocally(sentences, 135, 5.0) // 135 WPM, 5s minimum clip duration
	zap.L().Info("locally segmented script", zap.Int("segments_count", len(segments)))

	// 3. Process each segment
	for i := range segments {
		seg := &segments[i]
		
		// Build visual query: join keywords with brief settings
		searchKeywords := seg.Keywords
		if len(searchKeywords) == 0 {
			searchKeywords = []string{topic}
		}
		
		visualQuery := fmt.Sprintf("%s %s", strings.Join(searchKeywords, " "), brief.Location)
		seg.VisualQuery = visualQuery

		// A. Check for direct folder match in stock (Priority 1)
		var bestMatch *association.ScoredMatch
		if assocService != nil && stockRepo != nil {
			for _, kw := range searchKeywords {
				direct, found, err := assocService.FindDirectStockFolderCandidate(ctx, brief.Location, kw)
				if err == nil && found && direct != nil {
					// We matched a local stock folder! Query children to pick a clip.
					children, err := stockRepo.GetFolderChildren(ctx, direct.FolderID)
					if err == nil && len(children) > 0 {
						// Pick first clip
						for _, child := range children {
							if !child.IsFolder && (child.DriveLink != "" || child.LocalPath != "") {
								bestMatch = &association.ScoredMatch{
									ClipID: child.ID,
									Title:  child.Name,
									Path:   child.LocalPath,
									Score:  100, // Maximum score for direct stock match
									Source: "drive_stock",
									Link:   child.DriveLink,
								}
								zap.L().Info("found direct stock folder match",
									zap.Int("segment", seg.Index),
									zap.String("folder", direct.Name),
									zap.String("clip", child.Name),
								)
								break
							}
						}
					}
				}
				if bestMatch != nil {
					break
				}
			}
		}

		// B. If no direct stock folder match, use association search (vettoriale + lineare)
		if bestMatch == nil && assocService != nil {
			// Query embedding
			queryVector, _ := assocService.GenerateEmbedding(ctx, visualQuery)
			
			// Associate assets from all providers (stock_drive, artlist_stock, clip_drive, etc.)
			input := association.SegmentInput{
				Topic:     topic,
				Subject:   strings.Join(searchKeywords, " "),
				Keywords:  searchKeywords,
				Narrative: seg.Text,
			}
			
			matches := assocService.Associate(ctx, input)
			
			// Rescore via Hybrid Search engine (which applies a 60% semantic + 40% linear scoring and handles stock boost)
			scoredMatches := assocService.ScoreMedia(ctx, visualQuery, queryVector, matches)
			
			// Filter and prioritize stock if available
			if len(scoredMatches) > 0 {
				sortScoredMatches(scoredMatches)
				if float64(scoredMatches[0].Score) >= minScore {
					bestMatch = &scoredMatches[0]
					zap.L().Info("found database match",
						zap.Int("segment", seg.Index),
						zap.String("source", bestMatch.Source),
						zap.Int("score", bestMatch.Score),
					)
				}
			}
		}

		// C. If matches are poor, run fallback
		if bestMatch == nil {
			seg.NeedsHarvest = true
			
			// If metaphorical, run a micro LLM call to translate to concrete query
			harvestQuery := visualQuery
			if IsAbstractMetaphor(seg.Text, seg.Keywords) {
				zap.L().Info("segment text is metaphorical, translating via micro-LLM", zap.Int("segment", seg.Index))
				translated, err := TranslateMetaphorMicro(ctx, gen, seg.Text, *brief)
				if err == nil && translated != "" {
					harvestQuery = translated
				}
			}
			
			// Append styling modifiers from Director's Brief
			if brief.Mood != "" {
				harvestQuery += ", " + brief.Mood
			}
			seg.HarvestQuery = harvestQuery

			// Trigger Artlist active harvester in background (limit to 3 clips per query)
			if artlistService != nil {
				zap.L().Info("triggering background Artlist harvester for segment",
					zap.Int("segment", seg.Index),
					zap.String("query", harvestQuery),
				)
				go func(q string) {
					// Discovers, queues running, downloads and updates clips database in background
					_, _, _ = artlistService.DiscoverAndQueueRun(context.Background(), q, 3)
				}(harvestQuery)
			}
		} else {
			seg.SelectedClip = bestMatch
		}
	}

	totalDur := 0.0
	if len(segments) > 0 {
		totalDur = segments[len(segments)-1].EndSec
	}

	zap.L().Info("hybrid timeline plan complete", zap.Duration("elapsed", time.Since(startedAt)))
	return &HybridTimelinePlan{
		Topic:         topic,
		Brief:         *brief,
		TotalDuration: totalDur,
		Segments:      segments,
	}, nil
}

// Helpers
func sortScoredMatches(matches []association.ScoredMatch) {
	// Custom sort to prefer stock and higher score
	for i := range matches {
		if matches[i].Source == "drive_stock" || matches[i].Source == "stock_drive" {
			matches[i].Score += 10 // Additional prioritizing boost
		}
	}
	
	// Sort descending by score
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[i].Score < matches[j].Score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
}

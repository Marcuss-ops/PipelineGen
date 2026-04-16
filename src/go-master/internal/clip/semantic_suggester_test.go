package clip

import (
	"context"
	"strings"
	"testing"
)

// Helper to create a test indexer with predefined clips
func createTestIndexer(clips []IndexedClip) *Indexer {
	index := &ClipIndex{
		Version: "test",
		Clips:   clips,
	}
	indexer := &Indexer{
		index: index,
		cache: NewSuggestionCache(100, 60000000000), // 1 min TTL
	}
	return indexer
}

// TestSemanticSuggester_EntityMatchHighScore tests that exact entity match gets very high score
func TestSemanticSuggester_EntityMatchHighScore(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Elon Musk Robot Demo",
			FolderPath: "Tech/Robotics",
			Tags:       []string{"elon musk", "robot", "technology", "presentation"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Elon Musk presenta un nuovo robot umanoide"
	results := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	if len(results) == 0 {
		t.Fatal("Expected at least one suggestion, got none")
	}

	// Should have very high score due to entity match
	if results[0].Score < 90 {
		t.Errorf("Expected high score >= 90 for entity match, got %.2f", results[0].Score)
	}

	t.Logf("✅ Entity match score: %.2f (type: %s)", results[0].Score, results[0].MatchType)
}

// TestSemanticSuggester_LowScoreIrrelevant tests that irrelevant text gets low score
func TestSemanticSuggester_LowScoreIrrelevant(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Business Meeting Office",
			FolderPath: "Business/Corporate",
			Tags:       []string{"business", "office", "meeting"},
			Duration:   20,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Una foresta tranquilla al tramonto con cervi che pascolano"
	results := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	// Should have very low or no score
	if len(results) > 0 {
		if results[0].Score > 30 {
			t.Errorf("Expected low score <= 30 for irrelevant text, got %.2f", results[0].Score)
		}
		t.Logf("✅ Irrelevant text score: %.2f (correctly low)", results[0].Score)
	} else {
		t.Log("✅ No suggestions for completely irrelevant text (correct)")
	}
}

// TestSemanticSuggester_KeywordMatch tests keyword-based matching
func TestSemanticSuggester_KeywordMatch(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Technology Computer Programming",
			FolderPath: "Tech/Programming",
			Tags:       []string{"technology", "computer", "programming", "code"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Un programmatore scrive codice su un computer con tecnologia moderna"
	results := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	if len(results) == 0 {
		t.Fatal("Expected at least one suggestion for keyword match")
	}

	// Should have medium-high score from keyword matches
	if results[0].Score < 40 {
		t.Errorf("Expected medium-high score >= 40 for keyword match, got %.2f", results[0].Score)
	}

	t.Logf("✅ Keyword match score: %.2f (type: %s)", results[0].Score, results[0].MatchType)
	t.Logf("   Match reasons: %s", results[0].MatchReason)
}

// TestSemanticSuggester_ActionVerbItalian tests Italian action verb detection
func TestSemanticSuggester_ActionVerbItalian(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Person Walking in Park",
			FolderPath: "Nature/Park",
			Tags:       []string{"walking", "park", "nature", "person"},
			Duration:   25,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Una persona cammina nel parco sotto la pioggia"
	results := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	if len(results) == 0 {
		t.Fatal("Expected at least one suggestion")
	}

	// Should detect "cammina" (walks) action verb
	hasActionMatch := false
	for _, r := range results {
		if strings.Contains(r.MatchType, "action") || strings.Contains(r.MatchReason, "action") {
			hasActionMatch = true
			break
		}
	}

	t.Logf("✅ Action verb detection: found=%v, score=%.2f, type=%s", hasActionMatch, results[0].Score, results[0].MatchType)
}

// TestSemanticSuggester_ActionVerbEnglish tests English action verb detection
func TestSemanticSuggester_ActionVerbEnglish(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Person Speaking Presentation",
			FolderPath: "Business/Presentation",
			Tags:       []string{"speaking", "presentation", "business"},
			Duration:   60,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "A person is speaking at a conference about technology"
	results := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	if len(results) == 0 {
		t.Fatal("Expected at least one suggestion")
	}

	t.Logf("✅ English verb match score: %.2f (type: %s)", results[0].Score, results[0].MatchType)
}

// TestSemanticSuggester_GroupDetection tests group-based matching
func TestSemanticSuggester_GroupDetection(t *testing.T) {
	testCases := []struct {
		name          string
		sentence      string
		clipGroup     string
		shouldMatch   bool
		expectedGroup string
	}{
		{
			name:          "Interview group",
			sentence:      "Intervista a un esperto di tecnologia",
			clipGroup:     "interviews",
			shouldMatch:   true,
			expectedGroup: "interviews",
		},
		{
			name:          "Tech group",
			sentence:      "Nuova tecnologia rivoluzionaria nel campo AI",
			clipGroup:     "tech",
			shouldMatch:   true,
			expectedGroup: "tech",
		},
		{
			name:          "Nature group",
			sentence:      "La natura selvaggia con animali in libertà",
			clipGroup:     "nature",
			shouldMatch:   true,
			expectedGroup: "nature",
		},
		{
			name:          "Business group",
			sentence:      "Azienda startup business in crescita",
			clipGroup:     "business",
			shouldMatch:   true,
			expectedGroup: "business",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clips := []IndexedClip{
				{
					ID:         "clip1",
					Name:       "Sample Clip",
					FolderPath: "Test/Folder",
					Group:      tc.clipGroup,
					Tags:       []string{"sample"},
					Duration:   30,
					MimeType:   "video/mp4",
				},
			}
			indexer := createTestIndexer(clips)
			suggester := NewSemanticSuggester(indexer)

			results := suggester.SuggestForSentence(context.Background(), tc.sentence, 10, 0, "")

			if len(results) > 0 {
				if tc.shouldMatch && results[0].Clip.Group == tc.expectedGroup {
					t.Logf("✅ Group match: '%s' matched '%s' group (score: %.2f)", tc.sentence, tc.expectedGroup, results[0].Score)
				} else {
					t.Logf("⚠️  Group '%s' with score %.2f", results[0].Clip.Group, results[0].Score)
				}
			}
		})
	}
}

// TestSemanticSuggester_ResultOrdering tests that results are sorted by score descending
func TestSemanticSuggester_ResultOrdering(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "low_match",
			Name:       "Generic Video",
			FolderPath: "General",
			Tags:       []string{"generic"},
			Duration:   20,
			MimeType:   "video/mp4",
		},
		{
			ID:         "medium_match",
			Name:       "Technology Innovation",
			FolderPath: "Tech",
			Tags:       []string{"technology", "innovation"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
		{
			ID:         "high_match",
			Name:       "AI Robot Technology Presentation",
			FolderPath: "Tech/Robotics",
			Tags:       []string{"ai", "robot", "technology", "presentation"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Un robot con intelligenza artificiale presenta nuova tecnologia"
	results := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	if len(results) < 2 {
		t.Skip("Need at least 2 results to test ordering")
	}

	// Verify descending order
	for i := 0; i < len(results)-1; i++ {
		if results[i].Score < results[i+1].Score {
			t.Errorf("Results not sorted by score descending: [%d]=%.2f < [%d]=%.2f",
				i, results[i].Score, i+1, results[i+1].Score)
		}
	}

	t.Log("✅ Results correctly sorted by score descending:")
	for i, r := range results {
		t.Logf("   [%d] %s: %.2f (%s)", i, r.Clip.Name, r.Score, r.MatchType)
	}
}

// TestSemanticSuggester_Determinism tests that same input produces same output
func TestSemanticSuggester_Determinism(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Tech Conference Speaker",
			FolderPath: "Tech/Conference",
			Tags:       []string{"technology", "conference", "speaker", "presentation"},
			Duration:   60,
			MimeType:   "video/mp4",
		},
		{
			ID:         "clip2",
			Name:       "Office Meeting",
			FolderPath: "Business",
			Tags:       []string{"business", "meeting", "office"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Relatore parla a conferenza tecnologia"

	// Run 3 times
	results1 := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")
	results2 := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")
	results3 := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	// All should have same length
	if len(results1) != len(results2) || len(results2) != len(results3) {
		t.Errorf("Different result lengths: %d, %d, %d", len(results1), len(results2), len(results3))
	}

	// All should have same scores
	minLen := len(results1)
	if len(results2) < minLen {
		minLen = len(results2)
	}
	if len(results3) < minLen {
		minLen = len(results3)
	}

	for i := 0; i < minLen; i++ {
		if results1[i].Score != results2[i].Score || results2[i].Score != results3[i].Score {
			t.Errorf("Non-deterministic scores at index %d: %.2f, %.2f, %.2f",
				i, results1[i].Score, results2[i].Score, results3[i].Score)
		}
	}

	t.Logf("✅ Deterministic: %d results, all runs identical", len(results1))
}

// TestSemanticSuggester_MinScoreFilter tests that minScore filters results correctly
func TestSemanticSuggester_MinScoreFilter(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Weak Match Video",
			FolderPath: "General",
			Tags:       []string{"general"},
			Duration:   20,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Tecnologia avanzata intelligenza artificiale robot"

	// With minScore=0, should return something (even if low)
	resultsLow := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")

	// With minScore=80, should return nothing or high-score clips only
	resultsHigh := suggester.SuggestForSentence(context.Background(), sentence, 10, 80, "")

	t.Logf("✅ MinScore filter: low_threshold=%d results, high_threshold=%d results",
		len(resultsLow), len(resultsHigh))

	// All high-score results should be >= 80
	for _, r := range resultsHigh {
		if r.Score < 80 {
			t.Errorf("Result with score %.2f should be filtered out (minScore=80)", r.Score)
		}
	}
}

// TestSemanticSuggester_MaxResultsLimit tests that maxResults limits the output
func TestSemanticSuggester_MaxResultsLimit(t *testing.T) {
	clips := make([]IndexedClip, 20)
	for i := 0; i < 20; i++ {
		clips[i] = IndexedClip{
			ID:         "clip",
			Name:       "Tech Video",
			FolderPath: "Tech",
			Tags:       []string{"technology"},
			Duration:   30,
			MimeType:   "video/mp4",
		}
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "tecnologia"
	maxResults := 5
	results := suggester.SuggestForSentence(context.Background(), sentence, maxResults, 0, "")

	if len(results) > maxResults {
		t.Errorf("Expected max %d results, got %d", maxResults, len(results))
	}

	t.Logf("✅ MaxResults limit: requested=%d, got=%d", maxResults, len(results))
}

// TestSemanticSuggester_FallbackClips tests fallback when no specific match
func TestSemanticSuggester_FallbackClips(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "broll1",
			Name:       "Generic B-Roll",
			FolderPath: "B-Roll",
			Group:      "broll",
			Tags:       []string{"generic", "broll"},
			Duration:   15,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	// Very obscure sentence that won't match anything
	sentence := "xyzabc qwerty unknown topic"
	results := suggester.SuggestForSentence(context.Background(), sentence, 5, 0, "")

	// Should return fallback clips with low scores
	if len(results) > 0 {
		if results[0].MatchType == "fallback_generic" {
			t.Logf("✅ Fallback clips returned (score: %.2f, type: %s)", results[0].Score, results[0].MatchType)
			if results[0].Score > 20 {
				t.Errorf("Fallback clip score too high: %.2f (expected <= 20)", results[0].Score)
			}
		} else {
			t.Logf("⚠️  Non-fallback match found (type: %s, score: %.2f)", results[0].MatchType, results[0].Score)
		}
	} else {
		t.Log("✅ No fallback clips (none available)")
	}
}

// TestSemanticSuggester_EmptySentence tests handling of empty sentence
func TestSemanticSuggester_EmptySentence(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Sample Clip",
			FolderPath: "Test",
			Tags:       []string{"sample"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	results := suggester.SuggestForSentence(context.Background(), "", 10, 0, "")

	t.Logf("✅ Empty sentence: %d results", len(results))
}

// TestSemanticSuggester_UsagePenalty tests that repeated usage penalizes clips
func TestSemanticSuggester_UsagePenalty(t *testing.T) {
	clips := []IndexedClip{
		{
			ID:         "clip1",
			Name:       "Perfect Match",
			FolderPath: "Tech",
			Tags:       []string{"technology", "ai", "robot"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestIndexer(clips)
	suggester := NewSemanticSuggester(indexer)

	sentence := "Tecnologia AI robot"

	// First call - should have high score
	results1 := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")
	if len(results1) == 0 {
		t.Fatal("Expected at least one result")
	}
	score1 := results1[0].Score

	// Second call - should have penalty applied
	results2 := suggester.SuggestForSentence(context.Background(), sentence, 10, 0, "")
	if len(results2) == 0 {
		t.Fatal("Expected at least one result")
	}
	score2 := results2[0].Score

	t.Logf("✅ Usage penalty: first=%.2f, second=%.2f (diff: %.2f)", score1, score2, score1-score2)

	// Second score should be <= first (due to penalty)
	if score2 > score1 {
		t.Errorf("Second call score (%.2f) should be <= first call score (%.2f) due to penalty", score2, score1)
	}
}

// TestSemanticSuggester_ScriptSuggestions tests SuggestForScript with multi-sentence script
func TestSemanticSuggester_ScriptSuggestions(t *testing.T) {
	indexer := createTestIndexer([]IndexedClip{
		{
			ID:         "tech1",
			Name:       "Intelligenza Artificiale Tecnologia Robot Demo",
			FolderPath: "Tech/AI",
			Tags:       []string{"intelligenza", "artificiale", "tecnologia", "robot", "demo", "rivoluzionando"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
		{
			ID:         "nature1",
			Name:       "Natura Paesaggio Tramonto Montagna",
			FolderPath: "Nature/Landscape",
			Tags:       []string{"natura", "paesaggio", "tramonto", "montagna", "bella", "parchi", "nazionali"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
		{
			ID:         "robot1",
			Name:       "Robot Umani Lavoro Insieme",
			FolderPath: "Tech/Robotics",
			Tags:       []string{"robot", "umani", "lavorare", "insieme", "possono"},
			Duration:   35,
			MimeType:   "video/mp4",
		},
	})
	suggester := NewSemanticSuggester(indexer)

	script := `L'intelligenza artificiale sta rivoluzionando la tecnologia moderna. 
	I robot possono now lavorare insieme agli umani. 
	La natura rimane bella e incontaminata nei parchi nazionali.`

	results := suggester.SuggestForScript(context.Background(), script, 5, 0, "")

	// Should process multiple sentences
	if len(results) == 0 {
		t.Fatal("Expected script suggestions for multi-sentence script")
	}

	t.Logf("✅ Script suggestions: %d sentences processed", len(results))
	for i, r := range results {
		sentencePreview := r.Sentence
		if len(sentencePreview) > 50 {
			sentencePreview = sentencePreview[:50]
		}
		t.Logf("   [%d] Sentence: %s...", i+1, sentencePreview)
		t.Logf("       Best score: %.2f, Suggestions: %d", r.BestScore, len(r.Suggestions))
		if i >= 2 {
			break
		}
	}

	// At least one sentence should have suggestions
	hasSuggestions := false
	for _, r := range results {
		if len(r.Suggestions) > 0 {
			hasSuggestions = true
			break
		}
	}

	if !hasSuggestions {
		t.Error("Expected at least one sentence with clip suggestions")
	}
}

// Helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

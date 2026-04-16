package nvidia

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestNVIDIA_RealVerification testa la verifica AI con API NVIDIA reale
func TestNVIDIA_RealVerification(t *testing.T) {
	// Carica API key da environment o .env
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY not set - skipping real API test")
	}

	client, err := NewClient(&Config{
		APIKey:      apiKey,
		BaseURL:     "https://integrate.api.nvidia.com/v1",
		Model:       "z-ai/glm5",
		Temperature: 0.7,
		MaxTokens:   4096,
		Timeout:     120 * time.Second, // Increased for testing
	})
	if err != nil {
		t.Fatalf("Failed to create NVIDIA client: %v", err)
	}

	tests := []struct {
		name            string
		sceneText       string
		sceneKeywords   string
		videoTitle      string
		videoDesc       string
		expectRecommend string // "download", "review", "reject"
	}{
		{
			name:            "Pertinent - Quantum Computing",
			sceneText:       "I qubit superconduttori stanno rivoluzionando il calcolo quantistico",
			sceneKeywords:   "quantum computing, qubit, superconductor",
			videoTitle:      "Quantum Computing Explained: How Qubits Will Change Everything",
			videoDesc:       "Deep dive into quantum computing and superconducting qubits technology",
			expectRecommend: "download",
		},
		{
			name:            "Not Pertinent - Clickbait",
			sceneText:       "I qubit superconduttori stanno rivoluzionando il calcolo quantistico",
			sceneKeywords:   "quantum computing, qubit",
			videoTitle:      "YOU WON'T BELIEVE THIS!!! (GONE WRONG)",
			videoDesc:       "Crazy pranks and challenges compilation",
			expectRecommend: "reject",
		},
		{
			name:            "Somewhat Pertinent - Business/Tech",
			sceneText:       "L'intelligenza artificiale nel business moderno",
			sceneKeywords:   "AI, business, technology",
			videoTitle:      "AI Tools for Small Business - Complete Guide 2024",
			videoDesc:       "How to use AI tools to grow your small business",
			expectRecommend: "download",
		},
		{
			name:            "Off-Topic - Cooking",
			sceneText:       "I qubit superconduttori stanno rivoluzionando il calcolo quantistico",
			sceneKeywords:   "quantum computing, qubit",
			videoTitle:      "Perfect Pasta Recipe - Italian Cooking Masterclass",
			videoDesc:       "Learn how to make authentic Italian pasta from scratch",
			expectRecommend: "reject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.VerifyYouTubeTitle(
				context.Background(),
				tt.sceneText,
				tt.sceneKeywords,
				tt.videoTitle,
				tt.videoDesc,
			)
			if err != nil {
				t.Fatalf("VerifyYouTubeTitle failed: %v", err)
			}

			t.Logf("Result for '%s':", tt.name)
			t.Logf("  Score: %d", result.RelevanceScore)
			t.Logf("  Recommendation: %s", result.Recommendation)
			t.Logf("  Reason: %s", result.Reason)
			t.Logf("  Match Keywords: %v", result.MatchKeywords)

			// Verifica recommendation
			if result.Recommendation != tt.expectRecommend {
				t.Logf("⚠️  Expected '%s', got '%s' (AI might have different opinion)",
					tt.expectRecommend, result.Recommendation)
			}

			// Verifica score range
			if result.RelevanceScore < 0 || result.RelevanceScore > 100 {
				t.Errorf("Score out of range: %d (expected 0-100)", result.RelevanceScore)
			}

			// Verifica coerenza: se reject, score dovrebbe essere basso
			if result.Recommendation == "reject" && result.RelevanceScore > 50 {
				t.Logf("⚠️  Rejection with high score (%d) - inconsistent", result.RelevanceScore)
			}
			if result.Recommendation == "download" && result.RelevanceScore < 70 {
				t.Logf("⚠️  Download recommendation with low score (%d) - inconsistent", result.RelevanceScore)
			}
		})
	}
}

// TestNVIDIA_BatchVerification testa verifica batch di titoli
func TestNVIDIA_BatchVerification(t *testing.T) {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY not set - skipping real API test")
	}

	client, err := NewClient(&Config{
		APIKey:      apiKey,
		BaseURL:     "https://integrate.api.nvidia.com/v1",
		Model:       "z-ai/glm5",
		Temperature: 0.7,
		MaxTokens:   4096,
		Timeout:     120 * time.Second, // Increased for testing
	})
	if err != nil {
		t.Fatalf("Failed to create NVIDIA client: %v", err)
	}

	videos := []VideoCandidate{
		{
			ID:          "vid1",
			Title:       "Quantum Computing: The Future of Technology",
			Description: "Exploring quantum computing basics",
			URL:         "https://youtube.com/watch?v=1",
		},
		{
			ID:          "vid2",
			Title:       "How to Cook Perfect Pizza",
			Description: "Italian cooking tutorial",
			URL:         "https://youtube.com/watch?v=2",
		},
		{
			ID:          "vid3",
			Title:       "AI and Machine Learning in 2024",
			Description: "Latest AI developments explained",
			URL:         "https://youtube.com/watch?v=3",
		},
	}

	results, err := client.VerifyBatchTitles(
		context.Background(),
		"Introduzione al calcolo quantistico e AI",
		"quantum computing, AI, technology",
		videos,
	)
	if err != nil {
		t.Fatalf("VerifyBatchTitles failed: %v", err)
	}

	t.Logf("Batch verification results: %d videos", len(results))
	for i, r := range results {
		t.Logf("  Video %d: Score=%d, Recommendation=%s",
			i+1, r.RelevanceScore, r.Recommendation)
	}

	if len(results) != len(videos) {
		t.Errorf("Expected %d results, got %d", len(videos), len(results))
	}
}

// TestNVIDIA_Health test verifica disponibilità API
func TestNVIDIA_Health(t *testing.T) {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		t.Skip("NVIDIA_API_KEY not set - skipping health check")
	}

	client, err := NewClient(&Config{
		APIKey:      apiKey,
		BaseURL:     "https://integrate.api.nvidia.com/v1",
		Model:       "z-ai/glm5",
		Temperature: 0.7,
		MaxTokens:   4096,
		Timeout:     120 * time.Second, // Increased for testing
	})
	if err != nil {
		t.Fatalf("Failed to create NVIDIA client: %v", err)
	}

	err = client.CheckHealth(context.Background())
	if err != nil {
		t.Errorf("Health check failed: %v", err)
	} else {
		t.Log("✅ NVIDIA API is healthy")
	}
}

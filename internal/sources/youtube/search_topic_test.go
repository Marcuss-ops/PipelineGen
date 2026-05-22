package youtube

import "testing"

func TestScoreTopicSimilarityPrefersExactTopicMatch(t *testing.T) {
	meta := &VideoMetadata{
		Title:      "Denzel Washington Interview with Graham Bensinger",
		Uploader:   "Graham Bensinger",
		Duration:   14.5,
		UploadDate: "20240601",
		Tags:       []string{"denzel", "washington", "interview"},
	}

	if got := scoreTopicSimilarity("Denzel Washington Interview", meta); got < 90 {
		t.Fatalf("expected strong similarity score, got %d", got)
	}
	if got := scoreFormatMatch("Denzel Washington Interview", meta); got < 90 {
		t.Fatalf("expected strong format score, got %d", got)
	}
}

func TestScoreTopicSimilarityRewardsPartialOverlap(t *testing.T) {
	meta := &VideoMetadata{
		Title:    "Denzel Washington on Acting",
		Uploader: "Movie Times",
	}

	if got := scoreTopicSimilarity("Denzel Washington Interview", meta); got >= 90 {
		t.Fatalf("expected partial overlap, got %d", got)
	}
}

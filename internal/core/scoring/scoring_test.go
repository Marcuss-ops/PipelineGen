package scoring

import (
	"testing"
)

func TestTokenScore_EmptyInputs(t *testing.T) {
	if s := TokenScore(nil, []string{"a"}); s != 0 {
		t.Errorf("expected 0, got %d", s)
	}
	if s := TokenScore([]string{"a"}, nil); s != 0 {
		t.Errorf("expected 0, got %d", s)
	}
}

func TestTokenScore_ExactMatch(t *testing.T) {
	s := TokenScore([]string{"amish", "community"}, []string{"amish", "community"})
	if s != 100 {
		t.Errorf("expected 100, got %d", s)
	}
}

func TestTokenScore_PartialMatch(t *testing.T) {
	s := TokenScore([]string{"amish", "community", "technology"}, []string{"amish", "farm"})
	if s != 33 {
		t.Errorf("expected 33, got %d", s)
	}
}

func TestTokenScore_NoMatch(t *testing.T) {
	s := TokenScore([]string{"technology"}, []string{"amish"})
	if s != 0 {
		t.Errorf("expected 0, got %d", s)
	}
}

func TestTokenScore_PhraseBonus(t *testing.T) {
	s := TokenScore([]string{"amish", "community"}, []string{"amish"})
	if s > 100 {
		t.Errorf("expected capped at 100, got %d", s)
	}
	if s <= 50 {
		t.Errorf("expected > 50 (50%% + phrase bonus), got %d", s)
	}
}

func TestCalculate_NoTopic_NoMatch(t *testing.T) {
	r := Calculate(Params{
		Query: "technology",
		Name:  "amish farm life",
		Tags:  []string{"rural", "farming"},
	})
	if r.Score != 0 {
		t.Errorf("expected 0, got %d", r.Score)
	}
}

func TestCalculate_TopicMatchBoost(t *testing.T) {
	r := Calculate(Params{
		Query: "amish life",
		Topic: "amish",
		Name:  "amish community farming",
		Tags:  []string{"amish", "traditional"},
	})
	if r.Score <= 40 {
		t.Errorf("expected > 40 with topic match, got %d", r.Score)
	}
	if !r.TopicMatched {
		t.Error("expected topicMatched=true")
	}
}

func TestCalculate_TopicGate(t *testing.T) {
	r := Calculate(Params{
		Query: "farming technology",
		Topic: "amish",
		Name:  "modern farming equipment",
		Tags:  []string{"agriculture", "machinery"},
	})
	if r.Score > 40 {
		t.Errorf("expected <= 40 due to topic gate, got %d", r.Score)
	}
	if r.TopicMatched {
		t.Error("expected topicMatched=false for non-matching topic")
	}
}

func TestCalculate_DensityPenalty(t *testing.T) {
	r := Calculate(Params{
		Query: "dog",
		Topic: "",
		Name:  "cat bird fish rabbit hamster turtle parrot",
		Tags:  []string{"pets", "animals"},
	})
	if r.Score >= 50 {
		t.Errorf("expected score suppressed by density penalty, got %d", r.Score)
	}
}

func TestCalculate_ExactMatchBonus(t *testing.T) {
	r := Calculate(Params{
		Query: "sunset beach",
		Name:  "sunset beach panorama",
		Tags:  []string{"sunset", "beach"},
	})
	if r.Score < 50 {
		t.Errorf("expected >= 50 with exact match, got %d", r.Score)
	}
}

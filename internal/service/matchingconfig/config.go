package matchingconfig

import (
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v3"
)

type MatchingConfig struct {
	Matching struct {
		MinDefaultTokenLen int      `yaml:"min_default_token_len"`
		AllowShortTokens   []string `yaml:"allow_short_tokens"`
		Allow4LetterTokens []string `yaml:"allow_4_letter_tokens"`
		DefaultLimit       int      `yaml:"default_limit"`
		CandidateMultiplier int     `yaml:"candidate_multiplier"`
		MinScore           float64  `yaml:"min_score"`

		TextScoreWeight      float64 `yaml:"text_score_weight"`
		VectorScoreWeight    float64 `yaml:"vector_score_weight"`
		TopicBoostWeight     float64 `yaml:"topic_boost_weight"`
		CategoryBoostWeight  float64 `yaml:"category_boost_weight"`
		UsableForBoostWeight float64 `yaml:"usable_for_boost_weight"`
		QualityScoreWeight   float64 `yaml:"quality_score_weight"`
		NegativePenaltyWeight float64 `yaml:"negative_penalty_weight"`
		ReusePenaltyWeight   float64 `yaml:"reuse_penalty_weight"`

		NarrativeMinTokenLen int `yaml:"narrative_min_token_len"`
		NarrativeMaxTerms    int `yaml:"narrative_max_terms"`
		TotalMaxTerms        int `yaml:"total_max_terms"`
		AssociationMinScore  int `yaml:"association_min_score"`
	} `yaml:"matching"`

	ClipQuality struct {
		BaseScore          float64 `yaml:"base_score"`
		EmbeddingBonus     float64 `yaml:"embedding_bonus"`
		CategoryBonus      float64 `yaml:"category_bonus"`
		ReuseThreshold     int     `yaml:"reuse_threshold"`
		ReusePenaltyPerUse float64 `yaml:"reuse_penalty_per_use"`
	} `yaml:"clip_quality"`

	shortTokensMap  map[string]bool
	allowed4LetterMap map[string]bool
}

func LoadMatchingConfig(path string) (*MatchingConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &MatchingConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	cfg.shortTokensMap = make(map[string]bool)
	for _, t := range cfg.Matching.AllowShortTokens {
		cfg.shortTokensMap[strings.ToLower(t)] = true
	}

	cfg.allowed4LetterMap = make(map[string]bool)
	for _, t := range cfg.Matching.Allow4LetterTokens {
		cfg.allowed4LetterMap[strings.ToLower(t)] = true
	}

	return cfg, nil
}

func (c *MatchingConfig) IsMeaningfulToken(tok string) bool {
	t := strings.ToLower(tok)
	l := len(t)

	if l >= c.Matching.MinDefaultTokenLen {
		return true
	}

	if l <= 3 && c.shortTokensMap[t] {
		return true
	}

	if l == 4 && c.allowed4LetterMap[t] {
		return true
	}

	return false
}

package topicontology

import (
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v3"
)

type TopicConfig map[string]TopicEntry

type TopicEntry struct {
	CoreTerms         []string `yaml:"core_terms"`
	VisualSynonyms    [][]string `yaml:"visual_synonyms"`
	Avoid             []string `yaml:"avoid"`
	PreferredCategories []string `yaml:"preferred_categories"`
	Boost             float64  `yaml:"boost"`
}

func LoadOntology(path string) (TopicConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := make(TopicConfig)
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c TopicConfig) InferCategoryFromSearchTerm(term string) string {
	termLower := strings.ToLower(term)

	for topic, entry := range c {
		for _, keyword := range entry.CoreTerms {
			if strings.Contains(termLower, strings.ToLower(keyword)) {
				return topic
			}
		}
	}

	return "general"
}

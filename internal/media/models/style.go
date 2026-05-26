package models

// GenerationStyle defines a reusable prompt style for AI generation
type GenerationStyle struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// GenerationStyles is a container for multiple styles
type GenerationStyles struct {
	Styles []GenerationStyle `yaml:"styles" json:"styles"`
}

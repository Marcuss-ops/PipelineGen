package asset

// GenerationStyle defines a reusable prompt style for AI generation.
// Used by internal/media/generation/style_registry.go which loads
// style definitions from config/generation_styles.yaml.
//
// This type moved here from internal/domain/media/styles.go during the
// Wave-14 cut-over that eliminates internal/domain/media. The
// domain/media package previously held these types alongside type aliases;
// since only the aliases were meant to be deleted, the GenerationStyle
// types are preserved in the canonical domain/asset package.
type GenerationStyle struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// GenerationStyles is a container for multiple styles (YAML on-disk shape).
type GenerationStyles struct {
	Styles []GenerationStyle `yaml:"styles" json:"styles"`
}

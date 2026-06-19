package media

// GenerationStyle defines a reusable prompt style for AI generation.
// Used by internal/media/generation/style_registry.go which loads
// style definitions from config/generation_styles.yaml.
//
// This type moved here from internal/media/models/style.go during the
// media/models shim retirement. The legacy models package previously held
// these types alongside type aliases; since only the aliases were meant
// to be deleted, the GenerationStyle types are preserved in the canonical
// domain/media package.
type GenerationStyle struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// GenerationStyles is a container for multiple styles (YAML on-disk shape).
type GenerationStyles struct {
	Styles []GenerationStyle `yaml:"styles" json:"styles"`
}

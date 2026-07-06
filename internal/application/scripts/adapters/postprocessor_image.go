// Package adapters — postprocessor_image.go: image-related types.
//
// Extracted from postprocessor_registry.go (July 2026).
// Owns: SceneImage.
package adapters

// SceneImage is a single scene-image outcome from ImageProcessor.
// PR 9: images map to model-defined scenes 1:1.
type SceneImage struct {
	Index int
	Text  string // scene text used as the generation prompt
	URL   string // public URL of the generated image
}

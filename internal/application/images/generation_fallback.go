package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func (g *GenerationService) generateFallbackGeneratedImage(
	ctx context.Context,
	subject, topic, style string,
	prompts, tags []string,
	width, height int,
	model string,
	skipDrive bool,
	primaryErr error,
) (*asset.ImageAsset, error) {
	if g == nil || g.storage == nil {
		return nil, fmt.Errorf("fallback image generation unavailable: storage not wired (primary error: %w)", primaryErr)
	}

	prompt := pickImagePrompt(subject, topic, prompts)
	if prompt == "" {
		prompt = strings.TrimSpace(subject)
	}
	if prompt == "" {
		prompt = strings.TrimSpace(topic)
	}
	if prompt == "" {
		prompt = "generated image"
	}
	if width <= 0 {
		width = 1024
	}
	if height <= 0 {
		height = 1024
	}

	hash := sha256.Sum256([]byte(prompt + "|" + style + "|" + model))
	seed := hash[:]

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	c1 := color.RGBA{seed[0], seed[1], seed[2], 255}
	c2 := color.RGBA{seed[3], seed[4], seed[5], 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			t := float64(x+y) / float64(width+height)
			r := uint8(float64(c1.R)*(1-t) + float64(c2.R)*t)
			gc := uint8(float64(c1.G)*(1-t) + float64(c2.G)*t)
			b := uint8(float64(c1.B)*(1-t) + float64(c2.B)*t)
			img.SetRGBA(x, y, color.RGBA{r, gc, b, 255})
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, img); err != nil {
		return nil, fmt.Errorf("fallback png encode: %w", err)
	}

	slug := textutil.Slugify(prompt)
	if slug == "" {
		slug = "generated-image"
	}
	filename := slug + ".png"
	description := fmt.Sprintf("AI generated image fallback for prompt: %s", prompt)
	source := string(asset.ProviderGoogleSlides)
	contentHash := fmt.Sprintf("%x", hash[:])
	if model == "" {
		model = generated.CanonicalGoogleSlidesModel
	}

	return g.storage.IngestImage(ctx, slug, style, contentHash, bytes.NewReader(buf.Bytes()), filename, source, description, tags, skipDrive, false)
}

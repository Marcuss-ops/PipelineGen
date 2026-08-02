package images

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// decodeImageDimensions estrae larghezza e altezza da bytes immagine.
// Supporta JPEG, PNG, GIF. Per altri formati (webp, etc.) restituisce 0,0.
func decodeImageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// sha256Hash computes a short SHA-256 hash for idempotency keys.
func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}

// extractFilename extracts a filename from a URL with slug-based fallback.
func extractFilename(imgURL, fallback string) string {
	if idx := strings.LastIndex(imgURL, "/"); idx >= 0 {
		fn := imgURL[idx+1:]
		if qidx := strings.Index(fn, "?"); qidx >= 0 {
			fn = fn[:qidx]
		}
		if fn != "" && strings.Contains(fn, ".") {
			return fn
		}
	}
	return textutil.SlugifyWithMax(fallback, 100) + ".jpg"
}

// extractVQD parses the DuckDuckGo vqd token from the page HTML.
func extractVQD(html string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`vqd=['"](\d+(?:-\d+)?)['"]`),
		regexp.MustCompile(`"vqd":"(\d+(?:-\d+)?)"`),
	}
	for _, re := range patterns {
		matches := re.FindStringSubmatch(html)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}

// pickBestImage selects the largest image from DuckDuckGo results.
func pickBestImage(results []ddgImageResult) string {
	best := ""
	bestScore := 0
	for _, r := range results {
		img := r.Image
		if img == "" {
			if r.Thumbnail != "" {
				img = r.Thumbnail
			} else {
				continue
			}
		}
		if !strings.HasPrefix(img, "http") {
			continue
		}
		score := ddgImageScore(r)
		if score > bestScore {
			bestScore = score
			best = img
		}
	}
	return best
}

func uniqueAppend(slice []string, items ...string) []string {
	seen := make(map[string]bool)
	for _, s := range slice {
		seen[s] = true
	}
	for _, item := range items {
		if !seen[item] {
			slice = append(slice, item)
			seen[item] = true
		}
	}
	return slice
}

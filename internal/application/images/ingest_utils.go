package images

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

func (s *Service) aiImageDriveRootForSource(source, style string) string {
	if s == nil || s.cfg == nil {
		return ""
	}

	if !isAIImageSource(source) {
		return ""
	}

	styleFolders := map[string]string{
		"medieval":         "1yfCnjvpZ3ZuFs7W0pRFNGzapRLGIykPi",
		"whiteboard":       "1Znu_g8pUOXkXHG-1XkLMOcYN69umrlae",
		"anime":            "1e1pW8ZaQYTwDV0po6tIxx_vUql_6CD_v",
		"cinematic":        "1t6bhe8kquPqk7ypYzbobHqUq-HGjVdZw",
		"sketch":           "1QrC74aZ8It43pQa5l5G6BNWcc18ksIo2",
		"watercolor":       "1tzvn5PkOwZk3DPjjr8sIXKr9LKeM--rB",
		"cyberpunk":        "1x8xcUFtIj7hkGF6CsPJCM822ooJL9kMu",
		"realistic":        "1b5iP5aHekJUL1FB9ZC-WGkWxoDULyU9X",
		"heritage":         "1l_cdMqhKrstV94V7Ym7wemJTUZjjWLq_",
		"kawaii":           "1K5IcI3sC5qLID0M1ulSoUC355S_3lUNh",
		"professional-doc": "1g2Ef3yQCDWZ78YqnOnwhKmIghGJvPOPa",
		"cartoon":          "1ab_YSfuKpj4CCh9twk3st5zv9fvMwS8B",
		"retro-print":      "1141lRohkIiXp8NjGQlGj4bLLaQw6nCDb",
		"papercraft":       "1yWlji7wololy_q3l8GAcmmF8goxJmOih",
		"gothic":           "1CNNcNWY4YXyat9eqUsmsUEGeMmTXJY3t",
		"oil-painting":     "1mI07oRaeabhGSmjdyKOICl5vSK6uSO7i",
		"3d-render":        "1MWZy1rDXQKoAr0HRVMc7BdGAvqCaSe1y",
	}

	if folderID, ok := styleFolders[strings.ToLower(style)]; ok {
		return folderID
	}

	return s.cfg.Drive.ImagesFolder()
}

func isAIImageSource(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return false
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return false
	}

	switch source {
	case "google-flow",
		"google-vids",
		"google-vids-image",
		"google-slides",
		"nvidia",
		"nvidia-local",
		"local-nim",
		"flux-1-dev",
		"flux.1-schnell",
		"flux-1-schnell",
		"flux1-schnell",
		"flux-2-klein",
		"flux.2-klein-4b",
		"flux-2-klein-4b":
		return true
	default:
		return false
	}
}

// decodeImageDimensions estrae larghezza e altezza da bytes immagine.
// Supporta JPEG, PNG, GIF. Per altri formati (webp, etc.) restituisce 0,0.
func decodeImageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
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

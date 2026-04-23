package scriptdocs

import (
	"os"
	"strings"
	"time"

	"velox/go-master/internal/imagesdb"
)

var weakImageEntityTerms = map[string]bool{
	"above":        true,
	"stretching":   true,
	"dominating":   true,
	"however":       true,
	"romanian":      true,
	"therefore":     true,
	"although":      true,
	"because":       true,
	"while":         true,
	"during":        true,
	"between":       true,
	"through":       true,
	"without":       true,
	"within":        true,
	"around":        true,
	"regarding":     true,
}

func (s *ScriptDocService) SetImagesDB(db *imagesdb.ImageDB) {
	s.imagesDB = db
}

func (s *ScriptDocService) SetImageFinder(f imageFinderAPI) {
	if f != nil {
		s.imageFinder = f
	}
}

func (s *ScriptDocService) SetImageDownloader(d imageAssetDownloaderAPI) {
	if d != nil {
		s.imageDownloader = d
	}
}

func isWeakImageEntity(entity string) bool {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return true
	}
	key := normalizeKeyword(entity)
	if key == "" {
		return true
	}
	if weakImageEntityTerms[key] {
		return true
	}
	if len(strings.Fields(entity)) == 1 {
		if !isTitleLikeEntity(entity) && len(key) < 6 {
			return true
		}
	}
	return false
}

func anchorImageQuery(topic, entity string) string {
	topic = strings.TrimSpace(topic)
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return topic
	}
	if topic == "" {
		return entity
	}
	if strings.EqualFold(topic, entity) || strings.Contains(strings.ToLower(entity), strings.ToLower(topic)) {
		return entity
	}
	return strings.TrimSpace(topic + " " + entity)
}

func isTitleLikeEntity(entity string) bool {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return false
	}
	r := []rune(entity)
	first := r[0]
	if first < 'A' || first > 'Z' {
		return false
	}
	return true
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

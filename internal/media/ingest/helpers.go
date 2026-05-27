package ingest

import (
	"os"
	"regexp"
	"strings"

	"velox/go-master/internal/core/lifecycle"
)

func buildAssetID(kind Kind, hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return string(kind)
	}
	return string(kind) + ":" + hash
}

func toAssetKind(kind Kind) lifecycle.AssetKind {
	switch kind {
	case KindImage:
		return lifecycle.AssetKindImage
	case KindVoiceover:
		return lifecycle.AssetKindAudio
	case KindClip, KindStock:
		return lifecycle.AssetKindVideo
	default:
		return lifecycle.AssetKindDocument
	}
}

func normalizeKind(kind string) Kind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case string(KindImage):
		return KindImage
	case string(KindVoiceover):
		return KindVoiceover
	case string(KindClip):
		return KindClip
	case string(KindStock):
		return KindStock
	default:
		return ""
	}
}

func defaultGroupForKind(kind Kind, req *Request) string {
	switch kind {
	case KindImage:
		return slugOrFallback(firstNonEmpty(req.Group, req.Name, req.SourceID, "images"))
	case KindVoiceover:
		return slugOrFallback(firstNonEmpty(req.Group, req.Name, "voiceover"))
	case KindClip:
		return slugOrFallback(firstNonEmpty(req.Group, req.Source, req.Name, "clips"))
	case KindStock:
		return slugOrFallback(firstNonEmpty(req.Group, req.Source, req.Name, "stock"))
	default:
		return ""
	}
}

func slugOrFallback(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	slug := slugify(value)
	if slug == "" {
		return value
	}
	return slug
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile("[^a-z0-9]+")
	s = re.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func splitFolderPath(p string) []string {
	raw := strings.Split(p, "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func sameFile(a, b string) bool {
	aInfo, errA := os.Stat(a)
	bInfo, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(aInfo, bInfo)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

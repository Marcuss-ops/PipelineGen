package ingest

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
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

// shouldRejectAssetInput returns true when a file name or path clearly points
// to a non-media sidecar, temp artifact, or JSON metadata blob that must never
// enter the ingest pipeline as an indexable asset.
func shouldRejectAssetInput(pathOrName string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(pathOrName)))
	if base == "" || base == "." || base == ".." {
		return false
	}
	if base == "metadata.json" {
		return true
	}
	if strings.HasSuffix(base, ".json") {
		return true
	}
	switch filepath.Ext(base) {
	case ".tmp", ".temp", ".part", ".bak", ".swp", ".swo":
		return true
	}
	return strings.HasPrefix(base, ".")
}

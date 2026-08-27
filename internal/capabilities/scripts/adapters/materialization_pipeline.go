package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MaterializationPipelineItem is the explicit state passed between the
// materialization stages. It keeps source resolution, bytes, media facts and
// canonical asset construction separate from orchestration.
type MaterializationPipelineItem struct {
	Candidate     scriptpkg.SegmentAssetCandidate
	Source        string
	Bytes         []byte
	MediaType     string
	DurationUS    int64
	ContentSHA256 string
}

func resolveSource(candidate scriptpkg.SegmentAssetCandidate) (string, bool) {
	for _, source := range []string{candidate.SourceURL, candidate.DriveLink, candidate.LocalPath} {
		if source = strings.TrimSpace(source); source != "" {
			return source, true
		}
	}
	return "", false
}

func materializeBytes(item MaterializationPipelineItem, bytes []byte) MaterializationPipelineItem {
	item.Bytes = append([]byte(nil), bytes...)
	return item
}

func probeMedia(item MaterializationPipelineItem, mediaType string, durationUS int64) MaterializationPipelineItem {
	item.MediaType = strings.TrimSpace(mediaType)
	item.DurationUS = durationUS
	return item
}

func certifyContent(item MaterializationPipelineItem, sha256 string) MaterializationPipelineItem {
	item.ContentSHA256 = strings.TrimSpace(sha256)
	return item
}

func buildCanonicalAsset(item MaterializationPipelineItem) scriptpkg.SegmentAssetCandidate {
	candidate := item.Candidate
	candidate.SourceURL = item.Source
	if item.ContentSHA256 != "" {
		candidate.LegacyFileMD5 = item.ContentSHA256
	}
	return candidate
}

func commitCanonicalAsset(base []scriptpkg.SegmentAssetCandidate, asset scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	return appendProviderCandidatesUnique(base, []scriptpkg.SegmentAssetCandidate{asset})
}

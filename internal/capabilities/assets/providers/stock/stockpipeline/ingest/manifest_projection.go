package ingest

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// projectedManifestVideo holds one manifest artifact at its original
// index together with its explicit chunk index (when present).
type projectedManifestVideo struct {
	artifact job.Artifact
	position int
	index    int
	hasIndex bool
}

// projectManifestToPipelineResult converts the typed
// *job.ArtifactManifest into the legacy *PipelineResult used by
// pre-cutover callers via the ServiceRunner interface.
//
// Fail-closed (godlike/07 no-fake-availability): the function returns
// ErrStockManifestUnprojectable when the manifest cannot be projected
// into a meaningful result — nil manifest, zero artifacts, or no
// projectable artifacts (no video chunk AND no metadata). This
// prevents the silent-empty class where a SUCCEEDED job surfaced
// total_clips=0/total_chunks=0/chunks=[] despite real uploads.
//
// DEPRECATO: tenere solo per back-compat ServiceRunner.
func projectManifestToPipelineResult(manifest *job.ArtifactManifest) (*PipelineResult, error) {
	result := &PipelineResult{}
	if manifest == nil {
		return nil, ErrStockManifestUnprojectable
	}
	if len(manifest.Artifacts) == 0 {
		return nil, fmt.Errorf("%w: manifest %q carries zero artifacts", ErrStockManifestUnprojectable, manifest.JobID)
	}

	// The manifest is the canonical post-publication source. Keep the
	// legacy result deterministic: metadata is identified by kind, while
	// video artifacts are sorted by their explicit chunk index (not by
	// producer append order, which can vary when uploads run concurrently).
	var metadata *job.Artifact
	videoArtifacts := make([]projectedManifestVideo, 0)
	for position := range manifest.Artifacts {
		artifact := manifest.Artifacts[position]
		switch artifact.Kind {
		case job.ArtifactKindMetadata:
			if metadata == nil {
				metadata = &manifest.Artifacts[position]
			}
		case string(finalization.KindVideo):
			index, hasIndex := manifestIntValue(artifact.ArtifactMetadata, "chunk_index")
			videoArtifacts = append(videoArtifacts, projectedManifestVideo{
				artifact: artifact,
				position: position,
				index:    index,
				hasIndex: hasIndex,
			})
		}
	}
	// A manifest that is formally valid but carries neither a metadata
	// artifact nor any video chunk cannot be projected into a meaningful
	// legacy result (no links, no counts, no chunks). Failing closed here
	// keeps the SUCCEEDED-but-empty response class impossible.
	if metadata == nil && len(videoArtifacts) == 0 {
		return nil, fmt.Errorf("%w: manifest %q has %d artifacts but none projectable (no video chunk, no metadata)",
			ErrStockManifestUnprojectable, manifest.JobID, len(manifest.Artifacts))
	}

	hasManifestClipCount := false
	if metadata != nil {
		result.MetadataFileID = firstNonEmpty(
			metadata.RemoteFileID,
			manifestString(metadata.ArtifactMetadata, "drive_file_id"),
			manifestString(metadata.ArtifactMetadata, "file_id"),
		)
		result.MetadataLink = firstNonEmpty(
			metadata.RemoteWebViewLink,
			metadata.RemoteDownloadLink,
			manifestString(metadata.ArtifactMetadata, "drive_link"),
			manifestString(metadata.ArtifactMetadata, "drive_path"),
		)
		result.TotalClips = manifestInt(metadata.ArtifactMetadata, "total_clips")
		hasManifestClipCount = result.TotalClips > 0
	}

	sort.SliceStable(videoArtifacts, func(i, j int) bool {
		left, right := videoArtifacts[i], videoArtifacts[j]
		switch {
		case left.hasIndex && right.hasIndex:
			return left.index < right.index
		case left.hasIndex:
			return true
		case right.hasIndex:
			return false
		default:
			return left.position < right.position
		}
	})

	result.TotalChunks = len(videoArtifacts)
	result.Chunks = make([]ChunkResult, 0, len(videoArtifacts))
	usedChunkIndices := make(map[int]struct{}, len(videoArtifacts))
	for position, projected := range videoArtifacts {
		artifact := projected.artifact
		metadata := artifact.ArtifactMetadata
		index := projected.index
		if !projected.hasIndex {
			// Legacy manifests without chunk_index retain their stable
			// output order and start with their positional index.
			index = position
		}
		// A malformed manifest can contain duplicate explicit indices.
		// Preserve the first sorted artifact's requested index and move
		// later collisions to the next free deterministic index so the
		// legacy DTO never exposes duplicate chunk identities.
		for {
			if _, exists := usedChunkIndices[index]; !exists {
				break
			}
			index++
		}
		usedChunkIndices[index] = struct{}{}
		clipCount := manifestInt(metadata, "clip_count")
		if clipCount <= 0 {
			clipCount = 1
		}
		if !hasManifestClipCount {
			result.TotalClips += clipCount
		}
		driveFileID := firstNonEmpty(
			artifact.RemoteFileID,
			manifestString(metadata, "drive_file_id"),
			manifestString(metadata, "file_id"),
		)
		driveLink := firstNonEmpty(
			artifact.RemoteWebViewLink,
			manifestString(metadata, "drive_link"),
			manifestString(metadata, "drive_path"),
			artifact.RemoteDownloadLink,
		)
		hash := firstNonEmpty(artifact.SHA256, manifestString(metadata, "sha256"))
		uploaded := driveFileID != "" || driveLink != "" || artifact.RemoteDownloadLink != ""
		chunk := ChunkResult{
			Index:         index,
			TimelineStart: manifestFloat(metadata, "start_sec"),
			TimelineEnd:   manifestFloat(metadata, "end_sec"),
			LocalPath:     artifact.Path,
			DriveLink:     driveLink,
			DownloadLink:  artifact.RemoteDownloadLink,
			DriveFileID:   driveFileID,
			SHA256:        hash,
			Title:         manifestString(metadata, "title"), Rendered: artifact.Path != "",
			Uploaded: uploaded,
		}
		chunk.SourceIDs = manifestStringSlice(metadata, "source_ids")
		if len(chunk.SourceIDs) == 0 {
			chunk.SourceIDs = manifestStringSlice(metadata, "source_urls")
		}
		if len(chunk.SourceIDs) == 0 {
			if sourceURL := manifestString(metadata, "source_url"); sourceURL != "" {
				chunk.SourceIDs = []string{sourceURL}
			}
		}
		result.Chunks = append(result.Chunks, chunk)
	}
	if result.TotalClips == 0 {
		// Older manifests did not carry per-artifact clip counts. The
		// current stock pipeline emits one video artifact per clip, so
		// the number of video artifacts is the safe compatibility fallback.
		result.TotalClips = result.TotalChunks
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func manifestString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func manifestStringSlice(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	switch typed := values[key].(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			if text, ok := value.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func manifestFloat(values map[string]any, key string) float64 {
	if values == nil {
		return 0
	}
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	default:
		parsed, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
		return parsed
	}
}

func manifestIntValue(values map[string]any, key string) (int, bool) {
	if values == nil {
		return 0, false
	}
	if _, ok := values[key]; !ok {
		return 0, false
	}
	return int(manifestFloat(values, key)), true
}

func manifestInt(values map[string]any, key string) int {
	return int(manifestFloat(values, key))
}

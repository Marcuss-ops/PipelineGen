// Package clipindexer — helpers for parsing the embedding-server
// JSON responses.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): readJSONResponse was
// DELETED from this file. Its only consumer was indexing_api.go, itself removed
// with the legacy SQLite→Qdrant implementation, and `deadcode -test ./...`
// reported it as unreachable. The surviving helpers parse an already-decoded
// JSON object, so net/http, io and encoding/json are no longer imported here.
package clipindexer

import (
	"fmt"
)

// extractEmbedding returns the "embedding" array (slice of float64) from
// a sidecar JSON response, validating dimension > 0.
func extractEmbedding(body map[string]any) ([]float64, error) {
	return extractEmbeddingField(body, "embedding")
}

// extractEmbeddingField reads body[fieldName] as []float64. Returns a
// typed error if the field is missing or has the wrong shape.
func extractEmbeddingField(body map[string]any, fieldName string) ([]float64, error) {
	if body == nil {
		return nil, fmt.Errorf("nil response body")
	}
	raw, ok := body[fieldName]
	if !ok {
		return nil, fmt.Errorf("response missing field %q", fieldName)
	}
	vec, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("response field %q is not an array", fieldName)
	}
	out := make([]float64, 0, len(vec))
	for _, v := range vec {
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("field %q contains non-numeric value", fieldName)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("field %q is empty", fieldName)
	}
	return out, nil
}

// averageFrameEmbeddings averages frame-level vectors element-wise. The
// result is the same length as each input frame; callers can persist as
// media_assets.visual_embedding.
func averageFrameEmbeddings(frames []any) ([]float64, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames to average")
	}
	first, ok := frames[0].([]any)
	if !ok || len(first) == 0 {
		return nil, fmt.Errorf("first frame is not a vector")
	}
	dim := len(first)
	sums := make([]float64, dim)
	filled := 0
	for _, frame := range frames {
		vec, ok := frame.([]any)
		if !ok || len(vec) != dim {
			continue
		}
		for i, v := range vec {
			f, ok := v.(float64)
			if !ok {
				continue
			}
			sums[i] += f
		}
		filled++
	}
	if filled == 0 {
		return nil, fmt.Errorf("no frames with matching dimension")
	}
	out := make([]float64, dim)
	for i := range sums {
		out[i] = sums[i] / float64(filled)
	}
	return out, nil
}

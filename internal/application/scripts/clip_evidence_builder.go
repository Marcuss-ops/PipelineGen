// Package scripts — clip_evidence_builder.go converts the pack
// returned by ClipSourceBuilder.BuildClipContext (currently
// map[string]any for back-compat) into the typed ClipEvidence
// domain contract.
//
// This is a transitional adapter. When ClipSourceBuilder is
// migrated to return ClipEvidence directly (PR7), this file
// becomes the canonical constructor and the pack→evidence
// conversion is dropped.
package scripts

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildClipEvidence converts a ClipSourceBuilder output pack into
// a typed ClipEvidence. The pack shape is:
//
//   {
//     "clip_ids":         []string,
//     "clip_names":       []string,
//     "clip_drive_links": map[string]string,
//     "title":            string,
//     "language":         string,
//     "tone":             string,
//     "model":            string,
//     "clip_count":       int,
//   }
//
// Returns nil if the pack is nil or empty.
func BuildClipEvidence(pack interface{}, sourceText string) *scriptpkg.ClipEvidence {
	if pack == nil {
		return nil
	}
	m, ok := pack.(map[string]any)
	if !ok || m == nil {
		return nil
	}

	clipIDs := stringSlice(m["clip_ids"])
	if len(clipIDs) == 0 {
		return nil
	}

	ev := &scriptpkg.ClipEvidence{
		ClipIDs:       clipIDs,
		ClipCount:     len(clipIDs),
		AssembledText: strings.TrimSpace(sourceText),
		DriveLinks:    stringMap(m["clip_drive_links"]),
	}

	// Build excluded clips from any clips in the pack that failed
	// quality checks (populated by the resolver after BuildClipContext).
	if excluded, ok := m["excluded_clips"].([]map[string]any); ok {
		for _, ex := range excluded {
			id, _ := ex["clip_id"].(string)
			reason, _ := ex["reason"].(string)
			if id != "" {
				ev.Excluded = append(ev.Excluded, scriptpkg.ExcludedClip{
					ClipID: id,
					Reason: reason,
				})
			}
		}
	}

	return ev
}

// stringSlice extracts a []string from an interface{} value.
func stringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	raw, ok := v.([]string)
	if ok {
		return raw
	}
	// Handle []interface{} (JSON unmarshal may produce this).
	if ifaceSlice, ok := v.([]interface{}); ok {
		out := make([]string, 0, len(ifaceSlice))
		for _, item := range ifaceSlice {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// stringMap extracts a map[string]string from an interface{} value.
func stringMap(v interface{}) map[string]string {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]string)
	if ok {
		return m
	}
	// Handle map[string]interface{} (JSON unmarshal may produce this).
	if ifaceMap, ok := v.(map[string]interface{}); ok {
		out := make(map[string]string, len(ifaceMap))
		for k, val := range ifaceMap {
			if s, ok := val.(string); ok {
				out[k] = s
			} else {
				out[k] = fmt.Sprintf("%v", val)
			}
		}
		return out
	}
	return nil
}

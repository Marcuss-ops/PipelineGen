package audio

import (
	"encoding/json"
	"fmt"
	"math"
)

// MicrosecondsFromMilliseconds is the explicit legacy-boundary conversion
// helper for producers whose input DTOs still expose milliseconds.
func MicrosecondsFromMilliseconds(milliseconds int64) (int64, error) {
	return microsFromMillis(milliseconds)
}

func microsFromMillis(milliseconds int64) (int64, error) {
	if milliseconds < 0 || milliseconds > math.MaxInt64/1000 {
		return 0, fmt.Errorf("milliseconds value %d cannot be represented as microseconds", milliseconds)
	}
	return milliseconds * 1000, nil
}

// hasLegacyTimelineFields reports whether a wire payload still carries
// millisecond-era fields. The canonical v2 contract rejects them, so the
// boundary can return a typed error instead of silently converting.
func hasLegacyTimelineFields(data []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return false
	}
	legacyKeys := []string{"duration_ms", "timeline_start_ms", "source_in_ms", "source_out_ms"}
	for _, key := range legacyKeys {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	var segments []map[string]json.RawMessage
	if value, ok := raw["segments"]; ok && json.Unmarshal(value, &segments) == nil {
		for _, segment := range segments {
			for _, key := range legacyKeys {
				if _, ok := segment[key]; ok {
					return true
				}
			}
			for _, nestedKey := range []string{"video", "audio"} {
				var nested map[string]json.RawMessage
				if value, ok := segment[nestedKey]; ok && json.Unmarshal(value, &nested) == nil {
					for _, key := range legacyKeys[1:] {
						if _, ok := nested[key]; ok {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

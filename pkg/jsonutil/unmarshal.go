// Package jsonutil provides shared JSON decoding helpers. Leaf package:
// no internal imports.
//
// The repo rule is that persisted records must never silently degrade to
// zero-value structs on corrupt metadata. UnmarshalOrLog is the canonical
// fail-open-with-observability seam: callers that intend to fall back to a
// zero value (auxiliary metadata, telemetry merge) go through this helper so
// the degradation is always logged. Callers that cannot tolerate a fallback
// should propagate the error instead.
package jsonutil

import (
	"encoding/json"

	"go.uber.org/zap"
)

// UnmarshalOrLog decodes data into v exactly like json.Unmarshal, but logs a
// structured warning on failure instead of letting the caller swallow the
// error. It returns true on success and false on failure; v is left
// untouched when decoding fails.
//
// log may be nil — a nil logger skips the log line (callers without a
// logger should prefer explicit error propagation over a silent nil).
// what is a short stable identifier of the field being decoded (e.g.
// "media_assets.tags") so log aggregation can group degradation by source.
func UnmarshalOrLog(data []byte, v any, what string, log *zap.Logger) bool {
	if err := json.Unmarshal(data, v); err == nil {
		return true
	} else {
		if log != nil {
			log.Warn("json unmarshal failed; record degrades to zero value",
				zap.String("context", what),
				zap.Int("bytes", len(data)),
				zap.Error(err),
			)
		}
		return false
	}
}

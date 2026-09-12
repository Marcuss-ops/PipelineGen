package overlays

import "encoding/json"

// MarshalJSON keeps the millisecond timing tuple complete on the wire.
// StartMs/EndMs remain the canonical projection of the microsecond timeline;
// duration_ms is derived rather than stored so constructors cannot let the
// three values drift independently.
func (i OverlayItem) MarshalJSON() ([]byte, error) {
	type alias OverlayItem
	return json.Marshal(struct {
		alias
		DurationMS int64 `json:"duration_ms"`
	}{
		alias:      alias(i),
		DurationMS: i.EndMs - i.StartMs,
	})
}

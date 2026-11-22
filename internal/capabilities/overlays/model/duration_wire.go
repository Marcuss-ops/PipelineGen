package model

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

// UnmarshalJSON hydrates the typed chain projection at the wire boundary.
// ChainSegments is never decoded from a sibling JSON key (it is tagged "-"),
// so the typed view cannot drift from the durable Params slot on reload.
func (i *OverlayItem) UnmarshalJSON(data []byte) error {
	type alias OverlayItem
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*i = OverlayItem(decoded)
	if segments, ok := decodeChainSegments(i.Params); ok {
		i.ChainSegments = segments
	}
	return nil
}

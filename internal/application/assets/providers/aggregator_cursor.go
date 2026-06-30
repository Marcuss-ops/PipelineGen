package providers

import (
	"encoding/base64"
	"encoding/json"
)

// ── Cursor ─────────────────────────────────────────────────────────────

// cursorPayload is the unencoded (JSON-friendly) checkpoint built
// per Aggregate response. Opaque to callers — emitted + consumed via
// base64(JSON) by encodeCursor/decodeCursor.
type cursorPayload struct {
	// lastSeenProvider is the most recently returned provider name.
	// Best-effort: tells the next aggregator call which provider
	// "led" the previous page so subsequent pages can refine.
	lastSeenProvider string
	// lastSeenOffset is the next-page offset within the leading
	// provider. Aggregator re-reads it on decode to build the next
	// per-provider Limit hint.
	lastSeenOffset int
}

// LimitHintForProvider returns the per-provider NextPage hint. When
// the decoded cursor is empty, the hint is 0 — providers apply
// their native default (provider-default limit).
func (c *cursorPayload) LimitHintForProvider(_ string) int {
	if c == nil {
		return 0
	}
	return c.lastSeenOffset
}

func (c cursorPayload) MarshalJSON() ([]byte, error) {
	type payload struct {
		LastSeenProvider string `json:"last_seen_provider"`
		LastSeenOffset   int    `json:"last_seen_offset"`
	}
	return json.Marshal(payload{
		LastSeenProvider: c.lastSeenProvider,
		LastSeenOffset:   c.lastSeenOffset,
	})
}

func (c *cursorPayload) UnmarshalJSON(data []byte) error {
	type payload struct {
		LastSeenProvider string `json:"last_seen_provider"`
		LastSeenOffset   int    `json:"last_seen_offset"`
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c.lastSeenProvider = p.LastSeenProvider
	c.lastSeenOffset = p.LastSeenOffset
	return nil
}

// encodeCursor returns a base64(JSON) opaque string. Decode failures
// yield an empty payload (best-effort fallback).
func encodeCursor(p *cursorPayload) string {
	if p == nil {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor reads the base64(JSON) opaque cursor. Returns
// (nil, false) on decode failure so the caller can fall back to
// first-page semantics. Never returns an error to keep the API
// ergonomic — Cursor is best-effort pagination, not strict.
func decodeCursor(s string) (*cursorPayload, bool) {
	if s == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, false
	}
	return &p, true
}

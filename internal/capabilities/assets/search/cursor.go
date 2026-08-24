package assets

import (
	"encoding/base64"
	"encoding/json"
)

// cursorCodecVersion pins the wire-shape version. The decoder rejects
// cursors with v != 1 so a future layout bump doesn't silently
// mis-decode old cursors — a forward-only migration guard.
const cursorCodecVersion = 1

// fingerprintItem is the per-item representation embedded in the
// cursor JSON. Source + SourceRef together carry the 4-key dedup
// identity (AssetID, Source+"|"+SourceRef compound, SourceRef
// solo, canonicalURL, Hash). Score is preserved for ranking
// continuity across pages — clients can recover a stable order
// from the cursor alone if Summary/Candidate emission changed.
//
// SourceRef (PR 9, June 2026) added so the page-2 cursor can skip
// provider-backend candidates whose AssetID is empty. The wire
// schema is additive (omitempty); v1 cursors round-trip clean —
// the JSON decoder fills the missing field with "".
type fingerprintItem struct {
	AssetID   string  `json:"a"`
	Source    string  `json:"src"`
	SourceRef string  `json:"sr,omitempty"`
	Score     float64 `json:"s"`
}

// fingerprintBlob is the canonical on-wire representation. No
// rolling checksums or HMACs — the cursor is opaque to clients and
// the aggregator treats tampered cursors as ErrInvalidCursor.
type fingerprintBlob struct {
	Version int               `json:"v"`
	Items   []fingerprintItem `json:"items"`
}

// EncodeCursor serialises a Cursor (the in-memory string form, NOT
// yet base64-wrapped) to the wire-encoded form (base64url). Empty
// Cursor returns "" (no encoding needed; round-trip stable).
//
// The in-memory form is the marshalled JSON itself; the wire form
// is base64 of that JSON. This split lets tests construct Cursor
// values without base64 padding concerns.
func EncodeCursor(c Cursor) (string, error) {
	if c == "" {
		return "", nil
	}
	blob := fingerprintBlob{Version: cursorCodecVersion}
	if err := json.Unmarshal([]byte(string(c)), &blob); err != nil {
		return "", ErrInvalidCursor
	}
	if blob.Version != cursorCodecVersion {
		return "", ErrInvalidCursor
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EncodeCursorFromItems builds a Cursor from the last page of items
// already served to the client. The resulting Cursor lists exactly
// those items so the aggregator can skip them on the next call.
// Returns ErrEmptyCandidate if no item has any identity field set
// (AssetID || SourceRef || Source || Score).
func EncodeCursorFromItems(items []Candidate) (Cursor, error) {
	blob := fingerprintBlob{
		Version: cursorCodecVersion,
		Items:   make([]fingerprintItem, 0, len(items)),
	}
	for _, c := range items {
		if c.AssetID == "" && c.Source == "" && c.SourceRef == "" && c.Score == 0 {
			continue
		}
		blob.Items = append(blob.Items, fingerprintItem{
			AssetID:   c.AssetID,
			Score:     c.Score,
			Source:    c.Source,
			SourceRef: c.SourceRef,
		})
	}
	if len(blob.Items) == 0 {
		return "", ErrEmptyCandidate
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return "", err
	}
	return Cursor(raw), nil
}

// DecodeCursor parses a wire string back to a Cursor. Validates
// base64 decode, JSON shape, and the version marker. Returns
// ErrInvalidCursor for every malformed input — the caller (handler)
// maps it to HTTP 422.
//
// Note: DecodeCursor does NOT validate item identities — callers
// are responsible for ensuring seeded items pass dedup policy.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", ErrInvalidCursor
	}
	var blob fingerprintBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		return "", ErrInvalidCursor
	}
	if blob.Version != cursorCodecVersion {
		return "", ErrInvalidCursor
	}
	canonical, err := json.Marshal(blob)
	if err != nil {
		return "", ErrInvalidCursor
	}
	return Cursor(canonical), nil
}

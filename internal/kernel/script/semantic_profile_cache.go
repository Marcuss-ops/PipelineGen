package script

import (
	"fmt"
	"strings"
)

// SegmentSemanticProfileKey is the complete identity of a cached semantic
// profile. Any text or semantic-pipeline version change produces a different
// key and therefore invalidates the previous value without serving stale data.
type SegmentSemanticProfileKey struct {
	SegmentID                 string
	TextHash                  string
	UnderstandingModelVersion string
	PromptVersion             string
}

// String returns a deterministic, unambiguous representation of the key.
func (k SegmentSemanticProfileKey) String() string {
	return strings.Join([]string{
		k.SegmentID,
		k.TextHash,
		k.UnderstandingModelVersion,
		k.PromptVersion,
	}, "\x00")
}

// Key returns the cache identity for a profile.
func (p SegmentSemanticProfile) Key() SegmentSemanticProfileKey {
	return SegmentSemanticProfileKey{
		SegmentID:                 strings.TrimSpace(p.SegmentID),
		TextHash:                  strings.TrimSpace(p.TextHash),
		UnderstandingModelVersion: strings.TrimSpace(p.UnderstandingModelVersion),
		PromptVersion:             strings.TrimSpace(p.PromptVersion),
	}
}

// Fingerprint returns the stable serialized identity used by cache and
// persistence layers. It is empty for an incomplete profile identity.
func (p SegmentSemanticProfile) Fingerprint() string {
	key := p.Key()
	if _, err := NewSegmentSemanticProfileKey(key.SegmentID, key.TextHash, key.UnderstandingModelVersion, key.PromptVersion); err != nil {
		return ""
	}
	return key.String()
}

// NewSegmentSemanticProfileKey creates a normalized cache key from the
// canonical identity fields. It rejects incomplete identity so an empty key
// can never accidentally become a shared cache entry.
func NewSegmentSemanticProfileKey(segmentID, textHash, modelVersion, promptVersion string) (SegmentSemanticProfileKey, error) {
	key := SegmentSemanticProfileKey{
		SegmentID:                 strings.TrimSpace(segmentID),
		TextHash:                  strings.TrimSpace(textHash),
		UnderstandingModelVersion: strings.TrimSpace(modelVersion),
		PromptVersion:             strings.TrimSpace(promptVersion),
	}
	if key.SegmentID == "" || key.TextHash == "" || key.UnderstandingModelVersion == "" || key.PromptVersion == "" {
		return SegmentSemanticProfileKey{}, fmt.Errorf("segment semantic profile cache key requires segment_id, text_hash, model version and prompt version")
	}
	return key, nil
}

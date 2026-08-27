package script

import (
	"fmt"
	"strings"
	"sync"
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
		SegmentID:                 p.SegmentID,
		TextHash:                  p.TextHash,
		UnderstandingModelVersion: p.UnderstandingModelVersion,
		PromptVersion:             p.PromptVersion,
	}
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

// SegmentSemanticProfileCache is a bounded-domain, thread-safe in-memory
// cache. Put replaces only the exact key; version or text changes naturally
// miss, while Invalidate removes all profiles for a segment.
type SegmentSemanticProfileCache struct {
	mu      sync.RWMutex
	entries map[string]SegmentSemanticProfile
}

func NewSegmentSemanticProfileCache() *SegmentSemanticProfileCache {
	return &SegmentSemanticProfileCache{entries: make(map[string]SegmentSemanticProfile)}
}

// Get returns a defensive copy and reports whether the exact semantic
// identity was found.
func (c *SegmentSemanticProfileCache) Get(key SegmentSemanticProfileKey) (SegmentSemanticProfile, bool) {
	if c == nil {
		return SegmentSemanticProfile{}, false
	}
	c.mu.RLock()
	profile, ok := c.entries[key.String()]
	c.mu.RUnlock()
	if !ok {
		return SegmentSemanticProfile{}, false
	}
	return profile.Clone(), true
}

// Put stores a defensive copy under the profile's complete identity.
func (c *SegmentSemanticProfileCache) Put(profile SegmentSemanticProfile) error {
	if c == nil {
		return fmt.Errorf("segment semantic profile cache is nil")
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	key, err := NewSegmentSemanticProfileKey(profile.SegmentID, profile.TextHash, profile.UnderstandingModelVersion, profile.PromptVersion)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.entries[key.String()] = profile.Clone()
	c.mu.Unlock()
	return nil
}

// Invalidate removes all cached versions for one segment. This is used when
// the segment is deleted or when callers explicitly request a cold rebuild.
func (c *SegmentSemanticProfileCache) Invalidate(segmentID string) int {
	if c == nil {
		return 0
	}
	segmentID = strings.TrimSpace(segmentID)
	if segmentID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for encoded := range c.entries {
		key := strings.SplitN(encoded, "\x00", 2)
		if len(key) == 2 && key[0] == segmentID {
			delete(c.entries, encoded)
			removed++
		}
	}
	return removed
}

// Len returns the number of cached profile identities.
func (c *SegmentSemanticProfileCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

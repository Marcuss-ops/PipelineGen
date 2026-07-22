// Package script — research_cache.go is the canonical SSOT for
// research_cache identities and records.
//
// The cache key is a SHA-256 hex digest computed from the stable
// tuple (topic_fingerprint + language + research_version +
// source_fingerprint + max_steps). Every consumer that stores or
// looks up research_cache entries must use ComputeResearchCacheKey
// so the keyspace stays deterministic.
package script

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ResearchCacheRecord is the canonical shape of one research_cache row.
// It is deliberately a value struct: callers create a record, compute
// the key via ComputeResearchCacheKey, and pass it to a repository.
type ResearchCacheRecord struct {
	Key               string    // SHA256 cache key; PRIMARY KEY in SQLite.
	Topic             string    // Human-readable topic used as fallback.
	Language          string    // Target language of the source text.
	MaxSteps          int       // Research depth / max steps requested.
	SourceText        string    // The cached research text.
	ConceptID         string    // Optional media_concepts.id that produced this text.
	TopicFingerprint  string    // Stable topic identity (e.g., SHA256 of normalized topic).
	SourceFingerprint string    // Stable identity of the source material (URL, asset, document).
	ResolverVersion   string    // Version of the source resolver that produced the text.
	ResearchVersion   string    // Version of the research strategy / prompts.
	HitCount          int       // How many times the row has been reused.
	ExpiresAt         time.Time // Absolute TTL.
	CreatedAt         time.Time // Row creation time.
	UpdatedAt         time.Time // Last mutation time.
}

// ComputeResearchCacheKey returns the canonical SHA-256 hex key for a
// research_cache entry. The keyspace is defined as:
//
//	SHA256(topic_fingerprint + ":" + language + ":" + research_version + ":" + source_fingerprint + ":" + max_steps)
//
// The separators prevent collisions between adjacent components (e.g.
// topic_fingerprint="ab" + language="c" vs topic_fingerprint="a" +
// language="bc"). max_steps is represented as a decimal integer so the
// same source material researched with different depths produces
// distinct keys.
func ComputeResearchCacheKey(topicFingerprint, language, researchVersion, sourceFingerprint string, maxSteps int) string {
	topicFingerprint = strings.TrimSpace(topicFingerprint)
	language = strings.TrimSpace(language)
	researchVersion = strings.TrimSpace(researchVersion)
	sourceFingerprint = strings.TrimSpace(sourceFingerprint)

	payload := fmt.Sprintf(
		"%s:%s:%s:%s:%d",
		topicFingerprint,
		language,
		researchVersion,
		sourceFingerprint,
		maxSteps,
	)

	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

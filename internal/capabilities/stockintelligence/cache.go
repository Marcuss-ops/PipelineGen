// Package stockintelligence — cache.go is the content-addressed replay
// cache for the expensive I/O portion of Resolve (local search + hydrate +
// provider fallback). The cache is a REPLAY PROJECTION, never truth: a cache
// outage degrades to live discovery (the same contract as the VidRush
// per-query image caches), and only non-empty candidate sets are stored so a
// temporary provider observation can never poison later scenes.
//
// Why the key is query+policy+thresholds and NOT segment/subject/terms: the
// cached work product (the merged candidate set) is a pure function of the
// provider query, the effective thresholds and the plan's provider policy —
// local search and provider fallback consume only req.Query. Subject, visual
// terms and the sampling segment are PER-REQUEST sampler inputs: the winner
// is always re-sampled fresh against them, and provider candidates are
// re-stamped to the sampling segment on every serve (owner stamps are what
// the MediaSampler's owner_mismatch gate reads, so caching them across
// segments would silently change selection). Reuse therefore works across
// segments that resolve the same entity/query ("~0 on entity reuse") without
// ever aliasing one scene's ownership onto another. If resolve I/O ever
// becomes dependent on more inputs, resolveCacheSchemaVersion must change.
package stockintelligence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// resolveCacheSchemaVersion is the cache key's content schema. Any change to
// what Resolve caches (or to the cached entry's shape) MUST bump it so old
// rows can never satisfy a new reader.
const resolveCacheSchemaVersion = "stock-resolve-cache.v1"

// ResolveCache is the durable content-addressed seam behind Resolve's
// discovery work. Implementations belong to infrastructure (the canonical
// one pairs a bounded L1 LRU with the durable VidRushCachePort L2); the
// resolver only exchanges JSON payloads through this typed port. Errors are
// deliberately tolerated by the caller: a Get error is treated as a miss and
// a Put error is dropped, because the cache is a replay projection and never
// a correctness gate.
type ResolveCache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, payload []byte) error
}

// resolveCacheEntry is the request-independent work product of one live
// discovery pass. It is stored PRE-stamp: provider candidates keep the owner
// they were discovered with (usually empty) and are stamped with the sampling
// segment on every serve, exactly like the live path.
type resolveCacheEntry struct {
	Candidates           []Candidate `json:"candidates"`
	LocalCandidateCount  int         `json:"local_candidate_count"`
	FallbackReason       string      `json:"fallback_reason,omitempty"`
	ProviderLiveRequests int         `json:"provider_live_requests"`
	// MergedProvider records that the provider search succeeded and its
	// candidates were merged (it selects the sampler's error label and
	// preserves the historical branch semantics on replay).
	MergedProvider bool `json:"merged_provider,omitempty"`
}

func (e resolveCacheEntry) clone() resolveCacheEntry {
	out := e
	out.Candidates = cloneCandidates(e.Candidates)
	return out
}

func cloneCandidates(candidates []Candidate) []Candidate {
	if candidates == nil {
		return nil
	}
	out := make([]Candidate, len(candidates))
	copy(out, candidates)
	return out
}

// stampProviderCandidateOwners binds provider candidates without an owner to
// the segment currently being sampled. It mirrors the live merge exactly:
// only empty provider owners are stamped, so a candidate discovered FOR a
// specific segment keeps that owner and the sampler's owner_mismatch gate
// stays authoritative.
func stampProviderCandidateOwners(candidates []Candidate, segmentID string) {
	for i := range candidates {
		if candidates[i].Source == "provider" && strings.TrimSpace(candidates[i].OwnerSegmentID) == "" {
			candidates[i].OwnerSegmentID = segmentID
		}
	}
}

// resolveCacheKey derives the content-addressed identity of one discovery
// pass: schema version + normalized provider query + provider-policy
// signature + the effective thresholds and limit. Length-prefixed fields keep
// user-derived text from colliding across field boundaries.
func resolveCacheKey(req ResolveRequest) string {
	threshold, minQuality, limit := effectiveResolveLimits(req)
	h := digest.NewSHA256()
	writeResolveCacheKeyField(h, resolveCacheSchemaVersion)
	writeResolveCacheKeyField(h, normalizeResolveCacheText(req.Query))
	writeResolveCacheKeyField(h, strings.TrimSpace(req.PolicySignature))
	writeResolveCacheKeyField(h, strconv.Itoa(threshold))
	writeResolveCacheKeyField(h, strconv.FormatFloat(float64(minQuality), 'g', -1, 32))
	writeResolveCacheKeyField(h, strconv.Itoa(limit))
	return hex.EncodeToString(h.Sum(nil))
}

func writeResolveCacheKeyField(h hash.Hash, value string) {
	_, _ = fmt.Fprintf(h, "%d:", len(value))
	_, _ = h.Write([]byte(value))
}

func normalizeResolveCacheText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// effectiveResolveLimits resolves the request's zero values onto the same
// defaults Resolve applies, so an implicit and an explicit default share one
// cache entry.
func effectiveResolveLimits(req ResolveRequest) (threshold int, minQuality float32, limit int) {
	threshold = req.LocalCandidateThreshold
	if threshold == 0 {
		threshold = defaultLocalCandidateThreshold
	}
	minQuality = req.MinimumQuality
	if minQuality == 0 {
		minQuality = defaultMinimumQuality
	}
	return threshold, minQuality, defaultSearchLimit
}

// loadResolveCacheEntry reads one entry. A missing, malformed or unreadable
// row is a miss, never an error: the cache is a replay projection.
func loadResolveCacheEntry(ctx context.Context, cache ResolveCache, key string) (resolveCacheEntry, bool) {
	if cache == nil || key == "" {
		return resolveCacheEntry{}, false
	}
	raw, hit, err := cache.Get(ctx, key)
	if err != nil || !hit || len(raw) == 0 {
		return resolveCacheEntry{}, false
	}
	var entry resolveCacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return resolveCacheEntry{}, false
	}
	if len(entry.Candidates) == 0 {
		return resolveCacheEntry{}, false
	}
	return entry, true
}

// storeResolveCacheEntry persists one entry. Failures are dropped: losing the
// replay projection must never fail a live resolution that already has its
// candidates.
func storeResolveCacheEntry(ctx context.Context, cache ResolveCache, key string, entry resolveCacheEntry) {
	if cache == nil || key == "" || len(entry.Candidates) == 0 {
		return
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = cache.Put(ctx, key, raw)
}

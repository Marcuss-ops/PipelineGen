package linguistics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

// ProfileCount returns the number of explicit (non-fallback) language
// profiles currently loaded in the registry.
//
// Phase 8 split: scoring domain. ProfileCount is the canonical
// cardinality accessor used by operator dashboards and health checks
// (e.g., /api/lexicon/status). Reads mu.RLock-protected profiles map.
func (r *LexiconRegistry) ProfileCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.profiles)
}

// Version returns a hash-based version string that changes when any
// loaded profile changes. The fingerprint is computed at construction
// (NewLexiconRegistry sets r.version) and memoised; if r.version was
// somehow empty, a live fingerprint is recomputed.
//
// Phase 8 split: scoring domain. Version is the canonical change-
// detector used by caches that depend on lexicon content (e.g.,
// Qdrant embedding cache invalidation, language-detector warming).
func (r *LexiconRegistry) Version() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.version != "" {
		return r.version
	}
	return fingerprintLexiconRegistry(r.profiles, r.fallback)
}

// fingerprintLexiconRegistry computes a deterministic SHA-256-based
// hash over every loaded profile (sorted by key for stability) plus
// the fallback profile. The output is "lexicon-<16hex>" so it's
// recognisable in cache keys and log lines.
func fingerprintLexiconRegistry(profiles map[string]*LexiconProfile, fallback *LexiconProfile) string {
	h := sha256.New()

	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		writeLexiconFingerprint(h, key, profiles[key])
	}
	writeLexiconFingerprint(h, "_fallback", fallback)

	return "lexicon-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// writeLexiconFingerprint appends a single profile's content to the
// hash. The label prefixes (stop/func/block/neg/visual/verb/policy)
// ensure that two profiles with identical sets but different field
// shapes (e.g., one with VerbSuffixes and another without) hash
// differently — preserving the SSOT invariant that any semantic
// change yields a different Version.
func writeLexiconFingerprint(h hash.Hash, key string, p *LexiconProfile) {
	if p == nil {
		_, _ = h.Write([]byte(key + ":nil\n"))
		return
	}
	_, _ = h.Write([]byte(key + ":"))
	writeSortedSetFingerprint(h, "stop", p.StopWords)
	writeSortedSetFingerprint(h, "func", p.FunctionWords)
	writeSortedSetFingerprint(h, "block", p.EntityBlocklist)
	writeSortedSetFingerprint(h, "neg", p.NegativeParticles)
	writeSortedSetFingerprint(h, "visual", p.VisualVerbs)
	_, _ = h.Write([]byte("verb:"))
	for _, suffix := range p.VerbSuffixes {
		_, _ = h.Write([]byte(suffix))
		_, _ = h.Write([]byte("\n"))
	}
	_, _ = h.Write([]byte(fmt.Sprintf("policy:%d,%d,%d,%t\n",
		p.PhrasePolicy.MinWords,
		p.PhrasePolicy.MaxWords,
		p.PhrasePolicy.MaxResults,
		p.PhrasePolicy.RejectVerbsWhenAll,
	)))
}

// writeSortedSetFingerprint appends a label + sorted-key enumeration
// of the set to the hash. Sort is mandatory so two profiles built
// from the same data in different map-iteration order produce the
// same Version.
func writeSortedSetFingerprint(h hash.Hash, label string, set map[string]struct{}) {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = h.Write([]byte(label + ":"))
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte("\n"))
	}
}

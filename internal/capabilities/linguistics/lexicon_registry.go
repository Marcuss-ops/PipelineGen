// Package linguistics is the canonical home of language-linguistic
// data used across the pipeline. It owns the LexiconRegistry, which
// is the single source of truth for per-language profiles loaded from
// config/lexicons at bootstrap.
//
// godlike/06 SSOT: every stop-word map, function-word map, verb-suffix
// list and entity-blocklist in the codebase must originate from this
// package. No hardcoded maps elsewhere, no second representation.
//
// Phase 8 split (implemented lexical domains: morphology / stop-words /
// aliasing / scoring). This file holds ONLY the canonical LexiconRegistry
// struct, the file-loading constructor NewLexiconRegistry, the panic-on-error
// variant MustNewLexiconRegistry, and the Resolve + HasProfile orchestration
// methods.
//
// Subordinate types + methods live in domain-specific files:
//   - LexiconProfile struct → lexicon_profile.go
//   - PhraseExtractionPolicy + PhrasePolicy accessor → lexicon_aliasing.go
//   - VerbSuffixes accessor → lexicon_morphology.go
//   - StopWords / FunctionWords / EntityBlocklist / NegativeParticles
//     / VisualVerbs accessors → lexicon_stopwords.go
//   - ProfileCount + Version + fingerprint helpers → lexicon_scoring.go
//   - File loaders (loadWordSet, loadStringList, loadPhrasePolicy) →
//     lexicon_loaders.go
//   - cloneProfile + cloneStringSet → lexicon_clone.go
//   - defaultLexiconMu + SetDefaultLexicon + DefaultLexicon →
//     lexicon_default.go
package linguistics

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LexiconRegistry holds pre-loaded LexiconProfiles per language and
// provides safe read-only access. It is constructed once at bootstrap
// from files under config/lexicons/ and never mutated afterwards.
type LexiconRegistry struct {
	mu       sync.RWMutex
	profiles map[string]*LexiconProfile
	fallback *LexiconProfile
	version  string
}

// NewLexiconRegistry constructs a LexiconRegistry by scanning the
// given root directory for per-language subdirectories. Each
// subdirectory may contain:
//
//	stopwords.txt        — one word per line
//	function_words.txt   — one word per line
//	verb_morphology.txt  — verb suffixes, one per line
//	entity_blocklist.txt — words never treated as entities, one per line
//	phrase_policy.txt    — YAML or simple key=value lines (min_words, max_words, etc.)
//
// A "fallback" subdirectory is required and is used only when callers
// explicitly request the fallback profile. Missing configuration is an
// error; the registry never manufactures linguistic data.
func NewLexiconRegistry(rootDir string) (*LexiconRegistry, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("lexicon registry: root directory is required")
	}

	profiles := make(map[string]*LexiconProfile)

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, fmt.Errorf("lexicon registry: read root %q: %w", rootDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lang := entry.Name()
		dir := filepath.Join(rootDir, lang)
		profile := &LexiconProfile{
			StopWords:         make(map[string]struct{}),
			FunctionWords:     make(map[string]struct{}),
			EntityBlocklist:   make(map[string]struct{}),
			NegativeParticles: make(map[string]struct{}),
			VisualVerbs:       make(map[string]struct{}),
			VerbSuffixes:      nil,
			PhrasePolicy:      DefaultPhraseExtractionPolicy(),
		}

		if err := loadWordSet(filepath.Join(dir, "stopwords.txt"), profile.StopWords); err != nil {
			return nil, err
		}
		if err := loadWordSet(filepath.Join(dir, "function_words.txt"), profile.FunctionWords); err != nil {
			return nil, err
		}
		if err := loadWordSet(filepath.Join(dir, "entity_blocklist.txt"), profile.EntityBlocklist); err != nil {
			return nil, err
		}
		if err := loadWordSet(filepath.Join(dir, "negative_particles.txt"), profile.NegativeParticles); err != nil {
			return nil, err
		}
		if err := loadWordSet(filepath.Join(dir, "visual_verbs.txt"), profile.VisualVerbs); err != nil {
			return nil, err
		}
		if suffixes, err := loadStringList(filepath.Join(dir, "verb_morphology.txt")); err != nil {
			return nil, err
		} else {
			profile.VerbSuffixes = suffixes
		}
		if p, ok, err := loadPhrasePolicy(filepath.Join(dir, "phrase_policy.txt")); err != nil {
			return nil, err
		} else if ok {
			profile.PhrasePolicy = p
		}

		profiles[lang] = profile
	}

	fallback := profiles["fallback"]
	if fallback == nil {
		return nil, fmt.Errorf("lexicon registry: required fallback profile is missing in %q", rootDir)
	}
	return &LexiconRegistry{
		profiles: profiles,
		fallback: fallback,
		version:  fingerprintLexiconRegistry(profiles, fallback),
	}, nil
}

// MustNewLexiconRegistry is like NewLexiconRegistry but panics on
// error. Useful for tests and bootstrap wiring.
func MustNewLexiconRegistry(rootDir string) *LexiconRegistry {
	r, err := NewLexiconRegistry(rootDir)
	if err != nil {
		panic(fmt.Sprintf("lexicon registry: %v", err))
	}
	return r
}

// Resolve returns the profile for the given language tag. Language
// tags are normalised to a two-letter code with the region stripped
// (e.g. "en-US" -> "en"). Missing profiles fail fast; callers that
// need an error value can use ResolveRequired.
func (r *LexiconRegistry) Resolve(language string) *LexiconProfile {
	profile, err := r.ResolveRequired(language)
	if err != nil {
		panic(err)
	}
	return profile
}

// ResolveRequired returns the configured profile for a language or a
// typed configuration error. There is deliberately no implicit fallback:
// language selection is configuration, not a best-effort heuristic.
func (r *LexiconRegistry) ResolveRequired(language string) (*LexiconProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lang := normalizeLang(language)
	if lang == "" {
		return nil, fmt.Errorf("lexicon registry: language is required")
	}
	if p, ok := r.profiles[lang]; ok {
		return cloneProfile(p), nil
	}
	return nil, fmt.Errorf("lexicon registry: language %q is not configured", language)
}

// HasProfile reports whether the registry has an explicit (non-fallback)
// profile for the given language tag.
func (r *LexiconRegistry) HasProfile(language string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lang := normalizeLang(language)
	_, ok := r.profiles[lang]
	return ok
}

func normalizeLang(language string) string {
	lang := strings.ToLower(strings.TrimSpace(language))
	if idx := strings.IndexByte(lang, '-'); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

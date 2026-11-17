// Package linguistics is the canonical home of language-linguistic data used
// across the pipeline. The data is owned by BrainRust/lexicon: the generator
// `lexicon/tools/build_language_packs.py` emits `profiles.generated.json` from
// the Rust language sources, and this package exposes a read-only snapshot.
//
// godlike/06 SSOT: no stop-word map, function-word map, verb-suffix list or
// entity blocklist may be hardcoded here. The Rust profile is the authority;
// the embedded JSON is a generated projection with a parity gate.
package linguistics

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed profiles.generated.json
var generatedProfilesJSON []byte

const (
	generatedProfilesSchema = "go_lexicon_profiles.v1"
	languageProfileSchema   = "language_profile.v1"
)

// generatedProfilesPayload is the checked-in Go projection emitted by the
// Rust lexicon generator.
type generatedProfilesPayload struct {
	Schema            string                     `json:"schema"`
	Source            string                     `json:"source"`
	RustSchemaVersion string                     `json:"rust_schema_version"`
	Profiles          []generatedLanguageProfile `json:"profiles"`
}

type generatedLanguageProfile struct {
	Schema            string                 `json:"schema"`
	Language          string                 `json:"language"`
	Source            string                 `json:"source"`
	StopWords         []string               `json:"stopwords"`
	FunctionWords     []string               `json:"function_words"`
	EntityBlocklist   []string               `json:"entity_blocklist"`
	NegativeParticles []string               `json:"negative_particles"`
	VisualVerbs       []string               `json:"visual_verbs"`
	VerbSuffixes      []string               `json:"verb_suffixes"`
	WordSets          map[string][]string    `json:"word_sets"`
	PhrasePolicy      PhraseExtractionPolicy `json:"phrase_policy"`
}

// Profiles holds the immutable per-language profiles generated from the Rust
// lexicon sources. It is constructed once at bootstrap and never mutated.
type Profiles struct {
	mu            sync.RWMutex
	profiles      map[string]*LexiconProfile
	fallback      *LexiconProfile
	version       string
	source        string
	schemaVersion string
}

// NewProfiles parses the embedded Rust-generated snapshot. It fails closed on
// a schema mismatch, a missing fallback profile or an empty language entry;
// it never manufactures linguistic data.
func NewProfiles() (*Profiles, error) {
	var payload generatedProfilesPayload
	if err := json.Unmarshal(generatedProfilesJSON, &payload); err != nil {
		return nil, fmt.Errorf("lexicon profiles: decode generated snapshot: %w", err)
	}
	if payload.Schema != generatedProfilesSchema {
		return nil, fmt.Errorf("lexicon profiles: unsupported snapshot schema %q", payload.Schema)
	}
	if !strings.HasPrefix(payload.RustSchemaVersion, "language_profiles.v1-") {
		return nil, fmt.Errorf("lexicon profiles: generated snapshot carries no valid Rust schema version")
	}
	if len(payload.Profiles) == 0 {
		return nil, fmt.Errorf("lexicon profiles: generated snapshot has no profiles")
	}
	profiles := make(map[string]*LexiconProfile, len(payload.Profiles))
	var fallback *LexiconProfile
	for _, raw := range payload.Profiles {
		if raw.Schema != languageProfileSchema {
			return nil, fmt.Errorf("lexicon profiles: language %q has unsupported schema %q", raw.Language, raw.Schema)
		}
		language := normalizeLang(raw.Language)
		if language == "" {
			return nil, fmt.Errorf("lexicon profiles: generated snapshot carries an empty language")
		}
		profile := &LexiconProfile{
			StopWords:         stringSet(raw.StopWords),
			FunctionWords:     stringSet(raw.FunctionWords),
			EntityBlocklist:   stringSet(raw.EntityBlocklist),
			NegativeParticles: stringSet(raw.NegativeParticles),
			VisualVerbs:       stringSet(raw.VisualVerbs),
			VerbSuffixes:      append([]string(nil), raw.VerbSuffixes...),
			PhrasePolicy:      raw.PhrasePolicy,
			WordSets:          make(map[string]map[string]struct{}, len(raw.WordSets)),
		}
		for name, words := range raw.WordSets {
			profile.WordSets[name] = stringSet(words)
		}
		if language == "fallback" {
			fallback = profile
			continue
		}
		if _, duplicate := profiles[language]; duplicate {
			return nil, fmt.Errorf("lexicon profiles: duplicate language %q", language)
		}
		profiles[language] = profile
	}
	if fallback == nil {
		return nil, fmt.Errorf("lexicon profiles: required fallback profile is missing")
	}
	return &Profiles{
		profiles:      profiles,
		fallback:      fallback,
		version:       fingerprintLexiconProfiles(profiles, fallback),
		source:        payload.Source,
		schemaVersion: payload.RustSchemaVersion,
	}, nil
}

// MustNewProfiles is NewProfiles but panics on error, for bootstrap wiring.
func MustNewProfiles() *Profiles {
	profiles, err := NewProfiles()
	if err != nil {
		panic(err)
	}
	return profiles
}

// Source returns the Rust data directory that generated the snapshot.
func (r *Profiles) Source() string {
	if r == nil {
		return ""
	}
	return r.source
}

// SchemaVersion returns the Rust profile-set version embedded at generation
// time. The composition root validates it before installing the snapshot.
func (r *Profiles) SchemaVersion() string {
	if r == nil {
		return ""
	}
	return r.schemaVersion
}

// Resolve returns the profile for the given language tag. Missing profiles
// fail fast; callers that need an error value can use ResolveRequired.
func (r *Profiles) Resolve(language string) *LexiconProfile {
	profile, err := r.ResolveRequired(language)
	if err != nil {
		panic(err)
	}
	return profile
}

// ResolveRequired returns the configured profile for a language or a typed
// error. There is deliberately no implicit fallback: language selection is
// configuration, not a best-effort heuristic.
func (r *Profiles) ResolveRequired(language string) (*LexiconProfile, error) {
	if r == nil {
		return nil, fmt.Errorf("lexicon profiles: snapshot is not installed")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	lang := normalizeLang(language)
	if lang == "" {
		return nil, fmt.Errorf("lexicon profiles: language is required")
	}
	if lang == "fallback" {
		return cloneProfile(r.fallback), nil
	}
	if profile, ok := r.profiles[lang]; ok {
		return cloneProfile(profile), nil
	}
	return nil, fmt.Errorf("lexicon profiles: language %q is not configured", language)
}

// HasProfile reports whether the snapshot has an explicit (non-fallback)
// profile for the given language tag.
func (r *Profiles) HasProfile(language string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.profiles[normalizeLang(language)]
	return ok
}

func normalizeLang(language string) string {
	lang := strings.ToLower(strings.TrimSpace(language))
	if idx := strings.IndexByte(lang, '-'); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

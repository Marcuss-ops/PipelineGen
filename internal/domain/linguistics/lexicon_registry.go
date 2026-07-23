// Package linguistics is the canonical home of language-linguistic
// data used across the pipeline. It owns the LexiconRegistry, which
// is the single source of truth for per-language profiles loaded from
// config/lexicons at bootstrap.
//
// godlike/06 SSOT: every stop-word map, function-word map, verb-suffix
// list and entity-blocklist in the codebase must originate from this
// package. No hardcoded maps elsewhere, no second representation.
package linguistics

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// PhraseExtractionPolicy controls how intent resolvers and entity
// filters extract meaningful phrases from text. Each language profile
// carries its own policy so extraction rules differ between, say,
// English and Italian.
type PhraseExtractionPolicy struct {
	// MinWords is the minimum number of words in a valid phrase.
	MinWords int `yaml:"min_words" json:"min_words"`
	// MaxWords is the maximum number of words in a valid phrase.
	MaxWords int `yaml:"max_words" json:"max_words"`
	// MaxResults caps the number of extracted phrases.
	MaxResults int `yaml:"max_results" json:"max_results"`
	// RejectVerbsWhenAll when true rejects phrases where every word
	// looks like a verb form (common for Italian verb-only bigrams).
	RejectVerbsWhenAll bool `yaml:"reject_verbs_when_all" json:"reject_verbs_when_all"`
}

// DefaultPhraseExtractionPolicy returns a sensible default policy
// used by the fallback profile when no explicit phrase_policy file
// is present.
func DefaultPhraseExtractionPolicy() PhraseExtractionPolicy {
	return PhraseExtractionPolicy{
		MinWords:           2,
		MaxWords:           3,
		MaxResults:         5,
		RejectVerbsWhenAll: true,
	}
}

// LexiconProfile is the complete set of per-language linguistic data
// used by intent resolvers, entity filters and language detectors.
type LexiconProfile struct {
	StopWords       map[string]struct{}
	FunctionWords   map[string]struct{}
	EntityBlocklist map[string]struct{}
	VerbSuffixes    []string
	PhrasePolicy    PhraseExtractionPolicy
}

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
// A "fallback" subdirectory provides defaults used when no language-
// specific profile exists. If fallback is missing, the registry
// builds a minimal fallback from built-in defaults.
func NewLexiconRegistry(rootDir string) (*LexiconRegistry, error) {
	if rootDir == "" {
		return newBuiltInLexicon(), nil
	}

	profiles := make(map[string]*LexiconProfile)

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return newBuiltInLexicon(), nil
		}
		return nil, fmt.Errorf("lexicon registry: read root %q: %w", rootDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lang := entry.Name()
		dir := filepath.Join(rootDir, lang)
		profile := &LexiconProfile{
			StopWords:       make(map[string]struct{}),
			FunctionWords:   make(map[string]struct{}),
			EntityBlocklist: make(map[string]struct{}),
			VerbSuffixes:    nil,
			PhrasePolicy:    DefaultPhraseExtractionPolicy(),
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
		fallback = builtInFallbackProfile()
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
// (e.g. "en-US" -> "en"). If no profile exists for the language,
// the fallback profile is returned.
func (r *LexiconRegistry) Resolve(language string) *LexiconProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lang := normalizeLang(language)
	if p, ok := r.profiles[lang]; ok {
		return cloneProfile(p)
	}
	return cloneProfile(r.fallback)
}

// StopWords returns the stop-word set for the given language.
func (r *LexiconRegistry) StopWords(language string) map[string]struct{} {
	return r.Resolve(language).StopWords
}

// FunctionWords returns the function-word set for the given language.
func (r *LexiconRegistry) FunctionWords(language string) map[string]struct{} {
	return r.Resolve(language).FunctionWords
}

// EntityBlocklist returns the entity blocklist for the given language.
func (r *LexiconRegistry) EntityBlocklist(language string) map[string]struct{} {
	return r.Resolve(language).EntityBlocklist
}

// VerbSuffixes returns the verb suffixes for the given language.
func (r *LexiconRegistry) VerbSuffixes(language string) []string {
	return r.Resolve(language).VerbSuffixes
}

// PhrasePolicy returns the phrase extraction policy for the given language.
func (r *LexiconRegistry) PhrasePolicy(language string) PhraseExtractionPolicy {
	return r.Resolve(language).PhrasePolicy
}

// Version returns a hash-based version string that changes when any
// loaded profile changes.
func (r *LexiconRegistry) Version() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.version != "" {
		return r.version
	}
	return fingerprintLexiconRegistry(r.profiles, r.fallback)
}

// ── File-loading helpers ───────────────────────────────────────────

func loadWordSet(path string, dest map[string]struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lexicon registry: open %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		dest[strings.ToLower(word)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lexicon registry: scan %q: %w", path, err)
	}
	return nil
}

func loadStringList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lexicon registry: open %q: %w", path, err)
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.ToLower(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("lexicon registry: scan %q: %w", path, err)
	}
	return out, nil
}

func loadPhrasePolicy(path string) (PhraseExtractionPolicy, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PhraseExtractionPolicy{}, false, nil
		}
		return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: open %q: %w", path, err)
	}
	defer f.Close()

	p := DefaultPhraseExtractionPolicy()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: malformed phrase policy line %q in %q", line, path)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "min_words":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse min_words in %q: %w", path, parseErr)
			}
			p.MinWords = n
		case "max_words":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse max_words in %q: %w", path, parseErr)
			}
			p.MaxWords = n
		case "max_results":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse max_results in %q: %w", path, parseErr)
			}
			p.MaxResults = n
		case "reject_verbs_when_all":
			b, parseErr := strconv.ParseBool(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse reject_verbs_when_all in %q: %w", path, parseErr)
			}
			p.RejectVerbsWhenAll = b
		default:
			return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: unknown phrase policy key %q in %q", key, path)
		}
	}
	if err := scanner.Err(); err != nil {
		return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: scan %q: %w", path, err)
	}
	return p, true, nil
}

func normalizeLang(language string) string {
	lang := strings.ToLower(strings.TrimSpace(language))
	if idx := strings.IndexByte(lang, '-'); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

// addAll is a helper that inserts every word into the set.
func addAll(m map[string]struct{}, words ...string) {
	for _, w := range words {
		m[w] = struct{}{}
	}
}

// builtInFallbackProfile returns a comprehensive built-in lexicon
// that works across English, Italian, Spanish, French, German and
// Portuguese. It is used only when no "fallback" directory is present
// under config/lexicons/ and ensures tests pass without files on disk.
func builtInFallbackProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords:       builtInStopWords(),
		FunctionWords:   builtInFunctionWords(),
		EntityBlocklist: builtInEntityBlocklist(),
		VerbSuffixes:    []string{"ing", "ed", "are", "ere", "ire", "ando", "endo", "ato", "uto", "ito"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func builtInStopWords() map[string]struct{} {
	m := make(map[string]struct{}, 500)
	addAll(m,
		// English
		"the", "a", "an", "and", "or", "but", "for", "with", "from",
		"into", "onto", "about", "above", "across", "after", "against",
		"along", "among", "around", "at", "before", "behind", "below",
		"beneath", "beside", "between", "beyond", "by", "down", "during",
		"except", "inside", "instead", "near", "off", "on", "over",
		"through", "to", "toward", "towards", "under", "until", "upon",
		"within", "without", "you", "he", "she", "we", "they", "my",
		"your", "his", "her", "its", "our", "their", "this", "that",
		"these", "those", "what", "which", "who", "whom", "whose",
		"where", "when", "why", "how", "all", "any", "both", "each",
		"few", "more", "most", "other", "some", "such", "only", "own",
		"same", "so", "than", "too", "very", "just", "now", "then",
		"here", "am", "is", "are", "was", "were", "be", "been", "being",
		"have", "has", "had", "do", "does", "did", "will", "would",
		"could", "should", "may", "might", "must", "shall", "can",
		"cannot", "get", "got", "gets", "make", "made", "say", "said",
		"says", "go", "went", "goes", "going", "come", "came", "comes",
		"know", "knew", "known", "take", "took", "taken", "see", "saw",
		"seen", "think", "thought", "look", "looked", "looking", "use",
		"used", "using", "find", "found", "give", "gave", "given",
		"tell", "told", "ask", "asked", "asking", "seem", "seemed",
		"want", "wanted", "wanting", "need", "needed", "not", "no",
		"as", "if", "it", "i",
		// Italian
		"il", "lo", "la", "gli", "le", "un", "uno", "una", "dei",
		"degli", "della", "delle", "dello", "dell", "di", "da", "in",
		"con", "su", "per", "tra", "fra", "al", "allo", "alla", "ai",
		"agli", "alle", "dal", "dallo", "dalla", "dai", "dagli",
		"dalle", "nel", "nello", "nella", "nei", "negli", "nelle",
		"col", "coi", "sul", "sulla", "sui", "sulle", "e", "che",
		"ma", "se", "anche", "più", "meno", "quando", "dove", "perché",
		"chi", "cosa", "quale", "quali", "tutto", "tutti", "ogni",
		"qualche", "molto", "poco", "troppo", "abbastanza", "qui",
		"vi", "ci", "si", "mi", "ti", "sono", "sei", "è", "era",
		"erano", "fui", "fu", "furono", "avere", "ho", "hai", "ha",
		"hanno", "avevo", "aveva", "avevano", "avuto", "fare", "faccio",
		"fai", "fa", "fanno", "fatto", "potere", "posso", "puoi",
		"può", "possono", "volere", "voglio", "vuoi", "vuole",
		"vogliono", "dovere", "devo", "devi", "deve", "devono",
		"andare", "vado", "vai", "va", "vanno", "non", "del", "c",
		// Spanish
		"el", "los", "las", "unos", "unas", "de", "en", "y", "o",
		"que", "es", "son", "por", "para", "como", "pero", "sus",
		"me", "te", "nos", "os", "les", "tu", "ella", "nosotros",
		"vosotros", "ellos", "ellas",
		// French
		"une", "des", "du", "à", "par", "sur", "et", "ou", "mais",
		"comme", "ce", "cette", "sont", "est", "dans", "avec", "sans",
		"pas", "ne", "plus", "au", "aux", "ses", "nous", "vous",
		"ils", "elles",
		// German
		"der", "die", "das", "ein", "eine", "einen", "von", "zu",
		"mit", "für", "auf", "und", "oder", "aber", "wenn", "dass",
		"wie", "dieser", "diese", "sind", "ist", "nicht", "den", "dem",
		"einer", "eines", "einem", "nach", "aus", "bei", "vor",
		"durch", "über", "unter", "zwischen", "um", "gegen", "ohne",
		"bis", "zum", "zur", "beim", "vom", "als", "nur", "noch",
		"schon", "auch", "immer", "wieder", "etwas", "man", "ich",
		"er", "sie", "wir", "ihr",
		// Portuguese
		"os", "as", "do", "da", "dos", "das", "em", "na", "nas",
		"com", "sem", "ao", "aos", "não", "foi", "era", "ser",
		"mas", "pela", "pelo", "pelos", "pelas", "num", "numa",
		"dum", "duma", "este", "esta", "estes", "estas", "isso",
		"aquilo",
	)
	return m
}

func builtInFunctionWords() map[string]struct{} {
	m := make(map[string]struct{}, 200)
	addAll(m,
		"the", "a", "an", "of", "in", "on", "at", "to", "for", "with",
		"by", "from", "this", "that", "these", "those", "what", "which",
		"who", "whom", "whose", "where", "when", "why", "how", "all",
		"any", "both", "each", "every", "few", "more", "most", "some",
		"such", "only", "own", "same", "than", "too", "very", "just",
		"now", "then", "here", "there", "yet", "still", "subsequent",
		"subsequently", "previous", "previously", "following", "next",
		"before", "after", "during", "mount", "mountains", "river",
		"ocean", "sea", "august", "september", "october", "november",
		"december", "january", "february", "march", "april", "june",
		"july", "monday", "tuesday", "wednesday", "thursday", "friday",
		"saturday", "sunday", "north", "south", "east", "west", "first",
		"second", "third", "last", "one", "two", "three", "four", "five",
		"many", "other", "another", "behind", "beneath", "beyond",
		"above", "below", "within", "outside", "despite", "although",
		"because", "since", "unless", "while", "through", "across",
		"around", "along", "between", "again", "also", "perhaps",
		"possibly", "indeed", "though", "however", "moreover",
		"furthermore", "nevertheless", "meanwhile", "otherwise",
		"instead", "thus", "therefore", "hence", "consequently",
		"accordingly", "similarly", "likewise", "notably", "importantly",
		"surprisingly", "finally", "ultimately", "eventually",
		"gradually", "essentially", "basically", "primarily", "mainly",
		"particularly", "especially", "specifically", "generally",
		"typically", "usually", "normally", "commonly", "widely",
		"deeply", "greatly", "strongly", "highly", "western", "eastern",
		"northern", "southern", "ancient", "modern", "new", "old",
		"young", "great", "small", "large", "long", "short", "so",
		"law", "laws", "legal", "religion", "religions", "culture",
		"cultures", "society", "societies", "empire", "kingdom",
		"republic", "civilization", "people", "nations", "peoples",
		"world", "history",
		// Italian
		"il", "lo", "la", "gli", "le", "un", "uno", "una", "del",
		"dello", "della", "dei", "degli", "delle", "al", "allo",
		"alla", "ai", "agli", "alle", "dal", "dallo", "dalla", "dai",
		"dagli", "dalle", "nel", "nello", "nella", "nei", "negli",
		"nelle", "sul", "sullo", "sulla", "sui", "sugli", "sulle",
		"col", "collo", "colla", "coi", "cogli", "colle", "su", "per",
		"con", "tra", "fra", "di", "da", "che", "chi", "questo",
		"questa", "questi", "queste", "quello", "quella", "quelli",
		"quelle", "suo", "sua", "suoi", "sue", "mio", "mia", "tuo",
		"tua", "nostro", "nostra",
	)
	return m
}

func builtInEntityBlocklist() map[string]struct{} {
	return map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {}, "for": {},
		"nor": {}, "yet": {}, "so": {}, "in": {}, "on": {}, "at": {}, "to": {},
		"by": {}, "with": {}, "from": {}, "of": {}, "is": {}, "are": {},
		"was": {}, "were": {}, "be": {}, "been": {}, "being": {}, "have": {},
		"has": {}, "had": {}, "do": {}, "does": {}, "did": {}, "will": {},
		"would": {}, "could": {}, "should": {}, "may": {}, "might": {}, "must": {},
		"shall": {}, "can": {}, "not": {}, "no": {}, "this": {}, "that": {},
		"these": {}, "those": {}, "what": {}, "which": {}, "who": {}, "whom": {},
		"whose": {}, "where": {}, "when": {}, "why": {}, "how": {}, "all": {},
		"any": {}, "both": {}, "each": {}, "every": {}, "few": {}, "more": {},
		"most": {}, "some": {}, "such": {}, "only": {}, "own": {}, "same": {},
		"than": {}, "too": {}, "very": {}, "just": {}, "now": {}, "then": {},
		"there": {}, "here": {}, "about": {}, "above": {}, "across": {}, "after": {},
		"again": {}, "against": {}, "along": {}, "already": {}, "also": {},
		"although": {}, "among": {}, "around": {}, "because": {}, "before": {},
		"behind": {}, "below": {}, "beneath": {}, "beside": {}, "between": {},
		"beyond": {}, "down": {}, "during": {}, "except": {}, "inside": {},
		"into": {}, "near": {}, "off": {}, "onto": {}, "outside": {}, "over": {},
		"since": {}, "through": {}, "throughout": {}, "toward": {}, "towards": {},
		"under": {}, "unless": {}, "until": {}, "upon": {}, "within": {}, "without": {},
	}
}

// ── Global default ─────────────────────────────────────────────────

var defaultLexiconMu sync.RWMutex
var defaultLexicon *LexiconRegistry

// SetDefaultLexicon installs the package-level default lexicon registry.
// The composition root calls this once at bootstrap; tests may replace
// it to exercise file-backed fixtures.
func SetDefaultLexicon(r *LexiconRegistry) {
	if r == nil {
		return
	}
	defaultLexiconMu.Lock()
	defer defaultLexiconMu.Unlock()
	defaultLexicon = r
}

// DefaultLexicon returns the global default lexicon registry. If none
// has been installed yet, it creates a built-in registry with
// per-language profiles (en, it, es, fr, de) plus a union fallback.
// That fallback exists only so tests and explicit dev bootstrap can
// keep running before the composition root installs a file-backed
// registry.
func DefaultLexicon() *LexiconRegistry {
	defaultLexiconMu.RLock()
	if defaultLexicon != nil {
		lex := defaultLexicon
		defaultLexiconMu.RUnlock()
		return lex
	}
	defaultLexiconMu.RUnlock()

	defaultLexiconMu.Lock()
	defer defaultLexiconMu.Unlock()
	if defaultLexicon == nil {
		defaultLexicon = newBuiltInLexicon()
	}
	return defaultLexicon
}

func cloneProfile(p *LexiconProfile) *LexiconProfile {
	if p == nil {
		return nil
	}
	out := &LexiconProfile{
		StopWords:       cloneStringSet(p.StopWords),
		FunctionWords:   cloneStringSet(p.FunctionWords),
		EntityBlocklist: cloneStringSet(p.EntityBlocklist),
		VerbSuffixes:    append([]string(nil), p.VerbSuffixes...),
		PhrasePolicy:    p.PhrasePolicy,
	}
	return out
}

func cloneStringSet(src map[string]struct{}) map[string]struct{} {
	if len(src) == 0 {
		return map[string]struct{}{}
	}
	dst := make(map[string]struct{}, len(src))
	for k := range src {
		dst[k] = struct{}{}
	}
	return dst
}

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

func writeLexiconFingerprint(h hash.Hash, key string, p *LexiconProfile) {
	if p == nil {
		_, _ = h.Write([]byte(key + ":nil\n"))
		return
	}
	_, _ = h.Write([]byte(key + ":"))
	writeSortedSetFingerprint(h, "stop", p.StopWords)
	writeSortedSetFingerprint(h, "func", p.FunctionWords)
	writeSortedSetFingerprint(h, "block", p.EntityBlocklist)
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

// newBuiltInLexicon creates a registry pre-populated with per-language
// profiles derived from the old hardcoded quality_gate.go maps. Each
// profile has language-specific stop words so detectLanguage works
// correctly. The fallback profile contains the union for
// language-agnostic filtering (e.g. textutil.IsStopWord).
func newBuiltInLexicon() *LexiconRegistry {
	reg := &LexiconRegistry{
		profiles: map[string]*LexiconProfile{
			"en": englishBuiltInProfile(),
			"it": italianBuiltInProfile(),
			"es": spanishBuiltInProfile(),
			"fr": frenchBuiltInProfile(),
			"de": germanBuiltInProfile(),
		},
		fallback: builtInFallbackProfile(),
	}
	reg.version = fingerprintLexiconRegistry(reg.profiles, reg.fallback)
	return reg
}

func englishBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
			"be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
			"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "could": {},
			"should": {}, "of": {}, "in": {}, "on": {}, "at": {}, "to": {}, "for": {},
			"and": {}, "or": {}, "but": {}, "with": {}, "from": {}, "this": {},
			"not": {}, "no": {}, "it": {}, "that": {}, "i": {}, "you": {}, "he": {},
			"she": {}, "we": {}, "they": {}, "my": {}, "your": {}, "his": {}, "her": {},
			"its": {}, "our": {}, "their": {}, "what": {}, "which": {}, "who": {},
			"where": {}, "when": {}, "why": {}, "how": {}, "all": {}, "any": {},
			"both": {}, "each": {}, "few": {}, "more": {}, "most": {}, "other": {},
			"some": {}, "such": {}, "only": {}, "own": {}, "same": {}, "so": {},
			"than": {}, "too": {}, "very": {}, "just": {}, "now": {}, "then": {},
			"here": {}, "there": {}, "as": {}, "if": {},
		},
		// FunctionWords for the English profile use the comprehensive
		// built-in set (which covers sentence-start stop words, month
		// names, day names, adverbs, etc.) so entity filters like
		// isSentenceStartCapitalizedOnly work correctly.
		FunctionWords:   builtInFunctionWords(),
		EntityBlocklist: builtInEntityBlocklist(),
		VerbSuffixes:    []string{"ing", "ed", "en", "es"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func italianBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"il": {}, "la": {}, "lo": {}, "i": {}, "le": {}, "un": {}, "una": {},
			"di": {}, "a": {}, "da": {}, "in": {}, "con": {}, "su": {}, "per": {},
			"tra": {}, "fra": {}, "e": {}, "o": {}, "ma": {}, "se": {}, "che": {},
			"come": {}, "questo": {}, "questa": {}, "sono": {}, "è": {}, "del": {},
			"non": {}, "gli": {}, "dei": {}, "degli": {}, "della": {}, "delle": {},
			"dello": {}, "al": {}, "allo": {}, "alla": {}, "ai": {}, "agli": {}, "alle": {},
			"dal": {}, "dallo": {}, "dalla": {}, "dai": {}, "dagli": {}, "dalle": {},
			"nel": {}, "nello": {}, "nella": {}, "nei": {}, "negli": {}, "nelle": {},
			"col": {}, "coi": {}, "sul": {}, "sulla": {}, "sui": {}, "sulle": {},
			"chi": {}, "cosa": {}, "quale": {}, "quali": {}, "tutto": {}, "tutti": {},
			"ogni": {}, "qualche": {}, "molto": {}, "poco": {}, "troppo": {}, "anche": {},
			"più": {}, "meno": {}, "quando": {}, "dove": {}, "perché": {}, "si": {},
		},
		// Italian function words are also covered by the comprehensive
		// builtInFunctionWords() set, which includes both English and
		// Italian grammatical words.
		FunctionWords:   builtInFunctionWords(),
		EntityBlocklist: map[string]struct{}{},
		VerbSuffixes:    []string{"are", "ere", "ire", "ando", "endo", "ato", "uto", "ito"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func spanishBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"el": {}, "la": {}, "los": {}, "las": {}, "un": {}, "una": {}, "unos": {},
			"unas": {}, "de": {}, "a": {}, "en": {}, "con": {}, "por": {}, "para": {},
			"sobre": {}, "y": {}, "o": {}, "pero": {}, "si": {}, "que": {}, "como": {},
			"este": {}, "esta": {}, "son": {}, "es": {}, "del": {},
			"no": {}, "su": {}, "sus": {}, "me": {}, "te": {}, "se": {}, "nos": {}, "os": {},
			"le": {}, "les": {}, "lo": {}, "mi": {}, "tu": {},
		},
		FunctionWords:   nil,
		EntityBlocklist: nil,
		VerbSuffixes:    []string{"ar", "er", "ir", "ando", "endo"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func frenchBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"le": {}, "la": {}, "les": {}, "un": {}, "une": {}, "des": {}, "du": {},
			"de": {}, "à": {}, "en": {}, "par": {}, "pour": {}, "sur": {}, "et": {},
			"ou": {}, "mais": {}, "si": {}, "que": {}, "comme": {}, "ce": {}, "cette": {},
			"sont": {}, "est": {}, "dans": {}, "avec": {}, "sans": {}, "pas": {}, "ne": {}, "plus": {},
			"au": {}, "aux": {}, "ses": {}, "son": {}, "nous": {}, "vous": {}, "ils": {}, "elles": {},
		},
		FunctionWords:   nil,
		EntityBlocklist: nil,
		VerbSuffixes:    []string{"er", "ir", "re", "ant", "é", "ée"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func germanBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"der": {}, "die": {}, "das": {}, "ein": {}, "eine": {}, "einen": {},
			"von": {}, "zu": {}, "in": {}, "mit": {}, "für": {}, "auf": {}, "und": {},
			"oder": {}, "aber": {}, "wenn": {}, "dass": {}, "wie": {}, "dieser": {}, "diese": {},
			"sind": {}, "ist": {}, "nicht": {}, "dem": {}, "den": {}, "des": {},
			"einer": {}, "eines": {}, "einem": {}, "nach": {}, "aus": {}, "bei": {}, "vor": {},
			"durch": {}, "über": {}, "unter": {}, "zwischen": {}, "um": {}, "gegen": {},
			"ohne": {}, "bis": {}, "zum": {}, "zur": {}, "beim": {}, "vom": {},
		},
		FunctionWords:   nil,
		EntityBlocklist: nil,
		VerbSuffixes:    []string{"en", "t", "e", "st"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

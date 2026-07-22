package intent

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
)

// IntentResolverRegistry dispatches intent resolution to a
// language-specific resolver. It is the canonical replacement for
// the previous hardcoded monolithic resolver.
type IntentResolverRegistry struct {
	resolvers map[string]VisualIntentResolver
	fallback  VisualIntentResolver
}

// NewDefaultResolver returns the canonical intent resolver: a registry
// wired with English, Italian and a multilingual fallback. It keeps
// the same constructor name so existing callers and tests continue to
// work without modification.
func NewDefaultResolver() VisualIntentResolver {
	return &IntentResolverRegistry{
		resolvers: map[string]VisualIntentResolver{
			"en": newEnglishResolver(),
			"it": newItalianResolver(),
		},
		fallback: newFallbackResolver(),
	}
}

// Resolve selects the resolver matching the request language. Language
// tags are normalised to a two-letter code, with the region stripped,
// and fall back to the multilingual resolver when no specific resolver
// is available.
func (r *IntentResolverRegistry) Resolve(ctx context.Context, language, originalText, normalizedText string) (brain.VisualIntent, error) {
	lang := normalizeLanguageTag(language)
	resolver, ok := r.resolvers[lang]
	if !ok {
		resolver = r.fallback
	}
	return resolver.Resolve(ctx, language, originalText, normalizedText)
}

// Version returns the canonical intent-resolver registry version.
func (r *IntentResolverRegistry) Version() string {
	return "intent-registry-v1"
}

func normalizeLanguageTag(language string) string {
	lang := strings.ToLower(strings.TrimSpace(language))
	if idx := strings.IndexByte(lang, '-'); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

// ── Language-specific resolver configuration ─────────────────────

type langConfig struct {
	StopWords         map[string]struct{}
	NegativeParticles map[string]struct{}
	VisualVerbs       map[string]struct{}
	VerbSuffixes      []string
}

type baseResolver struct {
	name   string
	config langConfig
}

func (r *baseResolver) Version() string {
	return r.name + "-heuristic-v1"
}

func (r *baseResolver) Resolve(_ context.Context, _, originalText, normalizedText string) (brain.VisualIntent, error) {
	out := brain.VisualIntent{}

	// Entities are detected from the original text because
	// normalisation folds case and the entity heuristic relies on
	// capitalisation. A token that is just a sentence-initial
	// stopword (e.g. "The", "Il") is not treated as an entity.
	for _, tok := range tokenize(originalText) {
		if !isLikelyEntity(tok) {
			continue
		}
		clean := cleanEntityToken(tok)
		if clean == "" {
			continue
		}
		if _, stop := r.config.StopWords[clean]; stop {
			continue
		}
		out.Entities = append(out.Entities, clean)
	}

	normTokens := tokenize(normalizedText)
	var keywords []string
	var concept strings.Builder
	isStop := func(word string) bool {
		_, ok := r.config.StopWords[word]
		return ok
	}
	isNegative := func(word string) bool {
		_, ok := r.config.NegativeParticles[word]
		return ok
	}

	for i, tok := range normTokens {
		clean := strings.ToLower(strings.TrimSpace(tok))
		if clean == "" {
			continue
		}
		if len(clean) < 3 {
			continue
		}

		// Negative particles are kept as concepts but excluded from
		// keywords/objects.
		if isNegative(clean) {
			out.NegativeConcepts = append(out.NegativeConcepts, clean)
			// Also capture the following token as a negative concept if
			// it is meaningful.
			if i+1 < len(normTokens) {
				next := strings.ToLower(strings.TrimSpace(normTokens[i+1]))
				if next != "" && len(next) >= 3 && !isStop(next) && !isNegative(next) {
					out.NegativeConcepts = append(out.NegativeConcepts, next)
				}
			}
			continue
		}

		if isStop(clean) {
			continue
		}

		keywords = append(keywords, clean)

		if concept.Len() > 0 {
			concept.WriteByte(' ')
		}
		concept.WriteString(clean)

		if looksLikeVerb(clean, r.config.VisualVerbs, r.config.VerbSuffixes) {
			out.Actions = append(out.Actions, clean)
			out.VisualActions = append(out.VisualActions, clean)
		} else {
			out.Objects = append(out.Objects, clean)
			// Topics are a superset of objects: entities plus any
			// meaningful non-action token.
			out.Topics = append(out.Topics, clean)
		}
	}

	out.Keywords = uniqueStrings(keywords)
	out.SearchKeywords = out.Keywords
	if concept.Len() > 0 {
		out.Concepts = []string{concept.String()}
	}

	out.Entities = uniqueStrings(out.Entities)
	out.Actions = uniqueStrings(out.Actions)
	out.VisualActions = uniqueStrings(out.VisualActions)
	out.Objects = uniqueStrings(out.Objects)
	// Topics are a broader set than objects: they also include
	// detected named entities.
	out.Topics = uniqueStrings(append(out.Objects, out.Entities...))
	out.NegativeConcepts = uniqueStrings(out.NegativeConcepts)

	return out, nil
}

func looksLikeVerb(word string, visualVerbs map[string]struct{}, suffixes []string) bool {
	if _, ok := visualVerbs[word]; ok {
		return true
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(word, suffix) {
			return true
		}
	}
	return false
}

func newEnglishResolver() VisualIntentResolver {
	return &baseResolver{
		name: "en",
		config: langConfig{
			StopWords:         englishStopWords,
			NegativeParticles: map[string]struct{}{"not": {}, "no": {}, "never": {}, "neither": {}},
			VisualVerbs: map[string]struct{}{
				"watch": {}, "observe": {}, "look": {}, "see": {}, "study": {},
				"build": {}, "construct": {}, "walk": {}, "run": {}, "jump": {},
				"move": {}, "push": {}, "pull": {}, "enter": {}, "leave": {},
			},
			VerbSuffixes: []string{"ing", "ed"},
		},
	}
}

func newItalianResolver() VisualIntentResolver {
	return &baseResolver{
		name: "it",
		config: langConfig{
			StopWords:         italianStopWords,
			NegativeParticles: map[string]struct{}{"non": {}, "no": {}, "mai": {}, "ne": {}},
			VisualVerbs: map[string]struct{}{
				"guardare": {}, "osservare": {}, "vedere": {}, "studiare": {},
				"costruire": {}, "camminare": {}, "correre": {}, "saltare": {},
				"muovere": {}, "spingere": {}, "tirare": {}, "entrare": {}, "uscire": {},
			},
			VerbSuffixes: []string{"are", "ere", "ire", "ando", "endo", "ato", "uto", "ito", "avano", "evano", "ivano"},
		},
	}
}

func newFallbackResolver() VisualIntentResolver {
	return &baseResolver{
		name: "fallback",
		config: langConfig{
			StopWords:         fallbackStopWords,
			NegativeParticles: map[string]struct{}{"not": {}, "no": {}, "non": {}, "never": {}, "mai": {}},
			VisualVerbs:       map[string]struct{}{},
			VerbSuffixes:      []string{"ing", "ed", "ando", "endo", "ato", "ito"},
		},
	}
}

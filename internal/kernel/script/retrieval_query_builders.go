package script

import "strings"

const (
	defaultYouTubeQueryLimit = 5
	defaultArtlistQueryLimit = 5
	defaultImageQueryLimit   = 5
)

// BuildYouTubeQueries creates entity/context-oriented queries. Named entities
// and temporal terms lead; keywords and topic provide deterministic fallback
// context. No LLM or provider call is performed.
func BuildYouTubeQueries(profile SegmentSemanticProfile, limit int) []string {
	limit = normalizedQueryLimit(limit, defaultYouTubeQueryLimit)
	var queries []string
	for _, entity := range profile.Entities {
		if isRetrievalEntity(entity) {
			queries = append(queries, joinQuery(append([]string{entity.Value, profile.Topic}, temporalTerms(profile)...)...))
		}
	}
	queries = append(queries, profile.ImportantPhrases...)
	queries = append(queries, joinQuery(append([]string{profile.Topic}, append(termValues(profile, TermKindTechnology, TermKindSubject), temporalTerms(profile)...)...)...))
	queries = append(queries, termQuery(profile, TermKindSubject, TermKindContext))
	return uniqueQueries(queries, limit)
}

// BuildArtlistQueries creates visual-first queries from VisualTerms, visual
// entities and contextual terms. Editorial narrative phrases are not provider
// evidence and therefore never enter this provider projection.
func BuildArtlistQueries(profile SegmentSemanticProfile, limit int) []string {
	limit = normalizedQueryLimit(limit, defaultArtlistQueryLimit)
	var queries []string
	for _, term := range profile.VisualTerms {
		queries = append(queries, term.Value)
	}
	queries = append(queries, termQuery(profile, TermKindVisual, TermKindContext, TermKindAction, TermKindTechnology))
	queries = append(queries, joinQuery(weightedValues(profile.Keywords)...))
	for _, entity := range profile.Entities {
		if isVisualEntity(entity) {
			queries = append(queries, joinQuery(append([]string{entity.Value}, termValues(profile, TermKindVisual, TermKindContext, TermKindTechnology)...)...))
		}
	}
	return uniqueQueries(queries, limit)
}

// BuildImageQueries creates entity-first image queries, then falls back to
// visual/context terms. Dates are included only when they accompany an entity
// or visual concept, preventing bare years from becoming image searches.
// Editorial narrative phrases remain outside the provider projection.
func BuildImageQueries(profile SegmentSemanticProfile, limit int) []string {
	limit = normalizedQueryLimit(limit, defaultImageQueryLimit)
	var queries []string
	visual := termValues(profile, TermKindVisual, TermKindContext)
	temporal := temporalTerms(profile)
	for _, entity := range profile.Entities {
		if isRetrievalEntity(entity) {
			queries = append(queries, joinQuery(append([]string{entity.Value}, append(visual, temporal...)...)...))
		}
	}
	if len(profile.VisualTerms) > 0 {
		queries = append(queries, profile.VisualTerms[0].ValueIfPresent())
	}
	// A source-grounded topic is the deterministic fallback when extraction
	// yields no typed entity or visual term. Keep it bounded and provider-safe.
	if len(queries) == 0 && strings.TrimSpace(profile.Topic) != "" {
		queries = append(queries, profile.Topic)
	}
	queries = append(queries, visual...)
	return uniqueQueries(queries, limit)
}

func (k WeightedKeyword) ValueIfPresent() string { return strings.TrimSpace(k.Value) }

func isPersonEntity(entity ExtractedEntity) bool {
	return strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON")
}

func isVisualEntity(entity ExtractedEntity) bool {
	if strings.TrimSpace(entity.Value) == "" || isPersonEntity(entity) {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(entity.Type)) {
	case "KEYWORD", "DATE", "TIME", "CARDINAL", "ORDINAL", "MONEY", "PERCENT":
		return false
	default:
		return true
	}
}

func isRetrievalEntity(entity ExtractedEntity) bool {
	kind := strings.ToUpper(strings.TrimSpace(entity.Type))
	return strings.TrimSpace(entity.Value) != "" && kind != "KEYWORD" && kind != "CONCEPT"
}

func temporalTerms(profile SegmentSemanticProfile) []string {
	var out []string
	for _, term := range profile.Terms {
		if term.Kind == TermKindTemporal && strings.TrimSpace(term.Value) != "" {
			out = append(out, term.Value)
		}
	}
	return out
}

func termValues(profile SegmentSemanticProfile, kinds ...TermKind) []string {
	allowed := make(map[TermKind]struct{}, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = struct{}{}
	}
	var out []string
	for _, term := range profile.Terms {
		if _, ok := allowed[term.Kind]; ok && strings.TrimSpace(term.Value) != "" {
			out = append(out, term.Value)
		}
	}
	return out
}

func termQuery(profile SegmentSemanticProfile, kinds ...TermKind) string {
	return joinQuery(termValues(profile, kinds...)...)
}

func weightedValues(values []WeightedKeyword) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := value.ValueIfPresent(); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func joinQuery(parts ...string) string {
	seen := map[string]struct{}{}
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, word := range strings.Fields(part) {
			word = strings.TrimSpace(word)
			if word == "" {
				continue
			}
			key := strings.ToLower(word)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			words = append(words, word)
		}
	}
	if len(words) < 2 {
		return ""
	}
	return strings.Join(words, " ")
}

func uniqueQueries(candidates []string, limit int) []string {
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, limit)
	for _, candidate := range candidates {
		query := strings.Join(strings.Fields(strings.TrimSpace(candidate)), " ")
		if query == "" || len(strings.Fields(query)) < 2 {
			continue
		}
		key := strings.ToLower(query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
		if len(out) == limit {
			break
		}
	}
	return out
}

func normalizedQueryLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	return limit
}

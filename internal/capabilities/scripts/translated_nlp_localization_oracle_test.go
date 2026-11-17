package scriptgeneration

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// oracleLocalizedIdentityMatcher is the former Go implementation, retained
// only as a test oracle. Production uses the Rust matcher; this type keeps the
// existing translated-NLP integration tests meaningful without shipping a
// second authority in the binary. The Rust unit/protocol tests are the parity
// fixtures for the same cases.
type oracleLocalizedIdentityMatcher struct{}

func (oracleLocalizedIdentityMatcher) MatchLocalizedIdentities(_ context.Context, language, text string, sources []LocalizedSourceEntity) ([]LocalizedIdentityMatch, error) {
	var out []LocalizedIdentityMatch
	for index, source := range sources {
		kind := strings.TrimSpace(source.Kind)
		if kind == "" {
			continue
		}
		identity := strings.TrimSpace(firstNonEmptyIdentity(source.CanonicalName, source.Text))
		if identity == "" {
			continue
		}
		aliases := []string{identity}
		if strings.EqualFold(kind, "PERSON") {
			identity = oracleNormalizeVisualPersonName(identity)
			aliases = []string{identity}
			for _, run := range oraclePersonNameRuns(identity) {
				for start := 1; start < len(run); start++ {
					aliases = append(aliases, strings.Join(run[start:], " "))
				}
			}
		}
		for _, alias := range aliases {
			span, ok := findExactNameTokenSpan(text, alias)
			if !ok && strings.EqualFold(kind, "PERSON") {
				switch strings.ToLower(language) {
				case "pl", "de", "tr":
					span, ok = findInflectedPersonSpan(text, alias, language)
				}
			}
			if !ok || strings.TrimSpace(span.Text) == "" {
				continue
			}
			out = append(out, LocalizedIdentityMatch{
				SourceIndex: index,
				Kind:        kind,
				StartRune:   span.StartRune,
				EndRune:     span.EndRune,
				Surface:     span.Text,
			})
			break
		}
	}
	return out, nil
}

func (oracleLocalizedIdentityMatcher) CompleteLocalizedIdentities(_ context.Context, language, text string, sources []LocalizedSourceEntity, visual []string, existing []LocalizedIdentityMatch) ([]LocalizedIdentityMatch, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ru", "tr", "pl":
	default:
		return existing, nil
	}
	matched := make(map[int]struct{}, len(existing))
	for _, match := range existing {
		matched[match.SourceIndex] = struct{}{}
	}
	var people []int
	for index, source := range sources {
		if !strings.EqualFold(strings.TrimSpace(source.Kind), "PERSON") {
			continue
		}
		if _, ok := matched[index]; !ok {
			people = append(people, index)
		}
	}
	if len(people) == 0 {
		return existing, nil
	}
	type candidate struct {
		span scriptpkg.AnnotationSpan
	}
	candidates := make([]candidate, 0, len(visual))
	for _, value := range visual {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		span, ok := findExactNameTokenSpan(text, value)
		if !ok || span.EndRune <= span.StartRune {
			continue
		}
		occupied := false
		for _, match := range existing {
			if match.StartRune < span.EndRune && span.StartRune < match.EndRune {
				occupied = true
				break
			}
		}
		if !occupied {
			candidates = append(candidates, candidate{span: span})
		}
	}
	if len(candidates) != len(people) {
		return existing, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].span.StartRune < candidates[j].span.StartRune })
	out := append([]LocalizedIdentityMatch(nil), existing...)
	for index, candidate := range candidates {
		out = append(out, LocalizedIdentityMatch{
			SourceIndex: people[index],
			Kind:        "PERSON",
			StartRune:   candidate.span.StartRune,
			EndRune:     candidate.span.EndRune,
			Surface:     candidate.span.Text,
		})
	}
	return out, nil
}

// matchLocalizedSourceEntities keeps the old test entry point: it runs the
// oracle matcher through the same request/projection path production uses, so
// the tests also cover the Rust boundary mapping.
func matchLocalizedSourceEntities(text, language string, source *scriptpkg.SceneAnnotations) []localizedSourceMatch {
	sources, entities := localizedSourceRequest(source)
	matches, err := (oracleLocalizedIdentityMatcher{}).MatchLocalizedIdentities(context.Background(), language, text, sources)
	if err != nil {
		return nil
	}
	projected, err := localizedMatchesFromRust(text, entities, matches)
	if err != nil {
		return nil
	}
	return projected
}

func firstNonEmptyIdentity(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func oracleNormalizeVisualPersonName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "While "), "while "))
	for _, suffix := range []string{"'s", "’s"} {
		if len(value) > len(suffix) && strings.EqualFold(value[len(value)-len(suffix):], suffix) {
			value = strings.TrimSpace(value[:len(value)-len(suffix)])
			break
		}
	}
	return value
}

func oraclePersonNameRuns(text string) [][]string {
	var runs [][]string
	current := []string{}
	flush := func() {
		if len(current) > 0 {
			runs = append(runs, current)
			current = []string{}
		}
	}
	for _, word := range strings.Fields(text) {
		token := strings.Trim(word, ".,;:!?()[]{}\"“”'’")
		if oracleIsNameToken(token) {
			current = append(current, token)
			continue
		}
		flush()
	}
	flush()
	return runs
}

func oracleIsNameToken(token string) bool {
	if token == "" {
		return false
	}
	for index, character := range token {
		switch {
		case unicode.IsLetter(character):
			if index == 0 && !unicode.IsUpper(character) {
				return false
			}
		case character == '\'' || character == '’' || character == '-' || character == '.':
		default:
			return false
		}
	}
	return true
}

type oracleNameToken struct {
	start int
	end   int
	text  string
}

func findExactNameTokenSpan(text, candidate string) (scriptpkg.AnnotationSpan, bool) {
	want, have := oracleNameTokens(candidate), oracleNameTokens(text)
	if len(want) == 0 || len(have) < len(want) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for offset := range want {
			if !strings.EqualFold(want[offset].text, have[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			return oracleNameSpan(text, have[start], have[start+len(want)-1]), true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

func findInflectedPersonSpan(text, canonical, language string) (scriptpkg.AnnotationSpan, bool) {
	want, have := oracleNameTokens(canonical), oracleNameTokens(text)
	if len(want) < 2 || len(have) < len(want) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for offset := range want {
			if !oracleInflectedNameTokenMatches(language, want[offset].text, have[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			return oracleNameSpan(text, have[start], have[start+len(want)-1]), true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

func oracleNameSpan(text string, first, last oracleNameToken) scriptpkg.AnnotationSpan {
	return scriptpkg.AnnotationSpan{
		Text:      text[first.start:last.end],
		StartRune: utf8.RuneCountInString(text[:first.start]),
		EndRune:   utf8.RuneCountInString(text[:last.end]),
	}
}

func oracleInflectedNameTokenMatches(language, canonical, surface string) bool {
	canonical, surface = strings.ToLower(canonical), strings.ToLower(surface)
	if canonical == surface {
		return true
	}
	if utf8.RuneCountInString(canonical) < 3 || !strings.HasPrefix(surface, canonical) {
		return false
	}
	suffix := strings.TrimPrefix(surface, canonical)
	switch strings.ToLower(language) {
	case "pl":
		switch suffix {
		case "a", "ie", "y", "i", "u", "owi", "em", "ą", "ę", "om", "ami", "ach", "ów", "’a", "'a":
			return true
		}
	case "de":
		return suffix == "s"
	case "tr":
		switch suffix {
		case "'ın", "'in", "'un", "'ün", "'a", "'e", "'ı", "'i", "'u", "'ü", "ın", "in", "un", "ün", "a", "e", "ı", "i", "u", "ü":
			return true
		}
	}
	return false
}

func oracleNameTokens(text string) []oracleNameToken {
	var out []oracleNameToken
	start := -1
	for offset, character := range text {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || ((character == '\'' || character == '’') && start >= 0) {
			if start < 0 {
				start = offset
			}
			continue
		}
		if start >= 0 {
			out = append(out, oracleNameToken{start: start, end: offset, text: text[start:offset]})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, oracleNameToken{start: start, end: len(text), text: text[start:]})
	}
	return out
}

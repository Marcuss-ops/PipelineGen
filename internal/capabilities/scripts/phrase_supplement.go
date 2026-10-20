// Package scriptgeneration — phrase_supplement.go owns the grounded
// important-phrase supplement used by the overlay.prepare snapshot. Split out
// of runner_overlay_prepare.go to keep that file under the strict 600-LOC
// gate; same package, same behaviour.
package scriptgeneration

import (
	"sort"
	"strings"

	phrasepkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func supplementSourceImportantPhrases(ann *scriptpkg.SceneAnnotations, text, language string, limit int, corpus []string) {
	if ann == nil || limit <= 0 {
		return
	}
	blocked := make([][2]int, 0, len(ann.PrimaryEntities)+len(ann.SecondaryEntities))
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
		mentions := entity.Mentions
		if len(mentions) == 0 {
			value := strings.TrimSpace(entity.Text)
			if value == "" {
				value = strings.TrimSpace(entity.CanonicalName)
			}
			if span, ok := findEntitySpan(text, value); ok {
				mentions = []scriptpkg.AnnotationSpan{span}
			}
		}
		for _, mention := range mentions {
			if mention.EndRune > mention.StartRune {
				blocked = append(blocked, [2]int{mention.StartRune, mention.EndRune})
			}
		}
	}
	type candidate struct {
		span  scriptpkg.AnnotationSpan
		words int
	}
	byText := make(map[string]candidate, len(ann.ImportantPhrases)+limit)
	for _, phrase := range ann.ImportantPhrases {
		span, ok := findEntitySpan(text, phrase.Text)
		if !ok {
			continue
		}
		phrase.Text, phrase.StartRune, phrase.EndRune = span.Text, span.StartRune, span.EndRune
		byText[phraseKey(phrase.Text)] = candidate{span: phrase, words: len(strings.Fields(phrase.Text))}
	}
	// Ask for a full selector window even when the NLP source already filled
	// its per-scene quota. Keep short and long phrases in separate pools so
	// long candidates cannot consume the entire scene allowance before the
	// run-level short/long overlay budgets get a chance to work.
	selectorLimit := max(limit*3, limit)
	// The default phrase selector caps output at its lexicon MaxResults and
	// ranks complete/long phrases first. Filtering that small result afterward
	// leaves almost no short candidates. Query the 2–5-word lane directly so
	// short phrases have their own independently ranked candidate pool.
	for rank, phrase := range phrasepkg.ImportantPhrasesWithCorpusWordRange(text, blocked, selectorLimit, language, corpus, 2, 5) {
		span, ok := findEntitySpan(text, phrase)
		if !ok {
			continue
		}
		key := phraseKey(span.Text)
		item := candidate{span: scriptpkg.AnnotationSpan{
			Text: span.Text, StartRune: span.StartRune, EndRune: span.EndRune,
			Score: max(0.95-float64(rank)*0.01, 0.05), Kind: "key_statement",
		}, words: len(strings.Fields(span.Text))}
		if prior, exists := byText[key]; !exists || item.span.Score > prior.span.Score {
			byText[key] = item
		}
	}
	for rank, phrase := range phrasepkg.ImportantPhrasesWithCorpus(text, blocked, selectorLimit, language, corpus) {
		span, ok := findEntitySpan(text, phrase)
		if !ok {
			continue
		}
		phrase = span.Text
		words := len(strings.Fields(phrase))
		if words < 2 || words > 16 {
			continue
		}
		key := phraseKey(phrase)
		item := candidate{span: scriptpkg.AnnotationSpan{
			Text: phrase, StartRune: span.StartRune, EndRune: span.EndRune,
			Score: max(0.90-float64(rank)*0.01, 0.05), Kind: "key_statement",
		}, words: words}
		if prior, exists := byText[key]; !exists || item.span.Score > prior.span.Score {
			byText[key] = item
		}
	}
	short := make([]candidate, 0, len(byText))
	long := make([]candidate, 0, len(byText))
	for _, item := range byText {
		if item.words >= 2 && item.words <= 5 {
			short = append(short, item)
		} else if item.words >= 6 && item.words <= 16 {
			long = append(long, item)
		}
	}
	byEditorialStrength := func(candidates []candidate) {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].span.Score != candidates[j].span.Score {
				return candidates[i].span.Score > candidates[j].span.Score
			}
			if candidates[i].span.StartRune != candidates[j].span.StartRune {
				return candidates[i].span.StartRune < candidates[j].span.StartRune
			}
			return candidates[i].span.Text < candidates[j].span.Text
		})
	}
	byEditorialStrength(short)
	byEditorialStrength(long)
	selected := make([]scriptpkg.AnnotationSpan, 0, limit)
	shortSelected, longSelected := 0, 0
	// Explicit operator phrase requests are contractual and must survive
	// candidate balancing. Preserve them first, then use remaining slots for
	// the short and long candidate lanes.
	explicit := make([]candidate, 0)
	for _, item := range byText {
		if item.span.Score >= 1 {
			explicit = append(explicit, item)
		}
	}
	byEditorialStrength(explicit)
	for _, item := range explicit {
		if len(selected) >= limit {
			break
		}
		overlaps := false
		for _, prior := range selected {
			if item.span.StartRune < prior.EndRune && prior.StartRune < item.span.EndRune {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		selected = append(selected, item.span)
		if item.words <= 5 {
			shortSelected++
		} else {
			longSelected++
		}
	}
	shortTarget := max(0, (limit+1)/2-shortSelected)
	longTarget := max(0, limit/2-longSelected)
	appendNonOverlapping := func(candidates []candidate, target int) {
		for _, item := range candidates {
			if target <= 0 || len(selected) >= limit {
				break
			}
			overlaps := false
			for _, prior := range selected {
				if item.span.StartRune < prior.EndRune && prior.StartRune < item.span.EndRune {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			selected = append(selected, item.span)
			if item.words <= 5 {
				shortSelected++
			} else {
				longSelected++
			}
			target--
		}
	}
	appendNonOverlapping(short, shortTarget)
	appendNonOverlapping(long, longTarget)
	// If one lane lacks enough candidates in this scene, use the other lane's
	// remaining grounded candidates instead of returning an unnecessarily
	// short annotation list.
	if len(selected) < limit {
		appendNonOverlapping(short, limit-len(selected))
	}
	if len(selected) < limit {
		appendNonOverlapping(long, limit-len(selected))
	}
	ann.ImportantPhrases = selected
}

func phraseKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

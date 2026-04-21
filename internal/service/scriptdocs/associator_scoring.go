package scriptdocs

import (
	"slices"
	"strings"
	"unicode"

	"velox/go-master/internal/translation"
)

type SignalClass int

const (
	SignalClassEntity SignalClass = iota
	SignalClassSport
	SignalClassVisual
	SignalClassFallback
)

type ScoredSignal struct {
	Keyword    string
	Score      int
	Class      SignalClass
	Concept    string
	BaseConf   float64
}

func analyzeAndScore(frase string, conceptMap []clipConcept, domainContext string, fraseEntities []string) []ScoredSignal {
	fraseLower := strings.ToLower(frase)
	tokens := tokenizeWords(fraseLower)
	if len(tokens) == 0 {
		return nil
	}

	tokenSet := make(map[string]bool)
	for _, t := range tokens {
		tokenSet[t] = true
	}

	domainTokens := significantTokens(strings.ToLower(domainContext))
	domainBoost := isBoxingContext(domainTokens)

	var results []ScoredSignal

	for _, cm := range conceptMap {
		kwLower := normalizeKeyword(cm.Term)
		if len(kwLower) < 3 {
			continue
		}

		matched := false
		matchedToken := ""

		if tokenSet[kwLower] {
			matched = true
			matchedToken = kwLower
		} else if len(kwLower) >= 5 {
			for _, tok := range tokens {
				if strings.HasPrefix(tok, kwLower) {
					matched = true
					matchedToken = tok
					break
				}
			}
		}

		if matched {
			signalClass := classifySignal(kwLower, cm.Term)
			weight := getKeywordWeight(kwLower)

			if domainBoost && isBoxingSignal(kwLower) && len(fraseEntities) == 0 {
				weight += 2
			}

			isGenericOnly := isGenericOnlyKeyword(kwLower)
			if isGenericOnly {
				signalClass = SignalClassFallback
				weight = 1
			}

			results = append(results, ScoredSignal{
				Keyword:  matchedToken,
				Score:    weight,
				Class:    signalClass,
				Concept:  cm.Term,
				BaseConf: cm.BaseConf,
			})
		}
	}

	slices.SortFunc(results, func(a, b ScoredSignal) int {
		if a.Class != b.Class {
			return int(a.Class) - int(b.Class)
		}
		return b.Score - a.Score
	})

	return results
}

func isBoxingContext(tokens []string) bool {
	boxingTerms := map[string]bool{
		"boxe": true, "boxing": true, "boxer": true, "fight": true,
		"floyd": true, "mayweather": true, "tyson": true,
		"knockout": true, "ko": true, "ring": true, "gloves": true,
		"combat": true, "fighter": true, "champion": true,
	}
	for _, t := range tokens {
		if boxingTerms[t] {
			return true
		}
	}
	return false
}

func isBoxingSignal(kw string) bool {
	boxingSignals := map[string]bool{
		"boxing": true, "boxer": true, "fight": true, "fighter": true,
		"knockout": true, "ko": true, "ring": true, "gloves": true,
		"combat": true, "champion": true, "uppercut": true, "jab": true,
		"hook": true, "punch": true, "referee": true, "round": true,
	}
	return boxingSignals[kw]
}

func extractEntitiesFromPhrase(frase string) []string {
	fraseLower := strings.ToLower(frase)

	knownEntities := map[string][]string{
		"floyd": {"floyd", "mayweather", "money", "prettyboy"},
		"tyson": {"tyson", "ironmike", "mike", "baddest"},
		"ali":   {"ali", "muhammad", "greatest", "cassius"},
	}

	var found []string
	for entity, keywords := range knownEntities {
		for _, kw := range keywords {
			if strings.Contains(fraseLower, kw) {
				found = append(found, entity)
				break
			}
		}
	}
	return found
}

func applyEntityPenalty(conceptTerm string, fraseEntities []string) int {
	if len(fraseEntities) == 0 {
		return 0
	}

	conceptLower := strings.ToLower(conceptTerm)

	for _, ent := range fraseEntities {
		if strings.Contains(conceptLower, ent) {
			return 0
		}
	}

	if len(fraseEntities) > 0 {
		otherEntities := map[string]bool{
			"floyd": true, "mayweather": true, "tyson": true, "ali": true,
		}

		for _, ent := range fraseEntities {
			if otherEntities[conceptLower] && conceptLower != ent {
				return -50
			}
		}
	}

	return 0
}

func isEntityMismatch(conceptTerm string, fraseEntities []string) bool {
	if len(fraseEntities) == 0 {
		return false
	}

	conceptLower := strings.ToLower(conceptTerm)
	otherEntities := map[string]bool{
		"floyd": true, "mayweather": true, "tyson": true, "ali": true,
	}

	if otherEntities[conceptLower] {
		for _, ent := range fraseEntities {
			if conceptLower != ent {
				return true
			}
		}
	}

	return false
}

type VisualIntent int

const (
	VisualIntentUnknown VisualIntent = iota
	VisualIntentFaceoff
	VisualIntentTechnical
	VisualIntentCloseUp
	VisualIntentAtmosphere
	VisualIntentMedia
)

func classifyVisualIntent(frase string) VisualIntent {
	fraseLower := strings.ToLower(frase)

	closeUpPatterns := []string{
		"face", "eyes", "expression", "close-up", "close up", "looking at",
		"final shot", "frustration", "anger", "reaction", "smile", "grin",
		"pain", "exhaustion", "determination", "focus", "intensity",
	}
	for _, p := range closeUpPatterns {
		if strings.Contains(fraseLower, p) {
			return VisualIntentCloseUp
		}
	}

	faceoffPatterns := []string{
		"versus", "vs", "against", "clash", "collision", "two",
		"philosophies", "contrast", "comparison", "opponent", "opposing",
		"opposites", "face off", "showdown", "battle",
	}
	for _, p := range faceoffPatterns {
		if strings.Contains(fraseLower, p) {
			return VisualIntentFaceoff
		}
	}

	techPatterns := []string{
		"movement", "distance", "close the distance", "timing", "technique",
		"defense", "aggressive", "attack", "punch", "combination",
		"sparring", "training", "workout", "drill", "footwork",
		"strategy", "tactical", "counter", "hook", "jab", "uppercut",
	}
	for _, p := range techPatterns {
		if strings.Contains(fraseLower, p) {
			return VisualIntentTechnical
		}
	}

	mediaPatterns := []string{
		"interview", "press", "conference", "analyst", "commentator",
		"reporter", "media", "broadcast", "announcer", "speech",
		"statement", "address", "question", "answer", "discuss",
	}
	for _, p := range mediaPatterns {
		if strings.Contains(fraseLower, p) {
			return VisualIntentMedia
		}
	}

	atmoPatterns := []string{
		"arena", "crowd", "fans", "atmosphere", "stadium",
		"big fight", "event", "ceremony", "entrance", "walkout",
		"audience", "cheering", "roar", "explosion",
	}
	for _, p := range atmoPatterns {
		if strings.Contains(fraseLower, p) {
			return VisualIntentAtmosphere
		}
	}

	return VisualIntentUnknown
}

func preferredConceptsForIntent(intent VisualIntent, entity string) []string {
	switch intent {
	case VisualIntentCloseUp:
		if entity != "" {
			return []string{entity, "boxing", "fighter", "intense"}
		}
		return []string{"boxing", "fighter", "intense"}

	case VisualIntentFaceoff:
		return []string{"boxing", "arena", "fight", "tension", "confrontation"}

	case VisualIntentTechnical:
		return []string{"gym", "training", "boxing", "ring", "sparring", "workout"}

	case VisualIntentAtmosphere:
		return []string{"arena", "crowd", "boxing", "event", "stadium", " atmosphere"}

	case VisualIntentMedia:
		return []string{"press", "conference", "interview", "media"}

	default:
		if entity != "" {
			return []string{entity, "boxing", "gym", "arena"}
		}
		return []string{"boxing", "gym", "arena"}
	}
}

func classifySignal(kw string, concept string) SignalClass {
	entityTerms := map[string]bool{
		"floyd": true, "mayweather": true, "tyson": true, "mike tyson": true,
		"iron mike": true, "pretty boy": true, "money": true,
	}

	sportTerms := map[string]bool{
		"boxing": true, "boxer": true, "fight": true, "fighter": true,
		"knockout": true, "ko": true, "ring": true, "gloves": true,
		"combat": true, "champion": true, "championship": true,
		"punch": true, "uppercut": true, "jab": true, "hook": true,
		"referee": true, "round": true, "title": true, "weight": true,
	}

	visualTerms := map[string]bool{
		"gym": true, "training": true, "workout": true, "sparring": true,
		"arena": true, "stadium": true, "crowd": true, "audience": true,
		"press": true, "conference": true, "media": true, "microphones": true,
		" Spotlight": true, "lights": true, "ring": true,
	}

	if entityTerms[kw] || entityTerms[concept] {
		return SignalClassEntity
	}
	if sportTerms[kw] || sportTerms[concept] {
		return SignalClassSport
	}
	if visualTerms[kw] || visualTerms[concept] {
		return SignalClassVisual
	}

	return SignalClassFallback
}

func isGenericOnlyKeyword(kw string) bool {
	genericOnly := map[string]bool{
		"match": true, "game": true, "team": true, "player": true,
		"fans": true, "public": true, "people": true, "person": true,
		"man": true, "woman": true, "crowd": true, "audience": true,
		"event": true, "sport": true, "news": true, "story": true,
	}
	return genericOnly[kw]
}

func scoreConceptForPhrase(fraseLower string, cm clipConcept) (int, string) {
	tokens := tokenizeWords(fraseLower)
	if len(tokens) == 0 {
		return 0, ""
	}
	tokenSet := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = true
	}

	bestKeyword := ""
	bestLen := 0
	matchScore := 0
	for _, kw := range cm.Keywords {
		kwLower := normalizeKeyword(kw)
		if len(kwLower) < 3 {
			continue
		}

		matched := tokenSet[kwLower]
		if !matched && len(kwLower) >= 5 {
			for _, tok := range tokens {
				if strings.HasPrefix(tok, kwLower) {
					matched = true
					break
				}
			}
		}
		if matched {
			weight := getKeywordWeight(kwLower)
			matchScore += weight
			if len(kwLower) > bestLen {
				bestLen = len(kwLower)
				bestKeyword = kwLower
			}
		}
	}
	return matchScore, bestKeyword
}

var keywordWeights = map[string]int{
	"floyd": 10, "tyson": 10, "mayweather": 10,
	"boxing": 8, "boxer": 8, "knockout": 8, "gloves": 8, "ring": 7,
	"fight": 7, "fighter": 7, "combat": 7,
	"champion": 6, "championship": 6, "ko": 8, "uppercut": 7, "jab": 7, "hook": 7, "punch": 7,
	"gym": 5, "training": 5, "workout": 5, "sparring": 5,
	"arena": 4, "stadium": 4, "crowd": 3, "fans": 2, "audience": 3,
	"press": 4, "conference": 4, "media": 4,
	"match": 1, "game": 1, "team": 1, "player": 1,
	"people": 1, "person": 1, "man": 1, "woman": 1,
	"city": 1, "life": 1, "success": 1, "story": 1,
}

func getKeywordWeight(kw string) int {
	if w, ok := keywordWeights[kw]; ok {
		return w
	}
	return 2
}

func normalizeKeyword(raw string) string {
	return strings.TrimFunc(strings.ToLower(strings.TrimSpace(raw)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func isTooGenericArtlistTerm(term string) bool {
	t := normalizeKeyword(term)
	if t == "" {
		return true
	}
	generic := map[string]bool{
		"people": true, "person": true, "man": true, "woman": true, "human": true,
		"school": true, "student": true, "city": true, "street": true, "world": true,
		"success": true, "story": true, "life": true, "video": true, "music": true,
		"dopo": true, "nonostante": true, "breve": true, "questo": true,
	}
	return generic[t]
}

func isDisallowedArtlistTermForContext(term string, combatContext bool) bool {
	t := normalizeKeyword(term)
	if t == "" {
		return true
	}
	if isTooGenericArtlistTerm(t) {
		return true
	}
	if combatContext {
		switch t {
		case "business", "technology", "people", "person", "city":
			return true
		}
	}
	return false
}

func isUsableDynamicKeyword(raw string) bool {
	kw := normalizeKeyword(raw)
	if len(kw) < 3 {
		return false
	}
	hasLetter := false
	for _, r := range kw {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

func phraseMatchesTopicFolder(phrase, topic string, stockFolder StockFolder) (bool, string) {
	if stockFolder.ID == "" || stockFolder.ID == "root" || stockFolder.Name == "Stock" {
		return false, ""
	}
	phraseLower := strings.ToLower(phrase)
	for _, tok := range significantTokens(topic) {
		if strings.Contains(phraseLower, tok) {
			return true, tok
		}
	}
	folderTokens := significantTokens(strings.ReplaceAll(stockFolder.Name, "/", " "))
	for _, tok := range folderTokens {
		if strings.Contains(phraseLower, tok) {
			return true, tok
		}
	}
	return false, ""
}

func resolveReferenceKeyword(
	frase string,
	topic string,
	bestKeyword string,
	bestTerm string,
	translator *translation.ClipSearchTranslator,
) string {
	refKeyword := normalizeKeyword(bestKeyword)
	if refKeyword == "" {
		refKeyword = normalizeKeyword(bestTerm)
	}
	if refKeyword == "" {
		toks := significantTokens(frase)
		if len(toks) > 0 {
			refKeyword = normalizeKeyword(toks[0])
		}
	}
	if refKeyword == "" {
		toks := significantTokens(topic)
		if len(toks) > 0 {
			refKeyword = normalizeKeyword(toks[0])
		}
	}
	if refKeyword != "" && translator != nil {
		tr := translator.TranslateKeywords([]string{refKeyword})
		if len(tr) > 0 {
			refKeyword = normalizeKeyword(tr[0])
		}
	}
	return refKeyword
}

func clipMatchesContext(clip ArtlistClip, contextTokens []string, topicConceptTerms []string) (bool, string) {
	blob := strings.ToLower(strings.TrimSpace(clip.Term + " " + clip.Name + " " + clip.Folder))
	if blob == "" {
		return false, ""
	}

	seen := make(map[string]bool)
	for _, t := range topicConceptTerms {
		t = normalizeKeyword(t)
		if len(t) < 3 || seen[t] {
			continue
		}
		seen[t] = true
		if strings.Contains(blob, t) {
			return true, t
		}
	}
	for _, t := range contextTokens {
		t = normalizeKeyword(t)
		if len(t) < 4 || seen[t] {
			continue
		}
		seen[t] = true
		if strings.Contains(blob, t) {
			return true, t
		}
	}
	return false, ""
}

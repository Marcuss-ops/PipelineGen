package scriptdocs

import (
	"math/rand"
	"sort"
	"strings"
	"time"
)

// associateFullArtlistFast builds Artlist-only associations using the local Artlist index.
// It intentionally avoids dynamic download/search to keep fullartlist mode fast and stable.
func (s *ScriptDocService) associateFullArtlistFast(frasi []string, topic string) []ClipAssociation {
	if s.artlistIndex == nil || len(frasi) == 0 {
		return nil
	}

	type candidate struct {
		term  string
		score int
		conf  float64
	}
	context := strings.ToLower(topic + " " + strings.Join(frasi, " "))
	combatContext := looksLikeCombatContext(context)
	var candidates []candidate
	seenCand := make(map[string]bool)

	addCandidate := func(term string, score int, conf float64) {
		t := normalizeKeyword(term)
		if t == "" || seenCand[t] || isDisallowedArtlistTermForContext(t, combatContext) {
			return
		}
		seenCand[t] = true
		candidates = append(candidates, candidate{term: t, score: score, conf: conf})
	}

	for _, cm := range conceptMap {
		score, _ := scoreConceptForPhrase(context, cm)
		if score <= 0 {
			continue
		}
		addCandidate(cm.Term, score, cm.BaseConf)
	}
	for _, tok := range significantTokens(topic) {
		if clips := s.artlistClipsForTerm(tok); len(clips) > 0 {
			addCandidate(tok, 1, 0.70)
		}
	}
	for _, tok := range significantTokens(strings.Join(frasi, " ")) {
		if clips := s.artlistClipsForTerm(tok); len(clips) > 0 {
			addCandidate(tok, 1, 0.68)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].conf != candidates[j].conf {
			return candidates[i].conf > candidates[j].conf
		}
		return len(candidates[i].term) > len(candidates[j].term)
	})

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	usedClipIDs := make(map[string]bool)
	associations := make([]ClipAssociation, 0, 8)

	for _, c := range candidates {
		if len(associations) >= 8 {
			break
		}
		clips := s.artlistClipsForTerm(c.term)
		if len(clips) == 0 {
			continue
		}
		clip, ok := pickRandomArtlistClip(rng, clips, usedClipIDs, func(string) bool { return true })
		if !ok || clip == nil {
			continue
		}
		usedClipIDs[clip.URL] = true
		phrase := frasi[len(associations)%len(frasi)]
		associations = append(associations, ClipAssociation{
			Phrase:         phrase,
			Type:           "ARTLIST",
			Clip:           clip,
			Confidence:     c.conf,
			MatchedKeyword: c.term,
		})
	}
	return associations
}

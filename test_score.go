package main

import (
	"fmt"
	"strings"
)

type ClipConcept struct {
	Keywords []string
	Term     string
	BaseConf float64
}

type clipConcept = ClipConcept

var conceptMap = []clipConcept{
	{[]string{"persone", "persona", "uomo", "donna", "gente", "pubblico", "life", "living", "human"}, "people", 0.85},
	{[]string{"città", "arresto", "polizia", "prison", "crime", "violence", "criminal", "jail", "arrest"}, "city", 0.90},
	{[]string{"tech", "tecnologia", "online", "internet", "digitale", "tiktok", "youtube", "social"}, "technology", 0.80},
	{[]string{"gym", "palestra", "allenamento", "fitness", "workout", "training", "strength", "pesi", "muscle"}, "gym", 0.80},
	{[]string{"fight", "boxing", "boxer", "sport", "knockout", "champion", "medal", "ring", "gloves", "combattimento", "boxe", "pugile"}, "gym", 0.78},
	{[]string{"soldi", "money", "finance", "business", "azienda", "investimento", "denaro", "bank"}, "business", 0.75},
}

func scoreConceptForPhrase(fraseLower string, cm clipConcept) (int, string) {
	bestKeyword := ""
	bestLen := 0
	matchCount := 0

	for _, kw := range cm.Keywords {
		kwLower := strings.ToLower(kw)
		if strings.Contains(fraseLower, kwLower) {
			matchCount++
			if len(kw) > bestLen {
				bestLen = len(kw)
				bestKeyword = kw
			}
		}
	}

	return matchCount, bestKeyword
}

func main() {
	tests := []struct {
		name   string
		phrase string
		want   string
	}{
		{"boxing champion", "Davis swiftly became a national amateur champion, winning gold at the 2016 Junior Olympics", "gym"},
		{"street fighting", "Davis's early life was marked by a difficult upbringing and a fascination with street fighting", "gym"},
		{"gym training", "Mike Tyson si allenava in palestra ogni giorno, sollevando pesi massimi", "gym"},
		{"city arrest", "He was arrested in Baltimore and taken to city prison", "city"},
		{"business money", "He earned millions in revenue from his company, making big money investments", "business"},
		{"technology social", "He became famous on TikTok and YouTube, using social media platforms", "technology"},
		{"early life people", "Davis's early life was marked by difficult upbringing", "people"},
	}

	for _, tt := range tests {
		phraseLower := strings.ToLower(tt.phrase)

		type score struct {
			term    string
			count   int
			keyword string
		}
		var scores []score

		for _, cm := range conceptMap {
			count, kw := scoreConceptForPhrase(phraseLower, cm)
			if count > 0 {
				scores = append(scores, score{term: cm.Term, count: count, keyword: kw})
			}
		}

		for i := 0; i < len(scores)-1; i++ {
			for j := i + 1; j < len(scores); j++ {
				if scores[j].count > scores[i].count {
					scores[i], scores[j] = scores[j], scores[i]
				}
			}
		}

		if len(scores) == 0 {
			fmt.Printf("❌ %s: No concepts matched\n", tt.name)
			continue
		}

		best := scores[0]
		if best.term == tt.want {
			fmt.Printf("✅ %s: %s (score=%d, keyword=%q)\n", tt.name, best.term, best.count, best.keyword)
		} else {
			fmt.Printf("❌ %s: got %s, want %s (score=%d, keyword=%q)\n", tt.name, best.term, tt.want, best.count, best.keyword)
			for _, s := range scores {
				fmt.Printf("   - %s: score=%d\n", s.term, s.count)
			}
		}
	}
}

package scriptdocs

import "strings"

type ClipConcept struct {
	Keywords []string `json:"keywords"`
	Term     string   `json:"term"`
	BaseConf float64  `json:"base_conf"`
}

type clipConcept = ClipConcept

func GetConceptMap() []ClipConcept {
	return conceptMap
}

var conceptMap = []clipConcept{
	{[]string{"persone", "persona", "uomo", "donna", "gente", "pubblico", "people", "person", "crowd"}, "people", 0.85},
	{[]string{"città", "city", "arrest", "police", "prison", "crime"}, "city", 0.90},
	{[]string{"tech", "tecnologia", "technology", "online", "internet", "digital"}, "technology", 0.80},
	{[]string{"soldi", "finanza", "business", "money", "finance", "economy"}, "business", 0.75},
	{[]string{"gym", "palestra", "allenamento", "workout", "training", "fitness"}, "gym", 0.80},
	{[]string{"combattimento", "lotta", "boxe", "fight", "boxing", "combat"}, "gym", 0.78},
	{[]string{"correre", "corsa", "running", "marathon", "sprint"}, "running", 0.75},
	{[]string{"yoga", "meditazione", "meditation", "stretching", "flexibility"}, "yoga", 0.78},
	{[]string{"calcio", "soccer", "football", "goal", "stadium"}, "soccer", 0.80},
	{[]string{"nuoto", "swimming", "pool", "sea", "water"}, "swimming", 0.75},
	{[]string{"natura", "nature", "ambiente", "environment"}, "nature", 0.82},
	{[]string{"tramonto", "sunset", "evening", "dusk", "dawn"}, "sunset", 0.85},
	{[]string{"oceano", "ocean", "mare", "sea", "waves"}, "ocean", 0.83},
	{[]string{"montagna", "mountain", "peak", "summit", "climbing"}, "mountain", 0.80},
	{[]string{"foresta", "forest", "bosco", "trees", "woods"}, "forest", 0.80},
	{[]string{"pioggia", "rain", "storm", "shower"}, "rain", 0.78},
	{[]string{"neve", "snow", "winter", "cold"}, "snow", 0.78},
	{[]string{"cucina", "cooking", "kitchen", "chef", "food"}, "cooking", 0.82},
	{[]string{"cane", "dog", "puppy", "canine"}, "dog", 0.85},
	{[]string{"gatto", "cat", "kitten", "feline"}, "cat", 0.85},
	{[]string{"uccello", "bird", "flying", "feather"}, "bird", 0.82},
	{[]string{"cavallo", "horse", "riding", "stable"}, "horse", 0.82},
	{[]string{"farfalla", "butterfly", "caterpillar"}, "butterfly", 0.80},
	{[]string{"ragno", "spider", "web"}, "spider", 0.80},
	{[]string{"viaggio", "travel", "tourism", "vacation"}, "travel", 0.78},
	{[]string{"auto", "car", "vehicle", "road"}, "car", 0.78},
	{[]string{"treno", "train", "railway", "station"}, "train", 0.78},
	{[]string{"aereo", "airplane", "flight", "airport"}, "airplane", 0.78},
	{[]string{"concerto", "concert", "stage", "live music"}, "concert", 0.80},
	{[]string{"musica", "music", "instrument", "melody"}, "music", 0.80},
	{[]string{"danza", "dance", "ballet", "choreography"}, "dance", 0.78},
	{[]string{"festa", "party", "celebration"}, "party", 0.75},
	{[]string{"matrimonio", "wedding", "bride", "groom"}, "wedding", 0.82},
	{[]string{"famiglia", "family", "parents", "children"}, "family", 0.80},
	{[]string{"educazione", "education", "school", "student"}, "education", 0.78},
	{[]string{"scienza", "science", "laboratory", "research"}, "science", 0.78},
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

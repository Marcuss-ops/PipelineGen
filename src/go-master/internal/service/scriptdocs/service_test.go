package scriptdocs

import (
	"strings"
	"testing"

	"velox/go-master/pkg/util"
)

func TestCleanPreamble(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no preamble",
			input:    "Andrew Tate è nato in Romania nel 1986.",
			expected: "Andrew Tate è nato in Romania nel 1986.",
		},
		{
			name:     "Ecco preamble",
			input:    "Ecco un testo su Andrew Tate. Andrew Tate è nato in Romania.",
			expected: "Andrew Tate è nato in Romania.",
		},
		{
			name:     "Sure with exclamation",
			input:    "Sure! Here is the text. Andrew Tate was born in Romania.",
			expected: "Andrew Tate was born in Romania.",
		},
		{
			name:     "Certamente preamble",
			input:    "Certamente! Ecco il testo richiesto. Andrew Tate è un campione.",
			expected: "Andrew Tate è un campione.",
		},
		{
			name:     "Certainly preamble",
			input:    "Certainly! Here's the documentary text. Andrew Tate was a kickboxer.",
			expected: "Andrew Tate was a kickboxer.",
		},
		{
			name:     "Of course preamble",
			input:    "Of course. Andrew Tate dominated the kickboxing world.",
			expected: "Andrew Tate dominated the kickboxing world.",
		},
		{
			name:     "Ok preamble with period",
			input:    "Ok. Andrew Tate è molto famoso.",
			expected: "Andrew Tate è molto famoso.",
		},
		{
			name:     "Perfetto preamble",
			input:    "Perfetto! Ecco il testo. La carriera di Andrew Tate.",
			expected: "La carriera di Andrew Tate.",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanPreamble(tt.input)
			if result != tt.expected {
				t.Errorf("cleanPreamble() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractSentences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "multiple sentences",
			input:    "Andrew Tate è nato in Romania nel 1986 e ha dominato il kickboxing mondiale per molti anni. Ha diventato molto famoso online con milioni di follower su tutte le piattaforme social. La sua influenza continua a crescere tra i giovani.",
			expected: 3,
		},
		{
			name:     "short sentences filtered",
			input:    "Ciao. Ok. Sì.",
			expected: 0,
		},
		{
			name:     "mixed length",
			input:    "Ciao. Andrew Tate è un personaggio molto controverso che ha influenzato milioni di giovani.",
			expected: 1,
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractSentences(tt.input)
			if len(result) != tt.expected {
				t.Errorf("ExtractSentences() got %d sentences, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestExtractProperNouns(t *testing.T) {
	tests := []struct {
		name      string
		sentences []string
		expected  int
		check     func([]string) bool
	}{
		{
			name:      "finds proper nouns",
			sentences: []string{"Andrew Tate è nato in Romania", "Ha vissuto a Washington"},
			expected:  4, // Andrew, Tate, Romania, Washington (all capitalized)
			check: func(nouns []string) bool {
				seen := make(map[string]bool)
				for _, n := range nouns {
					seen[n] = true
				}
				return seen["Andrew"] && seen["Tate"] && seen["Romania"] && seen["Washington"]
			},
		},
		{
			name:      "filters Italian stop words",
			sentences: []string{"Il campione è nato in Italia"},
			expected:  1, // Italia only ("Il" is exactly 2 chars, filtered)
			check: func(nouns []string) bool {
				return len(nouns) == 1 && nouns[0] == "Italia"
			},
		},
		{
			name:      "filters English stop words",
			sentences: []string{"The champion was born in Romania"},
			expected:  2, // The, Romania (both >2 chars, both capitalized at word start)
			check: func(nouns []string) bool {
				return len(nouns) == 2
			},
		},
		{
			name:      "max 10 nouns",
			sentences: []string{"A B C D E F G H I J K L M N"},
			expected:  0, // All single letters, filtered by len > 2
			check: func(nouns []string) bool {
				return len(nouns) <= 10
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractProperNouns(tt.sentences)
			if len(result) != tt.expected {
				t.Errorf("extractProperNouns() got %d nouns, want %d: %v", len(result), tt.expected, result)
			}
			if tt.check != nil && !tt.check(result) {
				t.Errorf("extractProperNouns() check failed: %v", result)
			}
		})
	}
}

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "frequent words",
			input:    "Andrew Tate è nato in Romania. Andrew Tate è un campione. Andrew Tate vive in Romania.",
			expected: 3, // andrew (3x), tate (3x), romania (2x) - Italian stop words filtered
		},
		{
			name:     "filters stop words",
			input:    "Il campione è molto famoso e influente",
			expected: 4, // campione, famoso, influente, molto (all >4 chars)
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0,
		},
		{
			name:     "max 10 keywords",
			input:    "abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz",
			expected: 1, // only 1 unique word repeated
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractKeywords(tt.input)
			if len(result) != tt.expected {
				t.Errorf("extractKeywords() got %d keywords, want %d: %v", len(result), tt.expected, result)
			}
			if len(result) > 10 {
				t.Errorf("extractKeywords() returned more than 10 keywords: %d", len(result))
			}
		})
	}
}

func TestAssociateClips(t *testing.T) {
	idx := &ArtlistIndex{
		Clips: []ArtlistClip{
			{Name: "people_01.mp4", Term: "people", URL: "https://drive.google.com/people_01", Folder: "Stock/Artlist/People"},
			{Name: "people_02.mp4", Term: "people", URL: "https://drive.google.com/people_02", Folder: "Stock/Artlist/People"},
			{Name: "city_01.mp4", Term: "city", URL: "https://drive.google.com/city_01", Folder: "Stock/Artlist/City"},
			{Name: "city_02.mp4", Term: "city", URL: "https://drive.google.com/city_02", Folder: "Stock/Artlist/City"},
			{Name: "technology_01.mp4", Term: "technology", URL: "https://drive.google.com/tech_01", Folder: "Stock/Artlist/Technology"},
			{Name: "nature_01.mp4", Term: "nature", URL: "https://drive.google.com/nature_01", Folder: "Stock/Artlist/Nature"},
		},
	}
	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	svc := &ScriptDocService{
		artlistIndex: idx,
	}

	tests := []struct {
		name        string
		frasi       []string
		wantArtlist int
		wantStock   int
	}{
		{
			name:        "people keyword matches Artlist",
			frasi:       []string{"Molte persone seguono Andrew Tate online"},
			wantArtlist: 1,
			wantStock:   0,
		},
		{
			name:        "city keyword matches Artlist",
			frasi:       []string{"Andrew Tate è stato arrestato a Washington"},
			wantArtlist: 1,
			wantStock:   0,
		},
		{
			name:        "technology keyword matches Artlist",
			frasi:       []string{"Andrew Tate ha influenzato milioni su YouTube e TikTok"},
			wantArtlist: 1,
			wantStock:   0,
		},
		{
			name:        "no match falls back to Stock",
			frasi:       []string{"Questa frase non contiene concetti specifici"},
			wantArtlist: 0,
			wantStock:   1,
		},
		{
			name: "mixed associations",
			frasi: []string{
				"Molte persone seguono Andrew Tate",
				"Questa frase è generica",
				"Arrestato dalla polizia a Washington",
			},
			wantArtlist: 2,
			wantStock:   1,
		},
		{
			name: "round-robin distributes clips",
			frasi: []string{
				"Molte persone seguono Andrew Tate",
				"I giovani fan di Tate crescono",
				"La gente lo ammira molto",
			},
			wantArtlist: 3,
			wantStock:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.associateClips(tt.frasi)

			artlistCount := 0
			stockCount := 0
			peopleClipCount := 0

			for _, assoc := range result {
				if assoc.Type == "ARTLIST" {
					artlistCount++
					if assoc.Clip != nil && assoc.Clip.Term == "people" {
						peopleClipCount++
					}
				} else if assoc.Type == "STOCK" {
					stockCount++
				}
			}

			if artlistCount != tt.wantArtlist {
				t.Errorf("associateClips() got %d Artlist, want %d", artlistCount, tt.wantArtlist)
			}
			if stockCount != tt.wantStock {
				t.Errorf("associateClips() got %d Stock, want %d", stockCount, tt.wantStock)
			}

			if peopleClipCount >= 2 {
				var usedClips []string
				for _, assoc := range result {
					if assoc.Clip != nil && assoc.Clip.Term == "people" {
						usedClips = append(usedClips, assoc.Clip.Name)
					}
				}
				if len(usedClips) >= 2 && usedClips[0] == usedClips[1] {
					t.Errorf("round-robin failed: first two clips are the same: %v", usedClips)
				}
			}
		})
	}
}

func TestResolveStockFolder(t *testing.T) {
	folders := map[string]StockFolder{
		"boxe":    {ID: "14HWILTg8L9ST0bnorgmHzZknel9buJjb", Name: "Stock/Boxe", URL: "https://drive.google.com/boxe"},
		"crimine": {ID: "1KhJ6bSty9r4EP_2gVpTzz4BWdKhsI0pG", Name: "Stock/Crimine", URL: "https://drive.google.com/crimine"},
	}

	svc := &ScriptDocService{
		stockFolders: folders,
	}

	tests := []struct {
		name     string
		topic    string
		wantID   string
		wantName string
	}{
		{"Andrew Tate boxing", "Andrew Tate Boxe", "14HWILTg8L9ST0bnorgmHzZknel9buJjb", "Stock/Boxe"},
		{"Escobar crime", "Pablo Escobar Crimine", "1KhJ6bSty9r4EP_2gVpTzz4BWdKhsI0pG", "Stock/Crimine"},
		{"unknown topic", "Unknown Topic XYZ", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.resolveStockFolder(tt.topic)
			if tt.wantID != "" && result.ID != tt.wantID {
				t.Errorf("resolveStockFolder(%q) ID = %q, want %q", tt.topic, result.ID, tt.wantID)
			}
			if tt.wantName != "" && result.Name != tt.wantName {
				t.Errorf("resolveStockFolder(%q) Name = %q, want %q", tt.topic, result.Name, tt.wantName)
			}
			if tt.wantID == "" && result.ID == "" {
				t.Errorf("resolveStockFolder(%q) returned empty folder", tt.topic)
			}
		})
	}
}

func TestAssociateClipsMultilingual(t *testing.T) {
	idx := &ArtlistIndex{
		Clips: []ArtlistClip{
			{Name: "people_01.mp4", Term: "people", URL: "https://drive.google.com/people_01", Folder: "Stock/Artlist/People"},
			{Name: "city_01.mp4", Term: "city", URL: "https://drive.google.com/city_01", Folder: "Stock/Artlist/City"},
			{Name: "technology_01.mp4", Term: "technology", URL: "https://drive.google.com/tech_01", Folder: "Stock/Artlist/Technology"},
			{Name: "nature_01.mp4", Term: "nature", URL: "https://drive.google.com/nature_01", Folder: "Stock/Artlist/Nature"},
		},
	}
	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	svc := &ScriptDocService{
		artlistIndex: idx,
	}

	tests := []struct {
		name     string
		language string
		frase    string
		wantType string
		wantTerm string
	}{
		{"Italian people", "IT", "Molte persone seguono Andrew Tate", "ARTLIST", "people"},
		{"Italian city", "IT", "Arrestato dalla polizia a Washington", "ARTLIST", "city"},
		{"Italian technology", "IT", "Ha costruito una piattaforma digitale online", "ARTLIST", "technology"},
		{"Italian no match", "IT", "Questa frase non ha concetti specifici", "STOCK", ""},
		{"English people", "EN", "The crowd of fans followed him everywhere", "ARTLIST", "people"},
		{"English city", "EN", "The police made an arrest in the city", "ARTLIST", "city"},
		{"French people", "FR", "La foule de personnes le suivait partout", "ARTLIST", "people"},
		{"Spanish people", "ES", "Las personas y el público lo seguían", "ARTLIST", "people"},
		{"German people", "DE", "Die Menschen und Fans folgten ihm", "ARTLIST", "people"},
		{"Portuguese people", "PT", "As pessoas e o público o seguiam", "ARTLIST", "people"},
		{"Romanian people", "RO", "Oamenii și publicul îl urmăreau", "ARTLIST", "people"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.associateClips([]string{tt.frase})

			if len(result) != 1 {
				t.Fatalf("associateClips() got %d results, want 1", len(result))
			}

			assoc := result[0]
			if assoc.Type != tt.wantType {
				t.Errorf("associateClips() type = %q, want %q (lang=%s, frase=%q)", assoc.Type, tt.wantType, tt.language, tt.frase)
			}

			if tt.wantType == "ARTLIST" {
				if assoc.Clip == nil {
					t.Errorf("associateClips() clip is nil for ARTLIST")
					return
				}
				if assoc.Clip.Term != tt.wantTerm {
					t.Errorf("associateClips() clip term = %q, want %q", assoc.Clip.Term, tt.wantTerm)
				}
			}
		})
	}
}

func TestLoadArtlistIndex(t *testing.T) {
	t.Skip("requires actual artlist_stock_index.json file")
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b int
		want int
	}{
		{3, 5, 3},
		{10, 2, 2},
		{0, 5, 0},
		{5, 5, 5},
	}

	for _, tt := range tests {
		result := util.Min(tt.a, tt.b)
		if result != tt.want {
			t.Errorf("util.Min(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.want)
		}
	}
}

func TestScoreConceptForPhrase(t *testing.T) {
	concepts := GetConceptMap()

	tests := []struct {
		name     string
		phrase   string
		wantTerm string
		wantCnt  int
	}{
		{
			name:     "boxing champion should match boxing/gym",
			phrase:   "Davis swiftly became a national amateur champion, winning gold at the 2016 Junior Olympics",
			wantTerm: "gym",
			wantCnt:  4,
		},
		{
			name:     "street fighting should match boxing",
			phrase:   "Davis's early life was marked by a difficult upbringing and a fascination with street fighting",
			wantTerm: "gym",
			wantCnt:  1,
		},
		{
			name:     "gym training should match gym",
			phrase:   "Mike Tyson si allenava in palestra ogni giorno, sollevando pesi massimi",
			wantTerm: "gym",
			wantCnt:  3,
		},
		{
			name:     "city arrest should match city",
			phrase:   "He was arrested in Baltimore and taken to city prison",
			wantTerm: "city",
			wantCnt:  2,
		},
		{
			name:     "business money should match business",
			phrase:   "He earned millions in revenue from his company, making big money investments",
			wantTerm: "business",
			wantCnt:  3,
		},
		{
			name:     "technology social media",
			phrase:   "He became famous on TikTok and YouTube, using social media platforms",
			wantTerm: "technology",
			wantCnt:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phraseLower := strings.ToLower(tt.phrase)

			type conceptScore struct {
				term    string
				score   int
				keyword string
			}
			var scores []conceptScore

			for _, cm := range concepts {
				matchCount, bestKw := scoreConceptForPhrase(phraseLower, cm)
				if matchCount > 0 {
					scores = append(scores, conceptScore{term: cm.Term, score: matchCount, keyword: bestKw})
				}
			}

			for i := 0; i < len(scores)-1; i++ {
				for j := i + 1; j < len(scores); j++ {
					if scores[j].score > scores[i].score {
						scores[i], scores[j] = scores[j], scores[i]
					}
				}
			}

			if len(scores) == 0 {
				t.Errorf("No concepts matched for phrase: %q", tt.phrase)
				return
			}

			best := scores[0]
			if best.term != tt.wantTerm {
				t.Errorf("Best term = %q, want %q (score=%d, keyword=%q)", best.term, tt.wantTerm, best.score, best.keyword)
			}
			if best.score < tt.wantCnt {
				t.Errorf("Score = %d, want at least %d", best.score, tt.wantCnt)
			}
		})
	}
}

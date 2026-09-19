package phrases

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// These are deliberately unseen editorial texts. The test never names an
// expected phrase: it certifies only the selector's output properties.
var propertyTexts = map[string][]string{
	"it":    {"Mike Tyson allena movimenti disciplinati a Brooklyn.", "NASA coordina missioni scientifiche da Cape Canaveral.", "Christopher Wray dirige politiche pubbliche a Washington."},
	"en":    {"Mike Tyson trains disciplined movement in Brooklyn.", "NASA coordinates scientific missions from Cape Canaveral.", "Christopher Wray directs public policy in Washington."},
	"pl":    {"Mike Tyson trenuje zdyscyplinowane ruchy w Brooklyn.", "NASA koordynuje misje naukowe z Cape Canaveral.", "Christopher Wray kieruje polityką publiczną w Washington."},
	"ru":    {"Mike Tyson тренирует дисциплинированные движения в Brooklyn.", "NASA координирует научные миссии из Cape Canaveral.", "Christopher Wray руководит государственной политикой в Washington."},
	"de":    {"Mike Tyson trainiert disziplinierte Bewegungen in Brooklyn.", "NASA koordiniert wissenschaftliche Missionen aus Cape Canaveral.", "Christopher Wray leitet öffentliche Politik in Washington."},
	"es":    {"Mike Tyson entrena movimientos disciplinados en Brooklyn.", "NASA coordina misiones científicas desde Cape Canaveral.", "Christopher Wray dirige políticas públicas en Washington."},
	"pt-BR": {"Mike Tyson treina movimentos disciplinados em Brooklyn.", "NASA coordena missões científicas de Cape Canaveral.", "Christopher Wray dirige políticas públicas em Washington."},
	"fr":    {"Mike Tyson entraîne des mouvements disciplinés à Brooklyn.", "NASA coordonne des missions scientifiques depuis Cape Canaveral.", "Christopher Wray dirige la politique publique à Washington."},
	"tr":    {"Mike Tyson Brooklyn'de disiplinli hareketler çalışır.", "NASA Cape Canaveral'dan bilim görevlerini koordine eder.", "Christopher Wray Washington'da kamu politikasını yönetir."},
	"id":    {"Mike Tyson melatih gerakan disiplin di Brooklyn.", "NASA mengoordinasikan misi ilmiah dari Cape Canaveral.", "Christopher Wray mengarahkan kebijakan publik di Washington."},
}

func TestImportantPhrasesPropertiesAcrossLanguagesAndUnseenTexts(t *testing.T) {
	for language, texts := range propertyTexts {
		t.Run(language, func(t *testing.T) {
			profile := propertyProfile(language)
			for index, text := range texts {
				blocked := propertyBlockedSpans(text)
				first := ImportantPhrases(text, blocked, 5, language)
				second := ImportantPhrases(text, blocked, 5, language)
				if len(first) == 0 {
					t.Fatalf("text %d produced no phrases", index)
				}
				if got, want := phraseSetHash(first), phraseSetHash(second); got != want {
					t.Fatalf("text %d is not deterministic: %s != %s", index, got, want)
				}
				for _, phrase := range first {
					assertPhraseProperties(t, text, phrase, blocked, profile)
				}
			}
		})
	}
}

func propertyProfile(language string) *linguistics.LexiconProfile {
	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return nil
	}
	if registry.HasProfile(language) {
		return registry.Resolve(language)
	}
	return registry.Resolve("fallback")
}

func propertyBlockedSpans(text string) [][2]int {
	var spans [][2]int
	for _, surface := range []string{"Mike Tyson", "Brooklyn", "NASA", "Cape Canaveral", "Christopher Wray", "Washington"} {
		start := strings.Index(text, surface)
		if start < 0 {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(text[:start]), utf8.RuneCountInString(text[:start+len(surface)])})
	}
	return spans
}

func assertPhraseProperties(t *testing.T, text, phrase string, blocked [][2]int, profile *linguistics.LexiconProfile) {
	t.Helper()
	if !strings.Contains(text, phrase) {
		t.Fatalf("phrase %q is not grounded in %q", phrase, text)
	}
	words := strings.Fields(phrase)
	if len(words) < 2 || len(words) > 4 {
		t.Fatalf("phrase %q has %d words, want 2..4", phrase, len(words))
	}
	if profile != nil {
		first := strings.ToLower(words[0])
		last := strings.ToLower(strings.Trim(words[len(words)-1], ".,!?;:"))
		if _, ok := profile.StopWords[first]; ok {
			t.Fatalf("phrase %q starts with stop word %q", phrase, first)
		}
		if _, ok := profile.FunctionWords[first]; ok {
			t.Fatalf("phrase %q starts with function word %q", phrase, first)
		}
		if _, ok := profile.StopWords[last]; ok {
			t.Fatalf("phrase %q ends with stop word %q", phrase, last)
		}
		if _, ok := profile.FunctionWords[last]; ok {
			t.Fatalf("phrase %q ends with function word %q", phrase, last)
		}
	}
	start := utf8.RuneCountInString(text[:strings.Index(text, phrase)])
	end := start + utf8.RuneCountInString(phrase)
	for _, span := range blocked {
		if start < span[1] && span[0] < end {
			t.Fatalf("phrase %q overlaps blocked entity span [%d,%d)", phrase, span[0], span[1])
		}
	}
}

func phraseSetHash(phrases []string) string {
	hash := sha256.Sum256([]byte(strings.Join(phrases, "\x00")))
	return hex.EncodeToString(hash[:])
}

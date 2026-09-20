// Package translation — quality_test.go: contract tests for the translation
// quality gate.
package translation

import (
	"strings"
	"testing"
)

const (
	// englishSource is clause-for-clause the phrase used by the multilingual
	// rehearsal (and by the Dolly Parton certification lane).
	englishSource = "Consistency turns an idea into results. Progress rarely arrives in one dramatic moment; instead, it emerges from repeating a clear process, learning profoundly from each attempt, and systematically improving the next one. A strong creative workflow is what transforms a rough concept into a finished video by meticulously combining editorial judgment, precise timing, readable typography, and reliable rendering. The ultimate goal transcends simply making one successful clip; rather, it demands building an entire system capable of producing that same high quality again and again."

	italianTranslation = "La coerenza trasforma un'idea in risultati. Il progresso raramente arriva in un momento drammatico; invece, emerge dalla ripetizione di un processo chiaro, imparando profondamente da ogni tentativo, e migliorando sistematicamente il prossimo. Un forte flusso di lavoro creativo è quello che trasforma un concetto grezzo in un video finito combinando meticolosamente giudizio editoriale, tempi precisi, tipografia leggibile e rendering affidabile. L'obiettivo finale trascende semplicemente facendo una clip di successo; piuttosto, richiede la costruzione di un intero sistema in grado di produrre la stessa alta qualità ancora e ancora."

	englishSentence = "The quick brown fox jumps over the lazy dog and runs away"
)

func TestAssessTranslation_AcceptsPlausibleTranslation(t *testing.T) {
	got := AssessTranslation(englishSource, italianTranslation, "en", "it")
	if !got.Acceptable() {
		t.Fatalf("plausible translation rejected: %v (%s)", got.Issues, got.Reason)
	}
	if got.LengthRatio < 0.8 || got.LengthRatio > 1.3 {
		t.Fatalf("length ratio = %.2f, want a realistic en→it ratio", got.LengthRatio)
	}
}

func TestAssessTranslation_FlagsEachDegeneracy(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		translated string
		sourceLang string
		targetLang string
		want       QualityIssue
	}{
		{
			name:       "empty answer",
			source:     "hello world",
			translated: "   ",
			sourceLang: "en",
			targetLang: "it",
			want:       IssueEmpty,
		},
		{
			name:       "source leaked verbatim",
			source:     englishSource,
			translated: englishSource,
			sourceLang: "en",
			targetLang: "it",
			want:       IssueSourceLeak,
		},
		{
			name:       "truncated answer",
			source:     englishSource,
			translated: "Ciao.",
			sourceLang: "en",
			targetLang: "it",
			want:       IssueTruncated,
		},
		{
			name:       "runaway answer",
			source:     englishSource,
			translated: strings.Repeat("ciao mondo come stai ", 100),
			sourceLang: "en",
			targetLang: "it",
			want:       IssueRunaway,
		},
		{
			name:       "repetition loop",
			source:     "A long enough source sentence for the ratio guard to stay quiet here.",
			translated: strings.Repeat("this is a loop ", 10),
			sourceLang: "en",
			targetLang: "it",
			want:       IssueRepetition,
		},
		{
			name:       "wrong language",
			source:     "Ciao mondo",
			translated: englishSentence,
			sourceLang: "it",
			targetLang: "id",
			want:       IssueWrongLanguage,
		},
		{
			name:       "wrong script",
			source:     "Ciao mondo",
			translated: englishSentence,
			sourceLang: "it",
			targetLang: "ru",
			want:       IssueWrongLanguage,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessTranslation(tc.source, tc.translated, tc.sourceLang, tc.targetLang)
			if got.Acceptable() {
				t.Fatalf("expected issue %s, got an accepted answer", tc.want)
			}
			if !hasIssue(got, tc.want) {
				t.Fatalf("issues = %v, want %s (%s)", got.Issues, tc.want, got.Reason)
			}
		})
	}
}

func TestAssessTranslation_SameLanguageIsNotALeak(t *testing.T) {
	got := AssessTranslation("hello world", "hello world", "en", "en")
	if !got.Acceptable() {
		t.Fatalf("en→en no-op must not be flagged as a leak: %v (%s)", got.Issues, got.Reason)
	}
}

func TestAssessTranslation_ShortCueSkipsLengthGuard(t *testing.T) {
	got := AssessTranslation("hello", "ciao", "en", "it")
	if !got.Acceptable() {
		t.Fatalf("short cue wrongly judged: %v (%s)", got.Issues, got.Reason)
	}
}

func hasIssue(a Assessment, want QualityIssue) bool {
	for _, issue := range a.Issues {
		if issue == want {
			return true
		}
	}
	return false
}

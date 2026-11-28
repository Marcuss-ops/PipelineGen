package verbatimcheck

import (
	"strings"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// augiasVerbatimSource reproduces the AUGRENDER-04 condition: a 600+ word
// Augias scene authored in `source_text_verbatim` mode, carrying Markdown
// emphasis. The sanitizer must strip the formatting, and the produced audio
// must read the sanitized words with aligned boundaries.
func augiasVerbatimSource() string {
	sentences := []string{
		"**Corrado Augias** ricorda la Roma degli anni cinquanta con affetto e precisione.",
		"Il giornalista *racconta* la sua formazione tra i libri della biblioteca familiare.",
		"La `televisione` diventa per lui uno strumento di divulgazione civile.",
		"Ogni passaggio del suo **racconto** insiste sulla responsabilita della memoria.",
	}
	var builder strings.Builder
	for len(strings.Fields(builder.String())) < 700 {
		for _, sentence := range sentences {
			builder.WriteString(sentence)
			builder.WriteByte(' ')
		}
	}
	return strings.TrimSpace(builder.String())
}

func cuesFor(t *testing.T, text string, total time.Duration, wordsPerCue int) []Cue {
	t.Helper()
	words := strings.Fields(scriptpkg.SanitizeForSpeech(text))
	if len(words) == 0 {
		t.Fatal("fixture has no words")
	}
	var cues []Cue
	cursor := time.Duration(0)
	for start := 0; start < len(words); start += wordsPerCue {
		end := start + wordsPerCue
		if end > len(words) {
			end = len(words)
		}
		span := time.Duration(int64(total) * int64(end-start) / int64(len(words)))
		if span <= 0 {
			span = time.Millisecond
		}
		cues = append(cues, Cue{
			Index: len(cues),
			Start: cursor,
			End:   cursor + span,
			Text:  strings.Join(words[start:end], " "),
		})
		cursor += span
	}
	return cues
}

// The acceptance fixture: 600+ authored words with `**`, a clean transcript
// and boundaries inside the audio must pass every check.
func TestAugiasVerbatimTakePasses(t *testing.T) {
	source := augiasVerbatimSource()
	words := len(strings.Fields(source))
	if words < 600 {
		t.Fatalf("fixture has %d words, want 600+", words)
	}
	if !strings.Contains(source, "**") {
		t.Fatal("fixture must carry Markdown emphasis")
	}
	sanitized := scriptpkg.SanitizeForSpeech(source)
	for _, marker := range []string{"*", "#", "`", "~~"} {
		if strings.Contains(sanitized, marker) {
			t.Fatalf("sanitized source still contains %q", marker)
		}
	}
	audioDuration := 240 * time.Second
	cues := cuesFor(t, source, audioDuration, 12)
	report := Check(source, cues, audioDuration)
	if !report.OK() {
		t.Fatalf("report = %+v, want pass", report)
	}
	if report.SpokenWords != report.ExpectedWords {
		t.Fatalf("spoken %d, expected %d", report.SpokenWords, report.ExpectedWords)
	}
	if report.WordCoveragePercent != 100 {
		t.Fatalf("coverage = %v, want 100", report.WordCoveragePercent)
	}
	if report.Cues != len(cues) {
		t.Fatalf("cues = %d, want %d", report.Cues, len(cues))
	}
}

// A provider that pronounces the formatting token is a failure, not a warning.
func TestCheckRejectsMarkdownMarkersInSpeech(t *testing.T) {
	report := Check("**Corrado Augias** racconta.", []Cue{
		{Index: 0, Start: 0, End: time.Second, Text: "**Corrado** Augias racconta"},
	}, 2*time.Second)
	if report.OK() {
		t.Fatalf("report = %+v, want a markdown issue", report)
	}
	if report.Issues[0].Code != CodeMarkdownMarker {
		t.Fatalf("code = %q, want %q", report.Issues[0].Code, CodeMarkdownMarker)
	}
}

func TestCheckRejectsPronouncedEditorialDirective(t *testing.T) {
	source := "Ricostruisci Francesco Totti a partire dalle clip. La clip mostra la maglia."
	report := Check(source, []Cue{
		{Index: 0, Start: 0, End: time.Second, Text: "Ricostruisci Francesco Totti a partire dalle clip"},
		{Index: 1, Start: time.Second, End: 2 * time.Second, Text: "La clip mostra la maglia"},
	}, 3*time.Second)
	if report.OK() {
		t.Fatalf("report = %+v, want a directive issue", report)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == CodeEditorialDirective {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %+v, want %q", report.Issues, CodeEditorialDirective)
	}
}

func TestCheckRejectsBoundaryOutsideAudio(t *testing.T) {
	report := Check("Corrado Augias racconta la sua Roma.", []Cue{
		{Index: 0, Start: 0, End: 30 * time.Second, Text: "Corrado Augias racconta la sua Roma"},
	}, 5*time.Second)
	if report.OK() {
		t.Fatalf("report = %+v, want an out-of-audio issue", report)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == CodeBoundaryOutsideAudio {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %+v, want %q", report.Issues, CodeBoundaryOutsideAudio)
	}
}

func TestCheckRejectsNonMonotonicBoundaries(t *testing.T) {
	report := Check("Corrado Augias racconta la sua Roma con precisione.", []Cue{
		{Index: 0, Start: 2 * time.Second, End: 4 * time.Second, Text: "Corrado Augias"},
		{Index: 1, Start: time.Second, End: 3 * time.Second, Text: "racconta la sua Roma con precisione"},
	}, 10*time.Second)
	if report.OK() {
		t.Fatalf("report = %+v, want a boundary-order issue", report)
	}
	if report.Issues[0].Code != CodeBoundaryOrder {
		t.Fatalf("code = %q, want %q", report.Issues[0].Code, CodeBoundaryOrder)
	}
}

func TestCheckRejectsWordLoss(t *testing.T) {
	source := strings.Repeat("Corrado Augias racconta la sua Roma con precisione. ", 20)
	report := Check(source, []Cue{
		{Index: 0, Start: 0, End: time.Second, Text: "Corrado Augias"},
	}, 200*time.Second)
	if report.OK() {
		t.Fatalf("report = %+v, want a word-coverage issue", report)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == CodeWordCoverage {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %+v, want %q", report.Issues, CodeWordCoverage)
	}
}

func TestParseVTTReadsTimedCues(t *testing.T) {
	document := `WEBVTT

NOTE this line is ignored

intro
00:00:00.000 --> 00:00:02.500 align:start position:10%
<v Speaker>Corrado Augias racconta la sua Roma.

00:00:02.500 --> 00:00:05.000
La memoria diventa responsabilita civile.
`
	cues, err := ParseVTT(strings.NewReader(document))
	if err != nil {
		t.Fatalf("ParseVTT: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2", len(cues))
	}
	if cues[0].Start != 0 || cues[0].End != 2500*time.Millisecond {
		t.Fatalf("cue[0] window = %s–%s", cues[0].Start, cues[0].End)
	}
	if strings.Contains(cues[0].Text, "<v") {
		t.Fatalf("cue text kept the voice tag: %q", cues[0].Text)
	}
	if !strings.HasPrefix(cues[0].Text, "Corrado Augias") {
		t.Fatalf("cue text = %q", cues[0].Text)
	}
}

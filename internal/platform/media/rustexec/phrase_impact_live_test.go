package rustexec

// Live runtime certification for editorial.v1 (R01/R03/R04/R06/R07/R09/R10/R12).
//
// Unlike the fake-runner unit tests, this harness drives the REAL Rust
// binary in lexical mode (no embedder, no LLM, no server) over materialized
// scenes. It is deterministic by construction: same input bytes plus the
// same worker binary must yield byte-identical highlights.
// Skipped when the binary is absent; it never fails a hermetic run.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func liveBinaryPath(t *testing.T) string {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv("VELOX_RUST_PHRASE_IMPACT_PATH")); p != "" {
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return p
		}
		t.Skipf("VELOX_RUST_PHRASE_IMPACT_PATH=%q not found", p)
	}
	_, file, _, _ := runtime.Caller(0)
	candidate := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "bin", "phrase_impact")
	if info, err := os.Stat(candidate); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Skipf("BrainRust phrase_impact release worker not installed at %s; run make rust-fetch", candidate)
	}
	return candidate
}

// fiveSceneTranscript returns a deterministic 5-scene EN transcript about
// German industry plus per-scene inputs with verified byte offsets.
// Scene 4 repeats scene 2 verbatim (R04) and is therefore local-only.
func fiveSceneTranscript() (string, []SceneInput) {
	scenes := []struct {
		id    string
		topic string
		text  string
	}{
		{"scene-01", "German industrial employment", "Germany lost one hundred thousand industrial jobs in a single year, and factory orders declined sharply across every major region. Economists warn that the downturn may persist through next winter unless exports recover soon."},
		{"scene-02", "Rhine manufacturing output", "Steel plants along the Rhine cut shifts as energy prices stayed high through autumn. Managers describe thin order books and delayed investment decisions across the supply chain."},
		{"scene-03", "Formula 1 engineering", "Formula 1 engineers refined the hybrid power unit over thousands of test bench hours. Wind tunnel sessions shaped the new aerodynamic package for the coming season."},
		{"scene-04", "Rhine manufacturing output", "Steel plants along the Rhine cut shifts as energy prices stayed high through autumn. Managers describe thin order books and delayed investment decisions across the supply chain."},
		{"scene-05", "Berlin technology startups", "Berlin technology startups hired steadily while industrial towns struggled with layoffs. Cafés near the Spree filled each morning with founders chasing fresh funding rounds."},
	}
	var transcript strings.Builder
	inputs := make([]SceneInput, 0, len(scenes))
	seenText := map[string]int{}
	for i, s := range scenes {
		if i > 0 {
			transcript.WriteString(" ")
		}
		start := transcript.Len()
		transcript.WriteString(s.text)
		end := transcript.Len()
		in := SceneInput{SceneID: s.id, Text: s.text}
		topic := s.topic
		in.Topic = &topic
		// Repeated text (scene-04 == scene-02) stays local-only: no
		// fabricated choice of which occurrence is "the" one.
		if seenText[s.text] == 0 {
			st, en := start, end
			in.StartByte, in.EndByte = &st, &en
		}
		seenText[s.text]++
		inputs = append(inputs, in)
	}
	return transcript.String(), inputs
}

// validateLiveHighlights mirrors the certification validator: schema,
// unique ids, byte-exact span reconstruction (against the transcript when
// globally indexed, against the scene text otherwise), canonical bullet
// ranges.
func validateLiveHighlights(t *testing.T, transcript string, inputs []SceneInput, highlights []scriptpkg.SceneHighlight) {
	t.Helper()
	byID := map[string]string{}
	for _, in := range inputs {
		byID[in.SceneID] = in.Text
	}
	seen := map[string]struct{}{}
	for _, h := range highlights {
		_, dup := seen[h.SceneID]
		require.False(t, dup, "duplicate scene_id %q", h.SceneID)
		seen[h.SceneID] = struct{}{}
		sceneText, ok := byID[h.SceneID]
		require.True(t, ok, "unknown scene_id %q", h.SceneID)
		require.Contains(t, []string{"resolved", "unavailable"}, h.TitleStatus)
		if h.TitleStatus == "unavailable" {
			require.Nil(t, h.Title, "unavailable title must carry no text")
		}
		base := transcript
		if !h.GloballyIndexed {
			base = sceneText
		}
		raw := []byte(base)
		for _, sp := range h.Highlights {
			require.True(t, 0 <= sp.StartByte && sp.StartByte < sp.EndByte && sp.EndByte <= len(raw),
				"span bounds %d..%d in %d bytes", sp.StartByte, sp.EndByte, len(raw))
			require.Equal(t, sp.Text, string(raw[sp.StartByte:sp.EndByte]),
				"span bytes must reconstruct text exactly")
		}
		for _, b := range h.Bullets {
			require.True(t, b.SentenceStart < b.SentenceEnd, "bullet range must be non-empty")
			require.Contains(t, sceneText, b.Text, "bullet must be verbatim scene text")
		}
	}
}

func TestLiveFiveScenesProduceCertifiedHighlights(t *testing.T) {
	bin := liveBinaryPath(t)
	analyzer := NewPhraseImpactAnalyzer(bin, nil, nil)
	transcript, inputs := fiveSceneTranscript()
	ctx := context.Background()

	res, err := analyzer.AnalyzeScenes(ctx, transcript, "en", inputs)
	require.NoError(t, err)
	require.True(t, res.SceneHighlightsCertified, "coherent identity must certify")
	require.Len(t, res.SceneHighlights, 5, "R01/R03: one highlight per scene")

	// R03: per-scene bullets only.
	for _, h := range res.SceneHighlights {
		require.NotEmpty(t, h.Bullets, "scene %s should have bullets", h.SceneID)
	}
	// R04: repeated scene is local-only, first occurrence is global.
	byID := map[string]scriptpkg.SceneHighlight{}
	for _, h := range res.SceneHighlights {
		byID[h.SceneID] = h
	}
	require.True(t, byID["scene-02"].GloballyIndexed)
	require.False(t, byID["scene-04"].GloballyIndexed, "repeated text must not claim global offsets")

	// R09/R10 validator over the real response.
	validateLiveHighlights(t, transcript, inputs, res.SceneHighlights)

	// R12: determinism — identical input yields identical highlights.
	again, err := analyzer.AnalyzeScenes(ctx, transcript, "en", inputs)
	require.NoError(t, err)
	require.Equal(t, res.SceneHighlights, again.SceneHighlights, "retry must be byte-identical")
}

func TestLiveSingleSceneManifest(t *testing.T) {
	bin := liveBinaryPath(t)
	analyzer := NewPhraseImpactAnalyzer(bin, nil, nil)
	text := "Industria 4.0 reshaped Bavarian factories with sensors on every line. Production data now guides nightly maintenance decisions across three plants."
	st, en := 0, len(text)
	inputs := []SceneInput{{SceneID: "only", Text: text, StartByte: &st, EndByte: &en}}
	res, err := analyzer.AnalyzeScenes(context.Background(), text, "en", inputs)
	require.NoError(t, err)
	require.True(t, res.SceneHighlightsCertified)
	require.Len(t, res.SceneHighlights, 1, "R06: single scene yields a valid manifest")
	validateLiveHighlights(t, text, inputs, res.SceneHighlights)
}

func TestLiveUTF8EmojiOffsets(t *testing.T) {
	bin := liveBinaryPath(t)
	analyzer := NewPhraseImpactAnalyzer(bin, nil, nil)
	// R07: accents, symbols, emoji — every span must sit on char boundaries.
	text := "La città è bellissima e l’industria cresce piano. 🏭 Robots weld day and night, −6% fewer shifts."
	st, en := 0, len(text)
	inputs := []SceneInput{{SceneID: "s1", Text: text, StartByte: &st, EndByte: &en}}
	res, err := analyzer.AnalyzeScenes(context.Background(), text, "it", inputs)
	require.NoError(t, err)
	require.Len(t, res.SceneHighlights, 1)
	for _, sp := range res.SceneHighlights[0].Highlights {
		require.True(t, isCharBoundary(text, sp.StartByte) && isCharBoundary(text, sp.EndByte),
			"span %d..%d must respect UTF-8 boundaries", sp.StartByte, sp.EndByte)
	}
	validateLiveHighlights(t, text, inputs, res.SceneHighlights)
}

// liveBulletFixture returns an Italian transcript that exercises every branch
// of the document-wide bullet contract: a sentence longer than
// MAX_SEGMENT_WORDS (which the worker must chunk internally for analysis), a
// two-word fragment, and enough ordinary sentences for the chapter segmenter to
// produce chapters with bullets.
func liveBulletFixture() string {
	words := make([]string, 0, 45)
	for i := 0; i < 45; i++ {
		words = append(words, fmt.Sprintf("elemento%d", i))
	}
	var b strings.Builder
	b.WriteString("È imprescindibile comprendere che ")
	b.WriteString(strings.Join(words, " "))
	b.WriteString(" e che il quadro processuale resti comunque completo.")
	b.WriteString(" impronta 33.")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, " Il capitolo numero %d raccoglie i fatti principali del procedimento e li ordina con metodo rigoroso davanti al giudice.", i)
	}
	return b.String()
}

// liveWorkerSentences asks the real worker for the sentence (and chunk)
// sequence, so the assertions can be made against exactly what the analyzer
// sees rather than against a reimplementation of the splitter.
func liveWorkerSentences(t *testing.T, bin, transcript, language string) []string {
	t.Helper()
	runner := newPersistentRustProcessRunner()
	payload, err := json.Marshal(map[string]any{
		"operation": "split_sentences", "transcript": transcript, "language": language,
	})
	require.NoError(t, err)
	stdout, stderr, err := runner.Run(context.Background(), bin, append(payload, '\n'), phraseImpactOutputLimit)
	require.NoError(t, err, "split_sentences: %s", strings.TrimSpace(string(stderr)))
	var response phraseImpactResponse
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout), &response))
	require.True(t, response.OK, response.Error)
	require.NotEmpty(t, response.Sentences)
	texts := make([]string, len(response.Sentences))
	for i, sentence := range response.Sentences {
		texts[i] = sentence.Text
	}
	return texts
}

// liveSentenceCloses mirrors the worker's terminal-punctuation rule: a unit
// closes a sentence only when it ends on terminal punctuation, optionally
// followed by a closing quote or bracket.
func liveSentenceCloses(text string) bool {
	trimmed := strings.TrimRight(text, " \t\n\r")
	runes := []rune(trimmed)
	if len(runes) == 0 {
		return false
	}
	terminal := func(r rune) bool {
		switch r {
		case '.', '!', '?', '…', '。', '！', '？':
			return true
		}
		return false
	}
	if terminal(runes[len(runes)-1]) {
		return true
	}
	switch runes[len(runes)-1] {
	case '"', '\'', '’', '”', ')', ']', '}', '»':
		return len(runes) >= 2 && terminal(runes[len(runes)-2])
	}
	return false
}

// TestLiveSemanticBulletsConstrainGlobalAndSceneRanking exercises the unified
// semantic-candidate contract against the actual Rust worker, including the
// NDJSON decoder and both Phrase Impact bullet projections.
func TestLiveSemanticBulletsConstrainGlobalAndSceneRanking(t *testing.T) {
	if strings.TrimSpace(os.Getenv("VELOX_RUST_PHRASE_IMPACT_PATH")) == "" {
		t.Skip("install the BrainRust phrase_impact release with make rust-fetch or set VELOX_RUST_PHRASE_IMPACT_PATH")
	}
	bin := liveBinaryPath(t)
	first := "Germany lost one hundred thousand industrial jobs in a single year."
	second := "Factory orders declined for the sixth consecutive quarter."
	transcript := first + " " + second
	start, end := len(first)+1, len(transcript)
	sceneStart, sceneEnd := 0, len(transcript)
	analyzer := NewPhraseImpactAnalyzer(bin, nil, nil)
	result, err := analyzer.AnalyzeScenesWithSemanticBullets(context.Background(), transcript, "en", []SceneInput{{
		SceneID: "s1", Text: transcript, StartByte: &sceneStart, EndByte: &sceneEnd,
	}}, []scriptpkg.SemanticBulletSpan{{StartByte: start, EndByte: end, Text: second}})
	require.NoError(t, err)
	require.Equal(t, []string{second}, result.BulletPoints,
		"the semantic candidate pool must constrain global Phrase Impact ranking")
	require.Len(t, result.SceneHighlights, 1)
	require.Len(t, result.SceneHighlights[0].Bullets, 1)
	require.Equal(t, second, result.SceneHighlights[0].Bullets[0].Text,
		"the same candidate pool must constrain per-scene ranking")
}

// liveBulletHasSubstance mirrors the worker's MIN_BULLET_WORDS/MIN_BULLET_CHARS
// floor: a two-word tail is a fragment, not a bullet.
func liveBulletHasSubstance(text string) bool {
	if len(strings.Fields(text)) >= 4 {
		return true
	}
	alnum := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			alnum++
		}
	}
	return alnum >= 16
}

// liveLocateSpan finds the contiguous chunk span whose join reconstructs text
// exactly, which is how the worker itself composes a bullet.
func liveLocateSpan(texts []string, text string) (int, int, bool) {
	for start := range texts {
		joined := ""
		for end := start + 1; end <= len(texts); end++ {
			if end == start+1 {
				joined = texts[start]
			} else {
				joined += " " + texts[end-1]
			}
			if joined == text {
				return start, end, true
			}
			if len(joined) > len(text) {
				break
			}
		}
	}
	return 0, 0, false
}

// TestLiveGlobalAndChapterBulletsAreWholeSubstantiveSentences drives the REAL
// worker through the Go adapter and pins the document-wide bullet contract: the
// global and chapter surfaces obey the same anti-truncation and anti-fragment
// rule as the per-scene highlights. A bullet is a whole, substantive source
// sentence — never the head chunk of a sentence longer than MAX_SEGMENT_WORDS
// and never a two-word fragment.
func TestLiveGlobalAndChapterBulletsAreWholeSubstantiveSentences(t *testing.T) {
	bin := liveBinaryPath(t)
	analyzer := NewPhraseImpactAnalyzer(bin, nil, nil)
	transcript := liveBulletFixture()
	texts := liveWorkerSentences(t, bin, transcript, "it")

	// Fixture sanity: the worker must really have chunked the long sentence,
	// otherwise the regression this test guards would go unexercised.
	chunked := false
	for i := 1; i < len(texts); i++ {
		if !liveSentenceCloses(texts[i-1]) {
			chunked = true
			break
		}
	}
	require.True(t, chunked, "fixture must contain a sentence the worker chunks")

	st, en := 0, len(transcript)
	inputs := []SceneInput{{SceneID: "s1", Text: transcript, StartByte: &st, EndByte: &en}}
	res, err := analyzer.AnalyzeScenes(context.Background(), transcript, "it", inputs)
	require.NoError(t, err)

	assertBullet := func(surface, bullet string) {
		t.Helper()
		start, end, ok := liveLocateSpan(texts, bullet)
		require.True(t, ok, "%s bullet must be an exact contiguous source span: %q", surface, bullet)
		require.True(t, start == 0 || liveSentenceCloses(texts[start-1]),
			"%s bullet starts mid-sentence: %q", surface, bullet)
		require.True(t, end == len(texts) || liveSentenceCloses(texts[end-1]),
			"%s bullet ends mid-sentence: %q", surface, bullet)
		require.True(t, liveBulletHasSubstance(bullet), "%s bullet is a fragment: %q", surface, bullet)
		require.NotEqual(t, "impronta 33.", strings.TrimSpace(bullet),
			"%s bullet must not publish the two-word tail", surface)
	}

	require.NotEmpty(t, res.BulletPoints, "the fixture must produce global bullets")
	for _, bullet := range res.BulletPoints {
		assertBullet("global", bullet)
	}

	require.NotEmpty(t, res.ChapterManifest.Chapters, "the fixture must produce chapters")
	chapterBullets := 0
	for _, chapter := range res.ChapterManifest.Chapters {
		require.True(t, chapter.StartSentence < chapter.EndSentence)
		require.LessOrEqual(t, chapter.EndSentence, len(texts))
		for _, bullet := range chapter.Bullets {
			chapterBullets++
			require.True(t, 0 <= bullet.StartSentence && bullet.StartSentence < bullet.EndSentence,
				"chapter bullet range must be non-empty: %q", bullet.Text)
			require.LessOrEqual(t, bullet.EndSentence, len(texts))
			require.Equal(t, strings.Join(texts[bullet.StartSentence:bullet.EndSentence], " "), bullet.Text,
				"chapter bullet must be the exact concatenation of its source sentences")
			assertBullet("chapter", bullet.Text)
		}
	}
	require.Greater(t, chapterBullets, 0, "the fixture must produce chapter bullets")

	// The head chunk of the long sentence is the exact defect this contract
	// exists for: it must never surface as a bullet anywhere.
	head := ""
	for _, text := range texts {
		if strings.HasPrefix(text, "È imprescindibile comprendere che") {
			head = text
			break
		}
	}
	require.NotEmpty(t, head)
	require.False(t, liveSentenceCloses(texts[0]),
		"the long sentence's head chunk must not close a sentence, so it can never be published")
	require.NotContains(t, res.BulletPoints, head,
		"the chunked head of the long sentence must never be a global bullet")
	for _, chapter := range res.ChapterManifest.Chapters {
		for _, bullet := range chapter.Bullets {
			require.NotEqual(t, head, strings.TrimSpace(bullet.Text),
				"the chunked head of the long sentence must never be a chapter bullet")
		}
	}
}

func isCharBoundary(s string, i int) bool {
	if i < 0 || i > len(s) {
		return false
	}
	if i == len(s) {
		return true
	}
	c := s[i]
	return c < 0x80 || c >= 0xC0
}

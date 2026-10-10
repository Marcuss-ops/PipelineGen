package rustexec

// Live runtime certification for editorial.v1 (R01/R03/R04/R06/R07/R09/R10/R12).
//
// Unlike the fake-runner unit tests, this harness drives the REAL Rust
// binary in lexical mode (no embedder, no LLM, no server) over materialized
// scenes. It is deterministic by construction: same input bytes plus the
// same worker binary must yield byte-identical highlights.
// Skipped when the binary is absent; it never fails a hermetic run.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func liveBinaryPath(t *testing.T) string {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv("VELOX_RUST_PHRASE_IMPACT_PATH")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Skipf("VELOX_RUST_PHRASE_IMPACT_PATH=%q not found", p)
	}
	_, file, _, _ := runtime.Caller(0)
	candidate := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "bin", "phrase_impact")
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("live Rust worker not found at %s", candidate)
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

// phrase_impact_titles_bench_test.go measures the PRODUCTION Rust
// extractive-summary worker (bin/phrase_impact) over a fixed corpus of titled
// narration samples.
//
// Unlike the offline Python benchmark (scripts/bench/phrase_impact.py), which
// scores precomputed embeddings to compare extractors, this benchmark drives
// the real NDJSON worker the script runner wires in production
// (internal/platform/media/rustexec.PhraseImpactAnalyzer →
// wirePhraseImpactAnalyzer) in its deterministic lexical mode: no model, no
// network, no GPU. One title is one sub-benchmark, so "how long does each
// title take" is answered per title rather than as one blended average.
//
// A single long-lived worker is used for every sample, mirroring the
// persistent RustProcessRunner the adapter uses, so the reported latency is
// the per-analysis cost (one NDJSON request + one response) and not process
// startup. Stage timings reported by the worker itself (split / similarity /
// ranking / summary / bullet) are surfaced as custom metrics.
//
// Run it with:
//
//	go test ./scripts/bench -run '^$' -bench BenchmarkPhraseImpactTitles -benchmem
//
// Set PHRASE_IMPACT_BIN to point at another build; without a binary the
// benchmark skips so the suite stays hermetic.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// phraseImpactTitleSample is one titled narration the benchmark measures.
type phraseImpactTitleSample struct {
	Title     string
	Language  string
	Narration string
}

// phraseImpactTitleCorpus is deliberately fixed: the same titles and the same
// text on every invocation make the numbers comparable across commits. It
// mixes languages and lengths so a regression that only shows up on long or
// non-English narration is visible.
func phraseImpactTitleCorpus() []phraseImpactTitleSample {
	return []phraseImpactTitleSample{
		{
			Title:    "Mike Tyson — potenza e disciplina",
			Language: "it",
			Narration: "Mike Tyson trasforma pochi secondi sul ring in una dichiarazione di potenza. " +
				"La sua presenza combina velocità fulminea e una pressione costante. " +
				"La disciplina trasforma la potenza in controllo. " +
				"Ogni movimento nasce da anni di lavoro quotidiano e da una preparazione rigorosa. " +
				"Il pubblico ricorda i colpi, ma il vero vantaggio nasce dalla calma prima dell'azione.",
		},
		{
			Title:    "Caso Isabelle Caracristi — la dinamica e le domande aperte",
			Language: "pt",
			Narration: "Isabelle Caracristi, estudante de Direito de 22 anos, morreu em 13 de setembro de 2026 após cair de um condomínio na Aldeota, em Fortaleza. " +
				"Em 23 de setembro, a Pefoce informou que os laudos apontavam precipitação voluntária, com trajetória vertical. " +
				"As autoridades disseram que essa conclusão descreve a dinâmica da queda e não encerra a investigação sobre o contexto. " +
				"Cerca de 700 vídeos do condomínio foram analisados, mas o rooftop não tinha câmera. " +
				"A investigação segue aberta. Hipótese investigativa não é fato provado.",
		},
		{
			Title:    "Milton Leite — operação nas concessões de ônibus",
			Language: "pt",
			Narration: "A operação cumpriu 16 mandados de prisão e 18 buscas em endereços ligados ao transporte urbano. " +
				"Os investigadores apontam que, de 22 consórcios de ônibus, 12 seriam controlados por uma facção criminosa. " +
				"O grupo teria usado empresas de ônibus para lavar dinheiro ao longo de duas décadas. " +
				"Uma empresa de fachada teria sido aberta em nome de terceiros para esconder a propriedade real. " +
				"Os laudos descrevem a cadeia de contratos, mas o processo ainda depende de perícia contábil e de novas oitivas.",
		},
		{
			Title:    "Elon Musk — Marte e il profitto",
			Language: "it",
			Narration: "Elon Musk sostiene che una civiltà interplanetaria sia la migliore assicurazione contro l'estinzione. " +
				"Le sue aziende costruiscono razzi riutilizzabili, satelliti e auto elettriche. " +
				"I critici osservano che gli obiettivi pubblici e i ricavi privati procedono insieme, e che i tempi annunciati slittano spesso. " +
				"Gli ingegneri descrivono un metodo di iterazione rapida: prototipi economici, test frequenti, correzioni immediate. " +
				"Il dibattito riguarda se un'ambizione così grande possa restare trasparente e sicura.",
		},
		{
			Title:    "The Fall of the Roman Empire",
			Language: "en",
			Narration: "The Western Roman Empire did not fall in a single afternoon. " +
				"Fiscal strain, contested successions, and repeated frontier pressures eroded its capacity over centuries. " +
				"Provincial elites increasingly negotiated with local warlords rather than distant officials. " +
				"Trade routes shifted, tax revenues shrank, and the army grew dependent on federated allies. " +
				"Historians still argue about which cause mattered most, but few now accept a simple story of one invasion.",
		},
		{
			Title:    "AI Safety in 2026",
			Language: "en",
			Narration: "Machine learning systems now draft code, summarize evidence, and answer questions at scale. " +
				"That usefulness is exactly why evaluation matters: a model that is fluent can still be wrong with confidence. " +
				"Researchers propose layered safeguards, from red-teaming to runtime monitoring. " +
				"Regulators want auditable documentation of training data and known failure modes. " +
				"The open question is not whether the technology works, but whether institutions can keep pace with it.",
		},
	}
}

type phraseImpactWorkerRequest struct {
	Transcript  string         `json:"transcript"`
	Language    string         `json:"language"`
	Embeddings  [][]float32    `json:"embeddings"`
	LexicalOnly bool           `json:"lexical_only"`
	Options     map[string]any `json:"options"`
}

type phraseImpactWorkerStageTimings struct {
	SplitMS      float64 `json:"split_ms"`
	EmbeddingMS  float64 `json:"embedding_ms"`
	SimilarityMS float64 `json:"similarity_ms"`
	RankingMS    float64 `json:"ranking_ms"`
	SummaryMS    float64 `json:"summary_ms"`
	BulletMS     float64 `json:"bullet_ms"`
	TotalMS      float64 `json:"total_ms"`
}

type phraseImpactWorkerResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Result struct {
		Summary      string `json:"summary"`
		BulletPoints []struct {
			Text string `json:"text"`
		} `json:"bullet_points"`
		HeavySentences []struct {
			Text       string  `json:"text"`
			Importance float64 `json:"importance"`
		} `json:"heavy_sentences"`
		Timings phraseImpactWorkerStageTimings `json:"timings"`
	} `json:"result"`
}

// phraseImpactTitleCorpusIsUsable guards the benchmark inputs in the ordinary
// (non-benchmark) test run: a sample without a title, a language, or enough
// sentences would silently produce meaningless numbers.
func TestPhraseImpactTitleCorpusIsUsable(t *testing.T) {
	corpus := phraseImpactTitleCorpus()
	if len(corpus) < 4 {
		t.Fatalf("title corpus has %d samples, want at least 4", len(corpus))
	}
	seen := make(map[string]bool, len(corpus))
	for _, sample := range corpus {
		if strings.TrimSpace(sample.Title) == "" {
			t.Fatal("corpus sample has an empty title")
		}
		if seen[sample.Title] {
			t.Fatalf("corpus repeats the title %q", sample.Title)
		}
		seen[sample.Title] = true
		if strings.TrimSpace(sample.Language) == "" {
			t.Fatalf("%s: corpus sample has no language", sample.Title)
		}
		if sentences := strings.Count(strings.TrimSpace(sample.Narration), ".") +
			strings.Count(sample.Narration, "!") + strings.Count(sample.Narration, "?"); sentences < 3 {
			t.Fatalf("%s: corpus sample needs at least three sentences, got %d", sample.Title, sentences)
		}
	}
}

// phraseImpactBinaryPath resolves the production worker the same way the Go
// adapter does: an explicit override first, then the installed binary.
func phraseImpactBinaryPath(tb testing.TB) string {
	tb.Helper()
	if path := strings.TrimSpace(os.Getenv("PHRASE_IMPACT_BIN")); path != "" {
		return path
	}
	// This package lives at <root>/scripts/bench.
	candidate, err := filepath.Abs(filepath.Join("..", "..", "bin", "phrase_impact"))
	if err != nil {
		tb.Fatalf("resolve bin/phrase_impact: %v", err)
	}
	if _, statErr := os.Stat(candidate); statErr == nil {
		return candidate
	}
	tb.Skipf("phrase-impact Rust binary not available at %s; set PHRASE_IMPACT_BIN to run this benchmark", candidate)
	return ""
}

// phraseImpactWorker is one long-lived NDJSON worker process, matching the
// persistent runner the production adapter uses.
type phraseImpactWorker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func startPhraseImpactWorker(tb testing.TB, binary string) *phraseImpactWorker {
	tb.Helper()
	cmd := exec.Command(binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		tb.Fatalf("worker stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		tb.Fatalf("worker stdout: %v", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		tb.Fatalf("start phrase-impact worker: %v", err)
	}
	worker := &phraseImpactWorker{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20)}
	tb.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return worker
}

// analyze sends one request and blocks for its response, returning the wall
// time and the worker's own stage timings.
func (w *phraseImpactWorker) analyze(sample phraseImpactTitleSample) (time.Duration, phraseImpactWorkerResponse, error) {
	request := phraseImpactWorkerRequest{
		Transcript:  sample.Narration,
		Language:    sample.Language,
		Embeddings:  [][]float32{},
		LexicalOnly: true,
		Options:     map[string]any{"summary_length": "medium", "bullet_count": 5, "min_heavy": 3, "max_heavy": 15},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return 0, phraseImpactWorkerResponse{}, fmt.Errorf("encode request: %w", err)
	}
	started := time.Now()
	if _, err := w.stdin.Write(append(payload, '\n')); err != nil {
		return 0, phraseImpactWorkerResponse{}, fmt.Errorf("write request: %w", err)
	}
	line, err := w.stdout.ReadBytes('\n')
	if err != nil {
		return 0, phraseImpactWorkerResponse{}, fmt.Errorf("read response: %w", err)
	}
	elapsed := time.Since(started)
	var response phraseImpactWorkerResponse
	if err := json.Unmarshal(bytes.TrimSpace(line), &response); err != nil {
		return 0, phraseImpactWorkerResponse{}, fmt.Errorf("decode response: %w", err)
	}
	if !response.OK {
		return 0, phraseImpactWorkerResponse{}, fmt.Errorf("worker error: %s", response.Error)
	}
	return elapsed, response, nil
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	position := q * float64(len(sorted)-1)
	lower := int(position)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	fraction := position - float64(lower)
	return time.Duration(float64(sorted[lower])*(1-fraction) + float64(sorted[upper])*fraction)
}

// BenchmarkPhraseImpactTitles reports, per title, the NDJSON round-trip
// latency of the production worker and the worker's own stage breakdown.
func BenchmarkPhraseImpactTitles(b *testing.B) {
	binary := phraseImpactBinaryPath(b)
	worker := startPhraseImpactWorker(b, binary)

	for _, sample := range phraseImpactTitleCorpus() {
		sample := sample
		b.Run(sample.Title, func(b *testing.B) {
			// Warm up so the measured runs exclude first-call allocation and
			// decoder growth inside the worker.
			for i := 0; i < 3; i++ {
				if _, _, err := worker.analyze(sample); err != nil {
					b.Fatalf("warmup: %v", err)
				}
			}
			latencies := make([]time.Duration, 0, b.N)
			var last phraseImpactWorkerResponse
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				elapsed, response, err := worker.analyze(sample)
				if err != nil {
					b.Fatalf("analyze: %v", err)
				}
				latencies = append(latencies, elapsed)
				last = response
			}
			b.StopTimer()

			if last.Result.Summary == "" || len(last.Result.BulletPoints) == 0 || len(last.Result.HeavySentences) == 0 {
				b.Fatalf("worker returned an incomplete result for %q: summary=%q bullets=%d heavy=%d",
					sample.Title, last.Result.Summary, len(last.Result.BulletPoints), len(last.Result.HeavySentences))
			}
			sorted := append([]time.Duration(nil), latencies...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
			b.ReportMetric(float64(percentile(sorted, 0.50).Microseconds())/1000.0, "p50_ms")
			b.ReportMetric(float64(percentile(sorted, 0.95).Microseconds())/1000.0, "p95_ms")
			b.ReportMetric(float64(percentile(sorted, 0.50).Microseconds())/1000.0-last.Result.Timings.TotalMS, "overhead_ms")
			b.ReportMetric(last.Result.Timings.TotalMS, "rust_total_ms")
			b.ReportMetric(last.Result.Timings.RankingMS, "rust_ranking_ms")
			b.ReportMetric(last.Result.Timings.SummaryMS, "rust_summary_ms")
			b.ReportMetric(float64(len(last.Result.HeavySentences)), "heavy")
			b.ReportMetric(float64(len(last.Result.BulletPoints)), "bullets")
		})
	}
}

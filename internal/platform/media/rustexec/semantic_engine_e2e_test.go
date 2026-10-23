package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func resolveSemanticEngineBinary() string {
	if fromEnv := os.Getenv("VELOX_RUST_SEMANTIC_ENGINE_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() {
			return fromEnv
		}
	}
	candidate := filepath.Join("..", "..", "..", "..", "rust", "target", "release", "semantic-engine")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
		return candidate
	}
	return ""
}

func resolvePhraseImpactParityBinary(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("PHRASE_IMPACT_BIN")); configured != "" {
		if info, err := os.Stat(configured); err == nil && info.Mode().IsRegular() {
			return configured
		}
	}
	// The workspace package intentionally exposes one binary, whose argv[0]
	// selects the dedicated compatibility worker. Give that binary the worker
	// name in a temporary directory so the E2E exercises the current release.
	packageBinary := filepath.Join("..", "..", "..", "..", "rust", "target", "release", "pipelinegen-muscles")
	if info, err := os.Stat(packageBinary); err == nil && info.Mode().IsRegular() {
		absolute, err := filepath.Abs(packageBinary)
		if err == nil {
			alias := filepath.Join(t.TempDir(), "phrase_impact")
			if err := os.Symlink(absolute, alias); err == nil {
				return alias
			}
		}
	}
	legacyBinary := filepath.Join("..", "..", "..", "..", "bin", "phrase_impact")
	if info, err := os.Stat(legacyBinary); err == nil && info.Mode().IsRegular() {
		return legacyBinary
	}
	return ""
}

// TestSemanticEngineRealBinaryAnalysesWholeScript walks the real
// `semantic-engine` binary through the Go adapter: one NDJSON request for two
// scenes returns grounded bullets, a comparable chart, the date and every
// numeric value, with every span verified against the source.
func TestSemanticEngineRealBinaryAnalysesWholeScript(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not built; run cargo build --release -p semantic-engine")
	}
	first := "Il 42% dei tecnici ha segnalato il problema, contro il 18% del gruppo di controllo."
	second := "Il 15 marzo 2008 è la data del delitto."
	contextQuery := "I tifosi festeggiano sugli spalti dopo la vittoria della squadra."
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", []scriptgen.SemanticSceneInput{
		{SceneID: "s1", Text: first},
		{SceneID: "s2", Text: second},
		{SceneID: "s3", Text: contextQuery},
	})
	if err != nil {
		t.Fatalf("real semantic-engine batch failed: %v", err)
	}
	one := bundles["s1"]
	if one == nil || len(one.Bullets) == 0 {
		t.Fatalf("scene s1 bullets missing: %+v", one)
	}
	if len(one.Charts) != 2 {
		t.Fatalf("scene s1 charts=%+v, want 25%%-style comparison of 42%% and 18%%", one.Charts)
	}
	for _, chart := range one.Charts {
		if !strings.Contains(first, chart.Text) {
			t.Fatalf("chart candidate %q is not in the source", chart.Text)
		}
	}
	if len(one.Numeric) < 2 {
		t.Fatalf("scene s1 numeric facts=%+v, want both percentages", one.Numeric)
	}
	two := bundles["s2"]
	if two == nil || len(two.Temporal) != 1 {
		t.Fatalf("scene s2 temporal facts missing: %+v", two)
	}
	if two.Temporal[0].NormalizedDate != "2008-03-15" || two.Temporal[0].Precision != "day" {
		t.Fatalf("temporal fact: %+v", two.Temporal[0])
	}
	three := bundles["s3"]
	if three == nil {
		t.Fatal("scene s3 missing from semantic result")
	}
	contextualSearch := false
	phraseSearch := false
	for _, proposal := range three.Proposals {
		if proposal.Type != "entity_search" {
			continue
		}
		for _, query := range proposal.VisualQuerySuggestions {
			if query == contextQuery {
				contextualSearch = true
			}
			if query == "I tifosi festeggiano sugli spalti dopo" {
				phraseSearch = true
			}
		}
	}
	if !contextualSearch {
		t.Fatalf("real worker omitted the grounded sentence query for image/stock search: %+v", three.Proposals)
	}
	if !phraseSearch {
		t.Fatalf("real worker omitted the grounded multiword concept query for image/stock search: %+v", three.Proposals)
	}
}

func TestSemanticEngineRealBinaryMatchesPhraseImpactProducts(t *testing.T) {
	semanticBinary := resolveSemanticEngineBinary()
	if semanticBinary == "" {
		t.Skip("semantic-engine release binary not built; run cargo build --release -p semantic-engine")
	}
	phraseBinary := resolvePhraseImpactParityBinary(t)
	if phraseBinary == "" {
		t.Skip("pipelinegen-muscles release binary not built; needed for Phrase Impact parity")
	}
	transcript := "Marco Rossi fonda la società nel 2018. La squadra inaugura il nuovo impianto dopo due anni di lavori. Nel 2020 i tecnici registrano la capienza e verificano tutte le tribune. Il progetto coinvolge i residenti e gli operatori locali. Infine il club pubblica i risultati e ringrazia ogni partecipante."
	end := len(transcript)
	scenes := []scriptpkg.SceneAnalysisInput{{SceneID: "s1", Text: transcript, StartByte: intPointer(0), EndByte: &end}}
	semanticScenes := []scriptgen.SemanticSceneInput{{SceneID: "s1", Text: transcript}}
	adapter, err := NewSemanticEngineAdapter(NewSemanticEngineExecutor(semanticBinary, resolveFFmpegBinary(), zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	semantic, editorial, err := adapter.AnalyzeSemanticEditorial(context.Background(), transcript, "it", scenes)
	if err != nil {
		t.Fatalf("combined semantic/editorial analysis failed: %v", err)
	}
	if len(semantic) != len(semanticScenes) || editorial.Summary == "" {
		t.Fatalf("combined manifest is incomplete: scenes=%d editorial=%+v", len(semantic), editorial)
	}
	var bulletSpans []scriptpkg.SemanticBulletSpan
	for _, candidate := range semantic["s1"].Bullets {
		if candidate.Start >= 0 && candidate.End > candidate.Start && candidate.End <= len(transcript) && transcript[candidate.Start:candidate.End] == candidate.Text {
			score := candidate.Score
			bulletSpans = append(bulletSpans, scriptpkg.SemanticBulletSpan{StartByte: candidate.Start, EndByte: candidate.End, Text: candidate.Text, Score: &score})
		}
	}
	legacy, err := NewPhraseImpactAnalyzer(phraseBinary, nil, nil).AnalyzeScenesWithSemanticBullets(context.Background(), transcript, "it", scenes, bulletSpans)
	if err != nil {
		t.Fatalf("legacy Phrase Impact parity analysis failed: %v", err)
	}
	if !reflect.DeepEqual(editorial.Summary, legacy.Summary) ||
		!reflect.DeepEqual(editorial.BulletPoints, legacy.BulletPoints) ||
		!reflect.DeepEqual(editorial.HeavySentences, legacy.HeavySentences) ||
		!reflect.DeepEqual(editorial.ChapterManifest, legacy.ChapterManifest) ||
		!reflect.DeepEqual(editorial.SceneHighlights, legacy.SceneHighlights) {
		t.Fatalf("same Rust Phrase Impact core diverged: combined=%+v legacy=%+v", editorial, legacy)
	}
}

func TestSemanticEngineRealBinaryMatchesPhraseImpactWithSuppliedVectors(t *testing.T) {
	semanticBinary := resolveSemanticEngineBinary()
	if semanticBinary == "" {
		t.Skip("semantic-engine release binary not built; run cargo build --release -p semantic-engine")
	}
	phraseBinary := resolvePhraseImpactParityBinary(t)
	if phraseBinary == "" {
		t.Skip("pipelinegen-muscles release binary not built; needed for Phrase Impact parity")
	}
	transcript := "La squadra prepara il progetto e controlla ogni passaggio. Gli operatori registrano i risultati e verificano tutte le procedure. La squadra completa il lavoro e pubblica il rapporto finale."
	end := len(transcript)
	scenes := []scriptpkg.SceneAnalysisInput{{SceneID: "s1", Text: transcript, StartByte: intPointer(0), EndByte: &end}}
	makeEmbedder := func() *phraseImpactFakeEmbedder {
		return &phraseImpactFakeEmbedder{batchResponse: func(texts []string) [][]float32 {
			vectors := make([][]float32, len(texts))
			for index, text := range texts {
				vectors[index] = []float32{float32(len(text) + 1), float32(strings.Count(text, "a") + 1)}
			}
			return vectors
		}}
	}
	adapter, err := NewSemanticEngineAdapter(NewSemanticEngineExecutor(semanticBinary, resolveFFmpegBinary(), zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	combinedEmbedder := makeEmbedder()
	adapter.SetPassageEmbedder(combinedEmbedder)
	semanticBundles, err := adapter.SemanticBundles(context.Background(), "it", []scriptgen.SemanticSceneInput{{SceneID: "s1", Text: transcript}})
	if err != nil {
		t.Fatalf("semantic bullet source analysis failed: %v", err)
	}
	var bulletSpans []scriptpkg.SemanticBulletSpan
	for _, candidate := range semanticBundles["s1"].Bullets {
		if candidate.Start >= 0 && candidate.End > candidate.Start && candidate.End <= len(transcript) && transcript[candidate.Start:candidate.End] == candidate.Text {
			score := candidate.Score
			bulletSpans = append(bulletSpans, scriptpkg.SemanticBulletSpan{StartByte: candidate.Start, EndByte: candidate.End, Text: candidate.Text, Score: &score})
		}
	}
	_, combined, err := adapter.AnalyzeSemanticEditorial(context.Background(), transcript, "it", scenes)
	if err != nil {
		t.Fatalf("combined E5-shaped analysis failed: %v", err)
	}
	legacyEmbedder := makeEmbedder()
	legacy, err := NewPhraseImpactAnalyzer(phraseBinary, nil, legacyEmbedder).AnalyzeScenesWithSemanticBullets(
		context.Background(), transcript, "it", scenes, bulletSpans,
	)
	if err != nil {
		t.Fatalf("legacy E5-shaped parity analysis failed: %v", err)
	}
	if !reflect.DeepEqual(combined.Summary, legacy.Summary) ||
		!reflect.DeepEqual(combined.BulletPoints, legacy.BulletPoints) ||
		!reflect.DeepEqual(combined.HeavySentences, legacy.HeavySentences) ||
		!reflect.DeepEqual(combined.ChapterManifest, legacy.ChapterManifest) ||
		!reflect.DeepEqual(combined.SceneHighlights, legacy.SceneHighlights) {
		t.Fatalf("supplied-vector results diverged: combined=%+v legacy=%+v", combined, legacy)
	}
	if len(combinedEmbedder.calls) == 0 || len(legacyEmbedder.calls) == 0 {
		t.Fatalf("embedding preparation was skipped: combined=%v legacy=%v", combinedEmbedder.calls, legacyEmbedder.calls)
	}
}

func intPointer(value int) *int { return &value }

func TestSemanticEngineRealBinaryProjectsCrossSceneGroupsAgainstMemberSources(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not built; run cargo build --release -p semantic-engine")
	}
	inputs := []scriptgen.SemanticSceneInput{
		{SceneID: "compare-a", Text: "Lo Stadio Aurora è più capiente rispetto allo Stadio Olimpico."},
		{SceneID: "compare-b", Text: "Lo Stadio Aurora è più antico rispetto allo Stadio Olimpico."},
		{SceneID: "cause-a", Text: "Lo Stadio Aurora causa la crescita di Torino."},
		{SceneID: "cause-b", Text: "Torino causa la crescita di Milano."},
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", inputs)
	if err != nil {
		t.Fatalf("real cross-scene semantic analysis failed: %v", err)
	}
	check := func(sceneID, basis string, minimumMembers int) {
		t.Helper()
		bundle := bundles[sceneID]
		if bundle == nil {
			t.Fatalf("scene %q missing from result", sceneID)
		}
		var found *scriptgen.SemanticGroup
		for i := range bundle.Groups {
			if bundle.Groups[i].RankingBasis == basis {
				found = &bundle.Groups[i]
				break
			}
		}
		if found == nil || len(found.Members) < minimumMembers {
			t.Fatalf("scene %q group basis %q missing or too small: %+v", sceneID, basis, bundle.Groups)
		}
		for _, member := range found.Members {
			var source string
			for _, input := range inputs {
				if input.SceneID == member.SceneID {
					source = input.Text
					break
				}
			}
			if source == "" || member.Start < 0 || member.End > len(source) ||
				member.Start >= member.End || source[member.Start:member.End] != member.Evidence || member.Text != member.Evidence {
				t.Fatalf("cross-scene member is not grounded against its own source: %+v", member)
			}
		}
	}
	check("compare-a", "explicit_pair_comparison_across_scenes", 4)
	check("cause-a", "explicit_asserted_causal_chain_across_scenes", 4)
}

// semantic-extract runs BrainRust's semantic engine on a text file without
// creating a video job or writing to the media catalog. It writes a versioned
// extraction and production-plan report to stdout or to --output.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
	"go.uber.org/zap"
)

func main() {
	inputPath := flag.String("input", "", "text or transcript file to analyze, or - for stdin (required)")
	outputPath := flag.String("output", "-", "JSON output path, or - for stdout")
	language := flag.String("language", "en", "BCP-47 language code")
	enginePath := flag.String("engine", defaultEnginePath(), "path to the BrainRust semantic-engine binary")
	sceneWords := flag.Int("scene-words", 160, "target maximum words per scene chunk")
	flag.Parse()

	if strings.TrimSpace(*inputPath) == "" {
		fatal("--input is required")
	}
	if strings.TrimSpace(*language) == "" {
		fatal("--language cannot be empty")
	}
	if *sceneWords < 20 {
		fatal("--scene-words must be at least 20")
	}

	data, err := readInput(*inputPath)
	if err != nil {
		fatal("read input: %v", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		fatal("input text is empty")
	}

	chunks := splitIntoScenes(text, *sceneWords)
	semanticScenes := make([]scriptgen.SemanticSceneInput, 0, len(chunks))
	req := request{Operation: "analyze_script", Language: strings.TrimSpace(*language)}
	for i, chunk := range chunks {
		id := fmt.Sprintf("scene_%03d", i+1)
		semanticScenes = append(semanticScenes, scriptgen.SemanticSceneInput{SceneID: id, Text: chunk})
		req.Scenes = append(req.Scenes, scene{SceneID: id, Text: chunk})
	}
	executor := rustexec.NewSemanticEngineExecutor(*enginePath, "", zap.NewNop())
	defer executor.Close()
	adapter, err := rustexec.NewSemanticEngineAdapter(executor)
	if err != nil {
		fatal("create semantic engine adapter: %v", err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), req.Language, semanticScenes)
	if err != nil {
		fatal("run semantic engine: %v", err)
	}
	consolidation, plan, _, err := scriptgen.PlanScriptProduction(bundles)
	if err != nil {
		fatal("build production plan: %v", err)
	}
	report := buildReport(*inputPath, req.Language, text, *sceneWords, semanticScenes, bundles, consolidation, plan)
	result, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal("encode output JSON: %v", err)
	}
	result = append(result, '\n')
	if *outputPath == "-" {
		if _, err := os.Stdout.Write(result); err != nil {
			fatal("write stdout: %v", err)
		}
		return
	}
	if err := atomicwrite.WriteFile(*outputPath, result, 0o644); err != nil {
		fatal("write output: %v", err)
	}
}

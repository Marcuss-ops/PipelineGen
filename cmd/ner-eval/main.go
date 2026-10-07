// Command ner-eval evaluates one configured NER backend against an externally
// supplied, manually annotated JSON corpus. It never creates or mutates labels.
//
// Corpus format:
// {"version":"ner-corpus.v1","annotation_method":"human_double_annotation_adjudicated",
// "annotation_guidelines":"v1","annotators":["annotator-a","annotator-b"],
// "cases":[{"id":"...","language":"en","text":"...",
// "entities":[{"text":"...","label":"PERSON","start":0,"end":9}]}]}
//
// This file stays thin on purpose: it owns the package documentation, the
// protocol constants, flag parsing, argument validation and the run
// orchestration. Corpus loading/validation lives in corpus.go, backend
// construction in backends.go, the measurement loop in evaluate.go, the report
// schema in report.go and the scoring helpers in metrics.go.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	corpusVersion            = "ner-corpus.v1"
	defaultNERWarmupPasses   = 20
	defaultNERMeasuredPasses = 200
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ner-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	corpusPath := flags.String("corpus", "", "path to manually labeled ner-corpus.v1 JSON (required)")
	backendName := flags.String("backend", "rust", "backend: rust or spacy")
	rustPath := flags.String("rust-binary", "bin/visualner", "VisualNER executable path")
	spacyURL := flags.String("spacy-url", "", "spaCy sidecar base URL; required for backend=spacy")
	warmup := flags.Int("warmup", defaultNERWarmupPasses, "number of full-corpus warm-up passes before measurement")
	iterations := flags.Int("iterations", defaultNERMeasuredPasses, "number of full-corpus measured passes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(*corpusPath) == "" {
		return errors.New("--corpus is required; provide externally annotated gold data")
	}
	if *warmup < 0 || *warmup > 1000 {
		return fmt.Errorf("--warmup must be in 0..1000")
	}
	if *iterations < 1 || *iterations > 1000 {
		return fmt.Errorf("--iterations must be in 1..1000")
	}
	gold, data, err := loadCorpus(*corpusPath, *warmup, *iterations)
	if err != nil {
		return err
	}
	backend, closeBackend, err := newNERBackend(*backendName, *rustPath, *spacyURL)
	if err != nil {
		return err
	}
	result := evaluate(backend, closeBackend, gold, evaluationConfig{
		backendName:  strings.ToLower(strings.TrimSpace(*backendName)),
		warmup:       *warmup,
		iterations:   *iterations,
		corpusSHA256: digest.SHA256Bytes(data),
	})
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

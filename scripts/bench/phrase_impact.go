package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	lexical "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases/semantic"
)

type request struct {
	Action      string               `json:"action"`
	Sentences   []semantic.Sentence  `json:"sentences,omitempty"`
	Scores      []semantic.Impact    `json:"scores,omitempty"`
	Options     semantic.PeakOptions `json:"options,omitempty"`
	Text        string               `json:"text,omitempty"`
	Language    string               `json:"language,omitempty"`
	Limit       int                  `json:"limit,omitempty"`
	LexiconRoot string               `json:"lexicon_root,omitempty"`
}

type response struct {
	Scores  []semantic.Impact     `json:"scores,omitempty"`
	Peaks   []semantic.Impact     `json:"peaks,omitempty"`
	Timings semantic.StageTimings `json:"timings,omitempty"`
	Phrases []string              `json:"phrases,omitempty"`
	Error   string                `json:"error,omitempty"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	writer := bufio.NewWriter(output)
	defer writer.Flush()
	encoder := json.NewEncoder(writer)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return fmt.Errorf("decode request: %w", err)
		}
		var res response
		switch req.Action {
		case "score":
			scores, timings, err := semantic.ScoreSentencesWithTiming(req.Sentences)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Scores, res.Timings = scores, timings
			}
		case "peaks":
			peaks, err := semantic.SelectPeaks(req.Scores, req.Options)
			if err != nil {
				if errors.Is(err, semantic.ErrInvalidPeakInput) || errors.Is(err, semantic.ErrInvalidPeakOptions) {
					res.Error = err.Error()
				} else {
					return err
				}
			} else {
				res.Peaks = peaks
			}
		case "baseline":
			if req.LexiconRoot != "" {
				root, err := filepath.Abs(req.LexiconRoot)
				if err != nil {
					return fmt.Errorf("resolve lexicon root: %w", err)
				}
				registry, err := linguistics.NewLexiconRegistry(root)
				if err != nil {
					return fmt.Errorf("load phrase lexicon: %w", err)
				}
				if err := linguistics.SetDefaultLexicon(registry); err != nil {
					return fmt.Errorf("install phrase lexicon: %w", err)
				}
			}
			res.Phrases = lexical.ImportantPhrases(req.Text, nil, req.Limit, req.Language)
		default:
			res.Error = fmt.Sprintf("unknown action %q", req.Action)
		}
		if err := encoder.Encode(res); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request stream: %w", err)
	}
	return nil
}

// Command velox-clipseq computes and executes the merge decision for a
// sequence of rendered clips.
//
// Two adjacent clips become ONE artifact only when the incoming clip declares a
// transition; every other clip stays a standalone artifact. The command never
// decides which clips belong together — kernel/media/clipsequence owns that —
// and it never substitutes a transition the local renderer cannot reproduce.
//
// Usage:
//
//	velox-clipseq plan  -sequence seq.json
//	velox-clipseq merge -sequence seq.json -outdir out/ -codec <resolved codec> [-preset p] [-crf n]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/clipmerge"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: velox-clipseq <plan|merge> -sequence <file.json> [-outdir <dir>]")
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	sequencePath := flags.String("sequence", "", "clip sequence JSON")
	outDir := flags.String("outdir", "", "output directory (merge only)")
	crf := flags.Int("crf", 0, "crf for merged groups (0 = encoder default)")
	codec := flags.String("codec", "", "resolved video encoder for merged groups, e.g. the codec your config resolves (required for merge)")
	preset := flags.String("preset", "", "optional encoder preset")
	_ = flags.Parse(os.Args[2:])
	if *sequencePath == "" {
		fail("velox-clipseq: -sequence is required")
	}

	sequence, err := readSequence(*sequencePath)
	if err != nil {
		fail("velox-clipseq: %v", err)
	}
	plan, err := sequence.Plan()
	if err != nil {
		fail("velox-clipseq: %v", err)
	}
	switch command {
	case "plan":
		printJSON(plan)
	case "merge":
		if *outDir == "" {
			fail("velox-clipseq: -outdir is required for merge")
		}
		if *codec == "" {
			fail("velox-clipseq: -codec is required for merge (pass the codec your encoder policy resolves)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		artifacts, err := clipmerge.Merge(ctx, sequence, plan, clipmerge.Options{
			OutputDir: *outDir,
			Encoder:   media.EncoderPolicy{Codec: *codec, Preset: *preset, CRF: *crf},
			Logf:      func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
		})
		if err != nil {
			fail("velox-clipseq: %v", err)
		}
		printJSON(artifacts)
	default:
		fail("velox-clipseq: unknown command %q (want plan or merge)", command)
	}
}

func readSequence(path string) (clipsequence.Sequence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return clipsequence.Sequence{}, err
	}
	var sequence clipsequence.Sequence
	if err := json.Unmarshal(data, &sequence); err != nil {
		return clipsequence.Sequence{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return sequence, nil
}

func printJSON(value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail("velox-clipseq: encode output: %v", err)
	}
	fmt.Println(string(data))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

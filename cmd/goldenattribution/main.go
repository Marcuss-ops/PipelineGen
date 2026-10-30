// Command goldenattribution projects one or more golden-overlay summary.json
// files into a reproducible R0 attribution report.
//
// It is deliberately read-only: the input summaries are the certified
// artifacts, and this command adds the lane-level arithmetic that the existing
// per-run summary does not carry (render floor, lane utilization, FIFO vs LPT
// makespan, fixed per-batch overhead, phase sums). It never renders, submits,
// deploys or touches the database.
//
// Usage:
//
//	goldenattribution --lanes 3 --out report.json \
//	  results/closeout-r1/summary.json results/closeout-r2/summary.json results/closeout-r3/summary.json
//
// The report schema is pipelinegen.golden-attribution.v1. Each run records its
// source path and SHA-256; the report also carries the median across runs.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	lanes := flag.Int("lanes", 3, "GPU lanes used by the run (positive integer)")
	outPath := flag.String("out", "", "output JSON path (default: stdout)")
	markdownPath := flag.String("markdown", "", "optional Markdown report path")
	flag.Parse()
	if *lanes < 1 {
		fmt.Fprintln(os.Stderr, "goldenattribution: --lanes must be >= 1")
		os.Exit(2)
	}
	if len(flag.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "goldenattribution: at least one summary.json path is required")
		flag.Usage()
		os.Exit(2)
	}

	report, err := buildReport(flag.Args(), *lanes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goldenattribution: %v\n", err)
		os.Exit(1)
	}
	encoded, err := marshalReport(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goldenattribution: encode report: %v\n", err)
		os.Exit(1)
	}
	if *outPath == "" {
		_, _ = os.Stdout.Write(encoded)
	} else if err := writeFile(*outPath, encoded); err != nil {
		fmt.Fprintf(os.Stderr, "goldenattribution: write report: %v\n", err)
		os.Exit(1)
	}
	if *markdownPath != "" {
		if err := writeFile(*markdownPath, []byte(markdownReport(report))); err != nil {
			fmt.Fprintf(os.Stderr, "goldenattribution: write markdown: %v\n", err)
			os.Exit(1)
		}
	}
}

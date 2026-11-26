// Command payloadlint is the offline contract and semantic gate for the
// script.generate job corpus (ops/jobs/**).
//
// It walks a directory, recognises real request envelopes (a JSON object with
// version + items), and applies the SAME strict decoder and semantic checks as
// POST /api/script/generate and velox preflight. Job dumps, poll responses and
// unrelated JSON files are skipped, never reported as malformed payloads.
//
// Exit codes: 0 all envelopes coherent, 1 at least one payload failed,
// 2 usage/IO error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/payloadcheck"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

type payloadIssue struct {
	File     string   `json:"file"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type lintReport struct {
	OK       bool           `json:"ok"`
	Strict   bool           `json:"strict,omitempty"`
	Dir      string         `json:"dir"`
	Checked  int            `json:"checked"`
	Failed   int            `json:"failed"`
	Skipped  int            `json:"skipped"`
	Payloads []payloadIssue `json:"payloads,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("payloadlint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "ops/jobs", "directory to scan recursively")
	asJSON := fs.Bool("json", false, "emit a machine-readable report")
	strict := fs.Bool("strict", false, "treat semantic warnings as failures")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch fs.NArg() {
	case 0:
	case 1:
		*dir = fs.Arg(0)
	default:
		fmt.Fprintln(stderr, "payloadlint: expected at most one directory argument")
		return exitUsage
	}
	report, err := lintDirWithOptions(*dir, *strict)
	if err != nil {
		fmt.Fprintf(stderr, "payloadlint: %v\n", err)
		return exitUsage
	}
	if *asJSON {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "payloadlint: %v\n", err)
			return exitUsage
		}
		fmt.Fprintln(stdout, string(encoded))
	} else if report.OK {
		fmt.Fprintf(stdout, "payloadlint: ok — %d envelope(s) checked in %s (%d non-envelope files skipped)\n", report.Checked, report.Dir, report.Skipped)
		for _, payload := range report.Payloads {
			for _, warning := range payload.Warnings {
				fmt.Fprintf(stdout, "  - %s: warning: %s\n", payload.File, warning)
			}
		}
	} else {
		fmt.Fprintf(stderr, "payloadlint: FAIL — %d of %d envelope(s) in %s\n", report.Failed, report.Checked, report.Dir)
		for _, payload := range report.Payloads {
			for _, problem := range payload.Errors {
				fmt.Fprintf(stderr, "  - %s: %s\n", payload.File, problem)
			}
			for _, warning := range payload.Warnings {
				fmt.Fprintf(stderr, "  - %s: warning: %s\n", payload.File, warning)
			}
		}
	}
	if report.OK {
		return exitOK
	}
	return exitFailure
}

func lintDir(dir string) (lintReport, error) {
	return lintDirWithOptions(dir, false)
}

func lintDirWithOptions(dir string, strict bool) (lintReport, error) {
	report := lintReport{Dir: dir, Strict: strict}
	info, err := os.Stat(dir)
	if err != nil {
		return report, fmt.Errorf("read %s: %w", dir, err)
	}
	if !info.IsDir() {
		return report, fmt.Errorf("%s is not a directory", dir)
	}
	var files []string
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(entry.Name()) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return report, fmt.Errorf("walk %s: %w", dir, err)
	}
	sort.Strings(files)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return report, fmt.Errorf("read %s: %w", path, err)
		}
		if !looksLikeEnvelope(raw) {
			report.Skipped++
			continue
		}
		report.Checked++
		problems, warnings := checkEnvelope(raw)
		if len(problems) == 0 && len(warnings) == 0 {
			continue
		}
		failed := len(problems) > 0 || (strict && len(warnings) > 0)
		if failed {
			report.Failed++
		}
		report.Payloads = append(report.Payloads, payloadIssue{File: path, Errors: problems, Warnings: warnings})
	}
	report.OK = report.Failed == 0
	return report, nil
}

// looksLikeEnvelope distinguishes a real script.generate request from the
// many job-status dumps stored beside it. Only an object carrying both
// version and items enters the contract gate.
func looksLikeEnvelope(raw []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	_, hasVersion := fields["version"]
	_, hasItems := fields["items"]
	return hasVersion && hasItems
}

func checkEnvelope(raw []byte) (problems, warnings []string) {
	env, err := scriptpkg.DecodeGenerationEnvelopeStrict(raw)
	if err != nil {
		return []string{err.Error()}, nil
	}
	// Version 3 is the canonical wire: the V2-only duplicate spellings are
	// rejected, exactly as the ingress does. Version 2 keeps them readable
	// (with the semantic warnings below), so the recorded corpus stays valid.
	if reason := scriptpkg.RetiredContractFieldReason(raw, env.Version); reason != "" {
		problems = append(problems, reason)
	}
	// gencore's validator IS the API gate (it runs the structural Validate and
	// then the payload-only rules: retired `docs`, segment caps, source_text
	// limits, empty video_metadata). Running it here is what makes "the linter
	// accepts it" equal to "the endpoint accepts it".
	if err := gencore.NewDefaultPayloadValidator().ValidateEnvelope(env); err != nil {
		problems = append(problems, err.Error())
	}
	problems = append(problems, payloadcheck.SemanticErrors(env)...)
	warnings = append(warnings, payloadcheck.SemanticWarnings(env)...)
	return problems, warnings
}

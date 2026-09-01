// cmd/mediacert/main.go — mediacert CLI entry point.
//
// Usage:
//
//	mediacert verify result.json spec.json
//
// Loads the MediaResult and the Spec, fills any empty source_text_hash by
// recomputing it from source_text (so fixtures may omit the hash), runs
// mediacert.Certify and prints the human-readable report. Exits 0 when
// CERTIFIED=true, 1 otherwise. The canonical Make target is
// `make verify-vidrush-semantic`.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func main() {
	if len(os.Args) < 3 || (os.Args[1] != "verify" && os.Args[1] != "verify-operational") {
		printUsage()
		os.Exit(2)
	}
	resultPath := os.Args[2]
	result, err := loadResult(resultPath)
	if err != nil {
		fail("load result: %v", err)
	}
	result = normalizeResultHashes(result)

	var report mediacert.Report
	if os.Args[1] == "verify-operational" {
		report = mediacert.OperationalOwnershipReport(result)
	} else {
		if len(os.Args) < 4 {
			printUsage()
			os.Exit(2)
		}
		spec, err := loadSpec(os.Args[3])
		if err != nil {
			fail("load spec: %v", err)
		}
		report = mediacert.Certify(spec, result)
	}
	fmt.Print(mediacert.HumanReport(report))
	if !report.Certified {
		os.Exit(1)
	}
}

func loadSpec(path string) (mediacert.Spec, error) {
	var spec mediacert.Spec
	if err := loadJSON(path, &spec); err != nil {
		return mediacert.Spec{}, err
	}
	return spec, nil
}

func loadResult(path string) (mediacert.MediaResult, error) {
	var result mediacert.MediaResult
	if err := loadJSON(path, &result); err != nil {
		return mediacert.MediaResult{}, err
	}
	return result, nil
}

func loadJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("empty file: %s", path)
	}
	return json.Unmarshal(data, out)
}

// normalizeResultHashes fills any empty source_text_hash by recomputing it
// from the segment's source_text. This lets golden-fixture files omit the
// hash while still passing the SOURCE IMMUTABILITY check.
func normalizeResultHashes(result mediacert.MediaResult) mediacert.MediaResult {
	for i, seg := range result.Segments {
		if seg.SourceTextHash == "" && seg.SourceText != "" {
			result.Segments[i].SourceTextHash = script.ComputeCanonicalSegmentTextHash(seg.SourceText)
		}
	}
	return result
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mediacert: "+format+"\n", args...)
	os.Exit(1)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: mediacert verify <result.json> <spec.json>")
	fmt.Fprintln(os.Stderr, "       mediacert verify-operational <result.json>")
}

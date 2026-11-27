// Command overlaycert writes the canonical overlay certification report for a
// plan, optionally joined with worker execution evidence and an artifact hash.
//
// It is the only producer of the overlay verdict: certify_overlay_lane.sh does
// not re-check the same fields with jq. Exit codes are part of the contract so
// the script can distinguish a rejected run from an unmeasured one:
//
//	0  PASS
//	1  FAIL
//	2  usage or input error
//	3  UNVERIFIABLE (the selected mode promised proof that the evidence lacks)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/certification"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

func main() {
	os.Exit(run())
}

func run() int {
	planPath := flag.String("plan", "", "overlay plan JSON (bare plan or /full job document)")
	executionPath := flag.String("execution", "", "optional pipelinegen.overlay-execution-evidence.v1 JSON")
	artifactPath := flag.String("artifact", "", "optional artifact file; its sha256 is computed and bound to the report")
	artifactSHA := flag.String("artifact-sha256", "", "optional artifact sha256 when the bytes are remote")
	artifactKind := flag.String("artifact-kind", "overlay_artifact", "overlay_artifact or final_video")
	artifactFrames := flag.Int("artifact-frames", 0, "artifact frame count, when known")
	artifactFPSNum := flag.Int("artifact-fps-num", 0, "artifact frame rate numerator (default 24)")
	artifactFPSDen := flag.Int("artifact-fps-den", 0, "artifact frame rate denominator (default 1)")
	modeFlag := flag.String("mode", "fast", "fast, normal or certify")
	outPath := flag.String("out", "", "write the report here instead of stdout")
	runID := flag.String("run-id", "", "override the run id recorded in the report")
	tolerance := flag.Int("tolerance-frames", 0, "entry/exit tolerance in frames (default 2)")
	varietyMinItems := flag.Int("variety-min-items", 0, "minimum animated items for the variety gate (default 4)")
	varietyMinDistinct := flag.Int("variety-min-distinct", 0, "minimum distinct motions for the variety gate (default 2)")
	varietyMaxShare := flag.Float64("variety-max-share", 0, "maximum share of one motion for the variety gate (default 0.75)")
	flag.Parse()

	if *planPath == "" {
		fmt.Fprintln(os.Stderr, "overlaycert: -plan is required")
		return 2
	}
	mode, err := certification.ParseMode(*modeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "overlaycert: %v\n", err)
		return 2
	}
	planBytes, err := os.ReadFile(*planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "overlaycert: read plan: %v\n", err)
		return 2
	}
	plan, err := certification.DecodePlan(planBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "overlaycert: %v\n", err)
		return 2
	}

	var execution *certification.ExecutionEvidence
	if *executionPath != "" {
		executionBytes, readErr := os.ReadFile(*executionPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "overlaycert: read execution evidence: %v\n", readErr)
			return 2
		}
		execution, err = certification.DecodeExecution(executionBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "overlaycert: %v\n", err)
			return 2
		}
	}

	var artifact *certification.ArtifactEvidence
	switch {
	case *artifactPath != "":
		sum, size, hashErr := digest.SHA256File(*artifactPath)
		if hashErr != nil {
			fmt.Fprintf(os.Stderr, "overlaycert: hash artifact: %v\n", hashErr)
			return 2
		}
		artifact = &certification.ArtifactEvidence{
			Kind:       *artifactKind,
			SHA256:     sum,
			SizeBytes:  size,
			FrameCount: *artifactFrames,
			FPSNum:     *artifactFPSNum,
			FPSDen:     *artifactFPSDen,
		}
	case *artifactSHA != "":
		artifact = &certification.ArtifactEvidence{
			Kind:       *artifactKind,
			SHA256:     *artifactSHA,
			FrameCount: *artifactFrames,
			FPSNum:     *artifactFPSNum,
			FPSDen:     *artifactFPSDen,
		}
	}

	input := certification.Input{
		Plan:            plan,
		Execution:       execution,
		Artifact:        artifact,
		Mode:            mode,
		ToleranceFrames: *tolerance,
		Variety: certification.VarietyPolicy{
			Enabled:     true,
			MinItems:    *varietyMinItems,
			MinDistinct: *varietyMinDistinct,
			MaxShare:    *varietyMaxShare,
		},
	}
	report, err := certification.Build(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "overlaycert: %v\n", err)
		return 2
	}
	if *runID != "" {
		report.RunID = *runID
	}

	encoded, err := certification.Marshal(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "overlaycert: encode report: %v\n", err)
		return 2
	}
	if *outPath != "" {
		if err := atomicwrite.WriteFile(*outPath, encoded, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "overlaycert: write report: %v\n", err)
			return 2
		}
	} else {
		if _, err := os.Stdout.Write(encoded); err != nil {
			fmt.Fprintf(os.Stderr, "overlaycert: write stdout: %v\n", err)
			return 2
		}
	}

	fmt.Fprintf(os.Stderr, "overlaycert: verdict=%s mode=%s planned=%d rendered=%d verified=%d failed=%d unverifiable=%d\n",
		report.Verdict, report.Mode, report.Counts.Planned, report.Counts.Rendered,
		report.Counts.Verified, report.Counts.Failed, report.Counts.Unverifiable)

	switch report.Verdict {
	case certification.VerdictPass:
		return 0
	case certification.VerdictFail:
		return 1
	case certification.VerdictUnverifiable:
		return 3
	default:
		return 2
	}
}

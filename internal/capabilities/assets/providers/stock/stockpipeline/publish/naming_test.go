package publish

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"strings"
	"testing"
)

func TestRootFolderNameFallbacks(t *testing.T) {
	got := RunInputRootFolderName(&core.RunInput{
		FolderName:    "  Round 7  ",
		Subfolder:     "fallback",
		SearchQueries: []string{"query"},
	})
	if got != "Round 7" {
		t.Fatalf("RunInputRootFolderName() = %q, want Round 7", got)
	}

	got = RunInputRootFolderName(&core.RunInput{SearchQueries: []string{"boxing training"}})
	if got != "boxing training" {
		t.Fatalf("RunInputRootFolderName() query fallback = %q, want boxing training", got)
	}

	got = RunInputRootFolderName(&core.RunInput{DirectURLs: []string{"https://example.test/video/Some_Movie.mp4?v=1#clip"}})
	if got != "Some_Movie" {
		t.Fatalf("RunInputRootFolderName() URL fallback = %q, want Some_Movie", got)
	}

	if got := RunInputRootFolderName(nil); got != "stock" {
		t.Fatalf("RunInputRootFolderName(nil) = %q, want stock", got)
	}
}

func TestResolvedFolderIDNeverUsesWorkflowID(t *testing.T) {
	if got := RunInputResolvedFolderID(&core.RunInput{FolderID: "workflow-only"}); got != "" {
		t.Fatalf("RunInputResolvedFolderID() = %q, want empty for workflow-only ID", got)
	}
	if got := RunInputResolvedFolderID(&core.RunInput{DriveFolderID: "drive-folder", DriveFolderResolved: false}); got != "" {
		t.Fatalf("RunInputResolvedFolderID() = %q, want empty for unverified Drive ID", got)
	}
	if got := RunInputResolvedFolderID(&core.RunInput{DriveFolderID: " drive-folder ", DriveFolderResolved: true}); got != "drive-folder" {
		t.Fatalf("RunInputResolvedFolderID() = %q, want trimmed verified Drive ID", got)
	}
}

func TestExplicitClipNamingPreservesExistingFallbacks(t *testing.T) {
	plan := core.ClipPlan{Title: "Round 7 - Broner barcolla", StartSec: 993, EndSec: 1048}
	if got := PerClipLeafName(plan); got != "round-7-broner-barcolla" {
		t.Fatalf("PerClipLeafName() = %q, want canonical title slug", got)
	}

	plan.Slug = "!!!"
	if got := PerClipLeafName(plan); got != "round-7-broner-barcolla" {
		t.Fatalf("PerClipLeafName() punctuation fallback = %q, want title slug", got)
	}

	plan = core.ClipPlan{StartSec: 32, EndSec: 51}
	if got := PerClipLeafName(plan); got != "00-00-32_to_00-00-51" {
		t.Fatalf("PerClipLeafName() empty fallback = %q, want timestamp literal", got)
	}

	in := &core.RunInput{Subfolder: "root/Round_7/00-00-32_to_00-01-27"}
	if got := RunInputTimestampParentGroupName(in); got != "Round_7" {
		t.Fatalf("RunInputTimestampParentGroupName() = %q, want Round_7", got)
	}

	if got := RunInputClipFolderName(&core.RunInput{}, core.ClipPlan{Round: 7}, "metadata"); got != "Round 7" {
		t.Fatalf("RunInputClipFolderName() round = %q, want Round 7", got)
	}
	if got := RunInputClipFolderName(&core.RunInput{Subfolder: "root/parent/child"}, core.ClipPlan{Round: 7}, "parent"); got != "parent" {
		t.Fatalf("RunInputClipFolderName() explicit subfolder = %q, want parent", got)
	}
}

func TestSanitizedURLBasenameStripsQueryAndFragment(t *testing.T) {
	got := SanitizedURLBasename(" https://example.test/a/Some_Movie.mp4?v=1#fragment ")
	if got != "Some_Movie" {
		t.Fatalf("SanitizedURLBasename() = %q, want Some_Movie", got)
	}
	if strings.TrimSpace(got) != got {
		t.Fatalf("SanitizedURLBasename() returned surrounding whitespace: %q", got)
	}
}

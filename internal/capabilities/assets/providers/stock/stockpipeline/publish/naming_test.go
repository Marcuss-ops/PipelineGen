package assets

import (
	"strings"
	"testing"

	stocktypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/types"
)

func TestRootFolderNameFallbacks(t *testing.T) {
	got := RootFolderName(&stocktypes.RunInput{
		FolderName:    "  Round 7  ",
		Subfolder:     "fallback",
		SearchQueries: []string{"query"},
	})
	if got != "Round 7" {
		t.Fatalf("RootFolderName() = %q, want Round 7", got)
	}

	got = RootFolderName(&stocktypes.RunInput{SearchQueries: []string{"boxing training"}})
	if got != "boxing training" {
		t.Fatalf("RootFolderName() query fallback = %q, want boxing training", got)
	}

	got = RootFolderName(&stocktypes.RunInput{DirectURLs: []string{"https://example.test/video/Some_Movie.mp4?v=1#clip"}})
	if got != "Some_Movie" {
		t.Fatalf("RootFolderName() URL fallback = %q, want Some_Movie", got)
	}

	if got := RootFolderName(nil); got != "stock" {
		t.Fatalf("RootFolderName(nil) = %q, want stock", got)
	}
}

func TestResolvedFolderIDNeverUsesWorkflowID(t *testing.T) {
	if got := ResolvedFolderID(&stocktypes.RunInput{FolderID: "workflow-only"}); got != "" {
		t.Fatalf("ResolvedFolderID() = %q, want empty for workflow-only ID", got)
	}
	if got := ResolvedFolderID(&stocktypes.RunInput{DriveFolderID: "drive-folder", DriveFolderResolved: false}); got != "" {
		t.Fatalf("ResolvedFolderID() = %q, want empty for unverified Drive ID", got)
	}
	if got := ResolvedFolderID(&stocktypes.RunInput{DriveFolderID: " drive-folder ", DriveFolderResolved: true}); got != "drive-folder" {
		t.Fatalf("ResolvedFolderID() = %q, want trimmed verified Drive ID", got)
	}
}

func TestExplicitClipNamingPreservesExistingFallbacks(t *testing.T) {
	plan := stocktypes.ClipPlan{Title: "Round 7 - Broner barcolla", StartSec: 993, EndSec: 1048}
	if got := PerClipLeafName(plan); got != "round-7-broner-barcolla" {
		t.Fatalf("PerClipLeafName() = %q, want canonical title slug", got)
	}

	plan.Slug = "!!!"
	if got := PerClipLeafName(plan); got != "round-7-broner-barcolla" {
		t.Fatalf("PerClipLeafName() punctuation fallback = %q, want title slug", got)
	}

	plan = stocktypes.ClipPlan{StartSec: 32, EndSec: 51}
	if got := PerClipLeafName(plan); got != "00-00-32_to_00-00-51" {
		t.Fatalf("PerClipLeafName() empty fallback = %q, want timestamp literal", got)
	}

	in := &stocktypes.RunInput{Subfolder: "root/Round_7/00-00-32_to_00-01-27"}
	if got := TimestampParentGroupName(in); got != "Round_7" {
		t.Fatalf("TimestampParentGroupName() = %q, want Round_7", got)
	}

	if got := ClipFolderName(&stocktypes.RunInput{}, stocktypes.ClipPlan{Round: 7}, "metadata"); got != "Round 7" {
		t.Fatalf("ClipFolderName() round = %q, want Round 7", got)
	}
	if got := ClipFolderName(&stocktypes.RunInput{Subfolder: "root/parent/child"}, stocktypes.ClipPlan{Round: 7}, "parent"); got != "parent" {
		t.Fatalf("ClipFolderName() explicit subfolder = %q, want parent", got)
	}
	if got := TimestampParentLeafName(stocktypes.ClipPlan{ParentSlug: "parent_slug", Title: "ignored"}); got != "parent_slug" {
		t.Fatalf("TimestampParentLeafName() parent slug = %q, want parent_slug", got)
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

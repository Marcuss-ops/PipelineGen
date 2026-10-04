package prompts

import (
	"strings"
	"testing"

	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// sharedPrefixFixture builds a request carrying the shared-prefix fields.
func sharedPrefixFixture() ollamatypes.TextGenerationRequest {
	return ollamatypes.TextGenerationRequest{
		Language:          "en",
		OutputMode:        ollamatypes.OutputModePlainText,
		SharedPrefix:      "SHARED EDITORIAL HEADER + OUTPUT CONTRACT",
		SegmentAssignment: "SEGMENT 1\nTopic: cold open\nTarget words: about 80",
		SourceText:        "Milton leaves the courthouse.",
		Title:             "Milton",
	}
}

// TestBuildChatMessages_SharedPrefixLayoutPinsOrder pins the B3 layout: the
// shared prefix is the FIRST user-message block and the per-segment
// assignment is the LAST block, so the long shared run stays KV-cacheable
// across a per-segment fan-out. The shared run must be byte-identical across
// two different assignments (that is the property Ollama's prefix cache uses).
func TestBuildChatMessages_SharedPrefixLayoutPinsOrder(t *testing.T) {
	req1 := sharedPrefixFixture()
	req2 := sharedPrefixFixture()
	req2.SegmentAssignment = "SEGMENT 1\nTopic: chase scene\nTarget words: about 120"

	msgs1 := BuildChatMessages(&req1)
	msgs2 := BuildChatMessages(&req2)
	if len(msgs1) != 2 || len(msgs2) != 2 {
		t.Fatalf("chat messages = %d/%d, want 2/2", len(msgs1), len(msgs2))
	}

	user1 := msgs1[1].Content
	user2 := msgs2[1].Content

	if !strings.Contains(user1, "OVERRIDING WRITING INSTRUCTIONS") ||
		!strings.Contains(user1, "SHARED EDITORIAL HEADER + OUTPUT CONTRACT") {
		t.Fatalf("user message must open with the shared prefix block:\n%s", truncateForTest(user1))
	}
	firstBlockEnd := strings.Index(user1, "## END OF OVERRIDING INSTRUCTIONS ##")
	if firstBlockEnd < 0 {
		t.Fatalf("user message must contain the overriding block terminator")
	}
	assignmentIdx := strings.Index(user1, "## CURRENT SEGMENT ASSIGNMENT")
	if assignmentIdx < 0 {
		t.Fatalf("user message must contain the segment assignment block")
	}
	if assignmentIdx < firstBlockEnd {
		t.Fatalf("assignment block must come AFTER the shared block")
	}
	if !strings.HasSuffix(strings.TrimSpace(user1), "Ignore any other segment count or multi-paragraph requirement stated in the template.") {
		t.Fatalf("assignment block must be the LAST block of the user message, tail=%q", truncateForTest(user1[max(0, len(user1)-120):]))
	}

	// The KV-cache property: everything BEFORE the assignment block (system
	// message + shared prefix + template) must be byte-identical between two
	// per-segment calls of the same job.
	commonLen := longestCommonPrefixLen(user1, user2)
	if !strings.HasPrefix(user1[:commonLen], "## OVERRIDING WRITING INSTRUCTIONS") {
		t.Fatalf("common prefix must start at the shared block, got %q", truncateForTest(user1[:max(0, minInt(commonLen, 80))]))
	}
	if commonLen < assignmentIdx {
		t.Fatalf("common prefix (%d) must cover the whole shared run (assignment at %d)", commonLen, assignmentIdx)
	}
}

// TestBuildChatMessages_LegacyLayoutUnchanged pins the backward-compat
// contract: without SharedPrefix the user message is byte-identical to the
// legacy composition (Prompt prepended above the template, no assignment
// block).
func TestBuildChatMessages_LegacyLayoutUnchanged(t *testing.T) {
	req := ollamatypes.TextGenerationRequest{
		Language:   "en",
		OutputMode: ollamatypes.OutputModePlainText,
		Prompt:     "LEGACY BRIEF",
		SourceText: "Milton leaves the courthouse.",
		Title:      "Milton",
	}
	msgs := BuildChatMessages(&req)
	user := msgs[1].Content
	if !strings.Contains(user, "LEGACY BRIEF") {
		t.Fatalf("legacy prompt must stay in the user message")
	}
	if strings.Contains(user, "## CURRENT SEGMENT ASSIGNMENT") {
		t.Fatalf("legacy layout must not emit an assignment block")
	}
	if strings.Index(user, "LEGACY BRIEF") > strings.Index(user, "TASK: Write a true NARRATIVE DOCUMENTARY") {
		t.Fatalf("legacy prompt must stay ABOVE the task template")
	}
}

func longestCommonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func truncateForTest(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

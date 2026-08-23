package prompts

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

func TestApplyGroundingPolicy_ClipsPrimary(t *testing.T) {
	content := applyGroundingPolicy(scriptpkg.GroundingPolicyClipsPrimary, "TASK: write script")
	if !strings.Contains(content, "GROUNDING POLICY — CLIPS PRIMARY") {
		t.Errorf("expected CLIPS PRIMARY instruction, got:\n%s", content)
	}
	if !strings.Contains(content, "clip evidence is the MAIN source") {
		t.Errorf("expected clip evidence to be described as main source, got:\n%s", content)
	}
	if !strings.HasSuffix(content, "TASK: write script") {
		t.Errorf("expected original content to be preserved after policy block, got:\n%s", content)
	}
}

func TestApplyGroundingPolicy_SourcePrimary(t *testing.T) {
	content := applyGroundingPolicy(scriptpkg.GroundingPolicySourcePrimary, "TASK: write script")
	if !strings.Contains(content, "GROUNDING POLICY — SOURCE PRIMARY") {
		t.Errorf("expected SOURCE PRIMARY instruction, got:\n%s", content)
	}
	if !strings.Contains(content, "reference input (source_text) is the MAIN source") {
		t.Errorf("expected source_text to be described as main source, got:\n%s", content)
	}
}

func TestApplyGroundingPolicy_Balanced(t *testing.T) {
	content := applyGroundingPolicy(scriptpkg.GroundingPolicyBalanced, "TASK: write script")
	if !strings.Contains(content, "GROUNDING POLICY — BALANCED") {
		t.Errorf("expected BALANCED instruction, got:\n%s", content)
	}
	if !strings.Contains(content, "equal weight") {
		t.Errorf("expected equal weight mention, got:\n%s", content)
	}
}

func TestApplyGroundingPolicy_Empty(t *testing.T) {
	content := applyGroundingPolicy("", "TASK: write script")
	if content != "TASK: write script" {
		t.Errorf("expected unchanged content for empty policy, got:\n%s", content)
	}
}

func TestBuildChatMessages_IncludesGroundingPolicy(t *testing.T) {
	req := &types.TextGenerationRequest{
		Language:        "en",
		Tone:            "documentary",
		SourceText:      "The quick brown fox.",
		Title:           "Test",
		GroundingPolicy: scriptpkg.GroundingPolicySourcePrimary,
	}

	messages := BuildChatMessages(req)
	if len(messages) == 0 {
		t.Fatal("expected at least one message")
	}
	userMsg := messages[len(messages)-1]
	if userMsg.Role != "user" {
		t.Fatalf("expected last message to be user, got %q", userMsg.Role)
	}
	if !strings.Contains(userMsg.Content, "GROUNDING POLICY — SOURCE PRIMARY") {
		t.Errorf("expected grounding policy in user message, got:\n%s", userMsg.Content)
	}
}

func TestBuildChatMessages_PlainTextMaxCharsDoesNotSelectJSONPrompt(t *testing.T) {
	req := &types.TextGenerationRequest{
		Language:   "it",
		Tone:       "energico",
		SourceText: "La clip mostra un pugile durante l'allenamento.",
		Title:      "Test clip",
		MaxChars:   280,
		OutputMode: types.OutputModePlainText,
	}

	messages := BuildChatMessages(req)
	if len(messages) != 2 {
		t.Fatalf("expected system and user messages, got %d", len(messages))
	}
	user := messages[1].Content
	if strings.Contains(user, "raw JSON array") {
		t.Fatalf("plain-text request selected legacy JSON prompt:\n%s", user)
	}
	if !strings.Contains(user, "straight continuous prose only") {
		t.Fatalf("plain-text prose instructions missing from prompt:\n%s", user)
	}
}

func TestBuildChatMessages_LegacyScriptV1StillSelectsJSONPrompt(t *testing.T) {
	req := &types.TextGenerationRequest{
		Language:   "it",
		SourceText: "La clip mostra un pugile durante l'allenamento.",
		Title:      "Test clip",
		MaxChars:   280,
		OutputMode: types.OutputModeScriptV1,
	}

	messages := BuildChatMessages(req)
	if !strings.Contains(messages[1].Content, "raw JSON array") {
		t.Fatalf("legacy script_v1 request lost JSON prompt:\n%s", messages[1].Content)
	}
}

func TestBuildTextPrompt_IncludesGroundingPolicy(t *testing.T) {
	req := &types.TextGenerationRequest{
		Language:        "en",
		Tone:            "documentary",
		SourceText:      "The quick brown fox.",
		Title:           "Test",
		GroundingPolicy: scriptpkg.GroundingPolicyClipsPrimary,
	}

	prompt := BuildTextPrompt(req)
	if !strings.Contains(prompt, "GROUNDING POLICY — CLIPS PRIMARY") {
		t.Errorf("expected grounding policy in text prompt, got:\n%s", prompt)
	}
}

package script

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
)

func TestValidateGenerateBatchRequestRejectsInvalidRequest(t *testing.T) {
	req := &scripts.GenerateBatchRequest{
		ChannelID:             "",
		Language:              "xx",
		Duration:              60,
		DocTitle:              "",
		TargetWordsPerChapter: 700,
		BatchTopics: []scripts.BatchTopic{
			{Topic: "A", SourceText: "ok"},
			{Topic: "", SourceText: "ok"},
			{Topic: "C", SourceText: ""},
		},
	}

	errs := scripts.ValidateGenerateBatchRequest(req, "", map[string]struct{}{"en": {}, "it": {}})
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}

	// Note: "channel_id is required" and "items[2].source_text is empty" are
	// no longer hard errors — the handler defaults them to scriptsCfg.BatchChannelID
	// and the item's topic respectively. See handler_go::GenerateBatch.
	want := []string{
		"doc_title is required",
		"duration must be at least 120 seconds",
		"drive_folder_id is required",
		"language \"xx\" is not supported",
		"target_words_per_item must be between 800 and 5000",
		"items[1].topic is empty",
	}
	for _, needle := range want {
		found := false
		for _, err := range errs {
			if strings.Contains(err, needle) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected validation error containing %q, got %#v", needle, errs)
		}
	}
}

func TestValidateGenerateBatchRequestAcceptsEmptyChannelIDAndSourceText(t *testing.T) {
	// Regression: with the June 2026 simplification, channel_id and
	// items[].source_text are optional in the request body. The handler
	// defaults channel_id to scriptsCfg.BatchChannelID and source_text to
	// the item's topic. Validation should not flag them as errors.
	req := &scripts.GenerateBatchRequest{
		ChannelID:             "",
		Language:              "en",
		Duration:              300,
		DriveFolderID:         "folder-abc",
		DocTitle:              "Test Doc",
		TargetWordsPerChapter: 1500,
		BatchTopics: []scripts.BatchTopic{
			{Topic: "A", SourceText: ""}, // empty source_text is OK
		},
	}

	errs := scripts.ValidateGenerateBatchRequest(req, "folder-abc", map[string]struct{}{"en": {}, "it": {}})
	for _, err := range errs {
		if strings.Contains(err, "channel_id") {
			t.Errorf("channel_id should not be a validation error, got: %s", err)
		}
		if strings.Contains(err, "source_text is empty") {
			t.Errorf("items[].source_text should not be a validation error, got: %s", err)
		}
	}
}

// TestBuildBatchGoogleDocHTML was removed June 2026: the production
// buildBatchGoogleDocHTML helper and the generatedPart type live in the
// handler's HTML-build module which is being refactored. The replacement
// test (handler HTML structure) lands with the refactor PR — see
// internal/api/script/handler_script_handlers_handler_batch_html.go::buildBatchGoogleDocHTML.
//

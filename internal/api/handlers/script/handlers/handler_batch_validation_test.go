package handlers

import (
	"strings"
	"testing"
)

func TestValidateGenerateBatchRequestRejectsInvalidRequest(t *testing.T) {
	req := &GenerateBatchRequest{
		BaseGenerateRequest: BaseGenerateRequest{
			ChannelID: "",
			Language:  "xx",
			Duration:  60,
		},
		DocTitle:              "",
		TargetWordsPerChapter: 700,
		BatchTopics: []BatchTopic{
			{Topic: "A", SourceText: "ok"},
			{Topic: "", SourceText: "ok"},
			{Topic: "C", SourceText: ""},
		},
	}

	errs := validateGenerateBatchRequest(req, "", map[string]struct{}{"en": {}, "it": {}})
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}

	// Note: "channel_id is required" and "items[2].source_text is empty" are
	// no longer hard errors — the handler defaults them to scriptsCfg.BatchChannelID
	// and the item's topic respectively. See handler_batch.go::GenerateBatch.
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
	req := &GenerateBatchRequest{
		BaseGenerateRequest: BaseGenerateRequest{
			ChannelID:     "",
			Language:      "en",
			Duration:      300,
			DriveFolderID: "folder-abc",
		},
		DocTitle:              "Test Doc",
		TargetWordsPerChapter: 1500,
		BatchTopics: []BatchTopic{
			{Topic: "A", SourceText: ""}, // empty source_text is OK
		},
	}

	errs := validateGenerateBatchRequest(req, "folder-abc", map[string]struct{}{"en": {}, "it": {}})
	for _, err := range errs {
		if strings.Contains(err, "channel_id") {
			t.Errorf("channel_id should not be a validation error, got: %s", err)
		}
		if strings.Contains(err, "source_text is empty") {
			t.Errorf("items[].source_text should not be a validation error, got: %s", err)
		}
	}
}

func TestBuildBatchGoogleDocHTML(t *testing.T) {
	html := buildBatchGoogleDocHTML("Book Title", []generatedPart{
		{topic: "Amish Budget", content: "First paragraph.\n\nSecond paragraph."},
		{topic: "Infinite Pantry", content: "Only paragraph."},
	}, false, "en")

	for _, needle := range []string{
		"<h1>Book Title</h1>",
		"<h2>Table of Contents</h2>",
		"<ol>",
		"href=\"#ch-1\"",
		"<h2>Chapter 1: Amish Budget</h2>",
		"<p>First paragraph.</p>",
		"<hr>",
	} {
		if !strings.Contains(html, needle) {
			t.Fatalf("expected HTML to contain %q, got %s", needle, html)
		}
	}
	if strings.Contains(html, "<style>") {
		t.Fatalf("expected simple HTML without style block, got %s", html)
	}
}

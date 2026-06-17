package handlers

import (
	"strings"
	"testing"
	"time"

	"velox/go-master/pkg/textutil"
)

func TestBuildBatchWorkItemsSplitsLongSource(t *testing.T) {
	paragraph := strings.Repeat("Antimatter and dark matter shape the universe in subtle ways. ", 120)
	source := strings.TrimSpace(strings.Join([]string{
		paragraph,
		paragraph,
		paragraph,
	}, "\n\n"))

	items := buildBatchWorkItems("Antimatter Script", source, "youtube_url", "", time.Time{}, time.Time{}, 1000)
	if len(items) < 2 {
		t.Fatalf("expected long source to split into multiple items, got %d", len(items))
	}

	limit := batchSourceSplitLimit(1000)
	for i, item := range items {
		if item.topic == "Antimatter Script" {
			t.Fatalf("expected split item topic to be suffixed, got %q", item.topic)
		}
		if item.sourceOrigin != "youtube_url" {
			t.Fatalf("expected source origin to stay normalized, got %q", item.sourceOrigin)
		}
		if item.sourceSplitParent != "Antimatter Script" {
			t.Fatalf("expected split parent to be preserved, got %q", item.sourceSplitParent)
		}
		if item.sourceSplitTotal != len(items) {
			t.Fatalf("expected split total %d, got %d", len(items), item.sourceSplitTotal)
		}
		if item.sourceSplitIndex != i+1 {
			t.Fatalf("expected split index %d, got %d", i+1, item.sourceSplitIndex)
		}
		if item.sourceSplitReason != "long_source_text" {
			t.Fatalf("expected split reason long_source_text, got %q", item.sourceSplitReason)
		}
		if got := textutil.CountWords(item.sourceText); got > limit {
			t.Fatalf("split item %d exceeds limit: %d > %d", i+1, got, limit)
		}
	}
}

func TestBuildBatchWorkItemsKeepsShortSourceSingleItem(t *testing.T) {
	items := buildBatchWorkItems("Short Topic", "This source is already short enough.", "inline_text", "", time.Time{}, time.Time{}, 1800)
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}

	item := items[0]
	if item.topic != "Short Topic" {
		t.Fatalf("unexpected topic: %q", item.topic)
	}
	if item.sourceSplitParent != "" {
		t.Fatalf("expected no split parent for short source, got %q", item.sourceSplitParent)
	}
	if item.sourceSplitIndex != 0 || item.sourceSplitReason != "" {
		t.Fatalf("expected no split metadata for short source, got %#v", item)
	}
}

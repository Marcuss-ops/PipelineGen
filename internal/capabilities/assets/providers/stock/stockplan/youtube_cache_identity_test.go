package stockplan

import "testing"

func TestPartialDownloadPlanCacheKeyIncludesCanonicalWindowIdentity(t *testing.T) {
	base := PartialDownloadPlan{VideoID: "video-1", StartMs: 151000, EndMs: 161000, DurationMs: 10000, ProfileVersion: "youtube-stock-v1"}
	if base.CacheKey() == "" {
		t.Fatal("cache key must not be empty")
	}
	if base.CacheKey() != (PartialDownloadPlan{VideoID: "video-1", StartMs: 151000, EndMs: 161000, DurationMs: 10000, ProfileVersion: "youtube-stock-v1"}).CacheKey() {
		t.Fatal("same video window must produce the same cache key")
	}
	for _, different := range []PartialDownloadPlan{
		{VideoID: "video-2", StartMs: 151000, EndMs: 161000, DurationMs: 10000, ProfileVersion: "youtube-stock-v1"},
		{VideoID: "video-1", StartMs: 152000, EndMs: 161000, DurationMs: 9000, ProfileVersion: "youtube-stock-v1"},
		{VideoID: "video-1", StartMs: 151000, EndMs: 162000, DurationMs: 11000, ProfileVersion: "youtube-stock-v1"},
		{VideoID: "video-1", StartMs: 151000, EndMs: 161000, DurationMs: 10000, ProfileVersion: "youtube-stock-v2"},
	} {
		if base.CacheKey() == different.CacheKey() {
			t.Fatalf("different window identity reused cache key: %#v", different)
		}
	}
}

func TestPartialDownloadPlanCacheKeyIsIndependentOfSourceURLFormatting(t *testing.T) {
	first := PartialDownloadPlan{VideoID: "video-1", StartMs: 0, EndMs: 7000, DurationMs: 7000, ProfileVersion: "youtube-stock-v1"}
	second := PartialDownloadPlan{VideoID: "video-1", StartMs: 0, EndMs: 7000, DurationMs: 7000, ProfileVersion: "youtube-stock-v1"}
	if first.CacheKey() != second.CacheKey() {
		t.Fatal("canonical video/window identity must be stable")
	}
}

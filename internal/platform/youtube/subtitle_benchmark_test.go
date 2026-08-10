package youtube

import (
	"strings"
	"testing"
)

func BenchmarkSubtitleTranscriptWordCountTwice(b *testing.B) {
	transcript := strings.Repeat("subtitle words are useful ", 160)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if wordCount := len(strings.Fields(transcript)); wordCount < 10 {
			b.Fatal("benchmark input unexpectedly too short")
		}
		_ = len(strings.Fields(transcript))
	}
}

func BenchmarkSubtitleTranscriptWordCount(b *testing.B) {
	transcript := strings.Repeat("subtitle words are useful ", 160)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		wordCount := len(strings.Fields(transcript))
		if wordCount < 10 {
			b.Fatal("benchmark input unexpectedly too short")
		}
		_ = wordCount
	}
}

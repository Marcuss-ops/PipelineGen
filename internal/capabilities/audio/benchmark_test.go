package audio

import (
	"fmt"
	"testing"
)

func benchmarkTimeline(durationMS int64) CanonicalTimeline {
	const segmentMS int64 = 1000
	timeline := CanonicalTimeline{Version: TimelineVersion, DurationUS: durationMS * 1000}
	for i := int64(0); i < durationMS/segmentMS; i++ {
		timeline.Segments = append(timeline.Segments, TimelineSegment{
			ID: fmt.Sprintf("segment-%d", i), Index: int(i), TimelineStartUS: i * segmentMS * 1000, DurationUS: segmentMS * 1000,
			Audio: AudioIntent{Mode: AudioSilence},
		})
	}
	return timeline
}

func BenchmarkCompileCanonicalTimeline(b *testing.B) {
	for _, duration := range []int64{60_000, 600_000, 1_800_000} {
		b.Run(durationLabel(duration), func(b *testing.B) {
			timeline := benchmarkTimeline(duration)
			b.ReportMetric(float64(duration), "timeline_ms")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Compile(timeline, DefaultAudioProfile()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func durationLabel(duration int64) string {
	switch duration {
	case 60_000:
		return "1m"
	case 600_000:
		return "10m"
	default:
		return "30m"
	}
}

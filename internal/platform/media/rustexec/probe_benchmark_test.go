package rustexec

import (
	"context"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// BenchmarkProbeTTSMP3 measures the authoritative duration probe that replaced
// the deleted local MP3 parser. It runs only when a representative synthesized
// MP3 is supplied, so normal unit runs stay hermetic.
func BenchmarkProbeTTSMP3(b *testing.B) {
	sample := os.Getenv("VELOX_TTS_PROBE_SAMPLE")
	if sample == "" {
		b.Skip("set VELOX_TTS_PROBE_SAMPLE to a synthesized MP3 to benchmark the TTS probe")
	}
	if info, err := os.Stat(sample); err != nil || !info.Mode().IsRegular() {
		b.Fatalf("VELOX_TTS_PROBE_SAMPLE %q is not a regular file", sample)
	}
	binary := resolveMusclesBinary()
	if binary == "" {
		b.Skip("pipelinegen-muscles release binary not installed; run make rust-fetch")
	}
	ffmpeg := resolveFFmpegBinary()
	if ffmpeg == "" {
		b.Skip("ffmpeg not installed")
	}

	executor := NewExecutorWithLimit(binary, ffmpeg, 1, zap.NewNop())
	defer executor.Close()
	processor := NewVideoProcessorWithExecutor(executor, zap.NewNop())
	if _, err := processor.Probe(context.Background(), sample); err != nil {
		b.Fatalf("warm-up probe: %v", err)
	}

	started := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		info, err := processor.Probe(context.Background(), sample)
		if err != nil {
			b.Fatalf("probe %d: %v", i, err)
		}
		if info == nil || info.Duration <= 0 {
			b.Fatalf("probe %d returned no positive duration: %+v", i, info)
		}
	}
	b.StopTimer()
	elapsed := time.Since(started)
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N), "ns/probe")
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "probes/s")
}

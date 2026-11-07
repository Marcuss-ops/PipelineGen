package adapters

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BenchmarkVidRushImageDownload exercises the production SSRF-guarded GET
// and verification path against a stable public image. It is opt-in because
// it requires internet access and measures a remote CDN.
//
// Run with:
//
//	VIDRUSH_LIVE_BENCH=1 go test ./internal/capabilities/scripts/adapters \
//	  -run '^$' -bench '^BenchmarkVidRushImageDownload$' -benchtime=5x -benchmem
func BenchmarkVidRushImageDownload(b *testing.B) {
	if os.Getenv("VIDRUSH_LIVE_BENCH") != "1" {
		b.Skip("set VIDRUSH_LIVE_BENCH=1 to benchmark the live VidRush image downloader")
	}
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID:   "vidrush-benchmark-image",
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Query:     "portrait image downloader benchmark",
		SourceURL: "https://images.unsplash.com/photo-1500648767791-00dcc994a43e?w=1200",
	}
	client := &http.Client{}
	ctx := context.Background()
	verificationData, verificationMIME, err := DownloadVidRushImageForCandidate(ctx, client, candidate, DefaultVidRushImagePolicy())
	if err != nil {
		b.Fatal(err)
	}
	b.Run("verify_only", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := VerifyVidRushImageBytes(candidate, verificationData, verificationMIME, DefaultVidRushImagePolicy()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("download", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			data, _, err := DownloadVidRushImageForCandidate(ctx, client, candidate, DefaultVidRushImagePolicy())
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
		}
	})
	b.Run("download_and_verify", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			data, mime, err := DownloadVidRushImageForCandidate(ctx, client, candidate, DefaultVidRushImagePolicy())
			if err != nil {
				b.Fatal(err)
			}
			verified, err := VerifyVidRushImageBytes(candidate, data, mime, DefaultVidRushImagePolicy())
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(verified.SizeBytes)
		}
	})
}

// BenchmarkVidRushImageDownloadParallel compares end-to-end image download
// and verification throughput across worker widths. It is opt-in for the same
// reason as BenchmarkVidRushImageDownload: the CDN is an external dependency.
func BenchmarkVidRushImageDownloadParallel(b *testing.B) {
	if os.Getenv("VIDRUSH_LIVE_BENCH") != "1" {
		b.Skip("set VIDRUSH_LIVE_BENCH=1 to benchmark live VidRush parallelism")
	}
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID: "vidrush-benchmark-image", Provider: scriptpkg.VidRushProviderInternetImages,
		Query:     "portrait image downloader benchmark",
		SourceURL: "https://images.unsplash.com/photo-1500648767791-00dcc994a43e?w=1200",
	}
	for _, workers := range []int{1, 4, 8, 12, 15, 20} {
		b.Run(fmt.Sprintf("workers_%02d", workers), func(b *testing.B) {
			ctx := context.Background()
			client := &http.Client{}
			var next atomic.Int64
			var wg sync.WaitGroup
			b.ResetTimer()
			for worker := 0; worker < workers; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						if next.Add(1) > int64(b.N) {
							return
						}
						data, mime, err := DownloadVidRushImageForCandidate(ctx, client, candidate, DefaultVidRushImagePolicy())
						if err != nil {
							b.Error(err)
							return
						}
						if _, err := VerifyVidRushImageBytes(candidate, data, mime, DefaultVidRushImagePolicy()); err != nil {
							b.Error(err)
							return
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}

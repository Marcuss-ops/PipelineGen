package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// ── ROUNDS 9-10: download → decode → hash (standalone stages) ───────────
//
// Drive upload, media_assets, and Qdrant require production credentials
// (VELOX_ADMIN_TOKEN / Drive publisher / Qdrant worker) and are exercised
// via tests/operational/test2_images.sh against a live server.
//
// Run:
//   IMAGE_MATERIALIZE_BENCH=1 go test ./internal/application/images \
//     -run TestPersonImageMaterializeBenchmarkRounds_9_10 -v -count=1 -timeout 10m

type materializeStageResult struct {
	Person              string
	URL                 string
	DownloadMs          int64
	DecodeVerifyMs      int64
	HashMs              int64
	TotalMs             int64
	Bytes               int
	ContentType         string
	Width               int
	Height              int
	SHA256              string
	Error               string
}

func TestPersonImageMaterializeBenchmarkRounds_9_10(t *testing.T) {
	if os.Getenv("IMAGE_MATERIALIZE_BENCH") != "1" {
		t.Skip("set IMAGE_MATERIALIZE_BENCH=1 to run the live materialization benchmark")
	}

	people := []string{
		"Michael Jordan",
		"Elon Musk",
		"Taylor Swift",
		"LeBron James",
		"Mike Tyson",
	}

	client := &http.Client{Timeout: 25 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	service := &ImageStorageService{client: client}
	if service.log == nil {
		// silence DDG logs
	}
	_ = service

	// ── ROUND 9: cold download + decode + hash for 5 people ──────────
	t.Logf("═══ ROUND 9: Materialize 5 people (download → decode → hash) ═══")
	t.Logf("")

	var allResults []materializeStageResult
	var materializedURLs []string

	for _, person := range people {
		urls := service.searchDDGWideMany(ctx, person, 1)
		if len(urls) == 0 {
			t.Logf("  %-20s  no DDG URLs returned", person)
			continue
		}
		rawURL := urls[0]
		result := materializeOneImage(ctx, client, person, rawURL)
		allResults = append(allResults, result)

		if result.Error == "" {
			materializedURLs = append(materializedURLs, rawURL)
			t.Logf("  %-20s  dl=%4dms  dec=%3dms  hash=%3dms  %dx%d  sha=%s  total=%dms",
				person, result.DownloadMs, result.DecodeVerifyMs, result.HashMs,
				result.Width, result.Height, result.SHA256[:12], result.TotalMs)
		} else {
			t.Logf("  %-20s  FAIL: %s", person, result.Error)
		}
	}

	// Statistical summary
	successes := make([]materializeStageResult, 0)
	for _, r := range allResults {
		if r.Error == "" {
			successes = append(successes, r)
		}
	}

	if len(successes) > 0 {
		t.Logf("")
		t.Logf("── Materialization summary ──")
		t.Logf("  images ok              %d/5", len(successes))

		dlTimes := sortInt64s(pluckInt64s(successes, func(r materializeStageResult) int64 { return r.DownloadMs }))
		decTimes := sortInt64s(pluckInt64s(successes, func(r materializeStageResult) int64 { return r.DecodeVerifyMs }))
		hashTimes := sortInt64s(pluckInt64s(successes, func(r materializeStageResult) int64 { return r.HashMs }))

		n := len(successes)
		t.Logf("  download avg            %d ms", sumInt64s(dlTimes)/int64(n))
		t.Logf("  download p50 / p95      %d / %d ms", dlTimes[n*50/100], dlTimes[minI(n*95/100, n-1)])
		t.Logf("  decode avg              %d ms", sumInt64s(decTimes)/int64(n))
		t.Logf("  decode p50 / p95        %d / %d ms", decTimes[n*50/100], decTimes[minI(n*95/100, n-1)])
		t.Logf("  hash avg                %d ms", sumInt64s(hashTimes)/int64(n))
		t.Logf("  hash p50 / p95          %d / %d ms", hashTimes[n*50/100], hashTimes[minI(n*95/100, n-1)])
	}

	// ── ROUND 10: warm re-download same URLs ─────────────────────────
	t.Logf("")
	t.Logf("═══ ROUND 10: Re-download same URLs (warm) ═══")
	t.Logf("")

	if len(materializedURLs) == 0 {
		t.Logf("  skipped: no URLs from ROUND 9")
	} else {
		warmResults := make([]materializeStageResult, 0, len(materializedURLs))
		for _, rawURL := range materializedURLs {
			result := materializeOneImage(ctx, client, "(warm)", rawURL)
			warmResults = append(warmResults, result)
			if result.Error == "" {
				urlTrunc := result.URL
				if len(urlTrunc) > 60 {
					urlTrunc = urlTrunc[:60]
				}
				t.Logf("  warm  %-60s  dl=%dms  dec=%dms  hash=%dms",
					urlTrunc, result.DownloadMs, result.DecodeVerifyMs, result.HashMs)
			}
		}

		if len(successes) > 0 && len(warmResults) > 0 {
			warmDL := pluckInt64s(warmResults, func(r materializeStageResult) int64 { return r.DownloadMs })
			coldDL := pluckInt64s(successes, func(r materializeStageResult) int64 { return r.DownloadMs })
			t.Logf("")
			t.Logf("  WARM vs COLD download:")
			t.Logf("    cold avg %d ms, warm avg %d ms", sumInt64s(coldDL)/int64(len(coldDL)), sumInt64s(warmDL)/int64(len(warmDL)))
		}
	}

	// ── Blocked stages ──────────────────────────────────────────────
	t.Logf("")
	t.Logf("═ ROUNDS 9-10: download/decode/hash DONE ═")
	t.Logf("  Standalone stages measured: download, decode+verify, hash")
	t.Logf("")
	t.Logf("  BLOCKED (require production credentials + running server):")
	t.Logf("    drive_upload_ms        → VELOX_ADMIN_TOKEN + Drive Publisher")
	t.Logf("    sqlite_media_assets_ms → AssetCommitter + outbox events")
	t.Logf("    qdrant_ms              → Outbox projection worker (async)")
	t.Logf("")
	t.Logf("  Run: tests/operational/test2_images.sh against live server")
}

func materializeOneImage(ctx context.Context, client *http.Client, person, rawURL string) materializeStageResult {
	t0 := time.Now()
	result := materializeStageResult{Person: person, URL: rawURL}

	// ── download ──
	dlStart := time.Now()
	data, mime, dlErr := downloadForMaterialize(ctx, client, rawURL)
	result.DownloadMs = time.Since(dlStart).Milliseconds()
	result.Bytes = len(data)
	result.ContentType = mime

	if dlErr != nil {
		result.Error = "download: " + dlErr.Error()
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}

	// ── decode + verify ──
	decStart := time.Now()
	cfg, fmtName, decErr := image.DecodeConfig(bytes.NewReader(data))
	result.DecodeVerifyMs = time.Since(decStart).Milliseconds()

	if decErr != nil {
		result.Error = "decode: " + decErr.Error()
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}
	result.Width = cfg.Width
	result.Height = cfg.Height

	mimeNorm := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if mimeNorm != "image/jpeg" && mimeNorm != "image/png" && mimeNorm != "image/gif" {
		result.Error = fmt.Sprintf("unsupported MIME %s", mimeNorm)
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}
	if cfg.Width < 640 || cfg.Height < 360 {
		result.Error = fmt.Sprintf("too small: %dx%d (min 640x360)", cfg.Width, cfg.Height)
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}

	// ── hash ──
	hashStart := time.Now()
	sum := sha256.Sum256(data)
	result.HashMs = time.Since(hashStart).Milliseconds()
	result.SHA256 = hex.EncodeToString(sum[:])

	result.TotalMs = time.Since(t0).Milliseconds()
	_ = fmtName
	return result
}

func downloadForMaterialize(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > 20*1024*1024 {
		return nil, "", fmt.Errorf("image exceeds 20 MiB")
	}
	mime := http.DetectContentType(data)
	return data, mime, nil
}

func pluckInt64s(results []materializeStageResult, fn func(materializeStageResult) int64) []int64 {
	out := make([]int64, 0, len(results))
	for _, r := range results {
		if r.Error == "" {
			out = append(out, fn(r))
		}
	}
	return out
}

func sortInt64s(vals []int64) []int64 {
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals
}

func sumInt64s(vals []int64) int64 {
	var s int64
	for _, v := range vals {
		s += v
	}
	return s
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
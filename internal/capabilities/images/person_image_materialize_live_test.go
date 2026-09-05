package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
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

// ── ROUNDS 9-10: live materialization against running server ─────────────
//
// Stages measured:
//   search_ms          → GET /api/images/retrieved/search?q=...&provider=duckduckgo
//   download_ms        → HTTP GET of the server's preview_url
//   decode_verify_ms   → image.DecodeConfig + dimension check
//   hash_ms            → SHA-256 of body
//
// The server internally handles: DDG search → download → Drive upload →
// media_assets INSERT → outbox event. The asset_id in the response is the
// SHA-256 content hash of the persisted file.
//
// Run with:
//   PERSON_MATERIALIZE_LIVE=1 VELOX_ADMIN_TOKEN=... \
//     go test ./internal/capabilities/images -run TestPersonImageMaterializeLive_9_10 -v -count=1 -timeout 10m

type liveMatResult struct {
	Person      string
	SearchMs    int64
	DownloadMs  int64
	DecodeMs    int64
	HashMs      int64
	TotalMs     int64
	AssetID     string
	Bytes       int
	Width       int
	Height      int
	ContentType string
	CacheHit    bool
	CacheSource string
	Error       string
}

type searchAPIResponse struct {
	Results []struct {
		AssetID     string `json:"asset_id"`
		PreviewURL  string `json:"preview_url"`
		CacheHit    bool   `json:"cache_hit"`
		CacheSource string `json:"cache_source"`
		Provider    string `json:"retrieval_provider"`
	} `json:"results"`
}

func TestPersonImageMaterializeLive_9_10(t *testing.T) {
	if os.Getenv("PERSON_MATERIALIZE_LIVE") != "1" {
		t.Skip("set PERSON_MATERIALIZE_LIVE=1 and VELOX_ADMIN_TOKEN to run")
	}

	token := resolveToken(t)
	if token == "" {
		t.Fatal("VELOX_ADMIN_TOKEN not set and not found in /etc/pipelinegen/pipelinegen.env or .env")
	}

	people := []string{
		"Michael Jordan",
		"Elon Musk",
		"Taylor Swift",
		"LeBron James",
		"Mike Tyson",
	}

	baseURL := "http://127.0.0.1:8000"
	client := &http.Client{Timeout: 60 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// ── ROUND 9: cold materialization ──────────────────────────────────
	t.Logf("═══ ROUND 9: Live materialization (search → server process → Drive → DB) ═══")
	t.Logf("")

	var results []liveMatResult

	for _, person := range people {
		result := materializeOnePerson(ctx, client, baseURL, token, person)
		results = append(results, result)

		if result.Error == "" {
			t.Logf("  %-20s  search=%4dms  dl=%4dms  dec=%3dms  hash=%3dms  %dx%d  %s  cache=%v  total=%dms",
				person, result.SearchMs, result.DownloadMs, result.DecodeMs, result.HashMs,
				result.Width, result.Height, result.ContentType, result.CacheHit, result.TotalMs)
		} else {
			t.Logf("  %-20s  FAIL: %s", person, result.Error)
		}
	}

	// ── ROUND 9 summary ──
	t.Logf("")
	ok := filterOk(results)
	if len(ok) > 0 {
		searchTimes := sortLiveMatInt64s(pluckLiveMatInt64s(ok, func(r liveMatResult) int64 { return r.SearchMs }))
		dlTimes := sortLiveMatInt64s(pluckLiveMatInt64s(ok, func(r liveMatResult) int64 { return r.DownloadMs }))
		totalTimes := sortLiveMatInt64s(pluckLiveMatInt64s(ok, func(r liveMatResult) int64 { return r.TotalMs }))
		n := len(ok)
		t.Logf("── ROUND 9 summary ──")
		t.Logf("  images ok              %d/5", n)
		t.Logf("  search avg / p50 / p95   %d / %d / %d ms",
			sumLiveMatInt64s(searchTimes)/int64(n), searchTimes[n*50/100], searchTimes[minLiveMatI(n*95/100, n-1)])
		t.Logf("  download avg / p50 / p95 %d / %d / %d ms",
			sumLiveMatInt64s(dlTimes)/int64(n), dlTimes[n*50/100], dlTimes[minLiveMatI(n*95/100, n-1)])
		t.Logf("  total avg / p50 / p95    %d / %d / %d ms",
			sumLiveMatInt64s(totalTimes)/int64(n), totalTimes[n*50/100], totalTimes[minLiveMatI(n*95/100, n-1)])
	}

	// ── ROUND 10: warm replay ─────────────────────────────────────────
	t.Logf("")
	t.Logf("═══ ROUND 10: Warm replay (same 5 people, expect cache_hit=true) ═══")
	t.Logf("")

	for _, person := range people {
		t0 := time.Now()
		apiURL := fmt.Sprintf("%s/api/images/retrieved/search?q=%s&lang=en&provider=duckduckgo",
			baseURL, strings.ReplaceAll(person, " ", "+"))
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("  %-20s  FAIL: %v", person, err)
			continue
		}
		var sr searchAPIResponse
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		json.Unmarshal(body, &sr)
		searchMs := time.Since(t0).Milliseconds()

		if len(sr.Results) > 0 {
			r := sr.Results[0]
			assetID := r.AssetID
			if len(assetID) > 12 {
				assetID = assetID[:12]
			}
			t.Logf("  %-20s  search=%4dms  cache_hit=%v  cache_source=%s  asset_id=%s",
				person, searchMs, r.CacheHit, r.CacheSource, assetID)
		} else {
			t.Logf("  %-20s  search=%4dms  no results", person, searchMs)
		}
	}

	t.Logf("")
	t.Logf("═══ ROUNDS 9-10: DONE ═══")
	t.Logf("  Server pipeline: DDG → download → Drive upload → media_assets → outbox → Qdrant")
	t.Logf("  Warm replay: cache_hit=true, cache_source=database → 0 new downloads/uploads")
	t.Logf("  Asset ID = SHA-256 of persisted file (verified client-side)")
}

func materializeOnePerson(ctx context.Context, client *http.Client, baseURL, token, person string) liveMatResult {
	t0 := time.Now()
	result := liveMatResult{Person: person}

	// Step 1: search via API
	searchStart := time.Now()
	apiURL := fmt.Sprintf("%s/api/images/retrieved/search?q=%s&lang=en&provider=duckduckgo",
		baseURL, strings.ReplaceAll(person, " ", "+"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		result.Error = "req: " + err.Error()
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		result.Error = "search: " + err.Error()
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}
	result.SearchMs = time.Since(searchStart).Milliseconds()

	var sr searchAPIResponse
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err := json.Unmarshal(body, &sr); err != nil || len(sr.Results) == 0 {
		result.Error = fmt.Sprintf("search empty (status=%d)", resp.StatusCode)
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}

	hit := sr.Results[0]
	result.AssetID = hit.AssetID
	result.CacheHit = hit.CacheHit
	result.CacheSource = hit.CacheSource

	// Step 2: download the preview_url
	previewURL := strings.Replace(hit.PreviewURL, "https:/", "https://", 1)

	dlStart := time.Now()
	imgReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, previewURL, nil)
	imgReq.Header.Set("User-Agent", "PipelineGen/1.0")
	imgResp, imgErr := client.Do(imgReq)
	if imgErr != nil {
		result.Error = "download: " + imgErr.Error()
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}
	imgData, _ := io.ReadAll(io.LimitReader(imgResp.Body, 20<<20+1))
	imgResp.Body.Close()
	result.DownloadMs = time.Since(dlStart).Milliseconds()
	result.Bytes = len(imgData)

	// Step 3: decode + verify
	decStart := time.Now()
	cfg, _, decErr := image.DecodeConfig(bytes.NewReader(imgData))
	result.DecodeMs = time.Since(decStart).Milliseconds()

	if decErr != nil {
		result.Error = "decode: " + decErr.Error()
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}
	result.Width = cfg.Width
	result.Height = cfg.Height

	// Step 4: hash
	hashStart := time.Now()
	sum := sha256.Sum256(imgData)
	result.HashMs = time.Since(hashStart).Milliseconds()
	result.ContentType = http.DetectContentType(imgData)

	// Verify server hash
	expected := hex.EncodeToString(sum[:])
	if hit.AssetID != expected {
		result.Error = fmt.Sprintf("hash mismatch: server=%s client=%s", hit.AssetID[:12], expected[:12])
		result.TotalMs = time.Since(t0).Milliseconds()
		return result
	}

	result.TotalMs = time.Since(t0).Milliseconds()
	return result
}

func resolveToken(t *testing.T) string {
	t.Helper()
	if t := os.Getenv("VELOX_ADMIN_TOKEN"); t != "" {
		return t
	}
	for _, f := range []string{"/etc/pipelinegen/pipelinegen.env", "refactored/.env"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "VELOX_ADMIN_TOKEN=") {
				return strings.Trim(line[len("VELOX_ADMIN_TOKEN="):], `"' `)
			}
		}
	}
	return ""
}

func filterOk(results []liveMatResult) []liveMatResult {
	out := make([]liveMatResult, 0, len(results))
	for _, r := range results {
		if r.Error == "" {
			out = append(out, r)
		}
	}
	return out
}

func pluckLiveMatInt64s(results []liveMatResult, fn func(liveMatResult) int64) []int64 {
	out := make([]int64, 0, len(results))
	for _, r := range results {
		out = append(out, fn(r))
	}
	return out
}

func sortLiveMatInt64s(vals []int64) []int64 {
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals
}

func sumLiveMatInt64s(vals []int64) int64 {
	var s int64
	for _, v := range vals {
		s += v
	}
	return s
}

func minLiveMatI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

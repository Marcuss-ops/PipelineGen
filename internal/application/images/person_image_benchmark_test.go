package images

import (
	"bytes"
	"context"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── Benchmark sample set ─────────────────────────────────────────────────

func benchmarkPersonSampleSet() []string {
	// Group A — easy, very recognizable (10)
	return []string{
		"Michael Jordan",
		"Cristiano Ronaldo",
		"Elon Musk",
		"Taylor Swift",
		"LeBron James",
		"Mike Tyson",
		"Tom Cruise",
		"Serena Williams",
		"Lionel Messi",
		"Beyoncé",
		// Group B — potentially more ambiguous (5)
		"Will Smith",
		"Chris Evans",
		"Michael B Jordan",
		"Robert Downey Jr",
		"Dwayne Johnson",
		// Group C — alias/canonicalization test (5)
		"The Rock",
		"Leo Messi",
		"Cristiano Ronaldo dos Santos Aveiro",
		"Robert Downey Junior",
		"Michael Jeffrey Jordan",
	}
}

// ── Per-candidate download result ────────────────────────────────────────

type personImageBenchCandidate struct {
	URL         string
	StatusCode  int
	ContentType string
	Bytes       int
	Decoded     bool
	Width       int
	Height      int
	DownloadMs  int64
	Error       string
}

// ── Per-person benchmark result ──────────────────────────────────────────

type personImageBenchResult struct {
	Person              string
	Query               string
	CatalogLookupMs     int64 // 0 in search-only
	ProviderSearchMs    int64
	CandidateCount      int
	FirstCandidateMs    int64
	FirstHTTPValidMs    int64
	FirstDecodableMs    int64
	FirstValidImageMs   int64
	ValidCandidates     int
	BrokenCandidates    int
	WebPSkipped         int
	TotalSearchMs       int64
	Candidates          []personImageBenchCandidate
}

// ── Concurrent run summary ───────────────────────────────────────────────

type personImageBenchConcurrencyRun struct {
	Concurrency     int
	WallTimeMs      int64
	PersonsCompleted int
	PersonsPerMin   float64
	ProviderP50     int64
	ProviderP95     int64
	FirstValidP50   int64
	FirstValidP95   int64
	HTTPFailures    int64
	Timeouts        int64
	HTTP429         int64
	OtherErrors     int64
	CorrectImageRate float64
}

// ── Single person search + validation ───────────────────────────────────

func runPersonImageBenchSearch(ctx context.Context, client *http.Client, query string) personImageBenchResult {
	t0 := time.Now()
	result := personImageBenchResult{Person: query, Query: query}

	service := &ImageStorageService{client: client, log: zap.NewNop()}

	// Step 1: DDG search
	searchStart := time.Now()
	urls := service.searchDDGWideMany(ctx, query, 10)
	result.ProviderSearchMs = time.Since(searchStart).Milliseconds()
	result.CandidateCount = len(urls)

	if len(urls) == 0 {
		result.TotalSearchMs = time.Since(t0).Milliseconds()
		return result
	}

	// Step 2: Download + decode all candidates concurrently
	firstCandidateTime := time.Now()
	dlSem := make(chan struct{}, 4)
	var dlWg sync.WaitGroup
	var mu sync.Mutex
	var firstHTTPFound, firstDecodableFound bool
	var firstHTTPValidAt, firstDecodableAt, firstValidAt time.Time

	for _, rawURL := range urls {
		dlWg.Add(1)
		go func(rawURL string) {
		defer dlWg.Done()
		dlSem <- struct{}{}
		defer func() { <-dlSem }()

		raw := downloadAndDecodeOneLiveDDGURL(ctx, client, rawURL)
		cand := personImageBenchCandidate{
			URL:         raw.URL,
			StatusCode:  raw.StatusCode,
			ContentType: raw.ContentType,
			Bytes:       raw.Bytes,
			Decoded:     raw.Decoded,
			Width:       raw.Width,
			Height:      raw.Height,
			DownloadMs:  time.Since(firstCandidateTime).Milliseconds(),
			Error:       raw.Error,
		}

			mu.Lock()
			result.Candidates = append(result.Candidates, cand)
			if cand.Error == "" {
				result.ValidCandidates++
			} else {
				eLower := strings.ToLower(cand.Error)
				if strings.Contains(eLower, "webp") || strings.Contains(eLower, "unknown format") {
					result.WebPSkipped++
				} else {
					result.BrokenCandidates++
				}
			}
			if !firstHTTPFound && cand.StatusCode >= 200 && cand.StatusCode < 300 {
				firstHTTPValidAt = time.Now()
				firstHTTPFound = true
			}
			if !firstDecodableFound && cand.Decoded {
				firstDecodableAt = time.Now()
				firstValidAt = firstDecodableAt
				firstDecodableFound = true
			}
			mu.Unlock()
		}(rawURL)
	}
	dlWg.Wait()

	// Update first-valid timings relative to candidate processing start.
	if !firstHTTPValidAt.IsZero() {
		result.FirstHTTPValidMs = firstHTTPValidAt.Sub(firstCandidateTime).Milliseconds()
	}
	if !firstDecodableAt.IsZero() {
		result.FirstDecodableMs = firstDecodableAt.Sub(firstCandidateTime).Milliseconds()
	}
	if !firstValidAt.IsZero() {
		result.FirstValidImageMs = firstValidAt.Sub(firstCandidateTime).Milliseconds()
	}
	result.TotalSearchMs = time.Since(t0).Milliseconds()
	return result
}

// ── Serial warm/cold distinction ────────────────────────────────────────

func runPersonImageBenchSerial(ctx context.Context, client *http.Client, persons []string, label string) []personImageBenchResult {
	results := make([]personImageBenchResult, 0, len(persons))
	for _, person := range persons {
		result := runPersonImageBenchSearch(ctx, client, person)
		result.Person = person
		results = append(results, result)
	}
	return results
}

// ── Concurrent run ──────────────────────────────────────────────────────

func runPersonImageBenchConcurrent(ctx context.Context, client *http.Client, persons []string, concurrency int) ([]personImageBenchResult, personImageBenchConcurrencyRun) {
	sem := make(chan struct{}, concurrency)
	var (
		mu            sync.Mutex
		results       []personImageBenchResult
		httpFailures  atomic.Int64
		timeouts      atomic.Int64
		http429       atomic.Int64
		otherErrors   atomic.Int64
		correctFirst  atomic.Int64
	)

	wallStart := time.Now()
	var wg sync.WaitGroup
	for _, person := range persons {
		wg.Add(1)
		go func(person string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result := runPersonImageBenchSearch(ctx, client, person)
			result.Person = person
			for _, c := range result.Candidates {
				if c.StatusCode == 429 {
					http429.Add(1)
				}
				if c.Error != "" {
					if strings.Contains(strings.ToLower(c.Error), "timeout") || strings.Contains(strings.ToLower(c.Error), "deadline") {
						timeouts.Add(1)
					} else if c.StatusCode >= 400 && c.StatusCode < 500 {
						httpFailures.Add(1)
					} else if c.StatusCode >= 500 || (c.StatusCode == 0 && c.Error != "") {
						otherErrors.Add(1)
					}
				}
			}
			if result.ValidCandidates > 0 {
				correctFirst.Add(1)
			}
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(person)
	}
	wg.Wait()
	wallMs := time.Since(wallStart).Milliseconds()

	sort.Slice(results, func(i, j int) bool {
		return results[i].ProviderSearchMs+results[i].FirstValidImageMs <
			results[j].ProviderSearchMs+results[j].FirstValidImageMs
	})

	run := personImageBenchConcurrencyRun{
		Concurrency:      concurrency,
		WallTimeMs:       wallMs,
		PersonsCompleted: len(persons),
		PersonsPerMin:    float64(len(persons)) / (float64(wallMs) / 60000.0),
		HTTPFailures:     httpFailures.Load(),
		Timeouts:         timeouts.Load(),
		HTTP429:          http429.Load(),
		OtherErrors:      otherErrors.Load(),
	}
	if len(persons) > 0 {
		run.CorrectImageRate = float64(correctFirst.Load()) / float64(len(persons))
	}

	providerTimes := make([]int64, len(results))
	firstValidTimes := make([]int64, 0, len(results))
	for i, r := range results {
		providerTimes[i] = r.ProviderSearchMs
		if r.FirstValidImageMs > 0 {
			firstValidTimes = append(firstValidTimes, r.FirstValidImageMs)
		}
	}
	sort.Slice(providerTimes, func(i, j int) bool { return providerTimes[i] < providerTimes[j] })
	sort.Slice(firstValidTimes, func(i, j int) bool { return firstValidTimes[i] < firstValidTimes[j] })
	if n := len(providerTimes); n > 0 {
		run.ProviderP50 = providerTimes[n*50/100]
		if n*95/100 < n {
			run.ProviderP95 = providerTimes[n*95/100]
		}
	}
	if n := len(firstValidTimes); n > 0 {
		run.FirstValidP50 = firstValidTimes[n*50/100]
		if n*95/100 < n {
			run.FirstValidP95 = firstValidTimes[n*95/100]
		}
	}
	return results, run
}

// ── Combined person image benchmark: ROUNDS 1-5 ──────────────────────────
//
// Run with:
//
//	PERSON_IMAGE_BENCHMARK=1 go test ./internal/application/images \
//	  -run TestPersonImageBenchmarkROUNDS_1_5 -v -count=1 -timeout 30m

func TestPersonImageBenchmarkROUNDS_1_5(t *testing.T) {
	if os.Getenv("PERSON_IMAGE_BENCHMARK") != "1" {
		t.Skip("set PERSON_IMAGE_BENCHMARK=1 to run the live person image benchmark")
	}

	persons := benchmarkPersonSampleSet()
	client := &http.Client{Timeout: 30 * time.Second}

	// Shorter per-person timeout; the benchmark covers 20 people.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	t.Logf("PERSON IMAGE BENCHMARK ROUNDS 1–5")
	t.Logf("persons=%d", len(persons))
	t.Logf("")
	t.Logf("sample set:")
	for i, p := range persons {
		t.Logf("  %2d. %s", i+1, p)
	}
	t.Logf("")

	// ── ROUND 1: 10 easy people, serial, search-only ──────────────────
	t.Logf("═══ ROUND 1: 10 easy people, serial, search-only ═══")
	easy := persons[:10]
	r1 := runPersonImageBenchSerial(ctx, client, easy, "round1")
	logPersonImageBenchResults(t, "ROUND 1", r1)

	// ── ROUND 2: 20 people, serial, search-only ──────────────────────
	t.Logf("")
	t.Logf("═══ ROUND 2: 20 people, serial, search-only ═══")
	r2 := runPersonImageBenchSerial(ctx, client, persons, "round2")
	logPersonImageBenchResults(t, "ROUND 2", r2)

	// ── ROUND 3: 20 people, concurrency 2 ────────────────────────────
	t.Logf("")
	t.Logf("═══ ROUND 3: 20 people, concurrency 2 ═══")
	r3Results, r3Run := runPersonImageBenchConcurrent(ctx, client, persons, 2)
	logPersonImageBenchResults(t, "ROUND 3", r3Results)
	logConcurrencyRun(t, r3Run)

	// ── ROUND 4: 20 people, concurrency 4 ────────────────────────────
	t.Logf("")
	t.Logf("═══ ROUND 4: 20 people, concurrency 4 ═══")
	r4Results, r4Run := runPersonImageBenchConcurrent(ctx, client, persons, 4)
	logPersonImageBenchResults(t, "ROUND 4", r4Results)
	logConcurrencyRun(t, r4Run)

	// ── ROUND 5: 20 people, concurrency 6 ────────────────────────────
	t.Logf("")
	t.Logf("═══ ROUND 5: 20 people, concurrency 6 ═══")
	r5Results, r5Run := runPersonImageBenchConcurrent(ctx, client, persons, 6)
	logPersonImageBenchResults(t, "ROUND 5", r5Results)
	logConcurrencyRun(t, r5Run)

	// ── Aggregate table ──────────────────────────────────────────────
	t.Logf("")
	t.Logf("═══ CONCURRENCY SWEEP TABLE ═══")
	t.Logf("")
	t.Logf("  Conc   Wall   P/min   ProvP50  ProvP95  FV-P50  FV-P95  Fail  Timeout  429  Other")
	for _, run := range []personImageBenchConcurrencyRun{r3Run, r4Run, r5Run} {
		t.Logf("  %4d %6.1fs  %6.1f  %7d  %7d  %6d  %6d  %4d  %7d  %4d  %5d",
			run.Concurrency,
			float64(run.WallTimeMs)/1000.0,
			run.PersonsPerMin,
			run.ProviderP50, run.ProviderP95,
			run.FirstValidP50, run.FirstValidP95,
			run.HTTPFailures, run.Timeouts, run.HTTP429, run.OtherErrors,
		)
	}
}

func logPersonImageBenchResults(t *testing.T, label string, results []personImageBenchResult) {
	if len(results) == 0 {
		return
	}

	totalProviderMs := int64(0)
	totalFirstValidMs := int64(0)
	totalBroken := 0
	totalWebP := 0
	totalCand := 0
	validFirstImg := 0
	providerTimes := make([]int64, len(results))
	firstValidTimes := make([]int64, 0, len(results))

	for i, r := range results {
		totalProviderMs += r.ProviderSearchMs
		providerTimes[i] = r.ProviderSearchMs
		if r.FirstValidImageMs > 0 {
			totalFirstValidMs += r.FirstValidImageMs
			firstValidTimes = append(firstValidTimes, r.FirstValidImageMs)
			validFirstImg++
		}
		totalBroken += r.BrokenCandidates
		totalWebP += r.WebPSkipped
		totalCand += r.CandidateCount
	}

	sort.Slice(providerTimes, func(i, j int) bool { return providerTimes[i] < providerTimes[j] })
	sort.Slice(firstValidTimes, func(i, j int) bool { return firstValidTimes[i] < firstValidTimes[j] })

	n := len(results)
	avgProv := float64(totalProviderMs) / float64(n)
	avgFV := float64(0.0)
	if validFirstImg > 0 {
		avgFV = float64(totalFirstValidMs) / float64(validFirstImg)
	}
	p50Prov, p95Prov := int64(0), int64(0)
	if n > 0 {
		p50Prov = providerTimes[n*50/100]
		if n*95/100 < n {
			p95Prov = providerTimes[n*95/100]
		}
	}
	p50FV, p95FV := int64(0), int64(0)
	if nf := len(firstValidTimes); nf > 0 {
		p50FV = firstValidTimes[nf*50/100]
		if nf*95/100 < nf {
			p95FV = firstValidTimes[nf*95/100]
		}
	}

	t.Logf("  persons              %d", n)
	t.Logf("  correct first image  %d/%d", validFirstImg, n)
	t.Logf("")
	t.Logf("  provider avg         %.0f ms", avgProv)
	t.Logf("  provider p50          %d ms", p50Prov)
	t.Logf("  provider p95          %d ms", p95Prov)
	t.Logf("")
	t.Logf("  first_valid avg      %.0f ms", avgFV)
	t.Logf("  first_valid p50       %d ms", p50FV)
	t.Logf("  first_valid p95       %d ms", p95FV)
	t.Logf("")
	t.Logf("  total candidates     %d", totalCand)
	t.Logf("  WebP skipped         %d", totalWebP)
	t.Logf("  broken candidates    %d", totalBroken)
	t.Logf("")

	// Per-person detail
	t.Logf("  ── Per-person detail ──")
	t.Logf("  %-28s  Cand  Valid  Broken  WebP  ProvMS  FV-MS", "PERSON")
	for _, r := range results {
		webp := 0
		for _, c := range r.Candidates {
			if c.Error != "" && (strings.Contains(strings.ToLower(c.Error), "webp") || strings.Contains(strings.ToLower(c.Error), "unknown format")) {
				webp++
			}
		}
		t.Logf("  %-28s  %4d  %5d  %6d  %4d  %6d  %5d",
			r.Person, r.CandidateCount, r.ValidCandidates, r.BrokenCandidates, webp,
			r.ProviderSearchMs, r.FirstValidImageMs,
		)
	}
}

func logConcurrencyRun(t *testing.T, run personImageBenchConcurrencyRun) {
	t.Logf("")
	t.Logf("  ── Concurrency summary ──")
	t.Logf("  wall_time             %.1fs", float64(run.WallTimeMs)/1000.0)
	t.Logf("  persons_completed     %d", run.PersonsCompleted)
	t.Logf("  persons/min           %.1f", run.PersonsPerMin)
	t.Logf("  provider p50          %d ms", run.ProviderP50)
	t.Logf("  provider p95          %d ms", run.ProviderP95)
	t.Logf("  first_valid p50       %d ms", run.FirstValidP50)
	t.Logf("  first_valid p95       %d ms", run.FirstValidP95)
	t.Logf("  HTTP 4xx failures     %d", run.HTTPFailures)
	t.Logf("  timeouts              %d", run.Timeouts)
	t.Logf("  HTTP 429              %d", run.HTTP429)
	t.Logf("  other provider errs   %d", run.OtherErrors)
	t.Logf("  at-least-1-valid rate %.1f%%", run.CorrectImageRate*100)
}

// Ensure the host package symbols are accessible.
var _ = fmt.Sprintf
var _ = bytes.Compare
var _ = image.Decode
var _ = io.ReadAll
var _ = http.NewRequest
var _ = sync.Mutex{}
var _ = zap.NewNop
var _ = testing.T{}
var _ = context.Background
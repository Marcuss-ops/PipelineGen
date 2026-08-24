package images

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// ── Quality audit ────────────────────────────────────────────────────────
//
// Technical classification of the top-10 DuckDuckGo candidates per person:
//
//	VALID   — HTTP 200, decodable, width/height > 0
//	WEBP    — HTTP 200 but content-type image/webp (unsupported by the JPG/PNG gate)
//	BROKEN  — HTTP error, decode failure, or zero dimensions
//
// Semantic correctness (CORRECT PERSON vs WRONG PERSON vs GENERIC/LOGO)
// cannot be determined from URL bytes alone: it needs human eyes or a vision
// model. This audit therefore reports the deterministic technical accuracy
// (first VALID image by rank) and dumps a per-candidate table that a human
// reviewer can annotate. It also inspects the catalog tables directly.
//
// Run:
//   PERSON_QUALITY_AUDIT=1 go test ./internal/capabilities/images/workflow \
//     -run TestPersonImageQualityAudit -v -count=1 -timeout 15m

type auditCandidate struct {
	Rank        int
	URL         string
	ContentType string
	Bytes       int
	Width       int
	Height      int
	Label       string
	Error       string
}

func classifyAuditCandidate(raw liveDDGDownloadResult) auditCandidate {
	c := auditCandidate{
		URL:         raw.URL,
		ContentType: raw.ContentType,
		Bytes:       raw.Bytes,
		Width:       raw.Width,
		Height:      raw.Height,
		Error:       raw.Error,
	}
	switch {
	case raw.Error == "":
		c.Label = "VALID"
	case strings.Contains(strings.ToLower(raw.ContentType), "webp"),
		strings.Contains(strings.ToLower(raw.Error), "unknown format"):
		c.Label = "WEBP"
	default:
		c.Label = "BROKEN"
	}
	return c
}

func TestPersonImageQualityAudit(t *testing.T) {
	if os.Getenv("PERSON_QUALITY_AUDIT") != "1" {
		t.Skip("set PERSON_QUALITY_AUDIT=1 to run the live person image quality audit")
	}

	people := benchmarkPersonSampleSet()
	client := &http.Client{Timeout: 25 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	service := &ImageStorageService{client: client, log: zap.NewNop()}

	// ── Per-person technical classification ───────────────────────────
	t.Logf("═══ TECHNICAL CLASSIFICATION: top-10 DDG candidates per person ═══")
	t.Logf("")

	type personAudit struct {
		person         string
		cands          []auditCandidate
		firstValidRank int // 1-indexed; 0 if none
	}

	audits := make([]personAudit, 0, len(people))
	totalByLabel := map[string]int{"VALID": 0, "WEBP": 0, "BROKEN": 0}

	for _, person := range people {
		urls := service.searchDDGWideMany(ctx, person, 10)
		if len(urls) == 0 {
			t.Logf("  %-28s  no DDG URLs", person)
			audits = append(audits, personAudit{person: person})
			continue
		}

		raws := make([]liveDDGDownloadResult, len(urls))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i, rawURL := range urls {
			wg.Add(1)
			go func(i int, rawURL string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				raws[i] = downloadAndDecodeOneLiveDDGURL(ctx, client, rawURL)
			}(i, rawURL)
		}
		wg.Wait()

		cands := make([]auditCandidate, 0, len(urls))
		firstValid := 0
		for i, raw := range raws {
			c := classifyAuditCandidate(raw)
			c.Rank = i + 1
			cands = append(cands, c)
			totalByLabel[c.Label]++
			if firstValid == 0 && c.Label == "VALID" {
				firstValid = i + 1
			}
		}
		audits = append(audits, personAudit{person: person, cands: cands, firstValidRank: firstValid})

		// Per-person one-line summary
		valid, webp, broken := 0, 0, 0
		for _, c := range cands {
			switch c.Label {
			case "VALID":
				valid++
			case "WEBP":
				webp++
			default:
				broken++
			}
		}
		t.Logf("  %-28s  valid=%2d  webp=%d  broken=%d  first_valid_rank=%d",
			person, valid, webp, broken, firstValid)
	}

	// ── Top-K technical accuracy ──────────────────────────────────────
	t.Logf("")
	t.Logf("── Top-K technical accuracy (first VALID JPG/PNG by rank) ──")
	t.Logf("")
	for _, k := range []int{1, 3, 5, 10} {
		hit := 0
		for _, a := range audits {
			if a.firstValidRank > 0 && a.firstValidRank <= k {
				hit++
			}
		}
		t.Logf("  Top%-2d  %d/%d  (%.1f%%)", k, hit, len(audits), 100*float64(hit)/float64(len(audits)))
	}

	t.Logf("")
	t.Logf("── Label totals across %d candidates ──", len(people)*10)
	t.Logf("  VALID   %d", totalByLabel["VALID"])
	t.Logf("  WEBP    %d", totalByLabel["WEBP"])
	t.Logf("  BROKEN  %d", totalByLabel["BROKEN"])

	// ── Dump per-candidate table for manual semantic labeling ────────
	t.Logf("")
	t.Logf("── Candidate dump for MANUAL semantic labeling (human/vision) ──")
	t.Logf("   rank  label   content-type        bytes      dims        url")
	for _, a := range audits {
		t.Logf("  ── %s ──", a.person)
		for _, c := range a.cands {
			dims := "0x0"
			if c.Width > 0 || c.Height > 0 {
				dims = fmtInts(c.Width, c.Height)
			}
			urlTrunc := c.URL
			if len(urlTrunc) > 70 {
				urlTrunc = urlTrunc[:70]
			}
			t.Logf("   %3d  %-7s %-18s %8d  %10s  %s", c.Rank, c.Label, c.ContentType, c.Bytes, dims, urlTrunc)
		}
	}

	// ── Catalog inspection ───────────────────────────────────────────
	t.Logf("")
	t.Logf("═══ CATALOG INSPECTION: populate + inspect SQLite directly ═══")
	t.Logf("")

	inspectCatalogTables(t, ctx, client)
}

func fmtInts(a, b int) string {
	return itoa(a) + "x" + itoa(b)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func inspectCatalogTables(t *testing.T, ctx context.Context, client *http.Client) {
	dbPath := filepath.Join(t.TempDir(), "quality-audit-catalog.db")
	db := openLiveEntityImageCatalog(t, dbPath)
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	searcher := newLiveCatalogDDGSearcher(client)
	processor := adapters.NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := liveCatalogPlan()

	// Populate via the real search→persist path.
	if _, err := processor.Process(ctx, plan, liveCatalogPersonInput("mj-audit", "Michael Jordan")); err != nil {
		t.Fatalf("populate catalog: %v", err)
	}

	t.Logf("── entity_image_catalog_entities ──")
	rows, err := db.QueryContext(ctx, `SELECT canonical_entity_id, canonical_name, refresh_status, last_refresh_at FROM entity_image_catalog_entities ORDER BY canonical_entity_id`)
	if err != nil {
		t.Fatalf("query entities: %v", err)
	}
	entityCount := 0
	for rows.Next() {
		var id, name, status, lastRefresh string
		if err := rows.Scan(&id, &name, &status, &lastRefresh); err != nil {
			t.Fatal(err)
		}
		entityCount++
		t.Logf("  %-30s  name=%q  refresh=%s  last_refresh=%q", id, name, status, lastRefresh)
	}
	rows.Close()

	t.Logf("")
	t.Logf("── entity_image_catalog_candidates (Michael Jordan) ──")
	cRows, err := db.QueryContext(ctx, `
		SELECT rank, source_url, status, semantic_status, semantic_score, technical_score, quality_reason
		FROM entity_image_catalog_candidates
		WHERE canonical_entity_id = 'person:michael-jordan'
		ORDER BY rank`)
	if err != nil {
		t.Fatalf("query candidates: %v", err)
	}
	type candRow struct {
		rank        int
		url, status string
		semStatus   string
		semScore    float64
		techScore   float64
		reason      string
	}
	var cands []candRow
	for cRows.Next() {
		var c candRow
		if err := cRows.Scan(&c.rank, &c.url, &c.status, &c.semStatus, &c.semScore, &c.techScore, &c.reason); err != nil {
			t.Fatal(err)
		}
		cands = append(cands, c)
	}
	cRows.Close()

	for _, c := range cands {
		urlTrunc := c.url
		if len(urlTrunc) > 60 {
			urlTrunc = urlTrunc[:60]
		}
		t.Logf("  rank=%2d  status=%s  semantic=%s  sem_score=%.2f  tech_score=%.2f  reason=%q  url=%s",
			c.rank, c.status, c.semStatus, c.semScore, c.techScore, c.reason, urlTrunc)
	}

	t.Logf("")
	t.Logf("── entity_image_catalog_materializations ──")
	var matCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity_image_catalog_materializations`).Scan(&matCount); err != nil {
		t.Fatalf("query materializations: %v", err)
	}
	t.Logf("  materialization rows = %d (search-only, no Drive upload)", matCount)

	// ── Invariant checks ────────────────────────────────────────────
	t.Logf("")
	t.Logf("── Invariant checks ──")

	// 1 canonical row
	if entityCount != 1 {
		t.Errorf("canonical entity rows = %d, want 1", entityCount)
	} else {
		t.Logf("  ✓ 1 canonical entity row")
	}

	// ~10 candidates
	if len(cands) == 0 {
		t.Errorf("candidate rows = 0, want ~10")
	} else if len(cands) > 10 {
		t.Errorf("candidate rows = %d, want <=10", len(cands))
	} else {
		t.Logf("  ✓ %d candidate rows (≤10)", len(cands))
	}

	// 0 URL duplicates
	var dupCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT source_url FROM entity_image_catalog_candidates
			WHERE canonical_entity_id = 'person:michael-jordan'
			GROUP BY source_url HAVING COUNT(*) > 1
		)`).Scan(&dupCount); err != nil {
		t.Fatalf("query duplicates: %v", err)
	}
	if dupCount != 0 {
		t.Errorf("duplicate URL groups = %d, want 0", dupCount)
	} else {
		t.Logf("  ✓ 0 duplicate URLs")
	}

	// ── Key finding: technical_score at promotion time ──────────────
	t.Logf("")
	t.Logf("── KEY FINDING: technical validation at catalog promotion ──")
	zeroTech := 0
	for _, c := range cands {
		if c.techScore == 0 {
			zeroTech++
		}
	}
	t.Logf("  %d/%d candidates have technical_score = 0", zeroTech, len(cands))
	t.Logf("  reason: searchDDGWideMany returns URLs only (no width/height),")
	t.Logf("  so the download+decode gate is NOT applied at promotion time.")
	t.Logf("  A broken/WebP URL can enter the pool as fresh/accepted and only")
	t.Logf("  fails later during materialization.")
}

var _ = sort.Ints
var _ = sql.ErrNoRows

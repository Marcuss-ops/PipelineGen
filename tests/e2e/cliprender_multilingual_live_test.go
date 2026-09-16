// Package e2e — cliprender_multilingual_live_test.go: the RENDER half of the
// multilingual certificate.
//
// youtube_whisper_argos_live_test.go certifies the acquisition half:
//
//	YouTube 60s → local Whisper → PostgreSQL transcript → local Argos
//	  → 10 READY timed tracks → 10 validated ASS artifacts on Drive.
//
// This test certifies what the product actually ships: that those ten READY
// tracks render through the canonical POST /api/clips/render/batch endpoint
// into ten final MP4s over the centralized editorial background, each with the
// RIGHT language burned in, on the GPU backend, published to Drive and
// registered as a derived asset.
//
// It deliberately drives the HTTP endpoint the product exposes instead of an
// in-process worker: the wire contract (payload → 202 → job → settle child →
// derived asset) is part of what must hold. The previous ad-hoc Python harness
// exercised the same endpoint but lived outside the repository and asserted
// `metrics` instead of `metrics_v2`, which is exactly the kind of drift a
// checked-in certificate is supposed to prevent.
//
// Hard-gated behind VELOX_E2E_LIVE=1 + TEST_POSTGRES_DSN, and it additionally
// requires a running PipelineGen API plus an admin token; it is SKIPPED, never
// silently mocked, when either is missing.
//
// Required environment:
//
//	VELOX_E2E_LIVE=1            enables the live suite
//	TEST_POSTGRES_DSN           the SAME PostgreSQL the API server serves from
//	VELOX_E2E_ADMIN_TOKEN       bearer token accepted by POST /api/clips/render
//
// Optional environment:
//
//	VELOX_E2E_API_URL                    default http://127.0.0.1:8000
//	VELOX_E2E_SOURCE_ASSET_ID            default yt_<videoID>_0_60_whisper_v1
//	VELOX_E2E_YOUTUBE_URL                used to derive the default asset id
//	VELOX_EDITORIAL_BACKGROUNDS_DIR      certified plate fixtures directory
//	VELOX_E2E_RENDER_TIMEOUT             per-clip render budget (default 4m)
//	VELOX_E2E_REAL_DRIVE=1               assert Drive-issued ids (real upload)
//
// Run the acquisition certificate first: this test fails closed when the source
// asset carries fewer than ten READY transcript languages, because a render
// over missing tracks would "pass" while certifying nothing.
package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/jackc/pgx/v5/stdlib"

	wiringmedia "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// editorialBackgroundPlateID is the certified plate every language is rendered
// over. It is a registry alias, not a Drive id: clip.render resolves it through
// the same media SSOT the rest of the pipeline reads.
const editorialBackgroundPlateID = "drive-background-01"

// renderTerminalStatuses are the Master/Settle statuses that end a poll.
// WAITING_CHILDREN is included because the SUBMIT job's contract is satisfied
// the moment it handed off to its settle child: its result already carries the
// child id, so the artifact must be read from the child, not from the parent.
var renderTerminalStatuses = map[string]bool{
	"SUCCEEDED":           true,
	"PARTIALLY_SUCCEEDED": true,
	"FAILED":              true,
	"CANCELLED":           true,
	"INDEX_PENDING":       true,
	"WAITING_CHILDREN":    true,
}

// renderFailureStatuses are the statuses a submit job must never report.
var renderFailureStatuses = map[string]bool{
	"FAILED":    true,
	"CANCELLED": true,
}

// openLiveMediaDB opens the live PostgreSQL media handle and applies the
// canonical migrations WITHOUT truncating anything. It is the shared opener for
// every live test: the certificates build on each other (the render half
// consumes the acquisition half's rows), so wiping the schema belongs to the
// test that STARTS the chain, not to the opener.
func openLiveMediaDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	require.NotEmpty(t, dsn, "TEST_POSTGRES_DSN is required")
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx), "ping PostgreSQL test instance")

	for i, ddl := range []string{
		pgmigration.MediaSchemaDDL,
		pgmigration.MediaVectorSurfacesDDL,
		pgmigration.MediaHNSWIndexesDDL,
		pgmigration.MediaTimestampsTimestamptzDDL,
		pgmigration.MediaAssetVersionsDDL,
		pgmigration.MediaAssetProcessingDDL,
	} {
		_, err := db.ExecContext(ctx, ddl)
		require.NoErrorf(t, err, "apply media migration %d", i+1)
	}
	// The deterministic render cache is a clip.render table created at server
	// boot; a fresh database must carry it or the batch's cache path fails.
	require.NoError(t, cliprender.EnsureRenderCacheTable(ctx, db), "ensure clip_render_cache")
	return db
}

// editorialPlatesDir resolves the certified plate fixtures directory. When it
// cannot be found the caller registers the plates without a local copy and the
// canonical materializer fetches them from Drive.
func editorialPlatesDir() string {
	if v := strings.TrimSpace(os.Getenv(wiringmedia.EditorialBackgroundsDirEnv)); v != "" {
		return v
	}
	candidate := filepath.Join("..", "..", "..", "RenderingGen", "assets", "backgrounds")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

// ── HTTP transport ─────────────────────────────────────────────────────────

type clipRenderBatchItem struct {
	Position int    `json:"position"`
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	CacheHit bool   `json:"cache_hit"`
	AssetID  string `json:"asset_id"`
	Error    string `json:"error"`
}

type clipRenderBatchResponse struct {
	BatchID  string                `json:"batch_id"`
	Accepted int                   `json:"accepted"`
	Jobs     []clipRenderBatchItem `json:"jobs"`
}

type jobFullResponse struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Status   string          `json:"status"`
	Progress int             `json:"progress"`
	Error    string          `json:"error"`
	Result   json.RawMessage `json:"result"`
}

type clipRenderJobResult struct {
	Phase      string `json:"phase"`
	ChildJobID string `json:"child_job_id"`
	Asset      struct {
		AssetID      string `json:"asset_id"`
		DriveFileID  string `json:"drive_file_id"`
		DriveLink    string `json:"drive_link"`
		DrivePending bool   `json:"drive_pending"`
		Status       string `json:"publication_status"`
		SizeBytes    int64  `json:"size_bytes"`
	} `json:"asset"`
	Render struct {
		DurationSec float64 `json:"duration_sec"`
		Width       int     `json:"width"`
		Height      int     `json:"height"`
		FPSNum      int     `json:"fps_num"`
		FPSDen      int     `json:"fps_den"`
		Backend     string  `json:"backend"`
		SizeBytes   int64   `json:"size_bytes"`
	} `json:"render"`
	Transcript struct {
		Language string `json:"language"`
		Cues     int    `json:"cues"`
		Reused   bool   `json:"reused"`
	} `json:"transcript"`
	Subtitles struct {
		Mode   string `json:"mode"`
		SHA256 string `json:"sha256"`
	} `json:"subtitles"`
	Background struct {
		AssetID string `json:"asset_id"`
		SHA256  string `json:"sha256"`
	} `json:"background"`
	ContractID string `json:"contract_id"`
}

func postJSON(t *testing.T, client *http.Client, url, token string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoErrorf(t, err, "POST %s", url)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, data
}

// getJSON GETs a job surface. The token is required here too: the jobs read
// endpoints are behind the same bearer gate as the render submit endpoint.
func getJSON(t *testing.T, client *http.Client, url, token string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoErrorf(t, err, "build GET %s", url)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoErrorf(t, err, "GET %s", url)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s: %s", url, string(body))
	return body
}

// pollJobUntilTerminal polls GET /api/jobs/{id}/full until the job reaches a
// terminal status, returning the decoded envelope. The submit job completes as
// soon as the render is submitted (its result carries the settle child id), so
// callers that need the artifact poll the child instead.
func pollJobUntilTerminal(t *testing.T, client *http.Client, apiURL, jobID, token string, budget time.Duration) jobFullResponse {
	t.Helper()
	deadline := time.Now().Add(budget)
	var last jobFullResponse
	for time.Now().Before(deadline) {
		var full jobFullResponse
		require.NoError(t, json.Unmarshal(getJSON(t, client, apiURL+"/api/jobs/"+jobID+"/full", token), &full))
		last = full
		if renderTerminalStatuses[full.Status] {
			return full
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach a terminal status within %s (last status=%s progress=%d error=%q)", jobID, budget, last.Status, last.Progress, last.Error)
	return last
}

// settleResult resolves the artifact-producing settle child of a submitted
// render and returns its decoded result.
func settleResult(t *testing.T, client *http.Client, apiURL, masterJobID, token string, budget time.Duration) clipRenderJobResult {
	t.Helper()
	master := pollJobUntilTerminal(t, client, apiURL, masterJobID, token, budget)
	require.Falsef(t, renderFailureStatuses[master.Status], "submit job %s failed: %s", masterJobID, master.Error)

	var submit clipRenderJobResult
	require.NoErrorf(t, json.Unmarshal(master.Result, &submit), "decode submit result of %s", masterJobID)
	require.NotEmptyf(t, submit.ChildJobID, "submit job %s must hand off to a settle child", masterJobID)

	child := pollJobUntilTerminal(t, client, apiURL, submit.ChildJobID, token, budget)
	require.Equalf(t, "SUCCEEDED", child.Status, "settle job %s failed: %s", submit.ChildJobID, child.Error)

	var settled clipRenderJobResult
	require.NoErrorf(t, json.Unmarshal(child.Result, &settled), "decode settle result of %s", submit.ChildJobID)
	return settled
}

// renderItemsForLanguages builds one RenderRequest per language. Every item
// shares the SAME source asset and the SAME centralized editorial plate, and
// differs only in the transcript language, so the batch is exactly the
// production multilingual fan-out.
func renderItemsForLanguages(sourceAssetID, plateID, subfolder string, languages []string) []cliprender.RenderRequest {
	items := make([]cliprender.RenderRequest, 0, len(languages))
	for _, lang := range languages {
		items = append(items, cliprender.RenderRequest{
			SourceAssetID: sourceAssetID,
			Background: &cliprender.BackgroundSpec{
				Mode:    cliprender.BackgroundModeAsset,
				AssetID: plateID,
				Kind:    cliprender.BackgroundKindVideo,
			},
			Transcript: &cliprender.TranscriptSpec{
				Mode:     cliprender.TranscriptModeReuse,
				Language: lang,
			},
			Subtitles: &cliprender.SubtitlesSpec{
				Enabled: true,
				Mode:    cliprender.SubtitlesModeBurn,
				StyleID: "vidrush-default",
			},
			Destination: &cliprender.DestinationSpec{SubfolderName: subfolder},
			Execution:   &cliprender.ExecutionSpec{RequireGPU: true},
		})
	}
	return items
}

// ── the test ───────────────────────────────────────────────────────────────

func TestLiveClipRenderBatchCertifiesTenLanguageMP4s(t *testing.T) {
	requireLiveE2E(t)
	adminToken := strings.TrimSpace(os.Getenv("VELOX_E2E_ADMIN_TOKEN"))
	if adminToken == "" {
		t.Skip("VELOX_E2E_ADMIN_TOKEN not set; the render certificate needs the admin bearer token accepted by POST /api/clips/render")
	}
	apiURL := strings.TrimSuffix(liveEnv("VELOX_E2E_API_URL", "http://127.0.0.1:8000"), "/")
	budget := 4 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("VELOX_E2E_RENDER_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		require.NoErrorf(t, err, "VELOX_E2E_RENDER_TIMEOUT=%q", raw)
		budget = parsed
	}

	db := openLiveMediaDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	log := zaptest.NewLogger(t)

	// ── 0. The acquisition half must have run first. ──────────────────
	url := liveEnv("VELOX_E2E_YOUTUBE_URL", liveDefaultURL)
	videoID := videoIDFromURL(url)
	require.NotEmptyf(t, videoID, "could not resolve a YouTube video id from %q", url)
	sourceAssetID := liveEnv("VELOX_E2E_SOURCE_ASSET_ID",
		fmt.Sprintf("yt_%s_%d_%d_whisper_v1", videoID, liveSegmentStart, liveSegmentEnd))

	var sourceLanguages int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_text_tracks
		WHERE asset_id = $1 AND text_kind = 'transcript' AND status = 'READY' AND is_current = 1
	`, sourceAssetID).Scan(&sourceLanguages))
	require.Equalf(t, len(liveLanguages), sourceLanguages,
		"source asset %s must carry %d READY transcript languages before it can be certified for rendering; run TestLiveYouTube_WhisperSourceArgosTranslationDeliversSubtitleArtifacts first (got %d)",
		sourceAssetID, len(liveLanguages), sourceLanguages)

	// ── 1. The centralized plate MUST be registered canonically. ───────
	// A hand-written INSERT used to be the only way to get a plate row; this
	// bootstrap is the canonical writer, so a clean database becomes renderable
	// with no manual SQL at all.
	registrations, err := wiringmedia.EnsureEditorialAssets(ctx, db, editorialPlatesDir(), log)
	require.NoError(t, err, "register the curated editorial plates")
	require.NotEmpty(t, registrations)
	plate, ok := mediaregistry.LookupEditorialBackground(editorialBackgroundPlateID)
	require.Truef(t, ok, "%s must exist in the editorial registry", editorialBackgroundPlateID)
	for _, r := range registrations {
		require.Truef(t, len(r.SHA256) == 64, "registered plate %s must carry a canonical SHA-256", r.AssetID)
	}

	// ── 2. POST the 10-language batch to the real endpoint. ────────────
	client := &http.Client{Timeout: 30 * time.Second}
	subfolder := "Mike Tyson 10 Languages E2E"
	items := renderItemsForLanguages(sourceAssetID, editorialBackgroundPlateID, subfolder, liveLanguages)
	body := map[string]any{"items": items}

	// The batch fingerprint is the canonical per-language key of everything
	// downstream (the render cache, the job correlation, the derived asset
	// lineage), so it is computed with the capability's own Fingerprint rather
	// than re-derived here.
	fingerprints := make([]string, len(items))
	for i := range items {
		items[i].Normalize()
		fingerprint, fpErr := items[i].Fingerprint()
		require.NoErrorf(t, fpErr, "fingerprint render item %d", i)
		fingerprints[i] = fingerprint
	}

	status, raw := postJSON(t, client, apiURL+"/api/clips/render/batch", adminToken, body)
	require.Equalf(t, http.StatusAccepted, status, "batch submit must be accepted: %s", string(raw))
	var batch clipRenderBatchResponse
	require.NoErrorf(t, json.Unmarshal(raw, &batch), "decode batch response: %s", string(raw))
	require.Lenf(t, batch.Jobs, len(liveLanguages), "one batch entry per language")
	require.NotEmpty(t, batch.BatchID)

	// Position → language, so a mismatch is reported against the right item.
	settledByLanguage := map[string]clipRenderJobResult{}
	assetByLanguage := map[string]string{}
	for i, item := range batch.Jobs {
		lang := liveLanguages[i]
		require.Equalf(t, i, item.Position, "batch item order must be preserved")
		require.Emptyf(t, item.Error, "batch item %s failed at submit", lang)

		if item.CacheHit {
			require.Equalf(t, "CACHED", item.Status, "a cache hit must report CACHED for %s", lang)
			require.NotEmptyf(t, item.AssetID, "a cache hit must name the certified asset for %s", lang)
			assetByLanguage[lang] = item.AssetID
			continue
		}
		require.Equalf(t, "QUEUED", item.Status, "a miss must be QUEUED for %s", lang)
		require.NotEmptyf(t, item.JobID, "a queued item must carry a job id for %s", lang)

		result := settleResult(t, client, apiURL, item.JobID, adminToken, budget)
		settledByLanguage[lang] = result
		assetByLanguage[lang] = result.Asset.AssetID
	}

	// ── 3. Every settled output must satisfy the render contract. ──────
	// A settled item is one this run actually polled. A CACHED item (or a job
	// the queue deduplicated onto a previous identical batch) has no fresh job
	// result to inspect; its contract is certified by the durable proofs in the
	// next block, which is strictly stronger because they survive the process.
	for _, lang := range liveLanguages {
		result, settled := settledByLanguage[lang]
		if !settled {
			require.NotEmptyf(t, assetByLanguage[lang], "language %s produced no asset", lang)
			continue
		}
		require.Equalf(t, lang, result.Transcript.Language, "the burned track must be the %s transcript", lang)
		require.Positivef(t, result.Transcript.Cues, "language %s must render with timed cues", lang)
		require.Equal(t, "burn", result.Subtitles.Mode, "language %s must burn the subtitles in", lang)
		require.NotEmptyf(t, result.Subtitles.SHA256, "language %s must compile a subtitle artifact", lang)

		require.Equalf(t, editorialBackgroundPlateID, result.Background.AssetID,
			"language %s must be composited over the centralized editorial plate", lang)
		require.Equalf(t, plate.SHA256, result.Background.SHA256,
			"language %s must composite the CERTIFIED plate bytes, not the raw Drive file", lang)

		require.Equalf(t, 1920, result.Render.Width, "language %s width", lang)
		require.Equalf(t, 1080, result.Render.Height, "language %s height", lang)
		require.Equalf(t, 24, result.Render.FPSNum, "language %s fps numerator", lang)
		require.Equalf(t, 1, result.Render.FPSDen, "language %s fps denominator", lang)
		require.InDeltaf(t, float64(liveSegmentEnd), result.Render.DurationSec, 1.5,
			"language %s must render the full %ds clip", lang, liveSegmentEnd)
		require.Containsf(t, result.Render.Backend, "vulkan",
			"language %s must render on the GPU backend (require_gpu was set), got %q", lang, result.Render.Backend)
		require.Positivef(t, result.Render.SizeBytes, "language %s must produce output bytes", lang)

		require.Truef(t, strings.HasPrefix(result.Asset.AssetID, "cliprender_"),
			"language %s derived asset id must be content-addressed, got %q", lang, result.Asset.AssetID)
		// Publication is asynchronous by design: the settle job commits the
		// derived asset immediately and a durable outbox event performs the
		// Drive delivery, so a settle result may legitimately report PENDING.
		// A FAILED delivery is not a valid outcome here.
		require.Containsf(t, []string{"PUBLISHED", "PENDING", "REUSED"}, result.Asset.Status,
			"language %s publication status must be PUBLISHED/PENDING/REUSED, got %q", lang, result.Asset.Status)
	}

	// Distinct languages must produce distinct artifacts: a shared asset id
	// would mean the language-specific burn was silently dropped.
	seen := map[string]string{}
	for lang, assetID := range assetByLanguage {
		if previous, dup := seen[assetID]; dup {
			t.Errorf("languages %s and %s share the derived asset %s", previous, lang, assetID)
		}
		seen[assetID] = lang
	}

	// ── 4. Durable proofs: a certified GPU render + a registered asset. ─
	//
	// This is the part that certifies the RENDER, not just this process's run:
	// the deterministic render cache record is written ONLY after a render has
	// been validated and published, and it carries the owner-reported backend
	// and the exact output bytes. Requiring one per language means "the ten
	// MP4s exist" even when the queue deduplicated this batch onto an earlier
	// identical one, and it fails closed when nothing was ever rendered.
	realDrive := os.Getenv("VELOX_E2E_REAL_DRIVE") != ""
	for i, lang := range liveLanguages {
		assetID := assetByLanguage[lang]
		require.NotEmptyf(t, assetID, "language %s produced no derived asset", lang)
		require.Truef(t, strings.HasPrefix(assetID, "cliprender_"),
			"language %s derived asset id must be content-addressed, got %q", lang, assetID)

		var category, assetSHA string
		var durationMS int64
		require.NoErrorf(t, db.QueryRowContext(ctx,
			`SELECT category, duration_ms, COALESCE(content_sha256, '') FROM media_assets WHERE id = $1`, assetID,
		).Scan(&category, &durationMS, &assetSHA),
			"language %s derived asset %s must be registered in the media SSOT", lang, assetID)
		require.Equalf(t, "clip-render", category, "language %s derived asset category", lang)
		require.InDeltaf(t, float64(liveSegmentEnd*1000), float64(durationMS), 2000,
			"language %s derived asset duration", lang)

		var cacheSHA, backend string
		var width, height, fpsNum, fpsDen int
		var durationSec float64
		err := db.QueryRowContext(ctx, `
			SELECT sha256, backend, width, height, fps_num, fps_den, duration_sec
			FROM clip_render_cache WHERE fingerprint = $1`, fingerprints[i],
		).Scan(&cacheSHA, &backend, &width, &height, &fpsNum, &fpsDen, &durationSec)
		require.NoErrorf(t, err,
			"language %s has no certified render for its request: the batch was never rendered (fingerprint %s)", lang, fingerprints[i])
		require.Containsf(t, backend, "vulkan",
			"language %s must be certified on the GPU backend (require_gpu was set), got %q", lang, backend)
		require.Equalf(t, 1920, width, "language %s certified width", lang)
		require.Equalf(t, 1080, height, "language %s certified height", lang)
		require.Equalf(t, 24, fpsNum, "language %s certified fps numerator", lang)
		require.Equalf(t, 1, fpsDen, "language %s certified fps denominator", lang)
		require.InDeltaf(t, float64(liveSegmentEnd), durationSec, 1.5,
			"language %s certified duration", lang)
		require.Equalf(t, cacheSHA, assetSHA,
			"language %s registered bytes must be the certified render bytes", lang)
		require.Truef(t, strings.HasPrefix(assetID, "cliprender_"+cacheSHA[:24]),
			"language %s derived asset id must be the content address of the certified bytes", lang)

		if realDrive {
			require.NotEmptyf(t, waitForDriveFileID(t, ctx, db, assetID, 2*time.Minute),
				"language %s must complete its Drive delivery in real-Drive mode", lang)
		}
	}

	// ── 5. Idempotency: the identical batch is served from the cache. ───
	// The deterministic render cache is the reason a re-run costs no GPU: every
	// item must come back as a hit naming the SAME asset, and no new job may be
	// enqueued.
	status, raw = postJSON(t, client, apiURL+"/api/clips/render/batch", adminToken, body)
	require.Equalf(t, http.StatusAccepted, status, "repeat batch must be accepted: %s", string(raw))
	var repeat clipRenderBatchResponse
	require.NoErrorf(t, json.Unmarshal(raw, &repeat), "decode repeat batch response: %s", string(raw))
	require.Len(t, repeat.Jobs, len(liveLanguages))
	for i, item := range repeat.Jobs {
		lang := liveLanguages[i]
		require.Truef(t, item.CacheHit, "repeat run must be a cache hit for %s (status=%s)", lang, item.Status)
		require.Equalf(t, assetByLanguage[lang], item.AssetID,
			"repeat run must reuse the certified asset for %s", lang)
	}

	t.Logf("RENDER CERTIFICATE OK: source=%s plate=%s languages=%d assets=%d",
		sourceAssetID, editorialBackgroundPlateID, len(liveLanguages), len(assetByLanguage))
}

// waitForDriveFileID polls the derived asset until its asynchronous Drive
// delivery records a file id. The settle job commits the asset and a durable
// outbox event performs the upload, so the id appears after the job is done.
func waitForDriveFileID(t *testing.T, ctx context.Context, db *sql.DB, assetID string, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		var fileID string
		if err := db.QueryRowContext(ctx,
			`SELECT COALESCE(drive_file_id, '') FROM media_assets WHERE id = $1`, assetID,
		).Scan(&fileID); err == nil && fileID != "" {
			return fileID
		}
		time.Sleep(1 * time.Second)
	}
	return ""
}

// TestEditorialPlateBootstrapIsIdempotent pins the registration contract the
// render certificate depends on: running the bootstrap twice converges on the
// same canonical rows instead of duplicating or drifting.
func TestEditorialPlateBootstrapIsIdempotent(t *testing.T) {
	requireLiveE2E(t)
	db := openLiveMediaDB(t)
	ctx := context.Background()
	log := zaptest.NewLogger(t)

	first, err := wiringmedia.EnsureEditorialAssets(ctx, db, editorialPlatesDir(), log)
	require.NoError(t, err)
	second, err := wiringmedia.EnsureEditorialAssets(ctx, db, editorialPlatesDir(), log)
	require.NoError(t, err)
	require.Equal(t, len(first), len(second))

	for _, r := range second {
		var contentHash, driveFileID string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT content_sha256, drive_file_id FROM media_assets WHERE id = $1`, r.AssetID,
		).Scan(&contentHash, &driveFileID))
		require.Equalf(t, r.SHA256, contentHash, "plate %s content hash", r.AssetID)
		require.Equalf(t, r.DriveFileID, driveFileID, "plate %s Drive identity", r.AssetID)
	}
}

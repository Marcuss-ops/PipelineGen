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
//	VELOX_E2E_LANGUAGES                  narrow the language set (staged
//	                                     rollout: five first, then ten)
//	VELOX_E2E_RESET_RENDER_STATE=1       delete the PREVIOUS certificate's
//	                                     derived assets + cache rows first, so
//	                                     this run must actually render
//
// The reset flag is test-harness state management, NOT a render behaviour
// switch: production keeps exactly ONE render path, and the fresh-render
// certificate exists precisely so that "ten QUEUED" and "ten CACHED" are two
// separately proven facts (a clean state must render; the identical payload
// must then cost zero GPU) instead of one ambiguous run that may have served
// ten hits from a previous session.
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	_ "github.com/jackc/pgx/v5/stdlib"

	wiringmedia "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
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

// liveCertificateConfig loads the production config the live certificate runs
// against. The certificate must use the SAME credential files and data root the
// server uses, so they are anchored to the config file's directory.
func liveCertificateConfig(t *testing.T) *config.Config {
	t.Helper()
	loadDotEnvMissing(t, liveEnv("VELOX_E2E_DOTENV", filepath.Join("..", "..", ".env")))
	cfgPath := liveEnv("VELOX_E2E_CONFIG", filepath.Join("..", "..", "config.yaml"))
	resolved, err := config.GetResolvedFromPath(cfgPath)
	require.NoErrorf(t, err, "the live certificate needs the production config at %s", cfgPath)
	cfg := resolved.View()
	require.NotNil(t, cfg)
	cfgBase := filepath.Dir(cfgPath)
	cfg.Paths.CredentialsFile = anchorToConfig(cfgBase, cfg.Paths.CredentialsFile)
	cfg.Paths.TokenFile = anchorToConfig(cfgBase, cfg.Paths.TokenFile)
	// The data root is config-relative too, and the JOBS STORE lives under it
	// (<data_dir>/jobs/jobs.db.sqlite). Left relative, this test's process (cwd
	// `tests/e2e`) resolves it to a different — empty — directory than the
	// server, which registers the certificate's own render jobs. Anchoring it
	// makes "the jobs store" mean the same file in both processes.
	cfg.Storage.DataDir = anchorToConfig(cfgBase, cfg.Storage.DataDir)
	return cfg
}

// resetRenderCertificateJobs removes the FAILED clip.render jobs a PREVIOUS
// attempt of this exact batch left behind.
//
// Why this is necessary, and why it is not a cache bypass: the queue dedupes an
// enqueue on (type, correlation_id) and returns the existing job whenever one
// matches — including a job that already FAILED. The batch correlation is
// `batch_fingerprint + ":" + fingerprint[:16]`, and the batch fingerprint is
// derived purely from the render inputs, so after ONE failed attempt an
// identical, now-correct payload can never be rendered again: the submit is
// answered with the corpse of the previous run. That is a genuine operational
// trap (a transient GPU/path failure poisons the request forever), and it is
// precisely what the fresh-render certificate has to be able to step out of.
//
// Every clip.render job of THIS batch is removed, terminal ones included,
// because this function is only ever called together with the derived-asset
// reset: once the asset a SUCCEEDED job produced is gone, keeping the row does
// not preserve idempotency evidence — it makes the batch deduplicate onto a job
// whose output no longer exists, which is the opposite of a fresh render. The
// idempotency certificate does NOT depend on these rows: it re-submits the
// identical payload and is served by the deterministic render cache, whose rows
// the same reset clears.
func resetRenderCertificateJobs(t *testing.T, cfg *config.Config, fingerprints []string) int {
	t.Helper()
	require.NotNil(t, cfg, "config is required to locate the jobs store")
	jobsPath := cfg.Storage.JobsDBFullPath()
	require.FileExistsf(t, jobsPath, "the jobs store must exist to clear certificate job state (%s)", jobsPath)

	db, err := sql.Open("sqlite3", jobsPath+"?_busy_timeout=5000")
	require.NoErrorf(t, err, "open jobs store %s", jobsPath)
	defer func() { _ = db.Close() }()

	batchID := cliprender.BatchFingerprint(fingerprints)
	// EVERY clip.render job of these fingerprints is cleared, SUCCEEDED ones
	// included. Deleting only the non-succeeded rows was internally
	// inconsistent with the state reset next to it: that reset deletes the
	// derived assets these jobs produced, so a SUCCEEDED row is left claiming an
	// asset that no longer exists — and the batch then DEDUPLICATES onto it and
	// settles instantly against the missing asset instead of rendering. The
	// queue is not the idempotency mechanism for this certificate (the render
	// cache is, and its rows are cleared alongside), so there is nothing to
	// preserve by keeping the terminal rows.
	const q = `DELETE FROM jobs
		WHERE type = 'clip.render'
		  AND correlation_id = ?`
	removed := 0
	for _, fingerprint := range fingerprints {
		res, err := db.ExecContext(context.Background(), q, batchID+":"+fingerprint[:16])
		require.NoErrorf(t, err, "clear failed certificate job for fingerprint %s", fingerprint[:16])
		if affected, err := res.RowsAffected(); err == nil {
			removed += int(affected)
		}
	}
	return removed
}

// editorialPlateBytesSource builds the canonical content-addressed materializer
// the plate bootstrap hashes bytes with.
//
// The fixture directory is NOT sufficient: only drive-background-01 and
// drive-background-05 are checked in (deliberately sharing one digest), so a
// bootstrap limited to local files would register half the catalog and the
// certificate would "pass" over plates that were never verified. The canonical
// materializer hashes a registered fixture, a cached copy, or a fresh Drive
// download, and the bootstrap refuses any digest that is not the certified one.
func editorialPlateBytesSource(t *testing.T, log *zap.Logger) *drive.CanonicalAssetMaterializer {
	t.Helper()
	cfg := liveCertificateConfig(t)

	svc, err := drive.NewDriveServiceFromFiles(context.Background(), cfg)
	require.NoError(t, err, "the plate bootstrap needs usable Drive credentials to materialize and verify every plate")

	scratch := filepath.Join("..", "..", ".tmp", "e2e-editorial-plates")
	require.NoError(t, os.MkdirAll(scratch, 0o755))
	materializer, err := drive.NewCanonicalAssetMaterializer(&drive.Uploader{Service: svc, Log: log}, scratch, log)
	require.NoError(t, err, "canonical asset materializer construction must succeed")
	return materializer
}

// editorialPlateOptions is the bootstrap selection for the certificate: the
// plate the certificate actually renders over.
//
// It is deliberately ONE plate, not the whole catalog. The bootstrap is
// fail-closed on bytes it cannot read, and four of the six canonical plates
// (drive-background-02/03/04/06) are no longer reachable: their certified
// Drive identities return `googleapi: Error 404: File not found`, and their
// normalized fixtures were never checked into RenderingGen/assets/backgrounds
// (only 01 and 05 are, deliberately sharing one digest). Asking for the whole
// catalog therefore fails the certificate on plates it does not use — which is
// the read-boundary gate working, not a test problem. Registering the full
// catalog belongs to the operator path (`register-editorial-assets`) once those
// four source files are restored.
func editorialPlateOptions() wiringmedia.EditorialAssetsOptions {
	return wiringmedia.EditorialAssetsOptions{
		PlateIDs:  []string{editorialBackgroundPlateID},
		PlatesDir: editorialPlatesDir(),
	}
}

// editorialPlatesDir resolves the certified plate fixtures directory. It is an
// optimization, not the authority: the materializer hashes whatever it finds
// there and falls through to the content-addressed cache or Drive otherwise.
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
	registrations, err := wiringmedia.EnsureEditorialAssets(ctx, db, editorialPlateBytesSource(t, log), editorialPlateOptions(), log)
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

	// Fresh certificate: when the caller asks for a reset, the PREVIOUS run's
	// outputs and cache rows are gone before the batch is submitted, so every
	// item below must be QUEUED. Without it a warm cache would serve ten hits
	// and the render itself would never be certified.
	fresh := strings.TrimSpace(os.Getenv("VELOX_E2E_RESET_RENDER_STATE")) != ""
	if fresh {
		removed := resetRenderCertificateState(t, ctx, db, fingerprints)
		staleJobs := resetRenderCertificateJobs(t, liveCertificateConfig(t), fingerprints)
		t.Logf("certificate state reset: %d derived render asset(s) and %d stale clip.render job(s) removed", removed, staleJobs)
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
			require.Falsef(t, fresh,
				"language %s reported CACHED although the certificate state was reset: the fresh-render certificate must render", lang)
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

	// ── 4b. Goal-1 summary: one delivered, GPU, full-length artifact per language.
	//
	// These four counts ARE the Goal-1 acceptance line. They are computed from
	// the durable rows (cache record JOINed to its media asset) rather than from
	// this process's loop, so a batch the queue deduplicated onto an earlier
	// identical one still has to prove four real artifacts per language.
	var derivedCount, gpuCount, fullLengthCount, deliveredCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE c.backend = 'chronon_vulkan'),
		       COUNT(*) FILTER (WHERE a.duration_ms BETWEEN $2 AND $3),
		       COUNT(*) FILTER (WHERE COALESCE(a.drive_file_id, '') <> '')
		FROM clip_render_cache c
		JOIN media_assets a ON a.id = c.asset_id
		WHERE c.fingerprint = ANY(string_to_array($1, ','))
		  AND a.lifecycle_state = 'ACTIVE'
	`, strings.Join(fingerprints, ","),
		int64(liveSegmentEnd)*1000-2000, int64(liveSegmentEnd)*1000+2000,
	).Scan(&derivedCount, &gpuCount, &fullLengthCount, &deliveredCount),
		"Goal-1 summary query must see the certified render records")
	want := len(liveLanguages)
	require.Equalf(t, want, derivedCount, "Goal 1: one certified render per language")
	require.Equalf(t, want, gpuCount, "Goal 1: every render must be Chronon/Vulkan, never a software fallback")
	require.Equalf(t, want, fullLengthCount, "Goal 1: every render must cover the full %ds clip", liveSegmentEnd)
	if realDrive {
		require.Equalf(t, want, deliveredCount, "Goal 1: every final MP4 must carry a Drive-issued file id")
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

	// ── 6. No phantom location rows in the operational store. ──────────
	// The clip committer used to write a 'drive' asset_locations row even when
	// the clip had no Drive file id, producing a primary location with an empty
	// external_id that pointed at nothing (observed live on this very source
	// asset). The writer now emits the row only when a Drive identity exists, and
	// this assertion keeps the operational database honest: a location row must
	// never be a claim without a referent.
	var phantomDriveLocations int
	require.NoErrorf(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM asset_locations WHERE location_kind = 'drive' AND COALESCE(external_id, '') = ''`,
	).Scan(&phantomDriveLocations), "count phantom drive locations")
	require.Zerof(t, phantomDriveLocations,
		"the operational store must carry no 'drive' location row without a Drive file id")

	t.Logf("RENDER CERTIFICATE OK: source=%s plate=%s languages=%d assets=%d phantom_drive_locations=%d",
		sourceAssetID, editorialBackgroundPlateID, len(liveLanguages), len(assetByLanguage), phantomDriveLocations)
}

// resetRenderCertificateState deletes the derived render assets of a PREVIOUS
// certificate run together with their deterministic cache rows.
//
// It exists so the fresh-render certificate is unambiguous: with a warm cache
// the batch would answer ten CACHED and prove nothing about rendering (and, as
// the first Goal-1 run showed, ten CACHED assets that were never delivered to
// Drive). Deleting only cache rows AND the derived assets they point at keeps
// the source clip, the text tracks and the editorial plates untouched — the
// certificate rebuilds its own outputs from state it does not own.
//
// The predicate is deliberately narrow (category = 'clip-render' AND
// source = 'clip.render'): a row the certificate did not derive is never
// deleted, even if a fingerprint happened to collide.
func resetRenderCertificateState(t *testing.T, ctx context.Context, db *sql.DB, fingerprints []string) int {
	t.Helper()
	removed := 0
	for _, fingerprint := range fingerprints {
		var assetID string
		err := db.QueryRowContext(ctx,
			`SELECT asset_id FROM clip_render_cache WHERE fingerprint = $1`, fingerprint).Scan(&assetID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("read cache row %s: %v", fingerprint, err)
		}
		if _, err := db.ExecContext(ctx,
			`DELETE FROM clip_render_cache WHERE fingerprint = $1`, fingerprint); err != nil {
			t.Fatalf("delete cache row %s: %v", fingerprint, err)
		}
		if assetID == "" {
			continue
		}
		res, err := db.ExecContext(ctx, `
			DELETE FROM media_assets
			WHERE id = $1 AND category = 'clip-render' AND source = 'clip.render'`, assetID)
		if err != nil {
			t.Fatalf("delete derived asset %s: %v", assetID, err)
		}
		if affected, err := res.RowsAffected(); err == nil && affected > 0 {
			removed++
		}
		// A TERMINAL Drive-delivery row must go with the asset it delivered.
		// Derived asset ids are content-addressed, so re-rendering the same
		// request reproduces the SAME id — and the delivery enqueue is then
		// suppressed as a duplicate of the already-terminal row, leaving the
		// fresh asset permanently undelivered (drive_file_id empty, no Drive
		// file) while every gate reports success. The row is only evidence while
		// the asset it describes exists, which is precisely what the delete above
		// removes.
		if _, err := db.ExecContext(ctx, `
			DELETE FROM outbox_events
			WHERE aggregate_id = $1
			  AND event_type = 'clip.render.drive_delivery.requested.v1'`, assetID); err != nil {
			t.Fatalf("delete terminal Drive-delivery rows for %s: %v", assetID, err)
		}
	}
	return removed
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

	source := editorialPlateBytesSource(t, log)
	options := editorialPlateOptions()
	first, err := wiringmedia.EnsureEditorialAssets(ctx, db, source, options, log)
	require.NoError(t, err)
	second, err := wiringmedia.EnsureEditorialAssets(ctx, db, source, options, log)
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

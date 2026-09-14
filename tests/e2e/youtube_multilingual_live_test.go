// Package e2e — youtube_multilingual_live_test.go: the REAL end-to-end
// certificate for the YouTube → transcript → 10-language translation →
// PostgreSQL → search_text → embedding chain.
//
// This test is deliberately NOT hermetic. It is HARD-GATED behind
// VELOX_E2E_LIVE=1 so `go test ./...` stays hermetic (godlike/07: no fake
// availability — a live test that silently degrades to a mock is worse than
// no test).
//
// What it actually does (no mocks on the critical path):
//
//  1. Downloads ONE MINUTE of a real YouTube video with yt-dlp
//     (download-once: a single yt-dlp invocation, section-limited).
//  2. Fetches the video's real English subtitles with yt-dlp and parses them
//     through the canonical VTT parser.
//  3. Commits clip + READY transcript into PostgreSQL via the canonical
//     PostgresMediaCommitter.CommitClipTextAndIndexEvent (one PG tx).
//  4. Runs the REAL TextTrackMaterializer against the REAL PostgreSQL text
//     track repository with the REAL Ollama translator and the configured
//     10-language set (it, en, pl, ru, de, es, pt-BR, fr, tr, id).
//  5. Asserts 10 READY transcript rows with correct provenance, and that a
//     second identical run SKIPS (translation_key dedupe).
//  6. Rebuilds media_assets.search_text (multilingual) and requests the
//     reindex through the PostgreSQL outbox.
//  7. Drains the event with the canonical PostgresIndexWorker and the REAL
//     E5 embedding sidecar, then asserts the embedded text contains a
//     translated phrase — the proof that the translations are not merely
//     stored but INDEXED.
//
// Required environment:
//
//	TEST_POSTGRES_DSN        live PostgreSQL + pgvector DSN (see docker-compose.test-postgres.yml)
//	VELOX_E2E_LIVE=1         enables this test
//
// Optional environment:
//
//	VELOX_E2E_YOUTUBE_URL        default https://www.youtube.com/watch?v=iHaK0M-207o
//	VELOX_E2E_YTDLP              default "yt-dlp" (may be a multi-word command)
//	VELOX_E2E_WORKDIR            default ".tmp/e2e-youtube-live"
//	VELOX_E2E_OLLAMA_URL         default http://localhost:11434
//	VELOX_E2E_OLLAMA_MODEL       default gemma4:e2b
//	VELOX_E2E_EMBED_SERVER_URL   default http://127.0.0.1:8001
//	VELOX_E2E_FORCE_DOWNLOAD=1   re-download even when the cached artifacts exist
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"

	wiringmedia "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	localized "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	ollamapkg "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"

	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// liveLanguages is the canonical configured translation set (config.yaml
// media.multilingual.languages with translate_clips: true).
var liveLanguages = []string{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}

const (
	liveDefaultURL    = "https://www.youtube.com/watch?v=iHaK0M-207o"
	liveSegmentStart  = 0
	liveSegmentEnd    = 60
	liveSourceLang    = "en"
	liveEmbeddingDims = 768
)

func liveEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// requireLiveE2E gates the suite and returns the work directory.
func requireLiveE2E(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(os.Getenv("VELOX_E2E_LIVE")) == "" {
		t.Skip("VELOX_E2E_LIVE not set; skipping the live YouTube multilingual E2E (real download + Ollama + pgvector)")
	}
	if strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN")) == "" {
		t.Skip("TEST_POSTGRES_DSN not set; the live E2E needs the PostgreSQL + pgvector test instance")
	}
	workdir := liveEnv("VELOX_E2E_WORKDIR", filepath.Join(".tmp", "e2e-youtube-live"))
	require.NoError(t, os.MkdirAll(workdir, 0o755))
	return workdir
}

func openLiveDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx), "ping PostgreSQL test instance")

	migrations := []string{
		pgmigration.MediaSchemaDDL,
		pgmigration.MediaVectorSurfacesDDL,
		pgmigration.MediaHNSWIndexesDDL,
		pgmigration.MediaTimestampsTimestamptzDDL,
		pgmigration.MediaAssetVersionsDDL,
		pgmigration.MediaAssetProcessingDDL,
	}
	for i, ddl := range migrations {
		_, err := db.ExecContext(ctx, ddl)
		require.NoErrorf(t, err, "apply media migration %d", i+1)
	}
	for _, stmt := range []string{
		`TRUNCATE asset_text_track_segments, asset_text_tracks`,
		`TRUNCATE outbox_events`,
		`TRUNCATE media_embeddings`,
		`TRUNCATE media_assets CASCADE`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoErrorf(t, err, "truncate: %s", stmt)
	}
	return db
}

// runYtDlp executes the configured yt-dlp command and returns its stdout.
func runYtDlp(t *testing.T, args ...string) string {
	t.Helper()
	cmdline := strings.Fields(liveEnv("VELOX_E2E_YTDLP", "yt-dlp"))
	require.NotEmpty(t, cmdline, "VELOX_E2E_YTDLP resolved to an empty command")
	cmd := exec.Command(cmdline[0], append(cmdline[1:], args...)...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "yt-dlp %v failed: %s", args, string(out))
	return string(out)
}

// downloadOneMinute implements the download-once contract: ONE yt-dlp call
// that fetches only the configured section of the source video.
func downloadOneMinute(t *testing.T, workdir, url, videoID string) string {
	t.Helper()
	mp4 := filepath.Join(workdir, videoID+".mp4")
	if _, err := os.Stat(mp4); err == nil && os.Getenv("VELOX_E2E_FORCE_DOWNLOAD") == "" {
		t.Logf("reusing cached download %s", mp4)
		return mp4
	}
	section := fmt.Sprintf("*00:00:%02d-00:%02d:%02d", liveSegmentStart/60, liveSegmentEnd/60, liveSegmentEnd%60)
	if liveSegmentEnd < 60 {
		section = fmt.Sprintf("*00:00:00-00:00:%02d", liveSegmentEnd)
	}
	start := time.Now()
	runYtDlp(t,
		"-f", "bestvideo*+bestaudio/best",
		"--merge-output-format", "mp4",
		"--download-sections", section,
		"--force-keyframes-at-cuts",
		"-o", filepath.Join(workdir, "%(id)s.%(ext)s"),
		url,
	)
	t.Logf("downloaded %ds of source in %s", liveSegmentEnd-liveSegmentStart, time.Since(start).Round(time.Second))
	require.FileExists(t, mp4)
	return mp4
}

// fetchEnglishSubtitles downloads the real English subtitle track (manual or
// auto) and returns the VTT path.
func fetchEnglishSubtitles(t *testing.T, workdir, url, videoID string) string {
	t.Helper()
	vtt := filepath.Join(workdir, videoID+".en.vtt")
	if _, err := os.Stat(vtt); err == nil && os.Getenv("VELOX_E2E_FORCE_DOWNLOAD") == "" {
		return vtt
	}
	runYtDlp(t,
		"--skip-download",
		"--write-subs", "--write-auto-subs",
		"--sub-langs", "en.*",
		"--convert-subs", "vtt",
		"-o", filepath.Join(workdir, "%(id)s.%(ext)s"),
		url,
	)
	require.FileExists(t, vtt, "expected the English VTT subtitle track next to the video")
	return vtt
}

// sourceVideoIdentity asks yt-dlp for the real title/uploader.
func sourceVideoIdentity(t *testing.T, url string) (string, string) {
	t.Helper()
	out := runYtDlp(t, "--skip-download", "--print", "%(title)s|||%(uploader)s", url)
	line := strings.TrimSpace(out)
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = line[i+1:]
	}
	parts := strings.SplitN(line, "|||", 2)
	if len(parts) != 2 {
		return "YouTube clip", "unknown-channel"
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// sliceBundleToSegment parses the full-video VTT and keeps the cues that fall
// inside the downloaded section.
func sliceBundleToSegment(t *testing.T, vttPath string) (string, []detail.TimedCue) {
	t.Helper()
	_, cues, err := texttracks.ParseSubtitleFile(vttPath)
	require.NoError(t, err, "parse VTT %s", vttPath)

	limitMs := int64(liveSegmentEnd) * 1000
	var kept []detail.TimedCue
	var sb strings.Builder
	for _, cue := range cues {
		if cue.StartMs >= limitMs {
			continue
		}
		// YouTube auto-captions emit positioning/alignment cues that carry no
		// text. The canonical committer rejects empty text, so they are dropped
		// here — the same filtering the production subtitle resolver applies.
		if strings.TrimSpace(cue.Text) == "" {
			continue
		}
		kept = append(kept, cue)
		sb.WriteString(cue.Text)
		sb.WriteString(" ")
	}
	text := strings.Join(strings.Fields(sb.String()), " ")
	require.NotEmpty(t, text, "the first %ds of the video must carry subtitle text", liveSegmentEnd)
	return text, kept
}

// stubOutbox satisfies texttracks.OutboxEnqueuer. In this test the PostgreSQL
// IndexRequester port is wired, so a call here would be a wiring regression —
// the stub FAILS the test if the materializer ever falls back to the
// operational SQLite outbox.
type stubOutbox struct {
	t *testing.T
}

func (s stubOutbox) Enqueue(context.Context, *sql.Tx, string, string, string, string, string) (*outboxevents.EnqueueResult, error) {
	s.t.Errorf("materializer fell back to the operational SQLite outbox; the PostgreSQL reindex port must be used")
	return nil, fmt.Errorf("sqlite outbox must not be used")
}

// recordingEmbedder wraps the REAL E5 sidecar client and records the text it
// embedded, so the test can prove the translations entered the embedding
// input (not just the database).
type recordingEmbedder struct {
	inner assetpkg.Embedder
	mu    sync.Mutex
	texts []string
}

func (r *recordingEmbedder) Embed(ctx context.Context, text string) (assetpkg.EmbeddingResult, error) {
	r.mu.Lock()
	r.texts = append(r.texts, text)
	r.mu.Unlock()
	return r.inner.Embed(ctx, text)
}

func (r *recordingEmbedder) embeddedTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...)
}

func TestLiveYouTube_TranscriptTranslatedInTenLanguagesAndIndexed(t *testing.T) {
	workdir := requireLiveE2E(t)
	db := openLiveDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	url := liveEnv("VELOX_E2E_YOUTUBE_URL", liveDefaultURL)
	videoID := videoIDFromURL(url)
	require.NotEmpty(t, videoID, "could not resolve a YouTube video id from %q", url)

	log := zaptest.NewLogger(t, zaptest.Level(zapcore.InfoLevel))

	// ── 1. Real download (ONE minute, ONE yt-dlp invocation). ──────────
	mp4 := downloadOneMinute(t, workdir, url, videoID)
	if info, err := os.Stat(mp4); err == nil {
		t.Logf("source artifact: %s (%d bytes)", mp4, info.Size())
	}

	// ── 2. Real English subtitles → canonical VTT parse. ───────────────
	vtt := fetchEnglishSubtitles(t, workdir, url, videoID)
	transcript, cues := sliceBundleToSegment(t, vtt)
	t.Logf("source transcript: %d chars, %d cues", len(transcript), len(cues))

	title, channel := sourceVideoIdentity(t, url)
	t.Logf("source identity: %q by %q", title, channel)

	// ── 3. Canonical PostgreSQL commit (clip + READY transcript). ──────
	clipID := fmt.Sprintf("yt_%s_%d_%d_v1", videoID, liveSegmentStart, liveSegmentEnd)
	committer, err := wiringmedia.NewPostgresMediaCommitterFromDB(db, log)
	require.NoError(t, err)

	textHash := detail.TextHash(transcript, liveSourceLang, detail.TextTrackTranscript)
	sourceTrack := detail.TextTrack{
		AssetID:            clipID,
		LanguageCode:       liveSourceLang,
		TextKind:           detail.TextTrackTranscript,
		TextContent:        transcript,
		SourceType:         detail.TextSourceYouTubeSubtitle,
		SourceLanguageCode: liveSourceLang,
		IsOriginal:         true,
		Provider:           "yt-dlp",
		ModelName:          "youtube-subtitles",
		ModelVersion:       "v1",
		TextHash:           textHash,
		SourceVersion:      detail.SourceVersion(textHash, liveSourceLang, liveSourceLang, "yt-dlp", "youtube-subtitles", "v1", ""),
		Status:             detail.TextTrackReady,
		IsCurrent:          true,
	}

	clipAsset := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       videoID,
		LocalPath:     mp4,
		LegacyFileMD5: fmt.Sprintf("%064x", len(transcript)),
		SearchText:    title + " " + channel,
		Drive:         youtubetypes.ClipAssetDrive{FolderID: "", FolderPath: ""},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: liveSegmentStart,
			EndSec:   liveSegmentEnd,
			Duration: liveSegmentEnd - liveSegmentStart,
		},
		PolicyVersion: "v1",
		Metadata: youtubetypes.CanonicalClipMetadata{
			ClipID:          clipID,
			AssetID:         clipID,
			Title:           title,
			Summary:         fmt.Sprintf("%s — first %ds", title, liveSegmentEnd-liveSegmentStart),
			Description:     fmt.Sprintf("%s by %s", title, channel),
			SourceURL:       url,
			SourceProvider:  "youtube",
			VideoID:         videoID,
			SourceTitle:     title,
			SourceChannel:   channel,
			ClipStartSec:    liveSegmentStart,
			ClipEndSec:      liveSegmentEnd,
			ClipDurationSec: liveSegmentEnd - liveSegmentStart,
			PolicyVersion:   "v1",
			SourceVersion:   "live-e2e-v1",
		},
	}

	require.NoError(t, committer.CommitClipTextAndIndexEvent(ctx, localized.CommitLocalizedClipCommand{
		Clip:       clipAsset,
		TextTracks: []detail.TextTrack{sourceTrack},
		TimedTracks: []localized.TimedTextTrack{{
			LanguageCode: liveSourceLang,
			TextKind:     detail.TextTrackTranscript,
			SourceType:   detail.TextSourceYouTubeSubtitle,
			Cues:         cues,
		}},
		IndexEvent: youtubeports.IndexEventPayload{AggregateID: clipID, CreatedAt: time.Now().UTC()},
	}))

	// ── 4. Real materializer: real PG repo + real Ollama translator. ───
	repo, err := pgmedia.NewTextTrackRepositoryPG(db)
	require.NoError(t, err)

	registry, err := assetpkg.NewLanguageRegistryFromCodes(liveLanguages)
	require.NoError(t, err)

	ollamaURL := liveEnv("VELOX_E2E_OLLAMA_URL", "http://localhost:11434")
	ollamaModel := liveEnv("VELOX_E2E_OLLAMA_MODEL", "gemma4:e2b")
	client := ollamaclient.NewClient(ollamaURL, ollamaModel, 600)
	translator := translation.NewOllamaTranslator(ollamapkg.NewGenerator(client), log)

	materializer, err := texttracks.NewMaterializer(repo, translator, stubOutbox{t: t}, texttracks.ResolverConfig{
		Registry:         registry,
		SourceLanguage:   liveSourceLang,
		ModelVersion:     "live-e2e-" + ollamaModel,
		PromptVersion:    "v1",
		TranslationModel: "ollama",
		OllamaModel:      ollamaModel,
	}, log)
	require.NoError(t, err)
	materializer.SetConcurrency(4)

	// The post-translation index seam under test.
	materializer.SetIndexRequester(pgmedia.NewReindexRequester(db))
	materializer.SetSearchTextRebuilder(pgmedia.NewSearchTextRebuilder(db, strings.Join(liveLanguages, ",")).WithLogger(log))

	translateStart := time.Now()
	report, err := materializer.Materialize(ctx, clipID, liveSourceLang, textHash, detail.TextTrackTranscript, nil)
	require.NoError(t, err)
	t.Logf("translation fan-out: created=%v skipped=%v failed=%v in %s",
		report.CreatedLanguages, report.SkippedLanguages, report.FailedLanguages,
		time.Since(translateStart).Round(time.Second))
	require.Empty(t, report.FailedLanguages, "every configured language must translate")

	// ── 5. 10 READY transcript rows, one per configured language. ──────
	assertTranscriptLanguages(t, db, clipID, liveLanguages)

	// ── 6. Second identical run must SKIP (translation_key dedupe). ────
	second, err := materializer.Materialize(ctx, clipID, liveSourceLang, textHash, detail.TextTrackTranscript, nil)
	require.NoError(t, err)
	require.Empty(t, second.CreatedLanguages, "a second identical run must not retranslate")
	require.Empty(t, second.FailedLanguages)
	require.Len(t, second.SkippedLanguages, len(liveLanguages)-1,
		"every non-source language must be reused from the translation_key gate")

	// ── 7. Multilingual search_text + PostgreSQL outbox reindex. ───────
	var searchText string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT search_text FROM media_assets WHERE id = $1`, clipID).Scan(&searchText))
	require.Contains(t, searchText, transcript[:min(len(transcript), 40)], "the original transcript must be in search_text")

	italianText := transcriptLanguage(t, db, clipID, "it")
	require.Contains(t, searchText, firstWords(italianText, 40),
		"the Italian translation must be in media_assets.search_text")

	var pendingEvents int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE event_type = 'asset.index.requested' AND aggregate_id = $1
	`, clipID).Scan(&pendingEvents))
	require.GreaterOrEqual(t, pendingEvents, 1, "the reindex request must be in the PostgreSQL outbox")

	// ── 8. Real E5 embedding of the rebuilt text, then INDEXED. ────────
	embedURL := liveEnv("VELOX_E2E_EMBED_SERVER_URL", "http://127.0.0.1:8001")
	vectors := pgmedia.NewVectorSurfaceWriter(db)
	require.NoError(t, vectors.EnsureEmbeddingFamily(ctx, "text", coreembedding.ModelIDMultilingualE5, liveEmbeddingDims))
	embedder := &recordingEmbedder{inner: embeddings.NewHTTPTextEmbedder(embedURL)}
	worker := pgmedia.NewPostgresIndexWorker(
		pgmedia.NewOutboxRepository(db),
		vectors,
		pgmedia.NewEmbedAssetTextAdapter(db, embedder),
		coreembedding.ModelIDMultilingualE5,
	)

	drained := drainOutbox(t, db, worker, clipID)
	require.GreaterOrEqual(t, drained, 1, "at least one index event must be drained")

	texts := embedder.embeddedTexts()
	require.NotEmpty(t, texts, "the E5 sidecar must have been called")
	joined := strings.Join(texts, "\n")
	require.Contains(t, joined, firstWords(italianText, 40),
		"the embedded text MUST contain the translated transcript — this is what makes the translation searchable")

	var state string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT index_state FROM media_assets WHERE id = $1`, clipID).Scan(&state))
	require.Equal(t, "INDEXED", state, "the asset must reach INDEXED")

	var dims int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT vector_dims(embedding) FROM media_embeddings
		WHERE asset_id = $1 AND embedding_type = 'text' AND model_id = $2
	`, clipID, coreembedding.ModelIDMultilingualE5).Scan(&dims))
	require.Equal(t, liveEmbeddingDims, dims)

	t.Logf("LIVE E2E OK: clip=%s languages=%d INDEXED=%s embedding_dims=%d", clipID, len(liveLanguages), state, dims)
}

// assertTranscriptLanguages pins the 10-language matrix plus provenance.
func assertTranscriptLanguages(t *testing.T, db *sql.DB, clipID string, want []string) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `
		SELECT language_code, is_original, source_type, source_language_code, provider, status, translation_key
		FROM asset_text_tracks
		WHERE asset_id = $1 AND text_kind = 'transcript' AND is_current = 1
		ORDER BY language_code
	`, clipID)
	require.NoError(t, err)
	defer rows.Close()

	found := map[string]bool{}
	for rows.Next() {
		var lang, sourceType, srcLang, provider, status, translationKey string
		var isOriginal int
		require.NoError(t, rows.Scan(&lang, &isOriginal, &sourceType, &srcLang, &provider, &status, &translationKey))
		found[lang] = true
		require.Equal(t, "READY", status, "language %s must be READY", lang)
		if lang == liveSourceLang {
			require.Equal(t, 1, isOriginal, "the source language must be flagged original")
			continue
		}
		require.Equal(t, "translation", sourceType, "language %s must be a translation", lang)
		require.Equal(t, liveSourceLang, srcLang, "language %s must record its source language", lang)
		require.NotEmpty(t, provider, "language %s must record its provider", lang)
		require.NotEmpty(t, translationKey, "language %s must carry a translation_key", lang)
	}
	require.NoError(t, rows.Err())
	require.Len(t, found, len(want), "expected %d READY transcript languages, got %v", len(want), found)
	for _, lang := range want {
		require.True(t, found[lang], "missing READY transcript for configured language %q (got %v)", lang, found)
	}
}

func transcriptLanguage(t *testing.T, db *sql.DB, clipID, lang string) string {
	t.Helper()
	var text string
	require.NoError(t, db.QueryRowContext(context.Background(), `
		SELECT text_content FROM asset_text_tracks
		WHERE asset_id = $1 AND language_code = $2 AND text_kind = 'transcript' AND is_current = 1
	`, clipID, lang).Scan(&text))
	require.NotEmpty(t, text, "missing transcript for %s", lang)
	return text
}

// drainOutbox claims and handles the asset's index events until none are
// pending, returning how many events were handled.
func drainOutbox(t *testing.T, db *sql.DB, worker *pgmedia.PostgresIndexWorker, clipID string) int {
	t.Helper()
	ctx := context.Background()
	repo := pgmedia.NewOutboxRepository(db)
	handled := 0
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		claim, err := repo.ClaimNext(ctx, "live-e2e-worker", time.Minute)
		require.NoError(t, err)
		if claim == nil {
			break
		}
		require.Equal(t, clipID, claim.Event.AggregateID, "unexpected event for another aggregate")
		require.NoError(t, worker.Handle(ctx, claim))
		handled++
	}
	return handled
}

// videoIDFromURL extracts the v= parameter (or youtu.be path) from a URL.
func videoIDFromURL(raw string) string {
	u := strings.TrimSpace(raw)
	if i := strings.Index(u, "youtu.be/"); i >= 0 {
		id := u[i+len("youtu.be/"):]
		if j := strings.IndexAny(id, "?&/"); j >= 0 {
			id = id[:j]
		}
		return id
	}
	if i := strings.Index(u, "v="); i >= 0 {
		id := u[i+2:]
		if j := strings.IndexAny(id, "&/"); j >= 0 {
			id = id[:j]
		}
		return id
	}
	return ""
}

// firstWords returns the first n whitespace-separated words (bounded for
// robustness against trailing punctuation/format differences).
func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

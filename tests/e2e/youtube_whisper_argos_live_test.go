// Package e2e — youtube_whisper_argos_live_test.go: the Whisper-source
// certificate.
//
// WHY THIS TEST EXISTS (independent of youtube_multilingual_live_test.go):
// the older live test sources the transcript from a real YouTube VTT and
// translates with Ollama. That is a DIFFERENT chain from the one the product
// contract requires:
//
//	YouTube 60s -> audio -> LOCAL WHISPER -> timed transcript -> PostgreSQL
//	  -> ARGOS (local) -> 10 languages -> timed cues -> 10 ASS -> Drive
//
// In that older test a YouTube caption track satisfies acquisition before
// Whisper is ever reached, so a green result there says nothing about the
// Whisper path or about Argos. This test therefore asserts provenance, not
// just shape:
//
//   - the acquisition source MUST be `whisper` (source_type=whisper);
//   - no YouTube subtitle track may supply the transcript;
//   - all nine translations MUST carry provider=argos;
//   - the source transcript MUST carry TIMED cues (Whisper segments), because
//     an untimed transcript cannot produce a subtitle artifact;
//   - every configured language MUST yield a structurally valid ASS artifact
//     through the canonical delivery step, each published under the SAME
//     per-video Drive destination.
//
// Hard-gated behind VELOX_E2E_LIVE=1 + TEST_POSTGRES_DSN (godlike/07: a live
// test that silently degrades to a mock is worse than no test).
//
// Required environment:
//
//	TEST_POSTGRES_DSN         live PostgreSQL + pgvector DSN
//	VELOX_E2E_LIVE=1          enables this test
//
// Optional environment:
//
//	VELOX_E2E_YOUTUBE_URL        source video (must have NO usable captions)
//	VELOX_E2E_WORKDIR            download cache directory
//	VELOX_E2E_WHISPER_PYTHON     default ../../.venv-whisper/bin/python3
//	VELOX_E2E_WHISPER_SCRIPT     default ../../scripts/bridges/whisper_transcriber.py
//	VELOX_E2E_WHISPER_MODEL      default base (faster-whisper size alias)
//	VELOX_E2E_ARGOS_PYTHON       default ../../.venv-argos/bin/python3
//	VELOX_E2E_ARGOS_SCRIPTS_DIR  default ../../scripts
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	localized "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	sqlitetexttracks "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/texttracks"
	ytwhisper "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"

	wiringmedia "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
)

// ── Drive publication: contract mode and real mode ─────────────────────────
//
// default (VELOX_E2E_LIVE=1 only): the destination CONTRACT is what the
// regression was about (the .ass landing in the wrong tree), so the publisher
// records the exact destination it was asked for and returns a deterministic
// file id + link. The materializer treats an empty file id or link as a hard
// failure, so a recording publisher still pins the "Drive reference recorded"
// invariant — but it does NOT prove an upload happened.
//
// VELOX_E2E_REAL_DRIVE=1 upgrades that leg to a REAL upload: the publisher is
// the same one the server composition builds (drive.NewPublisher over the
// production credentials + destination registry), the subtitle layout is the
// production wiring.NewSubtitleRootLayoutResolver, the artifact registry is the
// real SQLite asset_subtitle_artifacts repository, and every Drive file id the
// delivery records is read BACK from the Drive API. The gate is deliberately
// separate from VELOX_E2E_LIVE so a plain live run can never write to the
// production Drive.

type recordedUpload struct {
	FolderID  string
	Subpath   []string
	Filename  string
	LocalPath string
}

type recordingPublisher struct {
	mu      sync.Mutex
	uploads []recordedUpload
}

func (p *recordingPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uploads = append(p.uploads, recordedUpload{
		FolderID:  req.DestinationFolderID,
		Subpath:   append([]string(nil), req.DestinationSubpath...),
		Filename:  req.Filename,
		LocalPath: req.LocalPath,
	})
	id := fmt.Sprintf("e2e-drive-file-%02d", len(p.uploads))
	return &delivery.PublishResult{
		FileID:      id,
		WebViewLink: "https://drive.example.test/" + id,
	}, nil
}

func (p *recordingPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	return req.DestinationFolderID, nil
}

func (p *recordingPublisher) recorded() []recordedUpload {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedUpload(nil), p.uploads...)
}

// recordedResult is what a publisher RETURNED for one publication: in real
// mode these are Drive-issued ids, which is what turns "the destination
// contract held" into "the artifact exists in Drive".
type recordedResult struct {
	Filename    string
	FileID      string
	WebViewLink string
}

// publisherProbe records the destination (before delegating, so a failing real
// upload still shows which target it failed for) AND the returned Drive
// identity, then delegates. Wrapping — rather than replacing — the publisher is
// what lets the SAME assertions run in both modes.
type publisherProbe struct {
	inner delivery.Publisher

	mu      sync.Mutex
	uploads []recordedUpload
	results []recordedResult
}

func (p *publisherProbe) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.mu.Lock()
	p.uploads = append(p.uploads, recordedUpload{
		FolderID:  req.DestinationFolderID,
		Subpath:   append([]string(nil), req.DestinationSubpath...),
		Filename:  req.Filename,
		LocalPath: req.LocalPath,
	})
	p.mu.Unlock()

	res, err := p.inner.Publish(ctx, req)
	if err != nil {
		return res, err
	}
	if res != nil {
		p.mu.Lock()
		p.results = append(p.results, recordedResult{
			Filename:    req.Filename,
			FileID:      res.FileID,
			WebViewLink: res.WebViewLink,
		})
		p.mu.Unlock()
	}
	return res, nil
}

func (p *publisherProbe) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	return p.inner.ResolveFolder(ctx, req)
}

func (p *publisherProbe) recorded() []recordedUpload {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedUpload(nil), p.uploads...)
}

func (p *publisherProbe) published() []recordedResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedResult(nil), p.results...)
}

// ── publication seam selection ──────────────────────────────────────────────

const e2eFakeClipRootID = "e2e-clip-folder-id"

// realDriveEnabled reports whether the certificate must perform REAL Drive
// uploads. Separate from VELOX_E2E_LIVE on purpose (godlike/07): the live flag
// gates network + DB access, this one gates writes to the production Drive.
func realDriveEnabled() bool {
	return strings.TrimSpace(os.Getenv("VELOX_E2E_REAL_DRIVE")) != ""
}

// driveTarget is the publication + registry seam under test.
type driveTarget struct {
	probe           *publisherProbe
	subRepo         detail.SubtitleArtifactRepository
	subtitleFolders texttracks.SubtitleFolderResolver
	clipsRoot       string
	subtitleRoot    string
	real            bool
	client          *gdrive.Service
}

// newDriveTarget builds either the recorded contract seam (default) or the real
// production publication seam (VELOX_E2E_REAL_DRIVE=1).
func newDriveTarget(t *testing.T, log *zap.Logger, videoID string) driveTarget {
	t.Helper()

	if !realDriveEnabled() {
		return driveTarget{
			probe:           &publisherProbe{inner: &recordingPublisher{}},
			subRepo:         newMemSubtitleRepo(),
			subtitleFolders: fixedSubtitleResolver{root: e2eFakeClipRootID, child: []string{"youtube_subtitles", videoID}},
			clipsRoot:       e2eFakeClipRootID,
			subtitleRoot:    e2eFakeClipRootID,
		}
	}

	cfgPath := liveEnv("VELOX_E2E_CONFIG", filepath.Join("..", "..", "config.yaml"))
	resolved, err := config.GetResolvedFromPath(cfgPath)
	require.NoErrorf(t, err, "VELOX_E2E_REAL_DRIVE=1 needs the production config at %s", cfgPath)
	cfg := resolved.View()
	require.NotNil(t, cfg)
	require.NotEmpty(t, cfg.Drive.YouTubeSubtitlesFolder(),
		"the production subtitle root must be configured for the real-Drive certificate")

	client, err := drive.NewDriveServiceFromFiles(context.Background(), cfg)
	require.NoError(t, err, "VELOX_E2E_REAL_DRIVE=1 needs usable Drive credentials")
	require.NotNil(t, client)

	// The SAME construction BuildDriveBundle performs for the server: destination
	// registry + folder manager + uploader into the canonical Publisher.
	pub, err := drive.NewPublisher(
		delivery.NewDestinationRegistry(cfg),
		drive.NewDriveFolderManagerAdapter(client, log),
		&drive.Uploader{Service: client, Log: log},
		log,
	)
	require.NoError(t, err, "canonical Drive publisher construction must succeed")

	t.Logf("REAL Drive mode: subtitle root=%s clips root=%s",
		cfg.Drive.YouTubeSubtitlesFolder(), cfg.Drive.ClipsFolder())

	return driveTarget{
		probe:           &publisherProbe{inner: pub},
		subRepo:         newSQLiteSubtitleRepo(t, log),
		subtitleFolders: wiring.NewSubtitleRootLayoutResolver(cfg.Drive.YouTubeSubtitlesFolder()),
		clipsRoot:       cfg.Drive.ClipsFolder(),
		subtitleRoot:    cfg.Drive.YouTubeSubtitlesFolder(),
		real:            true,
		client:          client,
	}
}

// newSQLiteSubtitleRepo opens the REAL asset_subtitle_artifacts repository (the
// canonical SQLite implementation the server wires) over a temp database, so
// the "registry holds a current READY row with a Drive reference" check runs
// against production code instead of a map.
func newSQLiteSubtitleRepo(t *testing.T, log *zap.Logger) detail.SubtitleArtifactRepository {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "subtitle-artifacts.sqlite")
	db, err := sql.Open("sqlite3", dsn+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ddl, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", "175_asset_subtitle_artifacts.sql"))
	require.NoError(t, err, "read the canonical subtitle-artifact DDL")
	_, err = db.Exec(string(ddl))
	require.NoError(t, err, "apply the canonical subtitle-artifact DDL")

	repo, err := sqlitetexttracks.NewSubtitleArtifactRepository(db, log)
	require.NoError(t, err)
	return repo
}

// ── in-memory subtitle artifact registry ────────────────────────────────────

type memSubtitleRepo struct {
	mu    sync.Mutex
	byKey map[string]*detail.SubtitleArtifact
}

func newMemSubtitleRepo() *memSubtitleRepo {
	return &memSubtitleRepo{byKey: map[string]*detail.SubtitleArtifact{}}
}

func subtitleKey(assetID, language string, format detail.SubtitleFormat) string {
	return assetID + "|" + language + "|" + string(format)
}

func (r *memSubtitleRepo) Upsert(_ context.Context, art *detail.SubtitleArtifact) error {
	if art == nil {
		return fmt.Errorf("memSubtitleRepo: nil artifact")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[subtitleKey(art.AssetID, art.LanguageCode, art.Format)] = art
	return nil
}

func (r *memSubtitleRepo) FindCurrent(_ context.Context, assetID, languageCode string, format detail.SubtitleFormat) (*detail.SubtitleArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	art := r.byKey[subtitleKey(assetID, languageCode, format)]
	if art == nil || !art.IsCurrent {
		return nil, nil
	}
	cp := *art
	return &cp, nil
}

func (r *memSubtitleRepo) ListByAsset(_ context.Context, assetID string) ([]detail.SubtitleArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []detail.SubtitleArtifact
	for _, art := range r.byKey {
		if art.AssetID == assetID {
			out = append(out, *art)
		}
	}
	return out, nil
}

// fixedSubtitleResolver publishes into <root>/<videoID>/ — the same layout the
// production NewSubtitleRootLayoutResolver produces.
type fixedSubtitleResolver struct {
	root  string
	child []string
}

func (r fixedSubtitleResolver) ResolveSubtitleLocation(_ context.Context, _, _ string) (texttracks.SubtitleLocation, error) {
	return texttracks.SubtitleLocation{FolderID: r.root, Subpath: r.child}, nil
}

// ── ASS structural validation ───────────────────────────────────────────────

func validateASSArtifact(t *testing.T, path string, clipDurationMs int64) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read %s", path)
	require.True(t, utf8.Valid(raw), "ASS file must be valid UTF-8: %s", path)
	content := string(raw)

	for _, section := range []string{"[Script Info]", "[V4+ Styles]", "[Events]"} {
		require.Containsf(t, content, section, "ASS %s is missing %s", path, section)
	}

	var cues int
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Dialogue:") {
			continue
		}
		cues++
		fields := strings.SplitN(strings.TrimPrefix(line, "Dialogue:"), ",", 4)
		require.GreaterOrEqualf(t, len(fields), 4, "malformed Dialogue line in %s: %q", path, line)
		start, ok := parseASSTimestamp(strings.TrimSpace(fields[1]))
		require.Truef(t, ok, "unparseable Dialogue start in %s: %q", path, fields[1])
		end, ok := parseASSTimestamp(strings.TrimSpace(fields[2]))
		require.Truef(t, ok, "unparseable Dialogue end in %s: %q", path, fields[2])
		require.GreaterOrEqualf(t, start, int64(0), "negative cue start in %s", path)
		require.Greaterf(t, end, start, "cue end must be after its start in %s", path)
		require.LessOrEqualf(t, end, clipDurationMs+1000,
			"cue in %s extends past the clip duration (%dms > %dms)", path, end, clipDurationMs)
	}
	require.Positivef(t, cues, "ASS %s must contain at least one Dialogue cue", path)
	return cues
}

// parseASSTimestamp parses the ASS H:MM:SS.cc timestamp into milliseconds.
func parseASSTimestamp(v string) (int64, bool) {
	var h, m int
	var s float64
	if _, err := fmt.Sscanf(v, "%d:%d:%f", &h, &m, &s); err != nil {
		return 0, false
	}
	return int64(h)*3600_000 + int64(m)*60_000 + int64(s*1000), true
}

// ── the test ────────────────────────────────────────────────────────────────

func TestLiveYouTube_WhisperSourceArgosTranslationDeliversSubtitleArtifacts(t *testing.T) {
	workdir := requireLiveE2E(t)
	db := openLiveDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	url := liveEnv("VELOX_E2E_YOUTUBE_URL", liveDefaultURL)
	videoID := videoIDFromURL(url)
	require.NotEmpty(t, videoID, "could not resolve a YouTube video id from %q", url)

	log := zaptest.NewLogger(t, zaptest.Level(zapcore.InfoLevel))
	clipID := fmt.Sprintf("yt_%s_%d_%d_whisper_v1", videoID, liveSegmentStart, liveSegmentEnd)

	// ── 1. Real download of the ~60s section. ──────────────────────────
	mp4 := downloadOneMinute(t, workdir, url, videoID)
	require.FileExists(t, mp4)

	// ── 2. LOCAL WHISPER is the transcript source. ────────────────────
	whisperModel := liveEnv("VELOX_E2E_WHISPER_MODEL", "base")
	require.NoError(t, os.Setenv("VELOX_WHISPER_MODEL", whisperModel))
	if os.Getenv("VELOX_WHISPER_DEVICE") == "" {
		require.NoError(t, os.Setenv("VELOX_WHISPER_DEVICE", "auto"))
	}

	adapter, err := ytwhisper.NewWhisperTranscriberAdapter(ytwhisper.WhisperTranscriberConfig{
		PythonBin:      liveEnv("VELOX_E2E_WHISPER_PYTHON", filepath.Join("..", "..", ".venv-whisper", "bin", "python3")),
		ScriptPath:     liveEnv("VELOX_E2E_WHISPER_SCRIPT", filepath.Join("..", "..", "scripts", "bridges", "whisper_transcriber.py")),
		DefaultTimeout: 10 * time.Minute,
	}, log)
	require.NoError(t, err, "the local Whisper bridge must be available for this test")

	whisperStart := time.Now()
	whisper, err := adapter.TranscribeAudioWithDetection(ctx, mp4)
	require.NoError(t, err, "local Whisper transcription must succeed")
	confidence := 0.0
	if whisper.Confidence != nil {
		confidence = *whisper.Confidence
	}
	t.Logf("whisper: %d chars, language=%s confidence=%.3f, %d cues in %s",
		len(whisper.Text), whisper.DetectedLanguage, confidence, len(whisper.Cues),
		time.Since(whisperStart).Round(time.Second))

	require.NotEmpty(t, strings.TrimSpace(whisper.Text), "Whisper returned an empty transcript")
	require.NotEmpty(t, whisper.Cues, "Whisper must return TIMED cues, not plaintext only")
	require.NotEmpty(t, whisper.DetectedLanguage, "Whisper must report a detected language")

	// Cue timing sanity: ordered, non-negative, inside the 60s window.
	var lastEnd int64
	for i, cue := range whisper.Cues {
		require.GreaterOrEqualf(t, cue.StartMs, int64(0), "cue %d has a negative start", i)
		require.Greaterf(t, cue.EndMs, cue.StartMs, "cue %d end must be after its start", i)
		require.NotEmptyf(t, strings.TrimSpace(cue.Text), "cue %d is empty", i)
		require.GreaterOrEqualf(t, cue.StartMs, lastEnd-250, "cue %d is not monotonic", i)
		lastEnd = cue.EndMs
	}
	require.LessOrEqual(t, whisper.Cues[len(whisper.Cues)-1].EndMs, int64(liveSegmentEnd)*1000+5000,
		"the last Whisper cue must fall inside the downloaded segment")

	title, channel := sourceVideoIdentity(t, url)

	// ── 3. Commit clip + READY WHISPER transcript to PostgreSQL. ──────
	committer, err := wiringmedia.NewPostgresMediaCommitterFromDB(db, log)
	require.NoError(t, err)

	textHash := detail.TextHash(whisper.Text, liveSourceLang, detail.TextTrackTranscript)
	sourceTrack := detail.TextTrack{
		AssetID:            clipID,
		LanguageCode:       liveSourceLang,
		TextKind:           detail.TextTrackTranscript,
		TextContent:        whisper.Text,
		SourceType:         detail.TextSourceWhisper,
		SourceLanguageCode: liveSourceLang,
		IsOriginal:         true,
		Provider:           "faster-whisper",
		ModelName:          whisperModel,
		ModelVersion:       "v1",
		TextHash:           textHash,
		SourceVersion:      detail.SourceVersion(textHash, liveSourceLang, liveSourceLang, "faster-whisper", whisperModel, "v1", ""),
		Status:             detail.TextTrackReady,
		IsCurrent:          true,
	}

	clipAsset := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       videoID,
		LocalPath:     mp4,
		LegacyFileMD5: fmt.Sprintf("%064x", len(whisper.Text)),
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
			Summary:         fmt.Sprintf("%s — first %ds (local Whisper source)", title, liveSegmentEnd-liveSegmentStart),
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
			SourceVersion:   "live-e2e-whisper-v1",
		},
	}

	require.NoError(t, committer.CommitClipTextAndIndexEvent(ctx, localized.CommitLocalizedClipCommand{
		Clip:       clipAsset,
		TextTracks: []detail.TextTrack{sourceTrack},
		TimedTracks: []localized.TimedTextTrack{{
			LanguageCode: liveSourceLang,
			TextKind:     detail.TextTrackTranscript,
			SourceType:   detail.TextSourceWhisper,
			Cues:         whisper.Cues,
		}},
		IndexEvent: youtubeports.IndexEventPayload{AggregateID: clipID, CreatedAt: time.Now().UTC()},
	}))

	// ── 3b. The persisted source MUST record Whisper as its provenance. ─
	var storedSource string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT source_type FROM asset_text_tracks
		WHERE asset_id = $1 AND language_code = $2 AND text_kind = 'transcript' AND is_current = 1
	`, clipID, liveSourceLang).Scan(&storedSource))
	require.Equal(t, "whisper", storedSource,
		"the transcript must be sourced from LOCAL WHISPER, not a YouTube subtitle track")

	repo, err := pgmedia.NewTextTrackRepositoryPG(db)
	require.NoError(t, err)

	// ── 4. REAL Argos translator → 10 languages. ──────────────────────
	argos, err := translation.NewArgosServerTranslator(translation.ArgosServerConfig{
		PythonBin:  liveEnv("VELOX_E2E_ARGOS_PYTHON", filepath.Join("..", "..", ".venv-argos", "bin", "python3")),
		ScriptsDir: liveEnv("VELOX_E2E_ARGOS_SCRIPTS_DIR", filepath.Join("..", "..", "scripts")),
	}, log)
	require.NoError(t, err, "the local Argos sidecar must be available for this test")
	t.Cleanup(func() { argos.Stop() })

	registry, err := assetpkg.NewLanguageRegistryFromCodes(liveLanguages)
	require.NoError(t, err)

	materializer, err := texttracks.NewMaterializer(repo, argos, stubOutbox{t: t}, texttracks.ResolverConfig{
		Registry:         registry,
		SourceLanguage:   liveSourceLang,
		ModelVersion:     translation.ArgosTranslationModelVersion,
		PromptVersion:    "v1",
		TranslationModel: translation.ArgosTranslationModel,
		OllamaModel:      translation.ArgosTranslationModel,
	}, log)
	require.NoError(t, err)
	materializer.SetConcurrency(4)
	materializer.SetIndexRequester(pgmedia.NewReindexRequester(db))
	materializer.SetSearchTextRebuilder(pgmedia.NewSearchTextRebuilder(db, strings.Join(liveLanguages, ",")).WithLogger(log))

	translateStart := time.Now()
	report, err := materializer.Materialize(ctx, clipID, liveSourceLang, textHash, detail.TextTrackTranscript, nil)
	require.NoError(t, err)
	t.Logf("argos fan-out: created=%v skipped=%v failed=%v in %s",
		report.CreatedLanguages, report.SkippedLanguages, report.FailedLanguages,
		time.Since(translateStart).Round(time.Second))
	require.Empty(t, report.FailedLanguages, "every configured language must translate through Argos")

	assertTranscriptLanguages(t, db, clipID, liveLanguages)

	// ── 5. Translations MUST carry provider=argos (not ollama). ───────
	rows, err := db.QueryContext(ctx, `
		SELECT language_code, provider, source_type
		FROM asset_text_tracks
		WHERE asset_id = $1 AND text_kind = 'transcript' AND is_current = 1 AND language_code <> $2
		ORDER BY language_code
	`, clipID, liveSourceLang)
	require.NoError(t, err)
	defer rows.Close()
	argosCount := 0
	for rows.Next() {
		var lang, provider, sourceType string
		require.NoError(t, rows.Scan(&lang, &provider, &sourceType))
		require.Equalf(t, "argos", provider, "language %s must be translated by local Argos, got provider %q", lang, provider)
		require.Equal(t, "translation", sourceType)
		argosCount++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, len(liveLanguages)-1, argosCount, "all nine target languages must be Argos translations")

	// ── 7. Canonical subtitle delivery → one validated ASS per language. ─
	assetLister := pgmedia.NewMediaClipAssetReader(pgmedia.NewMediaSearcher(db))
	assets, err := assetLister.List(ctx, assetpkg.Filter{IDs: []string{clipID}, Limit: 1})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	assetItem := assets[0]
	require.Truef(t, detail.RequiresSubtitles(string(assetItem.Source)),
		"a youtube clip must take subtitles (source=%q)", assetItem.Source)

	driveTarget := newDriveTarget(t, log, videoID)

	backfill, err := texttracks.NewBackfillService(texttracks.BackfillServiceDeps{
		Data: texttracks.BackfillDataDeps{
			Clips:      assetLister,
			Repo:       repo,
			Cues:       committer,
			SubArtRepo: driveTarget.subRepo,
		},
		Pipeline: texttracks.BackfillPipelineDeps{
			Materializer: materializer,
		},
		Delivery: texttracks.BackfillDeliveryDeps{
			Publisher:       driveTarget.probe,
			DriveFolderID:   driveTarget.clipsRoot,
			SubtitleFolders: driveTarget.subtitleFolders,
			// The production timing-faithful path: each source cue is translated
			// individually by the SAME Argos instance that translated the
			// transcripts, so every translated segment keeps its source window.
			CueTranslator: texttracks.NewCueTranslator(
				argos, liveSourceLang, "", texttracks.DefaultCueTranslationConcurrency, log),
		},
		Log: log,
	})
	require.NoError(t, err)

	subReport, err := backfill.MaterializeSubtitleArtifacts(
		ctx, assetItem, liveSourceLang, nil, detail.TextTrackTranscript,
	)
	require.NoError(t, err)
	t.Logf("subtitle delivery: delivered=%d failed=%v skipped=%v reason=%q",
		subReport.Delivered, subReport.Failed, subReport.Skipped, subReport.SkipReason)
	require.False(t, subReport.Skipped, "subtitle delivery must not be skipped (reason=%q)", subReport.SkipReason)
	require.Empty(t, subReport.Failed)
	require.Equal(t, len(liveLanguages), subReport.Delivered,
		"one ASS artifact must reach the registry for each configured language")

	uploads := driveTarget.probe.recorded()
	require.Len(t, uploads, len(liveLanguages), "one Drive publication per language")

	seenFiles := map[string]bool{}
	for _, up := range uploads {
		require.Equal(t, driveTarget.subtitleRoot, up.FolderID, "artifact must be published under the clip's Drive root")
		require.Equal(t, []string{ytadapters.SubtitleDriveGroup, videoID}, up.Subpath,
			"artifact must land in the per-video subtitle folder, not a second tree")
		require.Truef(t, strings.HasSuffix(up.Filename, ".ass"), "unexpected artifact name %q", up.Filename)
		require.Falsef(t, seenFiles[up.Filename], "duplicate Drive artifact %q", up.Filename)
		seenFiles[up.Filename] = true
		validateASSArtifact(t, up.LocalPath, int64(liveSegmentEnd)*1000)
	}
	require.Len(t, seenFiles, len(liveLanguages))

	// ── 7a. The registry must hold a current READY ASS per language. ────
	for _, lang := range liveLanguages {
		art, fErr := driveTarget.subRepo.FindCurrent(ctx, clipID, lang, detail.SubtitleFormatASS)
		require.NoError(t, fErr)
		require.NotNilf(t, art, "missing ASS artifact row for %s", lang)
		require.Equal(t, detail.SubtitleStatusReady, art.Status, "artifact %s must be READY (validation=%q)", lang, art.ValidationError)
		require.NotEmptyf(t, art.DriveFileID, "artifact %s must record a Drive file id", lang)
		require.NotEmptyf(t, art.DriveURL, "artifact %s must record a Drive URL", lang)
	}

	// ── 7b. REAL upload mode: the artifacts must EXIST in Drive. ──────
	//
	// The contract assertions above prove the delivery ASKED for the right
	// destination. This block proves the production publisher actually put the
	// files there: every recorded file id is re-read from the Drive API, all ten
	// must share ONE parent folder, and that folder must be <root>/<group>/<video>.
	if driveTarget.real {
		results := driveTarget.probe.published()
		require.Len(t, results, len(liveLanguages),
			"every language must come back with a Drive publication result")

		parentIDs := map[string]bool{}
		for _, res := range results {
			require.NotEmptyf(t, res.FileID, "Drive must return a file id for %s", res.Filename)
			require.NotContainsf(t, res.FileID, "e2e-drive-file-",
				"a REAL upload must return a Drive-issued id for %s", res.Filename)
			require.Containsf(t, res.WebViewLink, "drive.google.com",
				"Drive must return a real webViewLink for %s", res.Filename)

			file, gErr := driveTarget.client.Files.Get(res.FileID).
				Fields("id,name,parents,trashed").Context(ctx).Do()
			require.NoErrorf(t, gErr, "the uploaded artifact must be readable back from Drive: %s (%s)",
				res.Filename, res.FileID)
			require.Equal(t, res.Filename, file.Name)
			require.False(t, file.Trashed)
			for _, parent := range file.Parents {
				parentIDs[parent] = true
			}
		}

		require.Lenf(t, parentIDs, 1, "all ten artifacts must land in ONE Drive folder, got %d", len(parentIDs))
		var perVideoFolderID string
		for id := range parentIDs {
			perVideoFolderID = id
		}

		perVideo, gErr := driveTarget.client.Files.Get(perVideoFolderID).
			Fields("id,name,parents").Context(ctx).Do()
		require.NoError(t, gErr)
		require.Equal(t, videoID, perVideo.Name, "artifacts must land in the per-video folder")
		require.Len(t, perVideo.Parents, 1)

		group, gErr := driveTarget.client.Files.Get(perVideo.Parents[0]).
			Fields("id,name,parents").Context(ctx).Do()
		require.NoError(t, gErr)
		require.Equal(t, ytadapters.SubtitleDriveGroup, group.Name,
			"the per-video folder must hang off the canonical subtitle group folder")
		require.Contains(t, group.Parents, driveTarget.subtitleRoot,
			"the artifact tree must hang off the configured subtitle root, not a second tree")

		t.Logf("REAL Drive verified: %d artifacts under %s/%s/%s",
			len(results), driveTarget.subtitleRoot, group.Name, perVideo.Name)
	}

	// ── 7c. TIMED CUES for translation needs the projection step. ─────
	//
	// Argos translates cue-by-cue from TEXT (a translated track arrives with
	// text but no cues), so a translated transcript is only subtitle-ready
	// once the canonical delivery projects the source timing onto it. This is
	// the CuesWithText invariant whose absence produced 9 transcript rows and
	// ONE .ass in production; asserting it AFTER the delivery pins the fix at
	// the level where it actually happens.
	for _, lang := range liveLanguages {
		_, cues, fErr := repo.FindReady(ctx, clipID, lang, detail.TextTrackTranscript)
		require.NoErrorf(t, fErr, "FindReady(%s)", lang)
		require.NotEmptyf(t, cues, "language %s must carry timed cues — an untimed track cannot build an ASS artifact", lang)
	}

	// ── 8. Idempotent re-delivery: no duplicate uploads. ───────────────
	secondReport, err := backfill.MaterializeSubtitleArtifacts(
		ctx, assetItem, liveSourceLang, nil, detail.TextTrackTranscript,
	)
	require.NoError(t, err)
	require.Equal(t, len(liveLanguages), secondReport.Delivered, "a rerun reuses the recorded artifacts")
	require.Len(t, driveTarget.probe.recorded(), len(liveLanguages),
		"a second delivery must NOT upload duplicates to Drive")

	// ── 9. Multilingual search_text, PostgreSQL outbox, then INDEXED. ──
	var searchText string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT search_text FROM media_assets WHERE id = $1`, clipID).Scan(&searchText))
	italianText := transcriptLanguage(t, db, clipID, "it")
	require.Contains(t, searchText, firstWords(italianText, 40),
		"the Italian translation must be part of media_assets.search_text")

	var pending int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE event_type = 'asset.index.requested' AND aggregate_id = $1
	`, clipID).Scan(&pending))
	require.GreaterOrEqual(t, pending, 1, "the reindex request must live in the PostgreSQL outbox")

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

	require.NotEmpty(t, embedder.embeddedTexts(), "the E5 sidecar must have been called")
	require.Contains(t, strings.Join(embedder.embeddedTexts(), "\n"), firstWords(italianText, 40),
		"the embedded document must contain the Argos translation")

	var state string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT index_state FROM media_assets WHERE id = $1`, clipID).Scan(&state))
	require.Equal(t, "INDEXED", state)

	var dims int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT vector_dims(embedding) FROM media_embeddings
		WHERE asset_id = $1 AND embedding_type = 'text' AND model_id = $2
	`, clipID, coreembedding.ModelIDMultilingualE5).Scan(&dims))
	require.Equal(t, liveEmbeddingDims, dims)

	t.Logf("WHISPER E2E OK: clip=%s source=%s languages=%d ass=%d drive_targets=%d INDEXED=%s dims=%d",
		clipID, storedSource, len(liveLanguages), len(seenFiles), len(uploads), state, dims)
}

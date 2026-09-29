// Package e2e — subtitle_artifacts_catalog_validate_test.go: the LOCAL
// certificate for the subtitle-artifact catalog (no network, no server, no
// YouTube).
//
// WHY THIS TEST EXISTS: `asset_subtitle_artifacts` reports READY for a file
// whose only consumer decides "consumable" later — clip.render compiles it
// into the render, the multilingual materializer publishes it to Drive — and
// that single decision is texttracks.ValidateASSFile (last cue end within
// clipDurationMs + tolerance, structural integrity). An artifact can therefore
// be READY in the catalog and still be rejected at render time, which is the
// failure mode that ships clips without subtitles.
//
// The live certificate (youtube_subtitles_clip_window_live_test.go) proves the
// contract for ONE freshly acquired clip; this test proves it for the WHOLE
// local catalog — including every clip a register-batch run just delivered —
// so a regression in the materializer, the cue rebase or the translation
// alignment fails the default suite instead of failing a render.
//
// Honest about the catalog's three states:
//   - status=READY + file on disk → MUST validate (hard assertion);
//   - status=FAILED → the writer already rejected it and recorded why; it is
//     counted and logged, not re-asserted (those rows are the bug's
//     fingerprint, kept as evidence);
//   - file absent → counted and skipped: the catalog intentionally keeps rows
//     whose bytes were cleaned up or live only on Drive.
//
// Skips (never mocks) when there is nothing to certify: catalog file absent
// (clean CI checkout) or no current artifact rows.
//
// STRICT LANGUAGE-SET MODE: set VELOX_ASS_CATALOG_PREFIXES to a CSV of
// asset-id prefixes (e.g. the videos of a pilot run) and every matching asset
// must carry the full 10-language configured set in the catalog. The
// assertion is env-scoped because the historical catalog legitimately holds
// partially materialized legacy assets (1–9 languages) that predate the
// post-commit fan-out; a fresh batch is held to the full contract.
//
//	go test ./tests/e2e/ -run TestSubtitleArtifacts_CatalogCurrentFilesValidate -count=1 -v
//	VELOX_ASS_CATALOG_PREFIXES=yt_9Q6T-bzF4Vs,yt_Lsv8jps7H9k,yt_vVCFbOn77Co,yt_Kq7Kqku3GO0 go test ...
package e2e

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
)

// configuredLanguages is the canonical 10-language set the multilingual
// fan-out must deliver for every clip (source + 9 targets).
var configuredLanguages = []string{"en", "it", "de", "es", "pt-BR", "fr", "pl", "ru", "tr", "id"}

// assArtifactRow is the narrow projection of asset_subtitle_artifacts the
// certificate needs: WHICH file, on WHICH clip timeline, in WHICH language,
// and whether the writer already accepted or rejected it.
type assArtifactRow struct {
	assetID        string
	languageCode   string
	localPath      string
	clipDurationMs int64
	status         string
}

// moduleRoot walks up from the test's working directory (tests/e2e) to the
// directory holding go.mod, so both the catalog and the artifact paths — which
// are recorded RELATIVE to the module root — resolve the same way they do at
// runtime.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "go.mod not found above %s (test must live inside the module)", dir)
		dir = parent
	}
}

// TestSubtitleArtifacts_CatalogCurrentFilesValidate walks every CURRENT
// subtitle artifact in the local media catalog and asserts every READY .ass on
// disk passes the canonical validator with the clip's own duration.
//
// A failure here means a clip the catalog calls READY would be rejected by
// clip.render or by the Drive materializer: the subtitle pipeline reported
// success it did not deliver.
func TestSubtitleArtifacts_CatalogCurrentFilesValidate(t *testing.T) {
	root := moduleRoot(t)
	catalog := filepath.Join(root, "data", "media", "media.db.sqlite")
	if _, err := os.Stat(catalog); err != nil {
		t.Skipf("local media catalog absent at %s — nothing to certify", catalog)
	}

	db, err := sql.Open("sqlite3", "file:"+catalog+"?mode=ro&_busy_timeout=5000")
	require.NoError(t, err, "open media catalog read-only")
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT asset_id, language_code, local_path, clip_duration_ms, status
		FROM asset_subtitle_artifacts
		WHERE is_current = 1
		ORDER BY asset_id, language_code`)
	require.NoError(t, err, "read current subtitle artifacts")
	defer func() { _ = rows.Close() }()

	var artifacts []assArtifactRow
	for rows.Next() {
		var r assArtifactRow
		if err := rows.Scan(&r.assetID, &r.languageCode, &r.localPath, &r.clipDurationMs, &r.status); err != nil {
			require.NoError(t, err, "scan artifact row")
		}
		artifacts = append(artifacts, r)
	}
	require.NoError(t, rows.Err())
	require.NotZero(t, len(artifacts), "the catalog must hold at least one current artifact (empty catalog = nothing certified)")

	var (
		checked        int
		absent         int
		writerRejected int
	)
	certifiedByAsset := map[string]map[string]bool{}
	rowsByAsset := map[string]map[string]bool{}
	rejectedByAsset := map[string]int{}

	for _, a := range artifacts {
		if rowsByAsset[a.assetID] == nil {
			rowsByAsset[a.assetID] = map[string]bool{}
		}
		rowsByAsset[a.assetID][a.languageCode] = true

		if strings.EqualFold(a.status, "FAILED") {
			// The writer already rejected this artifact and recorded WHY
			// (validation_error). Re-asserting would only pin the known-bad
			// row; the certificate's job is to prove READY means consumable.
			writerRejected++
			rejectedByAsset[a.assetID]++
			continue
		}

		abs := a.localPath
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, a.localPath)
		}
		if _, err := os.Stat(abs); err != nil {
			absent++
			continue
		}
		if err := texttracks.ValidateASSFile(abs, a.clipDurationMs); err != nil {
			t.Errorf("artifact READY in the catalog but rejected by ValidateASSFile: asset=%s lang=%s path=%s duration_ms=%d: %v",
				a.assetID, a.languageCode, a.localPath, a.clipDurationMs, err)
			continue
		}
		checked++
		if certifiedByAsset[a.assetID] == nil {
			certifiedByAsset[a.assetID] = map[string]bool{}
		}
		certifiedByAsset[a.assetID][a.languageCode] = true
	}

	require.NotZero(t, checked, "no artifact file was present on disk — nothing was certified")
	t.Logf("certified %d READY .ass artifacts across %d assets (absent on disk: %d, writer-rejected rows: %d)",
		checked, len(certifiedByAsset), absent, writerRejected)

	// An asset whose artifact set contains NONE of the configured languages
	// never went through the multilingual pipeline at all. A set MISSING a
	// configured language is reported informationally here and held to the
	// full contract by the strict, prefix-scoped assertion below — the
	// historical catalog legitimately holds partially materialized legacy
	// assets, and a non-English SOURCE (e.g. a Welsh transcript materialized
	// as cy + the 9 targets) legitimately carries no English artifact.
	var withoutConfigured []string
	for assetID, langs := range certifiedByAsset {
		hasConfigured := false
		for _, l := range configuredLanguages {
			if langs[l] {
				hasConfigured = true
				break
			}
		}
		if !hasConfigured {
			withoutConfigured = append(withoutConfigured, assetID)
		}
	}
	if len(withoutConfigured) > 0 {
		t.Logf("assets certified without ANY configured language (never multilingual): %s", strings.Join(withoutConfigured, ", "))
	}

	// Assets whose writer rejected EVERY current row are surfaced loudly: a
	// clip that exists only in FAILED state ships no subtitles at all.
	for assetID, n := range rejectedByAsset {
		if len(rowsByAsset[assetID]) == n {
			t.Errorf("asset %s has %d current artifacts and ALL of them are status=FAILED — the clip ships no subtitles at all (the clip-window bug, unfixed for this asset)",
				assetID, n)
		}
	}

	// Strict full-set mode (env-scoped: the historical catalog legitimately
	// holds partially materialized legacy assets).
	prefixes := configuredPrefixes()
	if len(prefixes) == 0 {
		incomplete := incompleteAssets(rowsByAsset, configuredLanguages)
		t.Logf("full-set assertion skipped (set VELOX_ASS_CATALOG_PREFIXES to enforce); assets below the configured 10-language set: %d", len(incomplete))
		return
	}
	matched := 0
	for assetID, langs := range rowsByAsset {
		if !hasAnyPrefix(assetID, prefixes) {
			continue
		}
		matched++
		// The 9 configured TARGET languages must be there — that is the
		// multilingual contract. The 10th slot is the SOURCE language, which
		// is honestly whatever the acquisition chain detected (en for an
		// English video, cy for a Welsh one), so it is asserted as a COUNT,
		// not as a fixed code.
		for _, want := range configuredLanguages[1:] {
			if !langs[want] {
				t.Errorf("asset %s is missing the %q artifact (present: %s) — the multilingual fan-out did not deliver the configured target set",
					assetID, want, strings.Join(langKeys(langs), ","))
			}
		}
		if len(langs) < 10 {
			t.Errorf("asset %s carries %d languages, want at least 10 (9 configured targets + the source): %s",
				assetID, len(langs), strings.Join(langKeys(langs), ","))
		}
	}
	require.NotZero(t, matched, "VELOX_ASS_CATALOG_PREFIXES matched no asset — the strict assertion would silently pass")
	t.Logf("strict full-set assertion enforced on %d assets matching %v", matched, prefixes)
}

// configuredPrefixes reads the env-scoped strict-mode asset-id prefixes.
func configuredPrefixes() []string {
	raw := strings.TrimSpace(os.Getenv("VELOX_ASS_CATALOG_PREFIXES"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// incompleteAssets lists assets carrying fewer than the configured set.
func incompleteAssets(rowsByAsset map[string]map[string]bool, want []string) []string {
	var out []string
	for assetID, langs := range rowsByAsset {
		missing := 0
		for _, w := range want {
			if !langs[w] {
				missing++
			}
		}
		if missing > 0 {
			out = append(out, assetID)
		}
	}
	return out
}

// langKeys returns the language codes of a set, sorted for stable log and
// error output (package e2e already owns sortedKeys for map[string]struct{}).
func langKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Package e2e — dolly_parton_multilingual_runtime_test.go: the RUNTIME half of
// the Dolly Parton batch.
//
// dolly_parton_clips_test.go certifies the ACQUISITION half hermetically: the
// curated payload is a valid ExtractRequest, the five moments fit the source,
// and each moment owns a deterministic `yt_<videoID>_<start>_<end>_<policyVer>`
// clip id. It never generates a script and never renders a frame.
//
// This file covers what the product actually ships for that batch: the script
// is GENERATED AT RUNTIME from the five curated clips, and each requested
// language is rendered with the subtitles BURNED in that language.
//
// It pins three facts that a runtime can silently get wrong:
//
//  1. THE CLIPS ARE THE SOURCE. The generate request asks for exactly the five
//     canonical clip ids of the fixture (source.type=clips), so a run that
//     quietly falls back to "some other clips" cannot pass. The ids are DERIVED
//     from the fixture with the production identity builder, never copied: if
//     the payload changes, this gate fails instead of certifying a stale list.
//
//  2. SUBTITLES IN THE LANGUAGE OF THE SCRIPT. Every requested language must
//     produce at least one localized render whose `language` IS that language.
//     A fallback to the source transcript used to burn English subtitles into
//     the Italian and German renders while the job still reported SUCCEEDED
//     (cliprender's validateRequestedTranscriptLanguage now fails closed
//     instead of substituting a different track). This gate observes the
//     effect at the wire boundary, per language, rather than trusting the
//     absence of an error.
//
//  3. PERSISTED, NOT RE-DERIVED AT RUNTIME. Every produced clip must carry the
//     canonical content-addressed `cliprender_<sha256-prefix>` asset id (the
//     same identity the direct clip.render publisher mints) and a Drive link,
//     i.e. the row was committed to the media SSOT. Recomputing the render on
//     every read is exactly what the persistence contract removes.
//
// PIXEL-LEVEL VISIBILITY IS NOT RE-INVENTED HERE. That the burned glyphs are
// actually on screen (non-empty alpha coverage, inside the clip box) is the
// renderer's own invariant, certified by Chronon3D's text visibility audit
// (`verify_text_visibility`). Re-deriving it from a decoded MP4 in this test
// would be a second, weaker authority for a fact the render boundary already
// owns. What this gate certifies is the INPUT to that audit: a burned subtitle
// artifact for the RIGHT language exists for every language asked for.
//
// The live leg is hard-gated behind PIPELINEGEN_DOLLY_PARTON_LIVE=1 plus an
// admin token, and it additionally requires a running PipelineGen API with a
// GPU render lane. It is SKIPPED — never silently mocked — when either is
// missing. The hermetic contract test runs everywhere and always.
//
// Required environment (live leg only):
//
//	PIPELINEGEN_DOLLY_PARTON_LIVE=1   enables the live runtime test
//	VELOX_ADMIN_TOKEN                 bearer token accepted by POST /api/script/generate
//	  (or TOKEN_FILE, a shell-style env file carrying VELOX_ADMIN_TOKEN=...)
//
// Optional environment:
//
//	VELOX_API_BASE_URL                default http://127.0.0.1:8000
//	DOLLY_PARTON_LANGUAGES            comma-separated target languages
//	                                  (default it,es,de,fr — a staged rollout
//	                                  keeps the GPU budget bounded)
//	DOLLY_PARTON_RUNTIME_TIMEOUT      per-run budget (default 20m)
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	ytusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

const (
	// dollyMultilingualSourceLanguage is the language of the SOURCE interview
	// audio (Late Night with Conan O'Brien). It is deliberately not Italian:
	// the fixture summaries are Italian editorial metadata, not the language
	// the clips are spoken in, and deriving the source language from them
	// would make Whisper's detected "en" fail the requested-language gate —
	// which is exactly the mismatch that gate exists to catch.
	dollyMultilingualSourceLanguage = "en"

	// dollyMultilingualDriveSubfolder is the child folder the EXTRACTED source
	// clips publish under. It is NOT the destination of the localized renders:
	// a docs-enabled run publishes each languages' render beside that language's
	// script document, in <resolved documents root>/<job>/<language>. The clips
	// fields on render are the fallback of a docs-less run only.
	dollyMultilingualDriveSubfolder = "Dolly Parton"

	// dollyMultilingualSubtitlePreset is a concrete short-form subtitle style
	// that exists in the canonical preset registry and in the ASS typography
	// table, so the burned subtitle and the Chronon overlay agree on fonts.
	dollyMultilingualSubtitlePreset = "subs-young-clean"
)

// dollyMultilingualTargetLanguages is the default fan-out. Four languages keep
// a single run inside a sane GPU budget while still proving that the subtitle
// language is per-render and not a property of the source.
var dollyMultilingualTargetLanguages = []string{"it", "es", "de", "fr"}

// dollyPartonCanonicalClipIDs derives the five clip ids of the fixture with the
// PRODUCTION identity builder (the same call the extraction worker makes), so
// this gate can never pin a list the worker would not produce.
func dollyPartonCanonicalClipIDs(t *testing.T) []string {
	t.Helper()
	req := dollyPartonPayload(t)
	ids := make([]string, 0, len(req.Segments))
	for i, seg := range req.Segments {
		start, err := textutil.ParseTimestamp(seg.Start)
		require.NoError(t, err, "segment[%d]: start timestamp", i+1)
		end, err := textutil.ParseTimestamp(seg.End)
		require.NoError(t, err, "segment[%d]: end timestamp", i+1)
		// The content hash is an index-event input, NOT part of the asset id
		// (a re-index must not mint a second clip). It is derived here exactly
		// as dolly_parton_clips_test.go derives it, so both tests pin the same
		// identity for the same fixture.
		sum := sha256.Sum256([]byte(seg.Name + "\n" + seg.Summary))
		identity, err := detail.NewYouTubeClipIdentity(detail.YouTubeClipIdentityParams{
			VideoID:     dollyPartonVideoID,
			StartSec:    start,
			EndSec:      end,
			PolicyVer:   ytusecase.ProcessSegmentPolicyVersion,
			ContentHash: hex.EncodeToString(sum[:]),
		})
		require.NoError(t, err, "segment[%d]: canonical clip identity", i+1)
		ids = append(ids, identity.AssetID)
	}
	return ids
}

// dollyMultilingualLanguages resolves the target fan-out, optionally narrowed
// from the environment for a staged rollout.
func dollyMultilingualLanguages(t *testing.T) []string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("DOLLY_PARTON_LANGUAGES"))
	if raw == "" {
		return append([]string(nil), dollyMultilingualTargetLanguages...)
	}
	out := make([]string, 0, len(dollyMultilingualTargetLanguages))
	for _, part := range strings.Split(raw, ",") {
		lang, err := assetpkg.Normalize(strings.TrimSpace(part))
		if err != nil || lang == "und" {
			continue
		}
		out = append(out, lang)
	}
	if len(out) == 0 {
		t.Fatalf("DOLLY_PARTON_LANGUAGES=%q resolved to no language", raw)
	}
	return out
}

// dollyPartonMultilingualEnvelope builds the exact generate envelope this suite
// submits. It is the single source of truth for the request: the hermetic test
// decodes and asserts it, the live test posts it verbatim, so the two can never
// describe different runs.
func dollyPartonMultilingualEnvelope(t *testing.T, languages []string) []byte {
	t.Helper()
	clips := dollyPartonCanonicalClipIDs(t)
	docsLanguages := append([]string{dollyMultilingualSourceLanguage}, languages...)

	envelope := map[string]any{
		"version":        2,
		"preset":         "custom",
		"correlation_id": "dolly-parton-multilingual-20260918",
		"items": []any{
			map[string]any{
				"id":       "dolly-parton-multilingual",
				"project":  "dolly-parton-multilingual",
				"title":    "Dolly Parton — aneddoti da Late Night",
				"language": dollyMultilingualSourceLanguage,
				"tone":     "documentario ironico",
				"style": "Scrivi la narrazione usando esclusivamente l'evidenza delle clip fornite. " +
					"Non inventare aneddoti, date, citazioni o persone non presenti nella fonte. " +
					"Mantieni il registro colloquiale dell'intervista.",
				"source": map[string]any{
					"type":      "clips",
					"clip_ids":  clips,
					"num_clips": len(clips),
				},
				"script_params": map[string]any{
					"target_words":  180,
					"min_words":     120,
					"segment_words": 60,
				},
				"output": map[string]any{
					"save_to_db":        true,
					"extract_entities":  false,
					"generate_timeline": true,
					"voiceover_enabled": false,
					"languages":         languages,
					"render": map[string]any{
						"enabled":              true,
						"require_gpu":          true,
						"drive_folder_id":      dollyPartonDriveParentFolderID,
						"drive_subfolder_name": dollyMultilingualDriveSubfolder,
						"subtitles": map[string]any{
							"enabled": true,
							"mode":    "burn",
							"preset":  dollyMultilingualSubtitlePreset,
						},
						"background": map[string]any{
							"mode":     "asset",
							"asset_id": "drive-background-01",
						},
					},
					"drive_folder_id": dollyPartonDriveParentFolderID,
				},
				"audio": map[string]any{
					"mode": "NONE",
				},
				"media_plan": map[string]any{
					"mode":  "hybrid",
					"cache": map[string]any{"read": true, "write": true},
					"provider_policy": map[string]any{
						// The clips are supplied explicitly by id; every
						// provider stays off so nothing can substitute a
						// different asset behind the request.
						"internet_images":  "disabled",
						"artlist":          "disabled",
						"image_generation": "disabled",
						"youtube":          "disabled",
					},
					"extraction": map[string]any{
						"enabled":       false,
						"include":       []string{},
						"entity_images": map[string]any{"enabled": false},
					},
				},
				"docs": map[string]any{
					"enabled":   true,
					"languages": docsLanguages,
					"folder_id": dollyPartonDocsFolderID,
				},
			},
		},
	}

	body, err := json.Marshal(envelope)
	require.NoError(t, err, "marshal Dolly Parton multilingual envelope")
	return body
}

// TestDollyPartonMultilingualRequestContract pins the request the live runtime
// test submits, through the PRODUCTION wire type and builder: it must ask for
// the five curated clips, burn per-language subtitles, persist to the DB, and
// route the renders under the Dolly Parton folder. Without this the live leg
// could pass while asking for something else entirely.
func TestDollyPartonMultilingualRequestContract(t *testing.T) {
	languages := dollyMultilingualTargetLanguages
	body := dollyPartonMultilingualEnvelope(t, languages)

	var env scriptpkg.GenerationEnvelopeV2
	require.NoError(t, json.Unmarshal(body, &env),
		"the envelope must decode as the production GenerationEnvelopeV2 wire contract")
	require.Len(t, env.Items, 1, "the Dolly Parton batch is a single generate item")

	req, err := scriptgeneration.BuildGenerateRequest(&env, "dolly-parton-multilingual-contract")
	require.NoError(t, err, "the envelope must build through the production builder")

	// 1. The five curated clips ARE the source of the run.
	require.Equal(t, scriptgeneration.SourceClips, req.Source.Type,
		"the script must be generated from the curated clip evidence, not from prose")
	require.Equal(t, dollyPartonCanonicalClipIDs(t), req.Source.ClipIDs,
		"the run must consume exactly the five canonical clip ids of the fixture")

	// 2. Subtitles are burned, never optional output.
	require.True(t, req.Render.Enabled, "the render fan-out must be enabled")
	require.NotNil(t, req.Render.Subtitles, "render.subtitles is required for this batch")
	require.True(t, req.Render.Subtitles.Enabled, "subtitles must be enabled")
	require.Equal(t, "burn", req.Render.Subtitles.Mode,
		"subtitles must be BURNED: a sidecar track is not the shipped artifact")
	require.True(t, req.Render.RequireGPU,
		"the certified lane is the GPU lane; a silent software fallback must fail the run")

	// 3. The render is persisted, not re-derived on every read.
	require.True(t, req.SaveToDB, "the generated script and its renders must be saved")

	// 4. Source language is the interview language; targets are the requested
	//    set and are translated into, never substituted for the source.
	require.Equal(t, scriptgeneration.Language(dollyMultilingualSourceLanguage), req.SourceLanguage)
	require.Equal(t, languages, stringLanguages(req.Languages),
		"every requested target language must reach the durable request")
	for _, lang := range languages {
		require.NotEqualf(t, dollyMultilingualSourceLanguage, lang,
			"language %s is the source language; it cannot also be a translation target", lang)
	}

	// 5. The clips routing fields survive the wire as the DOCS-LESS fallback,
	//    but they are not the destination of this batch: docs are enabled, so
	//    every render publishes beside its own language's script document in
	//    <resolved documents root>/<job>/<language> (verified live by
	//    verifyDollyPartonMultilingualResult).
	require.Equal(t, dollyPartonDriveParentFolderID, req.Render.DriveFolderID)
	require.Equal(t, dollyMultilingualDriveSubfolder, req.Render.DriveSubfolderName)

	// 6. One document per language, source included.
	require.True(t, req.Docs.Enabled, "docs publishing is explicit for this batch")
	require.Equal(t, dollyPartonDocsFolderID, req.Docs.FolderID,
		"scripts and rendered clips must be rooted in the canonical Dolly Drive folder")
}

// stringLanguages flattens the domain Language slice for comparison.
func stringLanguages(in []scriptgeneration.Language) []string {
	out := make([]string, 0, len(in))
	for _, lang := range in {
		out = append(out, string(lang))
	}
	return out
}

// TestLiveDollyPartonMultilingualRuntime generates the script at runtime from
// the five curated Dolly Parton clips and certifies that every requested
// language rendered with its OWN burned subtitles, as a persisted,
// content-addressed asset.
func TestLiveDollyPartonMultilingualRuntime(t *testing.T) {
	if os.Getenv("PIPELINEGEN_DOLLY_PARTON_LIVE") != "1" {
		t.Skip("set PIPELINEGEN_DOLLY_PARTON_LIVE=1 to run the live Dolly Parton multilingual runtime test")
	}
	token := liveAdminToken()
	if token == "" {
		t.Skip("VELOX_ADMIN_TOKEN or TOKEN_FILE is required for the live Dolly Parton runtime test")
	}

	baseURL := strings.TrimRight(getenv("VELOX_API_BASE_URL", "http://127.0.0.1:8000"), "/")
	client := &http.Client{}
	languages := dollyMultilingualLanguages(t)
	body := dollyPartonMultilingualEnvelope(t, languages)

	jobID := submitDollyPartonRuntime(t, client, baseURL, token, body)
	full := waitForDollyPartonRuntime(t, client, baseURL, token, jobID)
	result := generationResult(full)
	require.NotNilf(t, result, "job %s completed without job.result.result", jobID)

	verifyDollyPartonMultilingualResult(t, result, languages)
	t.Logf("runtime PASS: job=%s languages=%v clips=%d", jobID, languages, len(dollyPartonCanonicalClipIDs(t)))
}

func submitDollyPartonRuntime(t *testing.T, client *http.Client, baseURL, token string, body []byte) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/script/generate", bytes.NewReader(body))
	require.NoError(t, err, "create generate request")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("dolly-parton-multilingual-%d", time.Now().UnixNano()))

	resp, err := client.Do(req)
	require.NoError(t, err, "POST /api/script/generate")
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	require.NoError(t, err, "read generate response")
	require.Truef(t, resp.StatusCode >= 200 && resp.StatusCode < 300,
		"POST /api/script/generate returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))

	var payload map[string]any
	require.NoErrorf(t, json.Unmarshal(responseBody, &payload), "decode generate response: %s", responseBody)
	jobID := stringAt(payload, "job_id")
	require.NotEmptyf(t, jobID, "generate response has no job_id: %s", responseBody)
	return jobID
}

func waitForDollyPartonRuntime(t *testing.T, client *http.Client, baseURL, token, jobID string) map[string]any {
	t.Helper()
	timeout := 20 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("DOLLY_PARTON_RUNTIME_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		full, status, err := getLiveJob(t, ctx, client, baseURL, token, jobID)
		if err != nil {
			t.Fatalf("GET /api/jobs/%s/full: %v", jobID, err)
		}
		if isSuccessStatus(status) {
			return full
		}
		if isFailureStatus(status) {
			t.Fatalf("Dolly Parton job %s failed with status %q: %s", jobID, status, compactJSON(full))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Dolly Parton job %s did not complete within %s; last status=%q", jobID, timeout, status)
		case <-ticker.C:
		}
	}
}

// verifyDollyPartonMultilingualResult asserts the three certified facts on the
// returned run: per-language renders, no silent source-language substitution,
// and a persisted canonical asset identity for every produced clip.
func verifyDollyPartonMultilingualResult(t *testing.T, result map[string]any, languages []string) {
	t.Helper()

	for _, failure := range mapsAt(result, "localized_render_failures") {
		t.Fatalf("localized render failure: %s", compactJSON(failure))
	}

	renders := mapsAt(result, "localized_renders")
	require.NotEmpty(t, renders, "the run must produce localized renders for the requested languages")

	// The run publishes the source language beside the localized targets. The
	// complete Cartesian matrix is therefore every canonical clip × (source +
	// requested target languages). Checking only "at least one render per
	// language" would allow a partial job (for example 4/5 clips) to report
	// success.
	clipIDs := dollyPartonCanonicalClipIDs(t)
	allLanguages := append([]string{dollyMultilingualSourceLanguage}, languages...)
	expected := len(clipIDs) * len(allLanguages)
	require.Len(t, renders, expected,
		"the result must contain exactly one render per clip/language cell, including the source language")
	byLanguage := make(map[string][]map[string]any, len(allLanguages))
	seenCells := make(map[string]struct{}, expected)
	for _, render := range renders {
		lang := strings.ToLower(stringAt(render, "language"))
		clipID := stringAt(render, "clip_id")
		require.NotEmpty(t, clipID, "every localized render must identify its source clip")
		cell := clipID + "\\x00" + lang
		if _, exists := seenCells[cell]; exists {
			t.Fatalf("duplicate localized render cell %q", cell)
		}
		seenCells[cell] = struct{}{}
		byLanguage[lang] = append(byLanguage[lang], render)
	}

	for _, want := range allLanguages {
		lang := strings.ToLower(want)
		got := byLanguage[lang]
		require.Lenf(t, got, len(clipIDs),
			"language %s must render every canonical clip: renders=%s",
			want, compactJSON(renders))
		for _, render := range got {
			// The language of the produced artifact is the requested one, and
			// the artifact is the persisted, content-addressed clip.
			require.Equalf(t, lang, strings.ToLower(stringAt(render, "language")),
				"render language must be the requested one, got %s", compactJSON(render))

			assetID := stringAt(render, "asset_id")
			sha := strings.ToLower(stringAt(render, "sha256"))
			require.GreaterOrEqualf(t, len(sha), 24,
				"language %s render must carry a real content hash, got %q", want, sha)
			require.Equalf(t, "cliprender_"+sha[:24], assetID,
				"language %s render must be the canonical content-addressed asset id, got %q", want, assetID)

			require.NotEmptyf(t, stringAt(render, "clip_id"),
				"language %s render must name the source clip it was rendered from", want)
			require.NotEmptyf(t, stringAt(render, "drive_link"),
				"language %s render must be published (no Drive link)", want)
			require.Truef(t, isSuccessStatus(stringAt(render, "status")),
				"language %s render is not certified: %s", want, compactJSON(render))
			require.Greaterf(t, integerAt(render, "duration_ms"), int64(0),
				"language %s render has no duration: %s", want, compactJSON(render))
		}
	}

	// The DESTINATION is per language: every language of a clip owns its own
	// folder (<documents root>/<job>/<language>), so a language is readable from
	// the layout. The flat pre-contract layout put every language of a clip in
	// ONE folder — this is the assertion that fails when it comes back, and it is
	// why the run result carries drive_folder_id at all.
	//
	// Within one language every clip legitimately shares the language folder; it
	// is ACROSS languages that a shared folder is the defect.
	folderByLanguage := make(map[string]string, len(allLanguages))
	for _, render := range renders {
		lang := strings.ToLower(stringAt(render, "language"))
		folder := strings.TrimSpace(stringAt(render, "drive_folder_id"))
		require.NotEmptyf(t, folder,
			"language %s render carries no destination folder: %s", lang, compactJSON(render))
		if existing, seen := folderByLanguage[lang]; seen {
			require.Equalf(t, existing, folder,
				"language %s published into two different folders (%s and %s); one language owns one folder per run",
				lang, existing, folder)
			continue
		}
		for other, otherFolder := range folderByLanguage {
			require.NotEqualf(t, otherFolder, folder,
				"languages %s and %s share the folder %s: every language must publish into its own folder",
				other, lang, folder)
		}
		folderByLanguage[lang] = folder
	}
	require.Len(t, folderByLanguage, len(allLanguages),
		"every requested language must own a destination folder, got %v", folderByLanguage)
}

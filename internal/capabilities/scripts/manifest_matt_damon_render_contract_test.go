package scriptgeneration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestMattDamonFiveClipsRenderContract pins the canonical visual contract of
// the Matt Damon 5-clip verification job: the Pale Olive Classic plate
// (`classic1`) behind an 85%-scaled foreground with a top-right text watermark
// and burned subtitles.
//
// It is deliberately a REQUEST-level gate (the manifest is what an operator
// submits), so it fails the moment any of the three verified facts drifts:
//
//   - background.asset_id == "classic1"  → the Pale Olive layer is really there
//   - foreground_scale_percent == 85     → the plate is actually VISIBLE (100%
//     paints the source over the whole canvas and hides the background)
//   - watermark.position == "top_right"  → the watermark is where it was verified
//
// A live run must additionally pixel-probe the produced clip; this test only
// guarantees the submitted intent cannot regress silently.
func TestMattDamonFiveClipsRenderContract(t *testing.T) {
	path := filepath.Join("..", "..", "..", "ops", "jobs", "matt_damon_5_clips.generate.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var envelope scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("manifest violates GenerationEnvelopeV2: %v", err)
	}

	item := envelope.Items[0]
	if len(item.Source.ClipIDs) != 5 {
		t.Fatalf("clip_ids = %d, want 5 (the 5-clip verification contract)", len(item.Source.ClipIDs))
	}

	render := item.Output.Render
	if !render.Enabled {
		t.Fatal("output.render.enabled must be true: the job is a render verification")
	}

	// ── Pale Olive Classic background plate ──────────────────────────
	if render.Background == nil {
		t.Fatal("output.render.background is missing: no background layer would be composited")
	}
	if render.Background.Mode != "asset" {
		t.Errorf("background.mode = %q, want %q", render.Background.Mode, "asset")
	}
	if render.Background.AssetID != "classic1" {
		t.Errorf("background.asset_id = %q, want %q (Pale Olive Classic plate)", render.Background.AssetID, "classic1")
	}

	// ── Foreground scale: the knob that keeps the plate visible ──────
	if render.ForegroundScalePercent != 85 {
		t.Errorf("foreground_scale_percent = %d, want 85 (100 hides the Pale Olive plate)", render.ForegroundScalePercent)
	}

	// ── Top-right text watermark ─────────────────────────────────────
	if render.Watermark == nil {
		t.Fatal("output.render.watermark is missing")
	}
	if !render.Watermark.Enabled {
		t.Error("watermark.enabled must be true")
	}
	if render.Watermark.Position != "top_right" {
		t.Errorf("watermark.position = %q, want %q", render.Watermark.Position, "top_right")
	}
	if render.Watermark.Text == "" {
		t.Error("watermark.text must be non-empty (an asset watermark needs asset_id instead)")
	}

	// ── Burned subtitles ─────────────────────────────────────────────
	if render.Subtitles == nil {
		t.Fatal("output.render.subtitles is missing")
	}
	if !render.Subtitles.Enabled {
		t.Error("subtitles.enabled must be true")
	}
	if render.Subtitles.Mode != "burn" {
		t.Errorf("subtitles.mode = %q, want %q", render.Subtitles.Mode, "burn")
	}

	// Normalize is the runtime path: it must be a NO-OP on this explicit
	// contract (85 and top_right survive), never a silent override back to
	// the 100% / default-position behavior this test exists to prevent.
	render.Normalize()
	if render.ForegroundScalePercent != 85 {
		t.Errorf("Normalize changed foreground_scale_percent to %d, want 85 preserved", render.ForegroundScalePercent)
	}
	if render.Watermark.Position != "top_right" {
		t.Errorf("Normalize changed watermark.position to %q, want top_right preserved", render.Watermark.Position)
	}
}

// ── 1 clip / 1 scene / 10 languages ──────────────────────────────────
//
// The smallest complete multilingual render: ONE source clip, ONE scene and
// ten languages. `renderLanguages` derives the render set from the source
// language plus the requested targets, so this manifest is exactly TEN renders
// of the same clip — one per language, each with its own burned subtitles, each
// published into its own Drive folder.

const (
	// verifyOneClipTenLanguagesManifestPath is the acceptance job of the
	// scenario, resolved from this package directory.
	verifyOneClipTenLanguagesManifestPath = "../../../ops/jobs/verify_1clip_10lang.generate.json"
	// verifyOneClipTenLanguagesClipID is the source clip the scenario renders.
	// Pinned so a payload edit that silently swapped the asset — and therefore
	// the scene count and the expected render matrix — fails here.
	verifyOneClipTenLanguagesClipID = "yt_vLRjqTIiMjc_0_25_v1"
	// verifyOneClipTenLanguagesSubtitlePreset exists in the canonical preset
	// registry AND in the ASS typography table, so the burned subtitle and the
	// Chronon overlay agree on fonts.
	verifyOneClipTenLanguagesSubtitlePreset = "subs-young"
)

// verifyOneClipTenLanguagesTargets is the requested translation fan-out; with the
// source language it forms the canonical ten-language set.
var verifyOneClipTenLanguagesTargets = []string{"it", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}

// TestVerifyOneClipOneSceneTenLanguagesManifestPinsTheRuntimeContract pins the
// runtime contract of the 1-clip/1-scene/10-language acceptance job against the
// REAL production contracts (no server, no Drive, no GPU). Three facts make the
// scenario possible and each one is checked here:
//
//  1. ONE CLIP IS THE SOURCE — source.type=clips with exactly one canonical
//     `yt_<videoID>_<start>_<end>_<policy>` id.
//
//  2. THE SUBTITLE-ONLY LANE — `audio.mode=NONE` is what fans one clip out over
//     every language (`expectedRenderUnits`) instead of rendering the source
//     audio once. Subtitles are BURNED: the shipped artifact is the MP4, and a
//     sidecar track is not it.
//
//  3. THE CLIPS FIELDS CANNOT DIVERT THE RENDER — the destination is
//     `<resolved documents root>/<job>/<language>`; the payload's
//     drive_folder_id/drive_subfolder_name are the fallback of a run with no
//     documents root at all. With docs enabled the documents root resolves
//     (here the configured default, since the payload pins no docs.folder_id) and
//     it is deliberately NOT the clips folder — which is what keeps every
//     language's clip beside the script it was rendered from.
func TestVerifyOneClipOneSceneTenLanguagesManifestPinsTheRuntimeContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "ops", "jobs", "verify_1clip_10lang.generate.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var envelope scriptpkg.GenerationEnvelopeV2
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("the manifest must decode as the production GenerationEnvelopeV2 wire contract: %v", err)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("manifest items = %d, want the single acceptance item", len(envelope.Items))
	}

	req, err := BuildGenerateRequest(&envelope, "verify-1clip-10lang-contract")
	if err != nil {
		t.Fatalf("the manifest must build through the production builder: %v", err)
	}

	// 1. ONE clip is the source of the run.
	if req.Source.Type != SourceClips {
		t.Fatalf("source type = %q, want %q (the script must be generated from the clip evidence)", req.Source.Type, SourceClips)
	}
	if len(req.Source.ClipIDs) != 1 || req.Source.ClipIDs[0] != verifyOneClipTenLanguagesClipID {
		t.Fatalf("source clips = %v, want exactly [%s]", req.Source.ClipIDs, verifyOneClipTenLanguagesClipID)
	}

	// 2. The subtitle-only lane and its burned subtitles.
	if req.Audio != "NONE" {
		t.Fatalf("audio mode = %q, want NONE (the only lane that fans one clip over every language)", req.Audio)
	}
	if !req.Render.Enabled {
		t.Fatal("the render fan-out must be enabled")
	}
	if !req.Render.RequireGPU {
		t.Fatal("the certified lane is the GPU lane; a silent software fallback must fail the run")
	}
	if req.Render.Subtitles == nil || !req.Render.Subtitles.Enabled || req.Render.Subtitles.Mode != "burn" {
		t.Fatalf("subtitles = %+v, want them enabled and BURNED (a sidecar is not the shipped artifact)", req.Render.Subtitles)
	}
	if req.Render.Subtitles.Preset != verifyOneClipTenLanguagesSubtitlePreset {
		t.Fatalf("subtitle preset = %q, want %q", req.Render.Subtitles.Preset, verifyOneClipTenLanguagesSubtitlePreset)
	}

	// 3. Ten renders of the same clip: the source language plus nine targets.
	if req.SourceLanguage != "en" {
		t.Fatalf("source language = %q, want the interview language en", req.SourceLanguage)
	}
	if len(req.Languages) != len(verifyOneClipTenLanguagesTargets) {
		t.Fatalf("target languages = %v, want %v", req.Languages, verifyOneClipTenLanguagesTargets)
	}
	for i, want := range verifyOneClipTenLanguagesTargets {
		if string(req.Languages[i]) != want {
			t.Fatalf("target language[%d] = %q, want %q", i, req.Languages[i], want)
		}
		if want == string(req.SourceLanguage) {
			t.Fatalf("language %q is the source language; it cannot also be a translation target", want)
		}
	}

	// 4. One document — and therefore one folder — per language, source included.
	if !req.Docs.Enabled {
		t.Fatal("docs publishing must be explicit for this batch")
	}
	if len(req.Docs.Languages) != len(verifyOneClipTenLanguagesTargets)+1 {
		t.Fatalf("docs languages = %v, want the source plus the nine targets", req.Docs.Languages)
	}

	// 5. The clips fields are a FALLBACK, never the destination.
	docsRoot, err := scriptpkg.ResolveScriptDocsFolderID(req.Docs.Enabled, req.Docs.FolderID, "canonical-docs-root")
	if err != nil {
		t.Fatalf("resolve documents root: %v", err)
	}
	if docsRoot != "canonical-docs-root" {
		t.Fatalf("documents root = %q, want the configured default (the payload pins no docs.folder_id)", docsRoot)
	}
	if docsRoot == req.Render.DriveFolderID {
		t.Fatalf("documents root and the clips folder are both %q; the clip would not sit beside its script", docsRoot)
	}

	// The path constant is asserted too, so a rename of the acceptance job is a
	// deliberate edit here rather than a silently dead gate.
	if _, err := os.Stat(filepath.FromSlash(verifyOneClipTenLanguagesManifestPath)); err != nil {
		t.Fatalf("acceptance manifest %s is not where this gate reads it: %v", verifyOneClipTenLanguagesManifestPath, err)
	}
}

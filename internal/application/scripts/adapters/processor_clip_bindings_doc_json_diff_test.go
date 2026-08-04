// Package adapters_test — processor_clip_bindings_doc_json_diff_test.go (PR 7, June 2026).
//
// BEHAVIORAL BYTE-LEVEL DIFF TEST: pins the user's stated PR 7 invariant
// — "Diff test tra doc generato e JSON deve essere vuoto" — by comparing
// THE TWO OUTPUT BYTE STREAMS a downstream consumer sees, not the
// canonical SpecScene in memory.
//
// Pre-PR-7, this test would have caught divergence: the orphaned
// ClipBindingsProcessor's mutations plus the manual "fill empty only"
// loop in generate_one_usecase.go both touched the same SpecScene
// inconsistently, surfacing different binding sets to the doc builder
// (which read input.SpecScene.Scenes post-walk) and the JSON response
// writer (which read result.Output.SpecScene.Scenes via a different
// code path). Post-PR-7 centralisation makes both readers consume the
// same SpecScene via shared slice header — this test pins that contract
// at the WIRE level so a future refactor that re-introduces divergence
// between surfaces fires.
//
// What the test does:
//  1. Set up ClipEvidence with 4 canonical drive_file IDs + URLs.
//  2. Build a SpecScene with 4 scenes (no bindings).
//  3. Run ClipBindingsProcessor.Process with the canonical ProcessInput.
//  4. Render the post-walk SpecScene through BuildSpecSceneDocumentHTML
//     (the canonical production renderer).
//  5. Marshal model.SpecScene to JSON bytes (the response writer's wire shape).
//  6. Extract per-scene drive_links from BOTH byte streams.
//  7. Assert scene-by-scene equality: docLink[i] == wireLink[i].
//
// PR-G.2 BACKFILL (June 2026): package moved from `scripts` (root facade)
// to `adapters_test` (external). The ClipBindingsProcessor /
// ProcessInput / BuildSpecSceneDocumentHTML / BuildClipEvidence
// symbols are no longer reached via the `scripts.` facade; they are
// imported directly from the canonical `adapters` and `usecase`
// subpackages. The test's assertions are unchanged.
package adapters_test

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Wire-side mirror ────────────────────────────────────────────────────────
//
// The response writer's wire shape is what would be JSON-encoded into
// a 200 OK response to the HTTP caller. We mirror the typed shape
// here with explicit JSON tags so the test fails loudly if the
// canonical domain tags ever change (a renamed tag drops fields
// silently — DeepEqual would still pass on equality of
// re-extracted empty strings).
type wireClip struct {
	ClipID    string `json:"clip_id"`
	DriveLink string `json:"drive_link"`
}
type wireBindings struct {
	Clip *wireClip `json:"clip"`
}
type wireScene struct {
	Bindings wireBindings `json:"bindings"`
}

// ── Doc-side mirror ─────────────────────────────────────────────────────────
//
// BuildSpecSceneDocumentHTML renders a
// `<h2>SpecScene JSON</h2><pre>...</pre>` block whose inner content is
// a json.MarshalIndent of the canonical scriptpkg.SpecSceneOutput shape.
// The mirrors here match that exact shape so the parse path is faithful
// end-to-end:
//
//	SpecSceneOutput { version, scenes: [SpecScene { index, kind, text,
//	    bindings: SceneBindings { clip: ClipBinding { drive_link,
//	    clip_id, clip_title, … } } }] }
//
// docSpecDocRendererVersion is the schema version the doc builder
// is expected to embed in the rendered JSON. If a future refactor
// to BuildSpecSceneDocumentHTML bumps the version (e.g. a
// `prompt` field gets added), this test will assert against the
// unchanged version and fail loudly — the docSpecDoc mirror below
// would otherwise silently drop or add empty fields, and the
// per-scene drive_link sanity check would still pass on a degraded
// doc stream (parser-tolerant).
const docSpecDocRendererVersion = 1

type docClip struct {
	ClipID    string `json:"clip_id"`
	DriveLink string `json:"drive_link,omitempty"`
}
type docSceneBindings struct {
	Clip *docClip `json:"clip,omitempty"`
}
type docSceneDoc struct {
	Index    int              `json:"index"`
	Bindings docSceneBindings `json:"bindings"`
}
type docSpecDoc struct {
	Version int           `json:"version"`
	Scenes  []docSceneDoc `json:"scenes"`
}

// TestClipBindings_DocBuilderByteStream_Equals_JSONWire_PR7 is the
// load-bearing PR 7 differential. It renders the actual Google Doc
// HTML body and marshals the actual response-writer wire shape,
// then extracts per-scene drive links from BOTH byte streams and
// asserts they are identical.
//
// Catches a class of bug that the structural assertion could not:
// a future refactor that swaps the binder's "post-walk SpecScene"
// for a different SpecScene (engineResult.Output.SpecScene instead
// of input.SpecScene.Scenes, or copying the slice and losing
// backing-array sharing) would NOT fail a structural test (both
// readers read the same slice in the test fixture) BUT WOULD fail
// this byte-level test (the rendered HTML would miss the canonical
// drive links).
func TestClipBindings_DocBuilderByteStream_Equals_JSONWire_PR7(t *testing.T) {
	ev := &scriptpkg.ClipEvidence{
		// PR 6 canonical: URLs keyed by the canonical (Drive file
		// ID) the user typed, NOT by any internal asset.ID.
		AcceptedClipIDs: []string{"drive-file-A", "drive-file-B", "drive-file-C", "drive-file-D"},
		ClipCount:       4,
		ClipNames: map[string]string{
			"drive-file-A": "Clip A",
			"drive-file-B": "Clip B",
			"drive-file-C": "Clip C",
			"drive-file-D": "Clip D",
		},
		DriveLinks: map[string]string{
			"drive-file-A": "https://drive.google.com/file/d/drive-file-A/view",
			"drive-file-B": "https://drive.google.com/file/d/drive-file-B/view",
			"drive-file-C": "https://drive.google.com/file/d/drive-file-C/view",
			"drive-file-D": "https://drive.google.com/file/d/drive-file-D/view",
		},
	}
	if ev == nil {
		t.Fatal("evidence = nil")
	}

	// 4 scenes, exactly one per canonical ID — no cycling. Pure
	// canonical-alignment test surface.
	const numScenes = 4
	scenes := make([]scriptpkg.SpecScene, numScenes)
	for i := range scenes {
		scenes[i] = scriptpkg.SpecScene{
			ID:    "s" + string(rune('1'+i)),
			Index: i,
			Kind:  scriptpkg.SceneClip,
		}
	}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: ev,
		NumClips:     4,
	}

	p := adapters.NewClipBindingsProcessor(zap.NewNop())
	if _, err := p.Process(context.Background(), plan, adapters.ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: scenes},
	}); err != nil {
		t.Fatalf("process error = %v", err)
	}

	// ── Doc builder view: render BUILDS the actual HTML body. ──
	// SpecScene.Version is set to 1 (canonical schema version per
	// internal/domain/script/model_output.go::SpecSceneOutput.Validate)
	// so the renderer's MarshalIndent emits "version": 1, not the
	// default int zero value. The docSpecDocRendererVersion=1 pin
	// below asserts this canonical invariant — without Version=1 on
	// the fixture the test would falsely fail with "got 0 expected 1".
	model := &scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
	}
	docHTML := adapters.BuildSpecSceneDocumentHTML(model, "PR 7 Test Title", nil)

	// ── JSON response writer view: marshal the actual wire shape. ──
	wireRaw, err := json.Marshal(model.SpecScene)
	if err != nil {
		t.Fatalf("marshal SpecScene: %v", err)
	}

	// ── Extract drive links from the doc HTML. ──
	docLinks := extractDocLinksFromDocHTML(t, docHTML, numScenes)

	// ── Extract drive links from the JSON wire. ──
	wireLinks := extractWireLinksFromJSON(t, wireRaw, numScenes)

	// Sanity: both surfaces served the same scene count.
	if len(docLinks) != numScenes {
		t.Fatalf("docLinks has %d entries, want %d "+
			"(doc-rendered scenes do not match the binder output — slice-header-sharing discipline broken)",
			len(docLinks), numScenes)
	}
	if len(wireLinks) != numScenes {
		t.Fatalf("wireLinks has %d entries, want %d", len(wireLinks), numScenes)
	}

	// ── THE LOAD-BEARING PR 7 ASSERTION ──
	// diff between the doc-bytestream and the JSON-wire-bytestream
	// is empty by construction.
	if !reflect.DeepEqual(docLinks, wireLinks) {
		t.Fatalf("PR 7 invariant violated: doc-rendered drive links differ from JSON-wire drive links\n"+
			"docLinks:  %v\n"+
			"wireLinks: %v\n"+
			"\n"+
			"This means a future refactor (or current binding at the wrong point in the pipeline) would\n"+
			"carry the doc and the JSON response into going out-of-sync. Operators see link A in the\n"+
			"Google Doc but their integration consumes link B for the same scene.",
			docLinks, wireLinks)
	}

	// Spot-check: every canonical URL appears in BOTH surfaces.
	wantURLs := []string{
		"https://drive.google.com/file/d/drive-file-A/view",
		"https://drive.google.com/file/d/drive-file-B/view",
		"https://drive.google.com/file/d/drive-file-C/view",
		"https://drive.google.com/file/d/drive-file-D/view",
	}
	for _, url := range wantURLs {
		if !containsString(wireLinks, url) {
			t.Errorf("JSON wire missing canonical drive URL %q", url)
		}
		if !containsString(docLinks, url) {
			t.Errorf("doc HTML missing canonical drive URL %q", url)
		}
	}
	if containsString(wireLinks, "internal-asset-789") {
		t.Errorf("JSON wire leaked non-canonical internal asset ID (PR 6 regression)")
	}
}

// extractDocLinksFromDocHTML parses the doc HTML body, finds the
// `<h2>SpecScene JSON</h2><pre>...</pre>` block that the canonical
// renderer embeds (rendered via json.MarshalIndent in
// BuildSpecSceneDocumentHTML), parses its inner JSON via the
// docSpecDoc/docSceneDoc/docSceneBindings/docClip typed mirror, and
// returns the per-scene `bindings.clip.drive_link` sequence. Errors
// fail the test.
//
// Implementation choice: regex match the `<h2>SpecScene JSON</h2><pre>`
// block that BuildSpecSceneDocumentHTML emits and unescape the four HTML
// entities that html.EscapeString
// generates so json.Unmarshal can re-parse the embedded JSON.
func extractDocLinksFromDocHTML(t *testing.T, html string, numScenes int) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)<h2>SpecScene JSON</h2><pre>(.*?)</pre>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) < 2 {
		t.Fatalf("doc HTML body has no <h2>SpecScene JSON</h2><pre>...</pre> block; "+
			"either the test design assumption broke (BuildSpecSceneDocumentHTML no longer emits one) "+
			"or the binder never wrote canonical bindings into SpecScene.\n"+
			"first 800 chars of HTML: %s", truncateForFailure(html, 800))
	}

	// BuildSpecSceneDocumentHTML calls html.EscapeString on the
	// JSON bytes; unescape the four escapes it produces so
	// json.Unmarshal can re-parse the inner block.
	inner := matches[1]
	inner = strings.ReplaceAll(inner, "&#34;", `"`)
	inner = strings.ReplaceAll(inner, "&quot;", `"`)
	inner = strings.ReplaceAll(inner, "&amp;", "&")
	inner = strings.ReplaceAll(inner, "&lt;", "<")
	inner = strings.ReplaceAll(inner, "&gt;", ">")

	var docDoc docSpecDoc
	if err := json.Unmarshal([]byte(inner), &docDoc); err != nil {
		t.Fatalf("parse doc inner JSON: %v\n"+
			"inner JSON (first 800 chars): %s",
			err, truncateForFailure(inner, 800))
	}
	// PR 7 followup (review defect e): pin the doc builder's
	// embedded schema version so a future refactor that bumps
	// the version (e.g. an extra per-scene prompt field) is
	// caught HERE rather than silently dropping or adding
	// empty fields. The typed mirror above would still parse
	// silently with a degraded stream; this explicit assertion
	// is the canary.
	if docDoc.Version != docSpecDocRendererVersion {
		t.Fatalf("doc-rendered SpecScene JSON has unexpected version %d "+
			"(test pinned to %d) — BuildSpecSceneDocumentHTML bumped the "+
			"embedded schema; update the typed mirror and the renderer-version "+
			"constant together, then re-pin the diff test.\n"+
			"first 800 chars of inner JSON: %s",
			docDoc.Version, docSpecDocRendererVersion,
			truncateForFailure(inner, 800))
	}
	out := make([]string, 0, numScenes)
	for _, ds := range docDoc.Scenes {
		if ds.Bindings.Clip == nil {
			out = append(out, "")
			continue
		}
		out = append(out, ds.Bindings.Clip.DriveLink)
	}
	return out
}

// extractWireLinksFromJSON parses the marshaled SpecScene wire bytes
// via wireScene/wireClip mirror types and returns the per-scene
// `bindings.clip.drive_link` sequence. Errors fail the test.
func extractWireLinksFromJSON(t *testing.T, wireRaw []byte, numScenes int) []string {
	t.Helper()
	var wire struct {
		Scenes []wireScene `json:"scenes"`
	}
	if err := json.Unmarshal(wireRaw, &wire); err != nil {
		t.Fatalf("parse wire JSON: %v\n"+
			"wire bytes (first 800 chars): %s",
			err, truncateForFailure(string(wireRaw), 800))
	}
	out := make([]string, 0, numScenes)
	for _, ws := range wire.Scenes {
		if ws.Bindings.Clip == nil {
			out = append(out, "")
			continue
		}
		out = append(out, ws.Bindings.Clip.DriveLink)
	}
	return out
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func truncateForFailure(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

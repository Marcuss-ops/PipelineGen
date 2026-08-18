package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type captureSourceResolver struct {
	got scriptpkg.SourceSpec
}

func (r *captureSourceResolver) Resolve(_ context.Context, src scriptpkg.SourceSpec, _ scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	r.got = src
	return &scriptpkg.ResolvedSource{Type: src.Type, Topic: src.Topic, SourceText: "resolved research source"}, nil
}

func TestSceneTextGeneratorResolveVidRushPlanPreservesResearchPolicy(t *testing.T) {
	capture := &captureSourceResolver{}
	registry := adapters.NewSourceRegistry(nil)
	if !registry.Register(scriptpkg.SourceResearch, capture) {
		t.Fatal("register research resolver")
	}
	registry.Freeze()

	generator := &SceneTextGenerator{Registry: registry}
	req := scriptgen.GenerateRequest{
		SourceLanguage: "it",
		Source: scriptgen.Source{
			Type:         scriptgen.SourceType(scriptpkg.SourceResearch),
			Topic:        "Maya",
			Query:        "Maya archaeology",
			Search:       true,
			ForceRefresh: true,
			CachePolicy:  scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModeForceRefresh, Version: "research-v1"},
			Research:     scriptpkg.ResearchPolicy{MaxQueries: 4, MaxPages: 8, MinSources: 3, RequireCitations: true},
		},
		Title: "Maya research",
	}

	if _, err := generator.ResolveVidRushPlan(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !capture.got.Search || !capture.got.ForceRefresh {
		t.Fatalf("research search policy was lost at the wiring boundary: %+v", capture.got)
	}
	if capture.got.CachePolicy.Mode != scriptpkg.SourceCacheModeForceRefresh || capture.got.CachePolicy.Version != "research-v1" {
		t.Fatalf("research cache policy was lost at the wiring boundary: %+v", capture.got.CachePolicy)
	}
	if capture.got.Research.MaxQueries != 4 || capture.got.Research.MaxPages != 8 || capture.got.Research.MinSources != 3 || !capture.got.Research.RequireCitations {
		t.Fatalf("research policy was lost at the wiring boundary: %+v", capture.got.Research)
	}
}

func TestSceneTextGeneratorResolveVidRushPlanCarriesMediaPlan(t *testing.T) {
	generator := &SceneTextGenerator{}
	req := scriptgen.GenerateRequest{
		SourceLanguage: "en",
		Source:         scriptgen.Source{Type: scriptgen.SourceText, Topic: "topic"},
		Title:          "media-plan-title",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleEnabled},
			Extraction:     mediadomain.MediaExtractionPolicy{Enabled: true, MaxEntitiesPerSegment: 9},
		},
	}

	plan, err := generator.ResolveVidRushPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil {
		t.Fatal("ResolveVidRushPlan returned nil plan")
	}
	if !plan.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		t.Fatal("media plan provider policy must be propagated into the resolved plan")
	}
	if plan.MediaPlan.Extraction.MaxEntitiesPerSegment != 9 {
		t.Fatalf("extraction limits = %+v, want max entities 9", plan.MediaPlan.Extraction)
	}
}

type scenetextClipResolver struct {
	clip *asset.Asset
}

func (r scenetextClipResolver) ResolveByMediaAssetID(context.Context, string) (*asset.Asset, error) {
	return r.clip, nil
}

func TestSceneTextGeneratorConvertScenesEnrichesCanonicalClipFields(t *testing.T) {
	clip := &asset.Asset{ID: "clip-1", Duration: 6500 * time.Millisecond}
	clip.SetLocalPath("/var/lib/pipelinegen/clips/clip-1.mp4")
	clip.SetFileHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	generator := &SceneTextGenerator{ClipAssets: scenetextClipResolver{clip: clip}}
	result := &usecase.EngineResult{Output: scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-1", Index: 0, Text: "clip narration",
			Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{
				ClipID: "clip-1", StartMs: 1200, EndMs: 4200,
			}},
		}}},
	}}

	scenes, err := generator.convertScenes(context.Background(), result, scriptgen.Language("en"), capabilityaudio.AudioModeNone, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 || scenes[0].Clip == nil {
		t.Fatalf("expected one enriched clip scene: %#v", scenes)
	}
	got := scenes[0].Clip
	if got.Path != "/var/lib/pipelinegen/clips/clip-1.mp4" || got.SHA256 == "" || got.AudioPath != got.Path || got.Duration != 6.5 || got.SourceInMS != 1200 || got.SourceOutMS != 4200 {
		t.Fatalf("canonical clip enrichment incomplete: %#v", got)
	}
}

func TestSceneTextGeneratorConvertScenesPreservesAllClipBindings(t *testing.T) {
	clip := &asset.Asset{ID: "registry-asset", Duration: 2 * time.Second}
	clip.SetLocalPath("/var/lib/pipelinegen/clips/clip.mp4")
	clip.SetFileHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	generator := &SceneTextGenerator{ClipAssets: scenetextClipResolver{clip: clip}}
	result := &usecase.EngineResult{Output: scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID: "scene-1", Index: 0, Text: "multi clip",
		Bindings: scriptpkg.SceneBindings{Clips: []scriptpkg.ClipBinding{{ClipID: "clip-a"}, {ClipID: "clip-b"}}},
	}}}}}
	scenes, err := generator.convertScenes(context.Background(), result, scriptgen.Language("en"), capabilityaudio.AudioModeCombinedTimeline, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 || len(scenes[0].Clips) != 2 || scenes[0].Clips[0].ID != "clip-a" || scenes[0].Clips[1].ID != "clip-b" {
		t.Fatalf("multi-clip bindings were not preserved: %#v", scenes)
	}
	if scenes[0].Clip != scenes[0].Clips[0] || len(scenes[0].AudioIntents) != 2 {
		t.Fatalf("primary alias or audio intents lost: %#v", scenes[0])
	}
}

// driveOnlyFixture returns a SpecScene bound to a Drive-only clip: registry
// row with a Drive link and duration, but no local path and no file hash.
func driveOnlyFixture() *usecase.EngineResult {
	return &usecase.EngineResult{Output: scriptpkg.ModelScriptOutputV1{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: "scene-1", Index: 0, Text: "clip narration",
			Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{
				ClipID: "clip-drive-1", ClipTitle: "Drive-only clip", DriveLink: "https://drive.google.com/file/d/clip-drive-1",
			}},
		}}},
	}}
}

// TestSceneTextGenerator_DriveOnlyClipTimelineDoesNotRequireLocalBinary
// certifies the planning boundary: a Drive-only clip with a ready transcript
// must resolve into a canonical clip reference (binding preserved) without any
// local binary identity being fabricated. requireLocalMedia=false is the
// GenerateTimeline-only path — it must not stage media or demand a SHA.
func TestSceneTextGenerator_DriveOnlyClipTimelineDoesNotRequireLocalBinary(t *testing.T) {
	clip := &asset.Asset{ID: "clip-drive-1", Duration: 45 * time.Second}
	clip.SetDriveLink("https://drive.google.com/file/d/clip-drive-1")

	generator := &SceneTextGenerator{ClipAssets: scenetextClipResolver{clip: clip}}
	scenes, err := generator.convertScenes(context.Background(), driveOnlyFixture(), scriptgen.Language("en"), capabilityaudio.AudioModeNone, false)
	if err != nil {
		t.Fatalf("timeline planning must not fail on a Drive-only clip: %v", err)
	}
	if len(scenes) != 1 || scenes[0].Clip == nil {
		t.Fatalf("expected one Drive-only clip scene: %#v", scenes)
	}
	got := scenes[0].Clip
	if got.Path != "" || got.SHA256 != "" || got.AudioPath != "" {
		t.Fatalf("no local binary identity may be fabricated: %#v", got)
	}
	if got.ID != "clip-drive-1" || got.DriveLink != "https://drive.google.com/file/d/clip-drive-1" {
		t.Fatalf("canonical binding must survive: %#v", got)
	}
	if got.Duration == 0 || got.SourceOutMS <= got.SourceInMS {
		t.Fatalf("planning duration must be estimated for timeline compilation: %#v", got)
	}
}

// TestSceneTextGenerator_RenderRequiresMaterializedBinary keeps the render
// boundary fail-closed: requireLocalMedia=true (RenderVideo) on the very same
// Drive-only clip must fail with the canonical "asset has no local path"
// error — never a fabricated clip reference.
func TestSceneTextGenerator_RenderRequiresMaterializedBinary(t *testing.T) {
	clip := &asset.Asset{ID: "clip-drive-1", Duration: 45 * time.Second}
	clip.SetDriveLink("https://drive.google.com/file/d/clip-drive-1")

	generator := &SceneTextGenerator{ClipAssets: scenetextClipResolver{clip: clip}}
	_, err := generator.convertScenes(context.Background(), driveOnlyFixture(), scriptgen.Language("en"), capabilityaudio.AudioModeNone, true)
	if err == nil || !strings.Contains(err.Error(), "asset has no local path") {
		t.Fatalf("RenderVideo=true must fail closed on a Drive-only clip: %v", err)
	}
}

// TestSceneTextGenerator_DriveReferenceCannotReplaceBinarySHA certifies the
// binary identity contract at the wiring boundary: a forged file_hash on a
// Drive-only asset must not fabricate a SHA, and legacy colon-form MD5 hashes
// must always fall back to the SHA-256 over the real materialized bytes.
func TestSceneTextGenerator_DriveReferenceCannotReplaceBinarySHA(t *testing.T) {
	// Forgery A: Drive-only asset with a forged file_hash — no local path, no identity.
	clip := &asset.Asset{ID: "clip-drive-1", Duration: 45 * time.Second}
	clip.SetDriveLink("https://drive.google.com/file/d/clip-drive-1")
	clip.SetFileHash("clip-drive-1")
	if _, err := renderAssetSHA256(clip); err == nil || !strings.Contains(err.Error(), "asset has no local path") {
		t.Fatalf("forged file_hash must never replace materialization: %v", err)
	}

	// A real materialized file is the only source of truth for the binary identity.
	path := t.TempDir() + "/clip.mp4"
	contents := []byte("real materialized bytes")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	want := hex.EncodeToString(sum[:])

	local := &asset.Asset{ID: "clip-local-1", Duration: 45 * time.Second}
	local.SetLocalPath(path)
	got, err := renderAssetSHA256(local)
	if err != nil || got != want {
		t.Fatalf("expected SHA-256 over real bytes, got %q err %v", got, err)
	}

	// Legacy Drive MD5 colon-form is never accepted as a binary identity.
	local.SetFileHash("0123456789abcdef0123456789abcdef:drive-md5")
	got, err = renderAssetSHA256(local)
	if err != nil || got != want {
		t.Fatalf("colon-form MD5 must fall back to bytes SHA-256, got %q err %v", got, err)
	}
}

// TestSceneTextGeneratorBuildPlanPropagatesMemoryGate certifies the durable
// runner's plan carries the memory-gate inputs the legacy finalizer used to
// own: UseMemory + ForceRefresh + a deterministic, content-aware CacheKey.
// Without these, the single-item durable path regenerates scene text on every
// warm replay instead of returning an exact hit.
func TestSceneTextGeneratorBuildPlanPropagatesMemoryGate(t *testing.T) {
	generator := &SceneTextGenerator{}
	req := scriptgen.GenerateRequest{
		IdempotencyKey: "maya-cold-001",
		Title:          "Maya cache verification",
		SourceLanguage: "it",
		Source: scriptgen.Source{
			Type:       scriptgen.SourceText,
			Topic:      "Maya cache verification",
			SourceText: "Una spedizione documenta una scoperta archeologica in una valle remota.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords:   360,
			UseMemory:     true,
			PromptVersion: "vidrush-maya-cache-v1",
		},
	}

	plan, err := generator.ResolveVidRushPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.UseMemory {
		t.Fatal("UseMemory must be propagated from ScriptParams into the plan")
	}
	if plan.ForceRefresh {
		t.Fatal("ForceRefresh must default false when neither envelope nor ScriptParams request it")
	}
	if plan.CacheKey == "" {
		t.Fatal("CacheKey must be computed for the plan")
	}
	if plan.SourceFingerprint == "" {
		t.Fatal("SourceFingerprint must be derived for a text source")
	}

	// Determinism: the same request must resolve to the same cache key.
	plan2, err := generator.ResolveVidRushPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.CacheKey != plan.CacheKey {
		t.Fatalf("cache key must be deterministic: %q != %q", plan2.CacheKey, plan.CacheKey)
	}

	// Content awareness: a different source text must change the cache key.
	changed := req
	changed.Source.SourceText = "Un testo completamente diverso su un altro argomento."
	plan3, err := generator.ResolveVidRushPlan(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if plan3.CacheKey == plan.CacheKey {
		t.Fatalf("cache key must change with source text: %q", plan.CacheKey)
	}

	// Envelope-level force_refresh must bypass the memory gate.
	forced := req
	forced.ForceRefresh = true
	planForce, err := generator.ResolveVidRushPlan(context.Background(), forced)
	if err != nil {
		t.Fatal(err)
	}
	if !planForce.ForceRefresh {
		t.Fatal("envelope-level ForceRefresh must propagate into the plan")
	}
}

// TestBuildEditorialPrompt_ForcesExplicitEntityMention certifies the
// entity-mention contract: the editorial prompt must demand that named
// entities be stated verbatim (never reduced to pronouns/generic
// descriptors), so the downstream entity extractor can recover the PERSON
// the controlled certification is trying to prove.
func TestBuildEditorialPrompt_ForcesExplicitEntityMention(t *testing.T) {
	prompt := buildEditorialPrompt(
		"Dwayne Johnson and wrestling",
		"Dwayne Johnson is the central person. The setting is Miami. The main concept is wrestling.",
		"Dwayne Johnson and wrestling",
		"",
		700,
		450,
		"Scrivi un documentario storico preciso",
		"",
		"it",
		"history-research-entities-v1",
	)
	for _, want := range []string{
		"Topic: Dwayne Johnson and wrestling",
		"Source text:",
		"Dwayne Johnson is the central person",
		"Target words: 700",
		"Min words: 450",
		"Style: Scrivi un documentario storico preciso",
		"Language: it",
		"Prompt version: history-research-entities-v1",
		"You MUST explicitly mention, verbatim and by name, the central person, the place and the main concept named in the source text",
		"never replace a named entity with only a pronoun or a generic descriptor",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("editorial prompt missing %q:\n%s", want, prompt)
		}
	}
}

// ── SceneTextStreamer wiring (adapter → Ollama engine) ───────────────────

// recordingStreamScriptGenerator is a ports.ScriptGenerator that returns one
// deterministic single-scene V1 envelope per call and records the
// interleaving of provider calls and scene emissions. The recorded sequence
// proves the adapter emits each scene before issuing the next model call
// (true streaming) rather than returning the complete result up front.
type recordingStreamScriptGenerator struct {
	mu     sync.Mutex
	calls  int
	events []string
}

func v1Envelope(text string) string {
	return `{"schema_version":1,"text":"` + text + `","specscene":{"version":1,"scenes":[{"id":"model-scene","index":0,"text":"` + text + `","kind":"narration","bindings":{}}]}}`
}

func (g *recordingStreamScriptGenerator) GenerateScript(_ context.Context, _ scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	g.mu.Lock()
	index := g.calls
	g.calls++
	g.events = append(g.events, fmt.Sprintf("call:%d", index))
	g.mu.Unlock()
	text := fmt.Sprintf("narration for segment %d", index)
	return &scriptports.GenerationResult{Script: v1Envelope(text), WordCount: 3, Model: "fake-model"}, nil
}

func (g *recordingStreamScriptGenerator) recordEmit(index int) {
	g.mu.Lock()
	g.events = append(g.events, fmt.Sprintf("emit:%d", index))
	g.mu.Unlock()
}

func (g *recordingStreamScriptGenerator) snapshotEvents() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

// TestSceneTextGeneratorStreamsSegments proves segmented calls overlap while
// each scene is emitted as soon as its own model result is final.
func TestSceneTextGeneratorStreamsSegmentsOneAtATime(t *testing.T) {
	gen := &recordingStreamScriptGenerator{}
	engine := usecase.NewEngine(gen, nil, zap.NewNop())
	generator := NewSceneTextGenerator(engine, zap.NewNop())

	req := scriptgen.GenerateRequest{
		SourceLanguage: "en",
		Source:         scriptgen.Source{Type: scriptgen.SourceText, Topic: "Segmented topic"},
		Title:          "Segmented",
		ScriptParams:   scriptpkg.ScriptSpec{SegmentTopics: []string{"Intro", "Body", "Outro"}},
	}

	var emitted []scriptgen.Scene
	_, err := generator.GenerateSceneTextStreamWithTrace(context.Background(), req, func(scene scriptgen.Scene) error {
		gen.recordEmit(scene.Index)
		emitted = append(emitted, scene)
		return nil
	})
	require.NoError(t, err)

	require.Len(t, emitted, 3, "three segments must produce three scenes")
	for i, scene := range emitted {
		require.Equal(t, i, scene.Index, "scenes must arrive in canonical order")
		require.Equal(t, fmt.Sprintf("scene-%d", i), scene.ID)
		require.Equal(t, fmt.Sprintf("narration for segment %d", i), scene.Text["en"])
	}
	// Fan-out proof: all calls are not forced through a call→emit barrier.
	events := gen.snapshotEvents()
	require.Len(t, events, 6)
	callCount := 0
	emitCount := 0
	for _, event := range events {
		if strings.HasPrefix(event, "call:") {
			callCount++
		} else if strings.HasPrefix(event, "emit:") {
			emitCount++
		}
	}
	require.Equal(t, 3, callCount)
	require.Equal(t, 3, emitCount)
	require.Contains(t, events[:3], "call:1", "a second segment must start before the first emission")
}

// proseBatchScriptGenerator returns a two-scene V1 envelope from a single
// model call, simulating the prose (no explicit segment) case.
type proseBatchScriptGenerator struct {
	mu    sync.Mutex
	calls int
}

func (g *proseBatchScriptGenerator) GenerateScript(_ context.Context, _ scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	script := `{"schema_version":1,"text":"one. two.","specscene":{"version":1,"scenes":[` +
		`{"id":"scene-0","index":0,"text":"one.","kind":"narration","bindings":{}},` +
		`{"id":"scene-1","index":1,"text":"two.","kind":"narration","bindings":{}}]}}`
	return &scriptports.GenerationResult{Script: script, WordCount: 4, Model: "fake-model"}, nil
}

// TestSceneTextGeneratorStreamFallsBackToBatchForProse documents the prose
// boundary: without explicit editorial segments there is no safe scene
// boundary, so the adapter keeps the single batch call and emits the
// already-planned scenes afterward (still through the same streaming emit
// seam the runner consumes).
func TestSceneTextGeneratorStreamFallsBackToBatchForProse(t *testing.T) {
	gen := &proseBatchScriptGenerator{}
	engine := usecase.NewEngine(gen, nil, zap.NewNop())
	generator := NewSceneTextGenerator(engine, zap.NewNop())

	req := scriptgen.GenerateRequest{
		SourceLanguage: "en",
		Source:         scriptgen.Source{Type: scriptgen.SourceText, Topic: "Prose topic"},
		Title:          "Prose",
	}

	var emitted []scriptgen.Scene
	_, err := generator.GenerateSceneTextStreamWithTrace(context.Background(), req, func(scene scriptgen.Scene) error {
		emitted = append(emitted, scene)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, gen.calls, "prose without segments must stay a single batch call")
	require.Len(t, emitted, 2)
	require.Equal(t, "scene-0", emitted[0].ID)
	require.Equal(t, "scene-1", emitted[1].ID)
}

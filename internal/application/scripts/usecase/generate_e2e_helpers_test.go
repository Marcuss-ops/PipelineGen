// Package scripts — generate_e2e_helpers_test.go provides shared
// test doubles and constructors for the mandatory E2E suite covering
// POST /api/script/generate.
package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// fakeClipResolver is a thread-safe stub for typedClipResolverPort.
// It maps clip IDs to *asset.Asset and can be configured to fail or
// return missing assets for specific IDs.
type fakeClipResolver struct {
	mu         sync.RWMutex
	clips      map[string]*asset.Asset
	mediaCalls []string
	driveCalls []string
}

func newFakeClipResolver() *fakeClipResolver {
	return &fakeClipResolver{clips: make(map[string]*asset.Asset)}
}

func (r *fakeClipResolver) AddClip(a *asset.Asset) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clips[a.ID] = a
}

func (r *fakeClipResolver) ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mediaCalls = append(r.mediaCalls, id)
	if a, ok := r.clips[id]; ok {
		return a, nil
	}
	return nil, nil
}

func (r *fakeClipResolver) ResolveByDriveFileID(ctx context.Context, id string) ([]*asset.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.driveCalls = append(r.driveCalls, id)
	if a, ok := r.clips[id]; ok {
		return []*asset.Asset{a}, nil
	}
	return nil, nil
}

// defaultClipSearchText is the shared source text used for clip
// evidence so the generated text overlaps with the source and passes
// the quality gate.
const defaultClipSearchText = "The quick brown fox jumps over the lazy dog."

// makeTestClip builds a minimal *asset.Asset suitable for clip-source
// resolution. duration is rounded to milliseconds and stored in metadata
// so BuildClipContext can read it.
func makeTestClip(id, name string, duration time.Duration) *asset.Asset {
	a := &asset.Asset{
		ID:         id,
		Name:       name,
		Filename:   name + ".mp4",
		MediaType:  asset.MediaTypeClip,
		Duration:   duration,
		SearchText: defaultClipSearchText,
		Metadata:   make(asset.Metadata),
	}
	a.SetDriveFileID("drive-" + id)
	a.SetDriveLink("https://drive.google.com/file/d/drive-" + id + "/view")
	a.SetMetadataInt("duration_ms", int(duration.Milliseconds()))
	return a
}

// makeTextOnlyItem returns a minimal GenerationItemV2 with SourceText.
func makeTextOnlyItem(id, sourceText string) scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:       id,
		Title:    "E2E " + id,
		Language: "en",
		Tone:     "neutral",
		Style:    "cinematic",
		Model:    "llama3:8b",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "e2e topic",
			SourceText: sourceText,
		},
		ScriptParams: scriptpkg.ScriptSpec{TargetWords: 10},
		Output: scriptpkg.OutputSpec{
			SaveToDB: false,
		},
	}
}

// makeClipsItem returns a GenerationItemV2 with SourceClips.
func makeClipsItem(id string, clipIDs []string, sourceText string) scriptpkg.GenerationItemV2 {
	item := makeTextOnlyItem(id, sourceText)
	item.Source = scriptpkg.SourceSpec{
		Type:       scriptpkg.SourceClips,
		ClipIDs:    clipIDs,
		SourceText: sourceText,
	}
	return item
}

// canonicalSceneJSON returns a V1 SpecScene JSON string.
//
// For clip sources it binds each accepted clip to exactly one scene,
// which satisfies the clip-native contract enforcement. The generated
// text is built from sourceText words so the quality gate's
// source_text coverage check passes.
func canonicalSceneJSON(numScenes int, clipIDs []string, sourceText string) string {
	// Build text that overlaps with the source text so coverage passes.
	// Use a deterministic phrase derived from the source text when possible.
	text := buildOverlappingText(numScenes, sourceText)

	// Bind each scene to a clip when clip IDs are provided.
	var scenes []string
	for i := 0; i < numScenes; i++ {
		clipID := ""
		if i < len(clipIDs) {
			clipID = clipIDs[i]
		}
		bindings := "{}"
		if clipID != "" {
			bindings = fmt.Sprintf(`{"clip":{"clip_id":%q}}`, clipID)
		}
		scenes = append(scenes, fmt.Sprintf(
			`{"id":"scene-%d","index":%d,"text":%q,"kind":"narration","bindings":%s}`,
			i, i, text, bindings,
		))
	}

	return fmt.Sprintf(
		`{"schema_version":1,"text":%q,"specscene":{"version":1,"scenes":[%s]}}`,
		text,
		strings.Join(scenes, ","),
	)
}

// stopWordTest reports whether a token is a common stop word.
func stopWordTest(token string) bool {
	return textutil.IsStopWord(token)
}

// buildOverlappingText returns a short sentence whose words are drawn
// from sourceText so the quality gate's source_text coverage stays high.
// If sourceText is empty, a safe default is returned.
func buildOverlappingText(numScenes int, sourceText string) string {
	defaultText := "The quick brown fox jumps over the lazy dog."
	if sourceText == "" {
		return defaultText
	}
	// Tokenise source text and keep only non-stop-word tokens so the
	// coverage check sees real overlap.
	tokens := tokenize(sourceText)
	var words []string
	for _, t := range tokens {
		if !stopWordTest(t) {
			words = append(words, t)
		}
	}
	if len(words) == 0 {
		return defaultText
	}

	// Build a sentence by repeating the unique source words. This
	// guarantees every generated token appears in the source text,
	// giving near-perfect coverage.
	var parts []string
	for i := 0; i < numScenes*3+3; i++ {
		parts = append(parts, words[i%len(words)])
	}
	return strings.Join(parts, " ") + "."
}

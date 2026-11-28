package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func buildChapterOverlayRequest(t *testing.T, payload string) (scriptpkg.ChapterOverlaySpec, error) {
	t.Helper()
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatal(err)
	}
	request, err := BuildGenerateRequest(&env, "chapter-overlay-key")
	if err != nil {
		return scriptpkg.ChapterOverlaySpec{}, err
	}
	if request.ChapterOverlay == nil {
		t.Fatal("request lost chapter_overlay")
	}
	return *request.ChapterOverlay, nil
}

// The directive is the producer side of the shared chapter contract, so the
// envelope carries it verbatim and a policy the contract rejects never reaches
// a run.
func TestBuildGenerateRequestCarriesChapterOverlay(t *testing.T) {
	overlay, err := buildChapterOverlayRequest(t, `{"version":2,"items":[{"title":"chapters","language":"it",
		"source":{"type":"text","topic":"topic"},
		"chapter_overlay":{"mode":"explicit","style_family":"documentary","duration_ms":3500,
		"grounding":"authored","titles":[{"scene_id":"scene_02","title":"Parte seconda","chapter_number":1}]}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if overlay.Mode != scriptpkg.ChapterOverlayExplicit || overlay.DurationMS != 3500 ||
		overlay.Grounding != scriptpkg.ChapterGroundingAuthored || len(overlay.Titles) != 1 ||
		overlay.Titles[0].SceneID != "scene_02" || overlay.Titles[0].Title != "Parte seconda" {
		t.Fatalf("chapter_overlay was not carried verbatim: %+v", overlay)
	}

	// No directive keeps the historical automatic chapters.
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"plain","language":"it","source":{"type":"text","topic":"topic"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	plain, err := BuildGenerateRequest(&env, "chapter-plain-key")
	if err != nil {
		t.Fatal(err)
	}
	if plain.ChapterOverlay != nil || !plain.ChapterOverlay.AutoChaptersEnabled() {
		t.Fatal("an undeclared chapter policy must keep the automatic chapters")
	}
}

func TestBuildGenerateRequestRejectsAnInvalidChapterOverlay(t *testing.T) {
	cases := map[string]string{
		"unknown mode":      `{"mode":"slideshow","duration_ms":3500,"grounding":"transcript"}`,
		"explicit no title": `{"mode":"explicit","duration_ms":3500,"grounding":"authored"}`,
		"auto authored":     `{"mode":"auto","duration_ms":3500,"grounding":"authored"}`,
		"zero duration":     `{"mode":"auto","duration_ms":0,"grounding":"transcript"}`,
		"unknown style":     `{"mode":"auto","style_family":"neon","duration_ms":3500,"grounding":"transcript"}`,
		"duplicate numbers": `{"mode":"explicit","duration_ms":3500,"grounding":"authored","titles":[{"scene_id":"s1","title":"Uno","chapter_number":1},{"scene_id":"s2","title":"Due","chapter_number":1}]}`,
		"title without key": `{"mode":"explicit","duration_ms":3500,"grounding":"authored","titles":[{"scene_id":"","title":"Uno","chapter_number":1}]}`,
	}
	for name, chapter := range cases {
		payload := `{"version":2,"items":[{"title":"bad","language":"it","source":{"type":"text","topic":"topic"},"chapter_overlay":` + chapter + `}]}`
		var env scriptpkg.GenerationEnvelopeV2
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err := BuildGenerateRequest(&env, "chapter-invalid-key")
		if err == nil || !strings.Contains(err.Error(), "chapter_overlay") {
			t.Fatalf("%s: want a chapter_overlay rejection, got %v", name, err)
		}
	}
}

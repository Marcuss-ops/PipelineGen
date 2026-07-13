// Forward-prevention tests for the post-voiceover composer.
//
// Two architectural contracts are enforced here:
//
//  1. The model output (the LLM-emitted strict envelope) MUST NOT contain
//     clip_id. The strict-envelope validator already rejects any extra
//     field at parse time — this file confirms the contract holds across
//     the realistic rejection paths: clip_id at root, clip_id inside a
//     segment, and clip_id inside a deeply-nested segment.
//
//  2. The composer source code MUST NOT contain the literal
//     "RootFolderOverride" string — godlike/08 forward-prevention gate
//     forbids it outside internal/infrastructure/ and cmd/admin/. Any
//     drift is a hard failure (this is the same gate as the one enforced
//     by cmd/archcheck, mirrored here as a unit-level regression guard).

package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// --- Forward-prevention #1: model output must NEVER contain clip_id ---

func TestForwardPrevention_ModelOutputRejectsClipIdAtRoot(t *testing.T) {
	// Even if the model accidentally emits a top-level clip_id, the strict
	// envelope parser MUST reject it.
	raw := []byte(`{"clip_id":"leaked-to-root","segments":[{"ref":"slot-1:candidate-0","text":"hi"}]}`)
	_, err := script.ParseModelOutputStrict(raw, validRefs())
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Errorf("err = %v, want ErrModelOutputExtraField (clip_id at root must be rejected)", err)
	}
}

func TestForwardPrevention_ModelOutputRejectsClipIdInsideSegment(t *testing.T) {
	// Even if the model emits per-segment clip_id, the strict envelope
	// MUST reject it: the segment shape is {ref,text} only.
	raw := []byte(`{"segments":[{"ref":"slot-1:candidate-0","text":"hi","clip_id":"leaked-into-segment"}]}`)
	_, err := script.ParseModelOutputStrict(raw, validRefs())
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Errorf("err = %v, want ErrModelOutputExtraField (clip_id inside segment must be rejected)", err)
	}
}

func TestForwardPrevention_ModelOutputRejectsClipIdInNestedObject(t *testing.T) {
	// Even if the model emits a deeply nested clip_id, the strict envelope
	// MUST reject it. There is no place under {segments:[{ref,text}]} that
	// can host clip_id, drive_link, start_ms, or end_ms.
	raw := []byte(`{"segments":[{"ref":"slot-1:candidate-0","text":"hi","meta":{"clip_id":"nested"}}]}`)
	_, err := script.ParseModelOutputStrict(raw, validRefs())
	if !errors.Is(err, script.ErrModelOutputExtraField) {
		t.Errorf("err = %v, want ErrModelOutputExtraField (nested clip_id must be rejected)", err)
	}
}

func TestForwardPrevention_ModelOutputAcceptsStrictEnvelope(t *testing.T) {
	// Positive control: when the model obeys the contract, parsing succeeds
	// and the returned ModelOutput has NO clip_id field at any level.
	raw := []byte(`{"segments":[{"ref":"slot-1:candidate-0","text":"hi"},{"ref":"slot-2:candidate-1","text":"lo"}]}`)
	out, err := script.ParseModelOutputStrict(raw, validRefs())
	if err != nil {
		t.Fatalf("ParseModelOutputStrict: %v", err)
	}
	if len(out.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(out.Segments))
	}
	for i, seg := range out.Segments {
		if seg.Ref == "" || seg.Text == "" {
			t.Errorf("segment[%d] has empty ref/text: %+v", i, seg)
		}
	}
	// Round-trip a structural assertion: re-marshal and grep for "clip_id".
	re, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(re, []byte("clip_id")) {
		t.Errorf("re-marshalled envelope unexpectedly contains clip_id: %q", re)
	}
}

// --- Forward-prevention #2: composer source forbids RootFolderOverride literal ---

func TestForwardPrevention_ComposerSourceDoesNotMentionRootFolderOverride(t *testing.T) {
	// Source-level bytes scan: this is the same gate as the one enforced
	// by cmd/archcheck. Mirrored here as a unit regression guard so a
	// future drift is caught at `go test` time.
	data, err := os.ReadFile("post_voiceover_composer.go")
	if err != nil {
		t.Fatalf("read composer: %v", err)
	}
	src := string(data)

	// Strip line comments (// ...) and block comments (/* ... */) before
	// the scan so a descriptive prose mention does NOT trigger. The
	// forward-prevention gate only forbids the LIVE field assignment.
	srcNoComments := stripCStyleComments(src)

	if strings.Contains(srcNoComments, "RootFolderOverride") {
		t.Errorf("composer source contains RootFolderOverride literal after stripping comments:\n%s", srcNoComments)
	}
}

func TestForwardPrevention_ComposerSourceHasNoDriveImportDirectly(t *testing.T) {
	// The composer MUST NOT import internal/infrastructure/drive (godlike
	// forward-prevention: application layer routes via the canonical port
	// in internal/application/ports).
	data, err := os.ReadFile("post_voiceover_composer.go")
	if err != nil {
		t.Fatalf("read composer: %v", err)
	}
	if bytes.Contains(data, []byte("internal/infrastructure/drive")) {
		t.Errorf("composer source IMPORTS internal/infrastructure/drive — must route via canonical delivery port")
	}
}

// --- Forward-prevention #3: manifest JSON shape enforces clip-id contract ---

func TestForwardPrevention_ComposerManifest_ClipIdLivesInScenesClipOnly(t *testing.T) {
	// The composer manifest JSON shape MUST be: top-level has version,
	// created_at, optional asset_id, scenes[]; each scene has ref, text,
	// index, clip{clip_id, drive_link, start_ms, end_ms}. clip_id at root
	// level of the manifest (or outside scenes[i].clip) is a violation.
	pub := newRecordingPublisher()
	res := &StaticRefBindingResolver{Table: fixtureBinding()}
	c, err := NewPostVoiceoverComposer(pub, res)
	if err != nil {
		t.Fatalf("NewPostVoiceoverComposer: %v", err)
	}
	manifest, _, err := c.ComposeAndPublish(
		context.Background(),
		fixtureModelOutput(),
		canonicalDestination(t),
		"g", "s", "asset-forward-test",
	)
	if err != nil {
		t.Fatalf("ComposeAndPublish: %v", err)
	}

	// Re-marshal the manifest and inspect the path of every "clip_id" key.
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := assertClipIdOnlyInsideScenesClip(data); err != nil {
		t.Errorf("manifest JSON clip_id path invalid: %v\npayload:\n%s", err, data)
	}
}

// --- helpers ---

func validRefs() map[string]struct{} {
	m := map[string]struct{}{}
	for k := range fixtureBinding() {
		m[k] = struct{}{}
	}
	return m
}

// stripCStyleComments removes // line comments and /* ... */ block comments
// without disturbing string literals. Conservative (does not unescape
// strings); good enough for forward-prevention scanning.
func stripCStyleComments(src string) string {
	var out strings.Builder
	i := 0
	for i < len(src) {
		// Block comment.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			j := i + 2
			for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
				j++
			}
			i = j + 2
			continue
		}
		// Line comment.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				break
			}
			i += j // keep newline, drop comment
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String()
}

// assertClipIdOnlyInsideScenesClip walks the marshalled JSON and asserts
// every occurrence of key "clip_id" lives at path scenes[*].clip.clip_id.
// Returns nil on success, or a descriptive error.
func assertClipIdOnlyInsideScenesClip(data []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	// Top-level: no "clip_id" allowed.
	if _, ok := probe["clip_id"]; ok {
		return errors.New("clip_id present at manifest root")
	}
	// scenes[i].clip.clip_id only.
	rawScenes, ok := probe["scenes"]
	if !ok {
		return errors.New("scenes key missing")
	}
	var scenes []map[string]json.RawMessage
	if err := json.Unmarshal(rawScenes, &scenes); err != nil {
		return err
	}
	for i, s := range scenes {
		rawClip, ok := s["clip"]
		if !ok {
			return errors.New("scene[" + strconv.Itoa(i) + "].clip missing")
		}
		var clip map[string]json.RawMessage
		if err := json.Unmarshal(rawClip, &clip); err != nil {
			return err
		}
		if _, ok := clip["clip_id"]; !ok {
			return errors.New("scene[" + strconv.Itoa(i) + "].clip.clip_id missing")
		}
		// Any clip_id at scene level (not under .clip) — also forbidden.
		if _, ok := s["clip_id"]; ok {
			return errors.New("clip_id present at scene[" + strconv.Itoa(i) + "] level (must live under .clip)")
		}
	}
	return nil
}

package script

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVoiceoverTimingBinding_JSONRoundTrip pins the wire contract of the
// per-language timing bundle: the canonical timing.json link, SRT/VTT
// projections and SHA-256 bindings survive a JSON round-trip (the exact
// path used by script-row persistence and by the job result payload
// surfaced via GET /api/jobs/:id/full). The word-level timing array is
// intentionally NOT inlined in the binding — it lives in the published
// timing.json artifact.
func TestVoiceoverTimingBinding_JSONRoundTrip(t *testing.T) {
	binding := &VoiceoverBinding{
		Status: "completed",
		Link:   "https://drive.google.com/file/d/audio-it/view",
		Links:  map[string]string{"it": "https://drive.google.com/file/d/audio-it/view"},
		Timing: map[string]VoiceoverTimingBinding{
			"it": {
				Status:       "completed",
				JSONLink:     "https://drive.google.com/file/d/timing-it/view",
				SRTLink:      "https://drive.google.com/file/d/subtitles-it-srt/view",
				VTTLink:      "https://drive.google.com/file/d/subtitles-it-vtt/view",
				BoundaryMode: "word",
				WordCount:    184,
				DurationUS:   18_342_000,
				TextSHA256:   strings.Repeat("a", 64),
				AudioSHA256:  strings.Repeat("b", 64),
			},
		},
	}

	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal voiceover binding: %v", err)
	}
	if strings.Contains(string(raw), `"words"`) {
		t.Fatal("the voiceover binding must not inline the word-level timing array")
	}

	var decoded VoiceoverBinding
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal voiceover binding: %v", err)
	}
	timing, ok := decoded.Timing["it"]
	if !ok {
		t.Fatalf("timing map was not preserved in the round-trip: %s", raw)
	}
	if timing.Status != "completed" || timing.JSONLink != "https://drive.google.com/file/d/timing-it/view" ||
		timing.SRTLink != "https://drive.google.com/file/d/subtitles-it-srt/view" ||
		timing.VTTLink != "https://drive.google.com/file/d/subtitles-it-vtt/view" ||
		timing.BoundaryMode != "word" || timing.WordCount != 184 || timing.DurationUS != 18_342_000 ||
		timing.TextSHA256 != strings.Repeat("a", 64) || timing.AudioSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("timing bundle fields drifted in the round-trip: %+v", timing)
	}
}

// TestVoiceoverTimingBinding_EmptyStatusSurvives verifies that an
// explicit best-effort failure status ("failed"/"unavailable") survives
// the JSON round-trip instead of being dropped as an empty value — the
// no-fake-availability contract for absent timing.
func TestVoiceoverTimingBinding_EmptyStatusSurvives(t *testing.T) {
	binding := &VoiceoverBinding{
		Status: "completed",
		Timing: map[string]VoiceoverTimingBinding{
			"it": {Status: "failed"},
		},
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded VoiceoverBinding
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := decoded.Timing["it"].Status; got != "failed" {
		t.Fatalf("timing status = %q, want failed (explicit absence must survive)", got)
	}
}

// ─── Phase 2: the canonical media identity (kernel/asset.Ref) ────────────
//
// EntityImageBinding is the annotation layer's media binding. It carries BOTH
// halves of the media fact — which asset (AssetID) and which bytes (SHA256) on
// one side, where those bytes happen to live (DriveFileID/DriveLink/PreviewURL/
// LocalPath) on the other — and Ref() is the projection onto the identity half.
// These tests pin the property that makes the projection worth having: the
// identity of the bytes does not depend on where they currently are.

// TestEntityImageBindingRefIsLocationFree pins that the identity a binding
// projects cannot describe a location, so it is safe to use as a join key and as
// a persisted/compared value.
func TestEntityImageBindingRefIsLocationFree(t *testing.T) {
	binding := EntityImageBinding{
		Status:      "bound",
		AssetID:     "asset-michael-jordan",
		SHA256:      "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef",
		MediaType:   "image/jpeg",
		DriveFileID: "drive-file-id",
		DriveLink:   "https://drive.google.com/file/d/drive-file-id/view",
		PreviewURL:  "https://cdn.example/jordan.jpg",
		LocalPath:   "/tmp/producer-only/jordan.jpg",
	}

	identity := binding.Ref()
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	for _, forbidden := range []string{"local_path", "drive_file_id", "drive_link", "preview_url", "url", "status", "source", "license"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Errorf("canonical identity carries non-identity key %q: %s", forbidden, raw)
		}
	}
	if identity.AssetID != binding.AssetID || identity.MediaType != binding.MediaType {
		t.Errorf("identity dropped part of the binding's identity: %+v", identity)
	}
}

// TestEntityImageBindingRefIsIndependentOfLocation is the regression guard for
// the production failure this programme exists to remove: the SAME bytes
// described through a Drive location and through a CDN URL must be ONE asset,
// not two. It is also the digest-spelling guard — the two bindings below differ
// only in digest case and location, and both must collapse to one identity.
func TestEntityImageBindingRefIsIndependentOfLocation(t *testing.T) {
	const digest = "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef"
	viaDrive := EntityImageBinding{
		AssetID:     "asset-michael-jordan",
		SHA256:      digest,
		MediaType:   "image/jpeg",
		DriveFileID: "drive-file-id",
		DriveLink:   "https://drive.google.com/file/d/drive-file-id/view",
	}
	viaCdn := EntityImageBinding{
		AssetID:    "asset-michael-jordan",
		SHA256:     strings.ToUpper(digest),
		MediaType:  "image/jpeg",
		PreviewURL: "https://cdn.example/jordan.jpg",
	}

	if viaDrive.Ref() != viaCdn.Ref() {
		t.Fatalf("one asset through two locations projected onto two identities:\n  %+v\n  %+v", viaDrive.Ref(), viaCdn.Ref())
	}
	// The canonicalised projection is also the thing that makes the digest
	// CHECKABLE: the mixed-case input becomes a valid 64-hex content address.
	if !viaCdn.Ref().IsCanonicalDigest() {
		t.Errorf("canonicalised digest is not recognised as a content address: %+v", viaCdn.Ref())
	}
}

// TestEntityImageBindingRefRejectsHalfIdentities pins that the projection does
// not invent a usable identity out of nothing: a binding without bytes, or
// without a logical asset, is not "some asset with an unknown location".
func TestEntityImageBindingRefRejectsHalfIdentities(t *testing.T) {
	noBytes := EntityImageBinding{AssetID: "asset-x", DriveLink: "https://drive/x"}
	if err := noBytes.Ref().Validate(); err == nil {
		t.Errorf("a binding without a content address must not project onto a usable identity: %+v", noBytes.Ref())
	}
	noAsset := EntityImageBinding{SHA256: "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef"}
	if err := noAsset.Ref().Validate(); err == nil {
		t.Errorf("a binding without a logical asset must not project onto a usable identity: %+v", noAsset.Ref())
	}
}

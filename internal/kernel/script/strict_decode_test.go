package script

// strict_decode_test.go — P0 contract hole: encoding/json hands a type with a
// custom UnmarshalJSON its WHOLE subtree, so the envelope's
// DisallowUnknownFields gate stopped at output/audio/media_plan. In the real
// job corpus (ops/jobs, 62 envelopes) `output.drive_subfolder_name` — a key
// that only exists at output.render.drive_subfolder_name — was dropped in
// 62/62 requests while the API advertised fail-closed decoding.

import (
	"strings"
	"testing"
)

func decodeEnvelopeForTest(t *testing.T, body string) (*GenerationEnvelopeV2, error) {
	t.Helper()
	return DecodeGenerationEnvelopeStrict([]byte(body))
}

func TestStrictDecodeRejectsUnknownOutputKey(t *testing.T) {
	_, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"output":{"drive_subfolder_name":"job-r25"}}]}`)
	if err == nil {
		t.Fatal("output.drive_subfolder_name is not an OutputSpec key and must fail closed")
	}
	if !strings.Contains(err.Error(), `json: unknown field "drive_subfolder_name"`) {
		t.Fatalf("error must keep the decoder taxonomy, got %q", err.Error())
	}
}

func TestStrictDecodeAcceptsCanonicalRenderSubfolder(t *testing.T) {
	env, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"output":{"render":{"enabled":true,"drive_subfolder_name":"job-r25"}}}]}`)
	if err != nil {
		t.Fatalf("canonical output.render.drive_subfolder_name must decode: %v", err)
	}
	if got := env.Items[0].Output.Render.DriveSubfolderName; got != "job-r25" {
		t.Fatalf("drive_subfolder_name = %q, want job-r25", got)
	}
}

func TestStrictDecodeRejectsUnknownBackgroundKey(t *testing.T) {
	_, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"output":{"render":{"background":{"mode":"blur_source","blur":"28"}}}}]}`)
	if err == nil {
		t.Fatal("an unknown background key must fail closed instead of being dropped")
	}
	if !strings.Contains(err.Error(), `json: unknown field "blur"`) {
		t.Fatalf("error must name the offending key, got %q", err.Error())
	}
}

func TestStrictDecodeRejectsUnknownAudioKey(t *testing.T) {
	_, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"audio":{"mixpolicy":"VOICEOVER_ONLY"}}]}`)
	if err == nil {
		t.Fatal("a misspelled audio key must fail closed")
	}
	if !strings.Contains(err.Error(), `json: unknown field "mixpolicy"`) {
		t.Fatalf("error must name the offending key, got %q", err.Error())
	}
}

func TestStrictDecodeRejectsUnknownBackgroundMusicKey(t *testing.T) {
	_, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"audio":{"background_music":[{"asset_id":"music-bed","gain":"loud"}]}}]}`)
	if err == nil {
		t.Fatal("an unknown background_music key must fail closed")
	}
	if !strings.Contains(err.Error(), `json: unknown field "gain"`) {
		t.Fatalf("error must name the offending key, got %q", err.Error())
	}
}

func TestStrictDecodeRejectsUnknownSoundEffectKey(t *testing.T) {
	_, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"audio":{"sound_effects":[{"asset_id":"whoosh","at_msec":10}]}}]}`)
	if err == nil {
		t.Fatal("an unknown sound_effect key must fail closed")
	}
	if !strings.Contains(err.Error(), `json: unknown field "at_msec"`) {
		t.Fatalf("error must name the offending key, got %q", err.Error())
	}
}

func TestStrictDecodeRejectsUnknownMediaPlanKey(t *testing.T) {
	_, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"media_plan":{"image_search":"automatic"}}]}`)
	if err == nil {
		t.Fatal("an unknown media_plan key must fail closed")
	}
	if !strings.Contains(err.Error(), `json: unknown field "image_search"`) {
		t.Fatalf("error must name the offending key, got %q", err.Error())
	}
}

// DecodeEnvelopeV2 is the replay path: the job worker re-reads a persisted
// payload through it. It must not be more lenient than the HTTP boundary, or
// a body the API rejects could still be replayed from storage.
func TestDecodeEnvelopeV2StaysStrictForNestedBlocks(t *testing.T) {
	_, err := DecodeEnvelopeV2([]byte(`{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"output":{"drive_subfolder_name":"job-r25"}}]}`))
	if err == nil {
		t.Fatal("DecodeEnvelopeV2 must reject a nested unknown key")
	}
	if !strings.Contains(err.Error(), `json: unknown field "drive_subfolder_name"`) {
		t.Fatalf("error must keep the decoder taxonomy, got %q", err.Error())
	}
}

// The audio and media_plan decoders exist to accept legacy wire shapes. The
// strict gate must not turn those accepted aliases into rejections.
func TestStrictDecodeKeepsLegacyAliases(t *testing.T) {
	env, err := decodeEnvelopeForTest(t, `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"t"},"audio":{"background_music":{"asset_id":"music-bed","end_ms":5000}},"media_plan":{"providers":["internet_images"]}}]}`)
	if err != nil {
		t.Fatalf("legacy aliases must keep decoding: %v", err)
	}
	bgm := env.Items[0].Audio.BackgroundMusic
	if len(bgm) != 1 {
		t.Fatalf("background_music object form must normalize to one entry, got %d", len(bgm))
	}
	if bgm[0].End == nil || bgm[0].End.IsVideoEnd() || bgm[0].End.Ms != 5000 {
		t.Fatalf("end_ms alias must map onto end=5000, got %+v", bgm[0].End)
	}
}

package texttracks

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestDecodeMaterializeJobPayloadAcceptsCanonicalAndLegacyForms(t *testing.T) {
	want := MaterializeJobPayload{
		AssetID:        "asset-1",
		SourceLanguage: "en",
		TextKinds:      []string{"transcript"},
	}
	canonical, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(base64.RawStdEncoding.EncodeToString(canonical))
	if err != nil {
		t.Fatal(err)
	}

	for name, raw := range map[string]json.RawMessage{
		"canonical": canonical,
		"legacy":    legacyJSON,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeMaterializeJobPayload(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.AssetID != want.AssetID || got.SourceLanguage != want.SourceLanguage || len(got.TextKinds) != 1 || got.TextKinds[0] != want.TextKinds[0] {
				t.Fatalf("decoded payload = %#v, want %#v", got, want)
			}
		})
	}
}

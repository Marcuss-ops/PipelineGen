package script

import (
	"fmt"
	"strings"
	"testing"
)

func decodeForVersion(t *testing.T, version int, items string) *GenerationEnvelopeV2 {
	t.Helper()
	body := fmt.Sprintf(`{"version":%d,"preset":"custom","items":[%s]}`, version, items)
	env, err := DecodeGenerationEnvelopeStrict([]byte(body))
	if err != nil {
		t.Fatalf("decode version %d: %v", version, err)
	}
	return env
}

func TestValidateAcceptsLegacyAndCanonicalEnvelopeVersions(t *testing.T) {
	item := `{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100}}`
	for _, version := range []int{EnvelopeVersionLegacy, EnvelopeVersionCanonical} {
		if err := decodeForVersion(t, version, item).Validate(); err != nil {
			t.Fatalf("version %d must be accepted: %v", version, err)
		}
	}
}

func TestValidateRejectsUnsupportedEnvelopeVersion(t *testing.T) {
	item := `{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100}}`
	for _, version := range []int{0, 1, 4, -1} {
		err := decodeForVersion(t, version, item).Validate()
		if err == nil {
			t.Fatalf("version %d must be rejected", version)
		}
		if !strings.Contains(err.Error(), "version must be 2") {
			t.Fatalf("version %d error = %v", version, err)
		}
	}
}

func TestRetiredContractFieldReasonRejectsV2OnlyOwners(t *testing.T) {
	cases := []struct {
		name string
		item string
		want []string
	}{
		{
			name: "output_audio",
			item: `{"source":{"type":"text","topic":"x"},"output":{"audio":{"mode":"COMBINED_TIMELINE"}}}`,
			want: []string{"items[0].output.audio", `item-level "audio"`},
		},
		{
			name: "script_params_force_refresh",
			item: `{"source":{"type":"text","topic":"x"},"script_params":{"force_refresh":true}}`,
			want: []string{"items[0].script_params.force_refresh", `envelope-level "force_refresh"`},
		},
		{
			name: "script_params_voiceover_group",
			item: `{"source":{"type":"text","topic":"x"},"script_params":{"voiceover_group":"vip"}}`,
			want: []string{"items[0].script_params.voiceover_group", "output.voiceover_group"},
		},
		{
			name: "legacy_map_type",
			item: `{"source":{"type":"text","topic":"x"},"map_type":"satellite"}`,
			want: []string{"items[0].map_type", "map_presentation.style"},
		},
		{
			name: "legacy_map_camera_animation",
			item: `{"source":{"type":"text","topic":"x"},"map_camera_animation":"slow_approach"}`,
			want: []string{"items[0].map_camera_animation", "map_presentation.camera"},
		},
		{
			name: "legacy_map_label_animation",
			item: `{"source":{"type":"text","topic":"x"},"map_label_animation":"none"}`,
			want: []string{"items[0].map_label_animation", "omit the key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"version":%d,"items":[%s]}`, EnvelopeVersionCanonical, tc.item)
			reason := RetiredContractFieldReason([]byte(body), EnvelopeVersionCanonical)
			for _, want := range tc.want {
				if !strings.Contains(reason, want) {
					t.Fatalf("reason = %q, want it to contain %q", reason, want)
				}
			}
			// The same body is still readable as legacy V2.
			if reason := RetiredContractFieldReason([]byte(body), EnvelopeVersionLegacy); reason != "" {
				t.Fatalf("version 2 must keep the legacy spelling readable, got %q", reason)
			}
		})
	}
}

func TestRetiredContractFieldReasonAcceptsCanonicalOwners(t *testing.T) {
	body := fmt.Sprintf(`{"version":%d,"force_refresh":true,"items":[{"source":{"type":"text","topic":"x"},"audio":{"mode":"COMBINED_TIMELINE"},"output":{"voiceover_group":"vip"},"map_presentation":{"preset":"italia_basemap_chiara"}}]}`, EnvelopeVersionCanonical)
	if reason := RetiredContractFieldReason([]byte(body), EnvelopeVersionCanonical); reason != "" {
		t.Fatalf("canonical owners must be accepted, got %q", reason)
	}
}

func TestRetiredContractFieldReasonNamesTheOffendingItem(t *testing.T) {
	body := fmt.Sprintf(`{"version":%d,"items":[{"source":{"type":"text","topic":"a"}},{"id":"second","source":{"type":"text","topic":"b"},"output":{"audio":{"mode":"VOICEOVER_ONLY"}}}]}`, EnvelopeVersionCanonical)
	reason := RetiredContractFieldReason([]byte(body), EnvelopeVersionCanonical)
	if !strings.Contains(reason, "items[1].output.audio") {
		t.Fatalf("reason = %q, want the second item path", reason)
	}
}

func TestRetiredContractFieldReasonIgnoresMalformedBody(t *testing.T) {
	if reason := RetiredContractFieldReason([]byte(`{"version":3,"items":[`), EnvelopeVersionCanonical); reason != "" {
		t.Fatalf("malformed body must be left to the strict decoder, got %q", reason)
	}
}

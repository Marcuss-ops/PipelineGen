package job

import (
	"encoding/json"
	"testing"
)

func TestMatchesPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		match   PayloadMatch
		want    bool
	}{
		{"empty matcher matches anything", `{"render_phase":"settle"}`, nil, true},
		{"absent key does not match", `{"render_phase":"submit"}`, PayloadMatch{"render_phase": "settle"}, false},
		{"string scalar matches", `{"render_phase":"settle"}`, PayloadMatch{"render_phase": "settle"}, true},
		{"number scalar matches", `{"attempt":1}`, PayloadMatch{"attempt": "1"}, true},
		{"bool scalar matches", `{"nested":true}`, PayloadMatch{"nested": "true"}, true},
		{"all pairs must match", `{"render_phase":"settle","attempt":2}`, PayloadMatch{"render_phase": "settle", "attempt": "1"}, false},
		{"unknown keys are irrelevant", `{"render_phase":"settle","other":"x"}`, PayloadMatch{"render_phase": "settle"}, true},
		{"malformed payload never matches a non-empty matcher", `not-json`, PayloadMatch{"render_phase": "settle"}, false},
		{"empty payload never matches a non-empty matcher", ``, PayloadMatch{"render_phase": "settle"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchesPayload(json.RawMessage(tc.payload), tc.match)
			if got != tc.want {
				t.Fatalf("MatchesPayload(%s, %v) = %v, want %v", tc.payload, tc.match, got, tc.want)
			}
		})
	}
}

func TestValidatePayloadMatch(t *testing.T) {
	if err := ValidatePayloadMatch(nil); err != nil {
		t.Fatalf("nil matcher must be valid, got %v", err)
	}
	if err := ValidatePayloadMatch(PayloadMatch{"render_phase": "settle"}); err != nil {
		t.Fatalf("well-formed matcher must be valid, got %v", err)
	}
	if err := ValidatePayloadMatch(PayloadMatch{"  ": "settle"}); err == nil {
		t.Fatal("a blank key must fail closed")
	}
}

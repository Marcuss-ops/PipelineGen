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

func TestExcludesPayload(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		notMatch PayloadNotMatch
		want     bool
	}{
		{"empty excluder excludes nothing", `{"render_phase":"settle"}`, nil, true},
		{"unset phase is not excluded", `{"source_asset_id":"a"}`, PayloadNotMatch{"render_phase": "settle"}, true},
		{"submit phase is not excluded", `{"render_phase":"submit"}`, PayloadNotMatch{"render_phase": "settle"}, true},
		{"settle phase is excluded", `{"render_phase":"settle"}`, PayloadNotMatch{"render_phase": "settle"}, false},
		{"scalar projection applies", `{"attempt":2}`, PayloadNotMatch{"attempt": "2"}, false},
		{"other keys are irrelevant", `{"render_phase":"submit","other":"x"}`, PayloadNotMatch{"render_phase": "settle"}, true},
		{"any excluded pair excludes", `{"render_phase":"submit","attempt":1}`, PayloadNotMatch{"render_phase": "settle", "attempt": "1"}, false},
		{"malformed payload is not excluded (never lose a job)", `not-json`, PayloadNotMatch{"render_phase": "settle"}, true},
		{"empty payload is not excluded", ``, PayloadNotMatch{"render_phase": "settle"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExcludesPayload(json.RawMessage(tc.payload), tc.notMatch)
			if got != tc.want {
				t.Fatalf("ExcludesPayload(%s, %v) = %v, want %v", tc.payload, tc.notMatch, got, tc.want)
			}
		})
	}
}

func TestValidatePayloadNotMatch(t *testing.T) {
	if err := ValidatePayloadNotMatch(nil); err != nil {
		t.Fatalf("nil excluder must be valid, got %v", err)
	}
	if err := ValidatePayloadNotMatch(PayloadNotMatch{"render_phase": "settle"}); err != nil {
		t.Fatalf("well-formed excluder must be valid, got %v", err)
	}
	if err := ValidatePayloadNotMatch(PayloadNotMatch{"  ": "settle"}); err == nil {
		t.Fatal("a blank key must fail closed")
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

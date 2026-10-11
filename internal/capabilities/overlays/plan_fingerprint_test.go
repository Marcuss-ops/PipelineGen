package overlays

import (
	"strings"
	"testing"
)

func TestPlanFingerprint_CrossLanguageVectors(t *testing.T) {
	// Pinned against the Rust `overlay_fingerprint` module: byte-for-byte
	// parity, not just properties.
	cases := map[string]string{
		`{"a":1}`:                    "9c3e82dd6fcae8b1",
		`{"b":[1,2],"a":{"z":true}}`: "156681ee96efe935",
		`{}`:                         "08f44b07b5901a25",
	}
	for raw, want := range cases {
		got, err := FingerprintPlanJSON([]byte(raw), true)
		if err != nil {
			t.Fatalf("fingerprint %s: %v", raw, err)
		}
		if got != want {
			t.Errorf("fingerprint %s = %s, want %s (Rust parity broken)", raw, got, want)
		}
	}
	got, err := FingerprintPlanJSON(
		[]byte(`{"scene_id":"s-01","preset":"dolly","asset":"drive:abc"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "472864c08fa1fe6d" {
		t.Errorf("plan vector = %s, want 472864c08fa1fe6d", got)
	}
}

func TestPlanFingerprint_KeyOrderIndependent(t *testing.T) {
	a, err := FingerprintPlanJSON([]byte(`{"b":1,"a":[1,2]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FingerprintPlanJSON([]byte(`{"a":[1,2],"b":1}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("key order must not change the fingerprint")
	}
	c, err := FingerprintPlanJSON([]byte(`{"clips":["a","b"]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	d, err := FingerprintPlanJSON([]byte(`{"clips":["b","a"]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if c == d {
		t.Error("array order must change the fingerprint")
	}
}

func TestPlanFingerprint_StripsVolatileState(t *testing.T) {
	withTiming, err := FingerprintPlanJSON([]byte(
		`{"a":1,"timing_state":"frozen","nested":{"attempt_id":"x","v":2}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := FingerprintPlanJSON([]byte(`{"a":1,"nested":{"v":2}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if withTiming != clean {
		t.Error("volatile keys must be stripped before hashing")
	}
	// Without stripping they differ (lib hashes exactly what it gets).
	raw, err := FingerprintPlanJSON([]byte(
		`{"a":1,"timing_state":"frozen","nested":{"attempt_id":"x","v":2}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if raw == clean {
		t.Error("stripVolatile=false must preserve volatile keys")
	}
}

func TestPlanFingerprint_InvalidJSONFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "not json", "{unclosed"} {
		if _, err := FingerprintPlanJSON([]byte(raw), true); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
	if _, err := FingerprintPlan(nil, true); err == nil {
		t.Error("expected error for nil plan")
	}
}

func TestPlanFingerprint_HexShape(t *testing.T) {
	got, err := FingerprintPlanJSON([]byte(`{"x":[1,{"y":null}]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Errorf("fingerprint must be 16 hex chars, got %q", got)
	}
}

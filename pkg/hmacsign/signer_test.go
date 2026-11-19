package hmacsign_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/hmacsign"
)

func TestSignRoundTrip(t *testing.T) {
	secret := []byte(strings.Repeat("a", 32))
	body := []byte(`{"k":"v"}`)
	sig := hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-1", body)
	if !strings.HasPrefix(sig, hmacsign.SignaturePrefix) {
		t.Fatalf("missing sig prefix: %q", sig)
	}
	// Deterministic: same inputs must produce the same wire value.
	again := hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-1", body)
	if sig != again {
		t.Fatalf("Sign is not deterministic: %q != %q", sig, again)
	}
}

func TestSignBindsTimestampEventIDAndBody(t *testing.T) {
	secret := []byte(strings.Repeat("a", 32))
	body := []byte(`{"k":"v"}`)
	base := hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-1", body)

	cases := map[string]string{
		"body":      hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-1", []byte(`{"k":"V"}`)),
		"event id":  hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-2", body),
		"timestamp": hmacsign.Sign(secret, "2026-06-20T15:42:01Z", "evt-1", body),
		"secret":    hmacsign.Sign([]byte(strings.Repeat("b", 32)), "2026-06-20T15:42:00Z", "evt-1", body),
	}
	for field, sig := range cases {
		if sig == base {
			t.Errorf("changing the %s must change the signature", field)
		}
	}
}

func TestCanonicalString_PrependsWithDots(t *testing.T) {
	got := string(hmacsign.CanonicalString("2026-06-20T15:42:00Z", "evt-X", []byte(`{}`)))
	want := "2026-06-20T15:42:00Z.evt-X.{}"
	if got != want {
		t.Fatalf("canonical string mismatch: want %q got %q", want, got)
	}
}

func TestSign_AcceptsArbitraryLengths(t *testing.T) {
	// Sign must not panic on sub-32-byte secrets (the config Validate
	// rejects those in production; here we ensure the helper itself
	// remains robust for unit tests).
	sig := hmacsign.Sign([]byte("k"), "t", "e", []byte("b"))
	if !strings.HasPrefix(sig, hmacsign.SignaturePrefix) {
		t.Fatalf("missing prefix on short-secret Sign: %q", sig)
	}
}

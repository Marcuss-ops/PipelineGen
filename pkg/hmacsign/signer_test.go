package hmacsign_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/hmacsign"
)

func TestSignRoundTrip(t *testing.T) {
	secret := []byte(strings.Repeat("a", 32))
	body := []byte(`{"k":"v"}`)
	sig := hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-1", body)
	if !strings.HasPrefix(sig, hmacsign.SignaturePrefix) {
		t.Fatalf("missing sig prefix: %q", sig)
	}
	if err := hmacsign.Verify(secret, "2026-06-20T15:42:00Z", "evt-1", body, sig); err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	secret := []byte(strings.Repeat("a", 32))
	body := []byte(`{"k":"v"}`)
	sig := hmacsign.Sign(secret, "2026-06-20T15:42:00Z", "evt-1", body)
	tampered := []byte(`{"k":"V"}`)
	if err := hmacsign.Verify(secret, "2026-06-20T15:42:00Z", "evt-1", tampered, sig); err == nil {
		t.Fatal("expected tampered body to fail verification")
	}
}

func TestVerifyMultipleSecrets_CurrentThenPrevious(t *testing.T) {
	cur := []byte(strings.Repeat("a", 32))
	prev := []byte(strings.Repeat("b", 32))
	body := []byte(`{"k":"v"}`)
	ts := "2026-06-20T15:42:00Z"

	sigCur := hmacsign.Sign(cur, ts, "evt-2", body)
	if err := hmacsign.VerifyMultipleSecrets([][]byte{cur, prev}, ts, "evt-2", body, sigCur); err != nil {
		t.Fatalf("current-secret sig rejected: %v", err)
	}
	sigPrev := hmacsign.Sign(prev, ts, "evt-2", body)
	if err := hmacsign.VerifyMultipleSecrets([][]byte{cur, prev}, ts, "evt-2", body, sigPrev); err != nil {
		t.Fatalf("previous-secret sig rejected (rotation window): %v", err)
	}
}

func TestVerifyMultipleSecrets_RejectsUnknown(t *testing.T) {
	cur := []byte(strings.Repeat("a", 32))
	unknown := []byte(strings.Repeat("c", 32))
	body := []byte(`{"k":"v"}`)
	sig := hmacsign.Sign(unknown, "2026-06-20T15:42:00Z", "evt-3", body)
	if err := hmacsign.VerifyMultipleSecrets([][]byte{cur}, "2026-06-20T15:42:00Z", "evt-3", body, sig); err == nil {
		t.Fatal("expected unknown-secret sig to fail")
	}
}

func TestVerifyWithReplayWindow(t *testing.T) {
	secret := []byte(strings.Repeat("a", 32))
	body := []byte(`{"k":"v"}`)
	now := time.Date(2026, 6, 20, 16, 0, 0, 0, time.UTC)
	ts5min := now.Add(-5 * time.Minute).Format(time.RFC3339)
	ts10min := now.Add(-10 * time.Minute).Format(time.RFC3339)

	sig5 := hmacsign.Sign(secret, ts5min, "evt-4", body)
	if err := hmacsign.VerifyWithReplayWindow([][]byte{secret}, ts5min, "evt-4", body, sig5, now, 5*time.Minute); err != nil {
		t.Fatalf("5-min-old sig should pass under 5-min window: %v", err)
	}
	if err := hmacsign.VerifyWithReplayWindow([][]byte{secret}, ts5min, "evt-4", body, sig5, now, 4*time.Minute); err != hmacsign.ErrStaleTimestamp {
		t.Fatalf("5-min-old sig should fail under 4-min window: %v", err)
	}
	sig10 := hmacsign.Sign(secret, ts10min, "evt-5", body)
	if err := hmacsign.VerifyWithReplayWindow([][]byte{secret}, ts10min, "evt-5", body, sig10, now, 5*time.Minute); err != hmacsign.ErrStaleTimestamp {
		t.Fatalf("10-min-old sig should fail replay: %v", err)
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
	// remains robust for unit tests + receivers running in tests).
	sig := hmacsign.Sign([]byte("k"), "t", "e", []byte("b"))
	if !strings.HasPrefix(sig, hmacsign.SignaturePrefix) {
		t.Fatalf("missing prefix on short-secret Sign: %q", sig)
	}
	if err := hmacsign.Verify([]byte("k"), "t", "e", []byte("b"), sig); err != nil {
		t.Fatalf("roundtrip on short secret must succeed: %v", err)
	}
}

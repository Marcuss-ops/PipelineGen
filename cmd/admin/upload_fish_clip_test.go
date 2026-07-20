// cmd/admin/upload_fish_clip_test.go — pins the canonical
// resolveFishClipDriveID resolution chain:
//
//  1. CLI --drive-id flag (operator's explicit override).
//  2. $VELOX_FISH_CLIP_DRIVE_ID env var (operator's CI env).
//
// godlike/06 SSOT: the chain ordering is the SOLE fact pinned here;
// any future expansion (e.g. env-var precedence within a list, or
// an additional look-up source) MUST land a companion test case
// alongside the change.
//
// godlike/07 NO-FAKE-AVAILABILITY: the chain fail-closes when no
// source resolves — these tests pin that the error message
// surfaces both upstream sources so the operator can act without
// guessing.
package main

import (
	"os"
	"strings"
	"testing"
)

// fakeEnvLookup records the env keys read by the chain so the test
// can also pin "the chain reads ONLY the canonical env var" — a
// godlike/06 SSOT property at the function-call boundary.
type fakeEnvLookup struct {
	values  map[string]string
	called  []string
	failKey string // if set, return error-as-string for this key
}

func (f *fakeEnvLookup) Lookup(key string) string {
	f.called = append(f.called, key)
	if f.failKey != "" && key == f.failKey {
		return "" // empty string signals "no value"
	}
	return f.values[key]
}

func TestResolveFishClipDriveID_FlagWins_WhenFlagAndEnvBothSet(t *testing.T) {
	env := &fakeEnvLookup{values: map[string]string{
		fishClipDriveIDEnvName: "env-id-should-not-be-used",
	}}
	id, source, err := resolveFishClipDriveID("flag-id", env.Lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "flag-id" {
		t.Errorf("driveID = %q, want %q (flag wins over env)", id, "flag-id")
	}
	if source != "flag" {
		t.Errorf("source = %q, want %q", source, "flag")
	}
	// godlike/06 SSOT: when the flag resolves the chain, the env
	// layer MUST NOT be touched (short-circuit on first hit).
	if len(env.called) != 0 {
		t.Errorf("envLookup called %d times (want 0 — flag short-circuits): keys=%v", len(env.called), env.called)
	}
}

func TestResolveFishClipDriveID_EnvWins_WhenFlagEmpty(t *testing.T) {
	env := &fakeEnvLookup{values: map[string]string{
		fishClipDriveIDEnvName: "env-id",
	}}
	id, source, err := resolveFishClipDriveID("", env.Lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "env-id" {
		t.Errorf("driveID = %q, want %q (env wins when flag empty)", id, "env-id")
	}
	if source != "env" {
		t.Errorf("source = %q, want %q", source, "env")
	}
	// godlike/06 SSOT: the env layer MUST read the canonical name
	// (not other vars) so a leaked VELOX_WORKER_TOKEN cannot be
	// mistaken for a fish clip ID.
	if len(env.called) != 1 || env.called[0] != fishClipDriveIDEnvName {
		t.Errorf("envLookup.called = %v, want exactly [%q]", env.called, fishClipDriveIDEnvName)
	}
}

func TestResolveFishClipDriveID_FailsClosed_WhenAllEmpty(t *testing.T) {
	env := &fakeEnvLookup{values: map[string]string{}}
	id, source, err := resolveFishClipDriveID("", env.Lookup)
	if err == nil {
		t.Fatalf("expected error when no source resolves, got id=%q source=%q", id, source)
	}
	if id != "" {
		t.Errorf("driveID = %q, want empty on fail-closed", id)
	}
	if source != "" {
		t.Errorf("source = %q, want empty on fail-closed", source)
	}
	// godlike/07 fail-closed contract: the error message MUST tell
	// the operator what to do — both upstream sources are named so
	// the operator doesn't have to grep the source to find out.
	msg := err.Error()
	if !strings.Contains(msg, "--drive-id") {
		t.Errorf("error message %q should mention --drive-id", msg)
	}
	if !strings.Contains(msg, fishClipDriveIDEnvName) {
		t.Errorf("error message %q should mention $%s", msg, fishClipDriveIDEnvName)
	}
}

func TestResolveFishClipDriveID_TrimSpaceOnFlag_FallsThroughToEnv(t *testing.T) {
	// godlike/06 SSOT: a whitespace-only flag MUST NOT short-circuit
	// the chain. The flag layer MUST treat whitespace as empty so
	// the env layer is consulted; the env layer's value is then
	// authoritative for that miss. Without this invariant, an
	// operator passing --drive-id "   " would either (a) silently
	// pass an empty-string to GetClip, or (b) pretend the flag was
	// set and skip the env lookup — both are silent failure modes.
	env := &fakeEnvLookup{values: map[string]string{
		fishClipDriveIDEnvName: "env-id",
	}}
	id, source, err := resolveFishClipDriveID("   \t  ", env.Lookup)
	if err != nil {
		t.Fatalf("unexpected err on whitespace flag + non-empty env: %v", err)
	}
	if id != "env-id" {
		t.Errorf("driveID = %q, want %q (env must resolve when flag is whitespace)", id, "env-id")
	}
	if source != "env" {
		t.Errorf("source = %q, want %q (env layer must be the source)", source, "env")
	}
	// godlike/06 SSOT: the flag layer MUST consult envLookup once
	// (whitespace ≠ short-circuit). Asserting the call signature
	// pins the canonical env-var name + a single call site.
	if len(env.called) != 1 || env.called[0] != fishClipDriveIDEnvName {
		t.Errorf("envLookup.called = %v, want exactly [%q]", env.called, fishClipDriveIDEnvName)
	}
}

func TestResolveFishClipDriveID_TrimSpaceOnEnv(t *testing.T) {
	env := &fakeEnvLookup{values: map[string]string{
		fishClipDriveIDEnvName: "   \t  ",
	}}
	// godlike/07: whitespace-only env falls through to fail-closed.
	// A misconfigured CI env (trailing newline from shell) MUST NOT
	// route through silently.
	id, _, err := resolveFishClipDriveID("", env.Lookup)
	if err == nil {
		t.Fatalf("expected error on whitespace-only env, got id=%q", id)
	}
	if id != "" {
		t.Errorf("driveID = %q, want empty on whitespace env", id)
	}
}

func TestResolveFishClipDriveID_RealOsEnv_Wiring(t *testing.T) {
	// godlike/06 SSOT smoke-test: the production wiring is
	// `resolveFishClipDriveID(*driveIDFlag, os.Getenv)`. This
	// case passes os.Getenv verbatim (via t.Setenv for hermeticity)
	// to verify the chain works against the canonical production
	// env-lookup function — without leaking state into other tests.
	t.Setenv(fishClipDriveIDEnvName, "ENV-LIVE-ID")
	// Belt-and-braces: any value left over from a previous test in
	// the same process MUST NOT be reached via the flag path. Keep
	// the flag empty.
	id, source, err := resolveFishClipDriveID("", os.Getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ENV-LIVE-ID" {
		t.Errorf("driveID = %q, want %q (live env wiring)", id, "ENV-LIVE-ID")
	}
	if source != "env" {
		t.Errorf("source = %q, want %q", source, "env")
	}
}

func TestResolveFishClipDriveID_ErrorContainsBothHints(t *testing.T) {
	// godlike/07 fail-closed contract: when no source resolves,
	// the error message MUST name both upstream sources so the
	// operator doesn't have to grep the discharge to find out what
	// to do. This is stricter than Test … _FailsClosed_WhenAllEmpty
	// (which only asserts err != nil) and pins the actionable
	// property at the message layer.
	env := &fakeEnvLookup{values: map[string]string{}}
	_, _, err := resolveFishClipDriveID("", env.Lookup)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	wants := []string{"--drive-id", fishClipDriveIDEnvName}
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing hint %q (operator can't act without it)", msg, want)
		}
	}
}

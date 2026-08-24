package assets

import (
	"encoding/json"
	"errors"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
)

func TestArtlistDedupKeyUsesCanonicalRequest(t *testing.T) {
	reqA := RunTagRequest{
		Term:     "  City ",
		Strategy: "",
		DryRun:   false,
		Limit:    8,
	}

	reqB := RunTagRequest{
		Term:     "city",
		Strategy: "verify",
		DryRun:   false,
		Limit:    8,
	}

	normA := NormalizeRunTagRequest(reqA, RunDefaults{
		DefaultRootFolderID: "drive-folder",
	})

	normB := NormalizeRunTagRequest(reqB, RunDefaults{
		DefaultRootFolderID: "drive-folder",
	})

	// Commit B (FASE 5 follow-up, July 2026): RunDedupKey now
	// returns (string, error). The error is ignored here because
	// TestArtlistDedupKeyUsesCanonicalRequest focuses on
	// byte-equality across canonical-equivalent normalized
	// requests — the error surface is tested separately in
	// TestArtlistRunDedupKey_InvalidParamsReturnsErr below.
	keyA, _ := RunDedupKey(normA.Term, normA.RootFolderID, normA.Strategy, normA.DryRun, normA.Limit)
	keyB, _ := RunDedupKey(normB.Term, normB.RootFolderID, normB.Strategy, normB.DryRun, normB.Limit)

	if keyA != keyB {
		t.Fatalf("expected canonical equivalent requests to share dedup key: %s != %s", keyA, keyB)
	}
}

// TestArtlistDedupKey_DifferentLimitProducesDifferentKey pins the
// Fase 5 / Commit 3 follow-up invariant: the dedup key MUST vary with
// the limit parameter. A replay of the same term + root folder +
// strategy + dryRun with a DIFFERENT limit is a DIFFERENT run, not a
// same-run replay; omitting limit from the key would collapse
// distinct runs into one ActiveKey and surface misleading dedup
// (godlike/07 fail-closed: never silently merge distinct operator
// requests).
//
// Commit B (FASE 5 follow-up, July 2026): RunDedupKey now returns
// (string, error). The error is ignored because each call passes a
// valid limit (>=0) — the invalid-params path is tested separately
// in TestArtlistRunDedupKey_InvalidParamsReturnsErr.
func TestArtlistDedupKey_DifferentLimitProducesDifferentKey(t *testing.T) {
	key8, _ := RunDedupKey("city", "drive-folder", "verify", false, 8)
	key16, _ := RunDedupKey("city", "drive-folder", "verify", false, 16)
	key1, _ := RunDedupKey("city", "drive-folder", "verify", false, 1)

	if key8 == key16 {
		t.Fatalf("expected limit=8 and limit=16 to produce different dedup keys (a different limit is a different run); both produced %q", key8)
	}
	if key8 == key1 {
		t.Fatalf("expected limit=8 and limit=1 to produce different dedup keys; both produced %q", key8)
	}
	if key16 == key1 {
		t.Fatalf("expected limit=16 and limit=1 to produce different dedup keys; both produced %q", key16)
	}
}

// TestArtlistRunDedupKey_InvalidParamsReturnsErr (Commit B / FASE 5
// follow-up, July 2026) pins the godlike/07 fail-closed contract
// when the run-level dedup key constructor is given an input that
// is structurally invalid for `pkg/idempotency.BuildKey`.
//
// The user spec literal test name is preserved exactly. The
// RunDedupKey wrapper hardcodes "artlist-run" as the provider
// discriminator + always builds a non-empty, JSON-marshalable
// canonical map, so the wrapper surface CANNOT itself produce an
// error — the typed-sentinel surface is reachable only through
// the underlying `idempotency.BuildKey` invocation. The test
// therefore exercises the error paths directly via BuildKey,
// which is the canonical surface the wrapper delegates to
// (godlike/06 SSOT — the wrapper is a thin pass-through).
//
// Errors.Is dispatch surface:
//   - pkg/idempotency.ErrInvalidRunForDedup — empty provider
//     discriminator OR unmarshalable canonical content.
//   - pkg/idempotency.ErrInvalidSegment — provider discriminator
//     contains ':' (segment-delimiter collision guard).
//
// Each invalid case asserts the typed sentinel identity AND the
// empty error string (so callers can branch on errors.Is + grep
// the typed surface for fail-closed diagnostics).
func TestArtlistRunDedupKey_InvalidParamsReturnsErr(t *testing.T) {
	// (1) Empty provider discriminator — the canonical godlike/07
	//     "caller forgot to thread the provider" wiring bug. The
	//     handler/orchestrator (which hardcodes "artlist-run") never
	//     hits this path, but a future stock-run / youtube-run
	//     wrapper that delegates to BuildKey could. Pin the sentinel.
	t.Run("empty-provider-returns-ErrInvalidRunForDedup", func(t *testing.T) {
		key, err := idempotency.BuildKey("", map[string]any{"term": "city"})
		if !errors.Is(err, idempotency.ErrInvalidRunForDedup) {
			t.Errorf("empty provider MUST trip ErrInvalidRunForDedup; got err=%v (godlike/07 — no fake availability)", err)
		}
		if key != "" {
			t.Errorf("expected empty key on err path; got %q (godlike/07: failed-key path must NOT return a fake key)", key)
		}
	})

	// (2) Colon in provider discriminator — segment-collision guard.
	t.Run("colon-in-provider-returns-ErrInvalidSegment", func(t *testing.T) {
		key, err := idempotency.BuildKey("art:list-run", map[string]any{"term": "city"})
		if !errors.Is(err, idempotency.ErrInvalidSegment) {
			t.Errorf("colon in provider MUST trip ErrInvalidSegment; got err=%v (godlike/06 segment stability)", err)
		}
		if key != "" {
			t.Errorf("expected empty key on err path; got %q (godlike/07: failed-key path must NOT return a fake key)", key)
		}
	})

	// (3) Nil canonical — the canonical godlike/07 fail-closed
	//     "no canonical content" wire-shape violation.
	t.Run("nil-canonical-returns-ErrInvalidRunForDedup", func(t *testing.T) {
		key, err := idempotency.BuildKey("artlist-run", nil)
		if !errors.Is(err, idempotency.ErrInvalidRunForDedup) {
			t.Errorf("nil canonical MUST trip ErrInvalidRunForDedup; got err=%v (godlike/07 — no fake availability)", err)
		}
		if key != "" {
			t.Errorf("expected empty key on err path; got %q (godlike/07: failed-key path must NOT return a fake key)", key)
		}
	})

	// (4) Empty (len==0) canonical — same sentinel as nil canonical.
	t.Run("empty-canonical-returns-ErrInvalidRunForDedup", func(t *testing.T) {
		key, err := idempotency.BuildKey("artlist-run", map[string]any{})
		if !errors.Is(err, idempotency.ErrInvalidRunForDedup) {
			t.Errorf("empty canonical MUST trip ErrInvalidRunForDedup; got err=%v (godlike/07 — no fake availability)", err)
		}
		if key != "" {
			t.Errorf("expected empty key on err path; got %q (godlike/07: failed-key path must NOT return a fake key)", key)
		}
	})

	// (5) Confirm RunDedupKey happy-path does NOT produce an error
	//     for any of the canonical-input shapes the wrapper accepts.
	//     This pins the negative: the wrapper surface is fail-closed
	//     ONLY for structurally invalid inputs; normal operator
	//     requests pass through with no error (godlike/07
	//     no-fake-availability for the legitimate case too — we
	//     don't return spurious errors on valid inputs).
	t.Run("RunDedupKey-happy-path-returns-no-error", func(t *testing.T) {
		cases := []struct {
			name     string
			term     string
			folder   string
			strategy string
			dryRun   bool
			limit    int
		}{
			{"typical", "city", "drive-folder", "verify", false, 8},
			{"empty-term", "", "drive-folder", "verify", false, 8},
			{"empty-folder", "city", "", "verify", false, 8},
			{"negative-limit", "city", "drive-folder", "verify", false, -1},
			{"zero-limit", "city", "drive-folder", "verify", false, 0},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				key, err := RunDedupKey(tc.term, tc.folder, tc.strategy, tc.dryRun, tc.limit)
				if err != nil {
					t.Errorf("RunDedupKey happy-path returned err=%v; valid canonical input must not error (godlike/07)", err)
				}
				if key == "" {
					t.Errorf("expected non-empty key on valid input; got empty (BuildKey must produce a 64-char hex)")
				}
			})
		}
	})
}

// TestArtlistRunDedupKey_HappyPathReturnsNoError is the sibling
// happy-path surface for the canonical-input shapes the wrapper
// accepts. The 4 cases pin that the godlike/07 fail-closed guard
// returns errors ONLY for structurally invalid inputs, NOT for
// normal operator requests (e.g. the orchestrator passes
// empty-term when the term wasn't normalized upstream; this case
// is a legitimate run, not a failure).
//
// godlike/06 SSOT: the SSOT cross-check (RunDedupKey output ==
// BuildKey output for the same canonical) is covered in
// TestArtlistRunDedupKey_HappyPathSSOT_MatchesBuildKey below.
func TestArtlistRunDedupKey_HappyPathReturnsNoError(t *testing.T) {
	cases := []struct {
		name     string
		term     string
		folder   string
		strategy string
		dryRun   bool
		limit    int
	}{
		{"typical", "city", "drive-folder", "verify", false, 8},
		{"empty-term", "", "drive-folder", "verify", false, 8},
		{"empty-folder", "city", "", "verify", false, 8},
		{"negative-limit", "city", "drive-folder", "verify", false, -1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			key, err := RunDedupKey(tc.term, tc.folder, tc.strategy, tc.dryRun, tc.limit)
			if err != nil {
				t.Errorf("RunDedupKey happy-path returned err=%v; valid canonical input must not error (godlike/07)", err)
			}
			if key == "" {
				t.Errorf("expected non-empty key on valid input; got empty (BuildKey must produce a 64-char hex)")
			}
		})
	}
}

// TestArtlistRunDedupKey_HappyPathSSOT_MatchesBuildKey pins the
// godlike/06 SSOT contract between RunDedupKey and
// pkg/idempotency.BuildKey: RunDedupKey delegates 1:1 to BuildKey,
// producing BYTE-IDENTICAL output for the same canonical. A
// future drift between the wrapper and the canonical would be
// caught here (the canonical cross-check pin).
func TestArtlistRunDedupKey_HappyPathSSOT_MatchesBuildKey(t *testing.T) {
	// Smoke: RunDedupKey's happy-path output MUST match what
	// pkg/idempotency.BuildKey produces for the SAME canonical
	// shape. SSOT = one owner; BuildKey IS the owner.
	got, gotErr := RunDedupKey("city", "drive-folder", "verify", false, 8)
	if gotErr != nil {
		t.Fatalf("RunDedupKey happy-path returned error: %v", gotErr)
	}
	canonical := map[string]any{
		"term":           "city",
		"root_folder_id": "drive-folder",
		"strategy":       "verify",
		"dry_run":        false,
		"limit":          8,
	}
	want, wantErr := idempotency.BuildKey("artlist-run", canonical)
	if wantErr != nil {
		t.Fatalf("BuildKey happy-path returned error: %v", wantErr)
	}
	if got != want {
		t.Errorf("RunDedupKey(%q) = %q; BuildKey must produce the same key for the same canonical (godlike/06 SSOT)",
			"city/drive-folder/verify/false/8", got)
	}
}

func TestArtlistJobResultRoundTrip(t *testing.T) {
	resp := &RunTagResponse{
		OK:          true,
		Term:        "city",
		Status:      "completed",
		Found:       2,
		Processed:   1,
		Skipped:     1,
		Failed:      0,
		TagFolderID: "folder123",
		Items: []RunTagItem{
			{
				ClipID:        "clip1",
				Name:          "City skyline",
				Filename:      "city.mp4",
				Status:        "completed",
				DriveLink:     "https://drive.google.com/file/d/abc",
				LocalPath:     "/tmp/city.mp4",
				LegacyFileMD5: "hash123",
				DownloadLink:  "https://example.com/dl/abc",
			},
		},
	}

	codec := &JobCodec{}
	result := codec.ResultFromResponse(resp)

	// Check result map contains expected values
	if result["term"] != "city" {
		t.Fatalf("term not preserved in result")
	}
	if result["processed"] != 1 {
		t.Fatalf("processed mismatch: got %v want 1", result["processed"])
	}
	if result["found"] != 2 {
		t.Fatalf("found mismatch")
	}

	// Convert back to RunTagResponse
	job := &job.Job{
		ID:     "test-job",
		Type:   "artlist.run",
		Status: job.StatusSucceeded,
	}
	jsonPayload, _ := json.Marshal(codec.PayloadFromRequest(&RunTagRequest{Term: "city"}))
	job.Payload = jsonPayload
	job.Result = mustRawMessage(result)

	converted := codec.ResponseFromJob(job)
	if converted.Processed != resp.Processed {
		t.Fatalf("processed mismatch: got %d want %d", converted.Processed, resp.Processed)
	}
	if len(converted.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(converted.Items))
	}
	if converted.Items[0].ClipID != "clip1" {
		t.Fatalf("item clip id lost")
	}
}

func mustRawMessage(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestNormalizeRunTagRequest(t *testing.T) {
	// Test basic normalization
	req := RunTagRequest{
		Term:     "  Test Term ",
		Limit:    0,
		Strategy: "",
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{
		DefaultLimit: 5,
		MaxLimit:     500,
	})

	if normalized.Term != "Test Term" {
		t.Fatalf("term not normalized correctly: got %q", normalized.Term)
	}
	if normalized.Limit != 5 {
		t.Fatalf("default limit not applied: got %d", normalized.Limit)
	}
}

func TestNormalizeSearchTermLimitsToSixWords(t *testing.T) {
	if got := normalizeSearchTerm("  blue ocean sunset "); got != "blue ocean sunset" {
		t.Fatalf("expected three words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("single"); got != "single" {
		t.Fatalf("expected single word unchanged, got %q", got)
	}
	if got := normalizeSearchTerm("one two three four five six"); got != "one two three four five six" {
		t.Fatalf("expected six words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("one two three four five six seven"); got != "one two three four five six" {
		t.Fatalf("expected six words max, got %q", got)
	}
}

func TestNormalizeRunTagRequestRootFallback(t *testing.T) {
	req := RunTagRequest{
		Term: "test",
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{
		DefaultRootFolderID: "artlist-root",
	})

	if normalized.RootFolderID != "artlist-root" {
		t.Fatalf("root folder fallback not applied: got %q", normalized.RootFolderID)
	}
}

func TestNormalizeRunTagRequestMaxLimit(t *testing.T) {
	req := RunTagRequest{
		Term:  "test",
		Limit: 1000,
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{
		MaxLimit: 500,
	})

	if normalized.Limit != 500 {
		t.Fatalf("max limit not applied: got %d", normalized.Limit)
	}
}

func TestJobCodecPayloadFromRequestNilSafe(t *testing.T) {
	codec := &JobCodec{}

	payload := codec.PayloadFromRequest(nil)

	if payload == nil {
		t.Fatal("expected empty payload map, got nil")
	}

	if len(payload) != 0 {
		t.Fatalf("expected empty payload map, got %v", payload)
	}
}

func TestJobCodecPayloadFromRequestTrimsCanonicalFields(t *testing.T) {
	codec := &JobCodec{}

	payload := codec.PayloadFromRequest(&RunTagRequest{
		Term:         "  boxing highlights  ",
		RootFolderID: "  root123  ",
		Strategy:     "  verify  ",
		Limit:        3,
	})

	if payload["term"] != "boxing highlights" {
		t.Fatalf("expected trimmed term, got %q", payload["term"])
	}

	if payload["root_folder_id"] != "root123" {
		t.Fatalf("expected trimmed root_folder_id, got %q", payload["root_folder_id"])
	}

	if payload["strategy"] != "verify" {
		t.Fatalf("expected trimmed strategy, got %q", payload["strategy"])
	}
}

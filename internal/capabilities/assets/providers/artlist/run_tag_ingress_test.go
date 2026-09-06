package artlist

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- OUTCOME-INGRESS-NORMALIZATION (September 2026) -----------------------
//
// Service.RunTag is the SINGLE entry boundary for every public execution
// path (JobAdapter.HandleJob, Service.JobHandler, direct callers). It
// normalizes the request with canonical cfg defaults before delegating to
// the orchestrator. These tests pin the two invariants that make the
// boundary safe:
//
//  1. NormalizeRunTagRequest is IDEMPOTENT — queued callers (HTTP handler
//     before enqueue, HandleJob before its root-folder skip gate) that
//     already normalized are not altered by the boundary's second pass.
//  2. Service.RunTag fails closed on a nil request instead of panicking.

func TestNormalizeRunTagRequest_Idempotent(t *testing.T) {
	defaults := RunDefaults{
		DefaultRootFolderID: "artlist-root",
		DefaultLimit:        4,
		MaxLimit:            500,
	}

	raw := RunTagRequest{
		Term:         "  city   night    skyline   downtown   streets   lights   people   rain  ",
		Limit:        0,
		RootFolderID: "",
		Strategy:     "unknown-strategy",
		DryRun:       false,
		Concurrency:  999,
	}
	once := NormalizeRunTagRequest(raw, defaults)
	twice := NormalizeRunTagRequest(once, defaults)

	assert.Equal(t, once, twice,
		"NormalizeRunTagRequest must be idempotent: the Service.RunTag boundary re-normalizes "+
			"requests that queued callers already normalized, so a second pass must be a no-op")
}

func TestNormalizeRunTagRequest_TruncatesTermToSixWords(t *testing.T) {
	// The canonical normalize keeps at most maxSearchWords (6) words: the
	// truncation boundary the orchestrator itself does NOT re-apply. This
	// pins the contract that a direct caller passing a long raw term gets
	// the same canonical term as a queued caller.
	got := NormalizeRunTagRequest(RunTagRequest{
		Term: "  one two   three four five six seven eight  ",
	}, RunDefaults{DefaultLimit: 10, MaxLimit: 500})
	if want := "one two three four five six"; got.Term != want {
		t.Fatalf("normalized term: want %q, got %q", want, got.Term)
	}
}

func TestServiceRunTag_NilRequestFailsClosed(t *testing.T) {
	// Service.RunTag must reject a nil request at the boundary with a
	// typed error instead of panicking inside normalization/orchestration.
	svc := &Service{runOrchestrator: &RunOrchestratorService{}}
	resp, err := svc.RunTag(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, strings.Contains(err.Error(), "req is nil"),
		"nil-request failure must be explicit, got %v", err)
}

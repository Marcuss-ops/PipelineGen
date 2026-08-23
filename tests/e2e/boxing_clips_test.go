// Package e2e — boxing clips end-to-end contract test (PR-BOXING-SMOKES-MIGRATION).
//
// Hermetic CONTRACT test for the 4 Pacquiao vs Broner boxing clips.
// Loads the canonical payload + per-segment response JSON fixtures and
// asserts that:
//
//  1. The 4-segment payload is well-formed (ExtractRequest shape per
//     internal/capabilities/youtube/dto/types.go::ExtractRequest).
//  2. Each segment passes the canonical SegmentPolicy gate
//     (internal/capabilities/youtube/dto/types.go::DefaultSegmentPolicy
//     = Min=4s / Max=60s). Anything outside the window is a fail
//     godlike/07 P0 violation (typed FailureCodeDurationOutOfRange).
//  3. The deterministic clipID pattern
//     yt_<videoID>_<startSec>_<endSec>_<policyVer> round-trips: the
//     expected_clip_id field in each response JSON matches the payload's
//     start_seconds + end_seconds + policy_version (per Commit 2/6 #4
//     policy_version-in-filename invariant).
//  4. Each round's canonical response shape matches
//     internal/capabilities/youtube/dto/types.go::ExtractItem
//     (status="processed" + clipID + drive_link/file_hash/local_path
//     SHAPE only — runtime values populated by production drivers).
//
// Why contract-only and not end-to-end runtime: the production pipeline
// (drive.Service credentials + Qdrant live stack + SQLite) cannot be
// exercised in this envelope (the live stack is documented DOWN). Per
// AGENTS.md Git-Lesson-2 direct-to-main + godlike/07 no-fake-availability:
// we DO NOT fabricate drive_file_id or file_hash values in the fixtures
// (the fixture JSONs mark them as "<runtime>" + name the production
// driver that produces them). The Go test thus validates SHAPE without
// EVER entering a runtime-success or fake-availability class.
//
// Per the user request 2026-07-05: replaces 4 obsolete .sh smokes
// (fase_b_clip_pipeline_smoke + qdrant_e2e_boxing_smoke +
//
//	stock_register_batch_boxing_smoke + stock_run_boxing_smoke) with
//	a hermetic Go test + canonical JSON test fixtures.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Defaults pinned to the canonical extraction pipeline constants. If
// these drift in dto/types.go::DefaultSegmentPolicy, this Go test
// SHOULD fail loudly so the operator updates BOTH the production code
// and the fixture values below in lockstep (godlike/06 SSOT).
// godlike/07 honest-scope-lock: the canonical production values live in
// internal/capabilities/youtube/dto/types.go::DefaultSegmentPolicy;
// the constants below are a CLONE for the hermetic fixture validation
// path. A future auditor can `diff` the two via a forward-pointer
// check (PR-BOXING-SMOKES-DRIFT-DETECTION, deadline 2026-08-01).
const (
	canonicalMinSegmentDuration = 4    // seconds — matches dto/types.go::DefaultSegmentPolicy().MinDuration
	canonicalMaxSegmentDuration = 60   // seconds — matches dto/types.go::DefaultSegmentPolicy().MaxDuration
	canonicalPolicyVersion      = "v1" // dto/types.go::ProcessSegmentPolicyVersion
)

// boxingFixturePath returns the absolute path to a box-clips JSON
// fixture (used by every subtest). Tests are expected to run from the
// repository root (Go's default `go test` working directory).
func boxingFixturePath(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err, "boxing test: failed to resolve cwd")
	return filepath.Join(wd, "..", "..", "tests", "fixtures", "clip_batch_4boxing", name)
}

// boxingPayload mirrors the canonical ExtractRequest shape
// (per dto/types.go::ExtractRequest) for the 4-segment payload.
type boxingPayload struct {
	VideoID            string             `json:"video_id"`
	VideoURL           string             `json:"video_url"`
	DriveFolderID      string             `json:"drive_folder_id"`
	PolicyVersion      string             `json:"policy_version"`
	ForceKeyframes     bool               `json:"force_keyframes"`
	KeepAudio          bool               `json:"keep_audio"`
	Normalize          bool               `json:"normalize"`
	Strategy           string             `json:"strategy"`
	ExtractionStrategy string             `json:"extraction_strategy"`
	Concurrency        int                `json:"concurrency"`
	Destination        map[string]any     `json:"destination"`
	Segments           []boxingPayloadSeg `json:"segments"`
}

type boxingPayloadSeg struct {
	Index           int      `json:"index"`
	Start           string   `json:"start"`
	End             string   `json:"end"`
	Name            string   `json:"name"`
	Tags            []string `json:"tags"`
	Summary         string   `json:"summary"`
	Topics          []string `json:"topics"`
	Speakers        []string `json:"speakers"`
	MentionedPeople []string `json:"mentioned_people"`
	Hook            string   `json:"hook"`
	QualityScore    float64  `json:"quality_score"`
	StartSeconds    int      `json:"start_seconds"`
	EndSeconds      int      `json:"end_seconds"`
	DurationSeconds int      `json:"duration_seconds"`
	DurationGate    string   `json:"duration_gate"`
}

// boxingResponse mirrors the canonical expected response shape
// documented per round. Intentional subset of ExtractItem + cross-checks.
type boxingResponse struct {
	Round                       string `json:"_round"`
	IndexInPayload              int    `json:"_index_in_payload"`
	VideoID                     string `json:"_video_id"`
	PolicyVersion               string `json:"_policy_version"`
	DriftSourceOfTruth          string `json:"_drift_source_of_truth"`
	ExpectedStatus              string `json:"expected_status"`
	ExpectedClipID              string `json:"expected_clip_id"`
	ExpectedClipIDFormat        string `json:"expected_clip_id_format"`
	ExpectedClipIDDeterministic bool   `json:"expected_clip_id_deterministic"`
	ExpectedStart               string `json:"expected_start"`
	ExpectedEnd                 string `json:"expected_end"`
	ExpectedStartSeconds        int    `json:"expected_start_seconds"`
	ExpectedEndSeconds          int    `json:"expected_end_seconds"`
	ExpectedDurationSeconds     int    `json:"expected_duration_seconds"`
	ExpectedDurationGate        string `json:"expected_duration_gate"`
	ExpectedFilename            string `json:"expected_filename"`
}

// TestE2E_Boxing_Clips_PayloadContract — validates the canonical
// payload shape + per-segment SegmentPolicy gate.
func TestE2E_Boxing_Clips_PayloadContract(t *testing.T) {
	payloadPath := boxingFixturePath(t, "payload.json")
	raw, err := os.ReadFile(payloadPath)
	require.NoError(t, err, "read payload fixture %s", payloadPath)

	var p boxingPayload
	require.NoError(t, json.Unmarshal(raw, &p), "unmarshal payload fixture")

	// Shape invariants (per dto/types.go::ExtractRequest).
	require.Equal(t, "9u4T_o3FxOU", p.VideoID, "video_id drifting — payload must be the canonical Pacquiao vs Broner video ID")
	require.Equal(t, canonicalPolicyVersion, p.PolicyVersion, "policy_version drift — must match ProcessSegmentPolicyVersion")
	require.True(t, p.KeepAudio, "keep_audio must be true (canonical sequence)")
	require.True(t, p.ForceKeyframes, "force_keyframes must be true (per-commit-2/6 #4 deterministic filename)")
	require.Equal(t, "verify", p.Strategy, "strategy must be verify (canonical default)")

	// 4-segment invariant — exactly 4 boxing rounds for the canonical batch.
	require.Len(t, p.Segments, 4, "boxing batch must have exactly 4 segments (round 7/9/12 + verdetto)")

	// Per-segment SegmentPolicy gate + index monotonicity.
	for i, seg := range p.Segments {
		require.Equal(t, i, seg.Index, "segment[%d].index must equal array position", i)
		require.GreaterOrEqual(t, seg.DurationSeconds, canonicalMinSegmentDuration,
			"segment[%d] %q duration %ds below canonical MinDuration=%d (would trigger FailureCodeDurationOutOfRange)",
			i, seg.Name, seg.DurationSeconds, canonicalMinSegmentDuration)
		require.LessOrEqual(t, seg.DurationSeconds, canonicalMaxSegmentDuration,
			"segment[%d] %q duration %ds above canonical MaxDuration=%d (would trigger FailureCodeDurationOutOfRange)",
			i, seg.Name, seg.DurationSeconds, canonicalMaxSegmentDuration)
		require.Equal(t, seg.EndSeconds-seg.StartSeconds, seg.DurationSeconds,
			"segment[%d] %q duration_seconds must equal end_seconds - start_seconds",
			i, seg.Name)
		require.Equal(t, "boxing", p.Destination["group"],
			"destination.group must be boxing (canonical batch marker)")
		require.NotEmpty(t, seg.Tags, "segment[%d] %q must have tags (Qdrant indexing hook)", i, seg.Name)
		require.NotEmpty(t, seg.MentionedPeople, "segment[%d] %q must have mentioned_people (rich metadata field per PR-RICH-METADATA-DTO-EXTEND)", i, seg.Name)
	}

	// Cross-segment: indices are unique + monotonic.
	seen := map[int]bool{}
	for _, seg := range p.Segments {
		require.False(t, seen[seg.Index], "segment[%d].index must be unique across batch", seg.Index)
		seen[seg.Index] = true
	}
}

// TestE2E_Boxing_Clips_ResponseContract — validates each round's
// response fixture shape + cross-validates against the payload
// via deterministic clipID derivation.
func TestE2E_Boxing_Clips_ResponseContract(t *testing.T) {
	payloadPath := boxingFixturePath(t, "payload.json")
	rawPayload, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	var p boxingPayload
	require.NoError(t, json.Unmarshal(rawPayload, &p))

	// Map segments by index for cross-validation.
	segmentByIndex := make(map[int]boxingPayloadSeg, len(p.Segments))
	for _, seg := range p.Segments {
		segmentByIndex[seg.Index] = seg
	}

	roundNames := []string{"round7", "round9", "round12", "verdetto"}
	for i, round := range roundNames {
		t.Run(round, func(t *testing.T) {
			respPath := boxingFixturePath(t, round+"_response.json")
			raw, err := os.ReadFile(respPath)
			require.NoError(t, err, "read response fixture %s", respPath)
			var r boxingResponse
			require.NoError(t, json.Unmarshal(raw, &r), "unmarshal response fixture for %s", round)

			seg, ok := segmentByIndex[i]
			require.True(t, ok, "response %s must match a payload segment by index", round)

			// ClipID deterministic derivation (canonical pattern: yt_<videoID>_<startSec>_<endSec>_<policyVer>).
			expectedClipID := fmt.Sprintf("yt_%s_%d_%d_%s", p.VideoID, seg.StartSeconds, seg.EndSeconds, p.PolicyVersion)
			require.Equal(t, expectedClipID, r.ExpectedClipID,
				"clipID drift — response %s expected_clip_id does not match deterministic derivation from payload", round)

			require.Equal(t, "processed", r.ExpectedStatus, "expected_status must be processed (canonical pipeline outcome)")
			require.Equal(t, seg.StartSeconds, r.ExpectedStartSeconds, "cross-validation: payload vs response start_seconds mismatch")
			require.Equal(t, seg.EndSeconds, r.ExpectedEndSeconds, "cross-validation: payload vs response end_seconds mismatch")
			require.Equal(t, seg.DurationSeconds, r.ExpectedDurationSeconds,
				"cross-validation: payload vs response duration_seconds mismatch (would imply SegmentPolicy drift)")
			require.GreaterOrEqual(t, r.ExpectedDurationSeconds, canonicalMinSegmentDuration,
				"response %s duration below canonical MinDuration=%d", round, canonicalMinSegmentDuration)
			require.LessOrEqual(t, r.ExpectedDurationSeconds, canonicalMaxSegmentDuration,
				"response %s duration above canonical MaxDuration=%d", round, canonicalMaxSegmentDuration)
			require.Equal(t, p.VideoID, r.VideoID, "response %s video_id mismatch", round)
			require.Equal(t, p.PolicyVersion, r.PolicyVersion, "response %s policy_version mismatch", round)
			require.NotEmpty(t, r.DriftSourceOfTruth, "response %s must pin its drift_source_of_truth (godlike/06 SSOT)", round)
			require.Contains(t, r.DriftSourceOfTruth, "dto/types.go", "response %s drift_source_of_truth must point at dto/types.go", round)
		})
	}
}

// TestE2E_Boxing_Clips_NamingContract — validates that the
// expected filename matches the canonical pattern (videoID + startSec +
// endSec + policyVer + safe segment name).
//
// Per process_segment.go::SegmentsSvc.BuildClipFilename: the canonical
// filename is `<videoID>_<startSec>_<endSec>_<policyVer>_<safeName>.mp4`.
// Safe segment name replaces unsafe chars with underscores and trims.
func TestE2E_Boxing_Clips_NamingContract(t *testing.T) {
	payloadPath := boxingFixturePath(t, "payload.json")
	rawPayload, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	var p boxingPayload
	require.NoError(t, json.Unmarshal(rawPayload, &p))

	roundNames := []string{"round7", "round9", "round12", "verdetto"}
	for _, round := range roundNames {
		t.Run(round, func(t *testing.T) {
			respPath := boxingFixturePath(t, round+"_response.json")
			raw, err := os.ReadFile(respPath)
			require.NoError(t, err)
			var r boxingResponse
			require.NoError(t, json.Unmarshal(raw, &r))

			require.NotEmpty(t, r.ExpectedFilename, "response %s expected_filename must be non-empty", round)
			require.Contains(t, r.ExpectedFilename, p.VideoID, "filename must contain videoID")
			require.Contains(t, r.ExpectedFilename, fmt.Sprintf("_%d_", r.ExpectedStartSeconds), "filename must contain startSec")
			require.Contains(t, r.ExpectedFilename, fmt.Sprintf("_%d_", r.ExpectedEndSeconds), "filename must contain endSec")
			require.Contains(t, r.ExpectedFilename, p.PolicyVersion, "filename must contain policy_version")
			require.True(t, strings.HasSuffix(r.ExpectedFilename, ".mp4"),
				"filename %q must end in .mp4 (canonical BuildClipFilename algorithm)",
				r.ExpectedFilename)
		})
	}
}

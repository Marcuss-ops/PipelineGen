// Package e2e — Dolly Parton clip-batch contract test (2026-09-17).
//
// Hermetic CONTRACT test for the canonical Dolly Parton interview batch:
// YouTube video vLRjqTIiMjc ("Dolly Parton Got Kicked Out Of A Hotel During
// Her First Trip To NYC | Late Night with Conan O'Brien"), 5 curated moments,
// published into the Drive clip library under the "Dolly Parton" subfolder.
//
// It pins, against the REAL production contracts and with no live stack
// (no server, no admin token, no Drive, no PostgreSQL):
//
//  1. The payload is a valid internal/capabilities/youtube/dto::ExtractRequest.
//     The fixture is decoded through the PRODUCTION unmarshaller, so the
//     nested-`destination` rule (ExtractRequest.UnmarshalJSON) is exercised
//     rather than mirrored.
//  2. The legacy top-level destination shape is still rejected — the fail
//     closed contract that stops a request from silently landing in the
//     default Drive hierarchy.
//  3. Every moment passes the canonical SegmentPolicy Min gate and fits inside
//     the source video; the two moments above the 60s canonical default are
//     named explicitly together with the segment-duration floor the deployed
//     service must run with (VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS).
//  4. Each moment owns a DETERMINISTIC, DISTINCT clip id
//     yt_<videoID>_<startSec>_<endSec>_<policyVersion> (SSOT:
//     kernel/asset/detail.NewYouTubeClipIdentity + youtube/usecase
//     .ProcessSegmentPolicyVersion). Two runs of the same payload therefore
//     UPSERT the same 5 media_assets rows — the idempotent-replay property
//     that keeps the clip table free of duplicates.
//
// Live counterpart: POST /api/clips/process with
// tests/fixtures/clip_batch_vLRjqTIiMjc/payload.json (server + admin token +
// Drive). This test never claims a Drive artifact exists.

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

const (
	// dollyPartonVideoID / dollyPartonSourceURL identify the source interview.
	dollyPartonVideoID   = "vLRjqTIiMjc"
	dollyPartonSourceURL = "https://www.youtube.com/watch?v=vLRjqTIiMjc"

	// dollyPartonSourceDurationSeconds is the YouTube-declared duration
	// (PT5M57S). Every segment end must stay inside it.
	dollyPartonSourceDurationSeconds = 357

	// dollyPartonDriveParentFolderID is config.yaml drive.normal_clips_source_folder:
	// the canonical Drive clip-library root the batch publishes under.
	dollyPartonDriveParentFolderID = "1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2"

	// dollyPartonDocsFolderID is the canonical Drive root for the generated
	// scripts and the language-specific rendered clips. It is intentionally
	// separate from the source clip-library root above.
	dollyPartonDocsFolderID = "1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K"

	// dollyPartonSubfolderName is the child folder materialised (and reused)
	// under the parent by the extraction worker's get-or-create step.
	dollyPartonSubfolderName = "Dolly Parton"

	// dollyPartonDeployedMaxSegmentDurationSeconds mirrors the RUNNING
	// service's VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS (config.yaml
	// jobs.youtube_max_segment_duration_seconds declares the canonical 60s
	// default; the environment override wins at runtime). Two moments of this
	// batch are longer than 60s, so this floor is a hard deployment
	// requirement, not a preference.
	dollyPartonDeployedMaxSegmentDurationSeconds = 240

	// dollyPartonLongestSegmentSeconds is the longest curated moment. The
	// deployed segment-duration gate must be >= this value.
	dollyPartonLongestSegmentSeconds = 85
)

// dollyPartonFixtureDir is the canonical fixture directory (tests are expected
// to run from the package directory).
func dollyPartonPayloadPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err, "dolly parton batch: failed to resolve cwd")
	return filepath.Join(wd, "..", "fixtures", "clip_batch_vLRjqTIiMjc", "payload.json")
}

func dollyPartonPayloadBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(dollyPartonPayloadPath(t))
	require.NoError(t, err, "dolly parton batch: read payload fixture")
	return raw
}

// dollyPartonPayload decodes the fixture through the PRODUCTION
// ExtractRequest unmarshaller — the wire contract itself, not a hand-rolled
// mirror struct.
func dollyPartonPayload(t *testing.T) youtubetypes.ExtractRequest {
	t.Helper()
	var req youtubetypes.ExtractRequest
	require.NoError(t, json.Unmarshal(dollyPartonPayloadBytes(t), &req),
		"the fixture must decode as the canonical ExtractRequest wire shape")
	return req
}

// TestDollyPartonClipBatchMatchesCanonicalExtractRequest pins the payload
// shape: source, destination routing and one complete segment per curated
// moment.
func TestDollyPartonClipBatchMatchesCanonicalExtractRequest(t *testing.T) {
	req := dollyPartonPayload(t)

	require.Equal(t, dollyPartonSourceURL, req.URL)

	videoID, err := urlutil.ExtractVideoID(req.URL)
	require.NoError(t, err, "the source URL must yield a canonical YouTube video id")
	require.Equal(t, dollyPartonVideoID, videoID)

	require.Len(t, req.Segments, 5, "the curated batch carries exactly 5 moments")
	require.NotNil(t, req.Destination, "Destination is REQUIRED: a Drive-less extraction is a silent local-only run")

	dest := req.Destination
	require.Equal(t, dollyPartonDriveParentFolderID, dest.FolderID,
		"clips publish under drive.normal_clips_source_folder")
	require.Equal(t, dollyPartonSubfolderName, dest.SubfolderName,
		"clips publish into the 'Dolly Parton' child folder, never the library root")
	require.True(t, dest.CreateSubfolder,
		"create_subfolder must be true so the worker get-or-creates the child folder")
	require.NotEqual(t, dollyPartonVideoID, dest.SubfolderName,
		"an empty subfolder_name makes the handler fall back to a video-id folder; this batch pins the operator-named folder instead")

	for i, seg := range req.Segments {
		name := fmt.Sprintf("segment[%d]", i+1)
		require.True(t, strings.TrimSpace(seg.Name) != "", "%s: name is required (it becomes the clip filename slug)", name)
		require.True(t, strings.TrimSpace(seg.Summary) != "", "%s: summary is required (it feeds the indexed search text)", name)
		require.NotEmpty(t, seg.Tags, "%s: tags are required", name)
		require.Equal(t, "Conan O'Brien", seg.SourceChannel, "%s: source_channel is pinned", name)

		start, err := textutil.ParseTimestamp(seg.Start)
		require.NoError(t, err, "%s: start timestamp must parse", name)
		end, err := textutil.ParseTimestamp(seg.End)
		require.NoError(t, err, "%s: end timestamp must parse", name)
		require.Less(t, start, end, "%s: start must precede end", name)
		require.LessOrEqual(t, end, dollyPartonSourceDurationSeconds,
			"%s: the moment must fit inside the PT5M57S source", name)
	}
}

// TestDollyPartonClipBatchRejectsTheLegacyTopLevelDestinationShape keeps the
// fail-closed destination contract honest for THIS payload: lifting the
// destination fields to the top level must fail at decode time instead of
// dropping the operator's Drive intent.
func TestDollyPartonClipBatchRejectsTheLegacyTopLevelDestinationShape(t *testing.T) {
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(dollyPartonPayloadBytes(t), &fields))

	var dest map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fields["destination"], &dest))

	// The three keys this batch actually declares are the canonical ones the
	// handler normalises; lifting them to the top level is exactly the shape
	// the pre-destination wire format used.
	require.Contains(t, dest, "folder_id")
	require.Contains(t, dest, "subfolder_name")
	require.Contains(t, dest, "create_subfolder")
	require.Len(t, dest, 3, "the fixture destination must stay minimal (folder_id + subfolder_name + create_subfolder)")

	for key, value := range dest {
		fields[key] = value
	}
	delete(fields, "destination")

	legacyRaw, err := json.Marshal(fields)
	require.NoError(t, err)

	var legacyReq youtubetypes.ExtractRequest
	err = json.Unmarshal(legacyRaw, &legacyReq)
	require.Error(t, err, "the legacy top-level destination shape must be rejected, never silently honoured")
	require.Contains(t, err.Error(), "destination",
		"the rejection must name the destination nesting requirement")
}

// TestDollyPartonClipBatchDurationGate pins the canonical Min gate, the
// deployment floor the two long moments require, and the exact set of moments
// that exceed the 60s canonical default.
func TestDollyPartonClipBatchDurationGate(t *testing.T) {
	req := dollyPartonPayload(t)

	canonical := youtubetypes.DefaultSegmentPolicy()
	require.Equal(t, 4, canonical.MinDuration, "canonical SegmentPolicy.MinDuration drift: update the fixture comment")
	require.Equal(t, 60, canonical.MaxDuration, "canonical SegmentPolicy.MaxDuration drift: update the fixture comment")

	var (
		longest    int
		overCustom []int
	)
	for i, seg := range req.Segments {
		start, err := textutil.ParseTimestamp(seg.Start)
		require.NoError(t, err, "segment[%d]: start timestamp", i+1)
		end, err := textutil.ParseTimestamp(seg.End)
		require.NoError(t, err, "segment[%d]: end timestamp", i+1)

		duration := end - start
		require.GreaterOrEqual(t, duration, canonical.MinDuration,
			"segment[%d]: %ds is below the canonical Min gate", i+1, duration)
		require.LessOrEqual(t, duration, dollyPartonDeployedMaxSegmentDurationSeconds,
			"segment[%d]: %ds exceeds the deployed VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS=%d",
			i+1, duration, dollyPartonDeployedMaxSegmentDurationSeconds)
		if duration > canonical.MaxDuration {
			overCustom = append(overCustom, i+1)
		}
		if duration > longest {
			longest = duration
		}
	}

	require.Equal(t, dollyPartonLongestSegmentSeconds, longest,
		"longest curated moment drift: the deployment floor must be re-verified")
	require.GreaterOrEqual(t, dollyPartonDeployedMaxSegmentDurationSeconds, longest,
		"segments longer than %ds fail with FailureCodeDurationOutOfRange: run the extraction with VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS >= %d",
		longest, longest)
	require.Equal(t, []int{3, 5}, overCustom,
		"exactly moments 3 and 5 exceed the 60s canonical default; if the fixture changes, update the fixture comment AND re-check the deployment override")
}

// TestDollyPartonClipBatchClipIDsAreDeterministicAndDistinct proves the
// idempotency property that keeps media_assets duplicate-free: the clip id is
// a pure function of (videoID, startSec, endSec, policyVersion), so a re-run of
// this payload targets the same 5 rows and every moment owns a distinct row.
func TestDollyPartonClipBatchClipIDsAreDeterministicAndDistinct(t *testing.T) {
	req := dollyPartonPayload(t)

	wantIDs := []string{
		"yt_vLRjqTIiMjc_0_25_v1",
		"yt_vLRjqTIiMjc_39_90_v1",
		"yt_vLRjqTIiMjc_100_184_v1",
		"yt_vLRjqTIiMjc_211_256_v1",
		"yt_vLRjqTIiMjc_270_355_v1",
	}

	gotIDs := make([]string, 0, len(req.Segments))
	seen := make(map[string]int, len(req.Segments))

	for i, seg := range req.Segments {
		start, err := textutil.ParseTimestamp(seg.Start)
		require.NoError(t, err, "segment[%d]: start timestamp", i+1)
		end, err := textutil.ParseTimestamp(seg.End)
		require.NoError(t, err, "segment[%d]: end timestamp", i+1)

		// The content hash is only an index-event input here; the ASSET id
		// must not depend on it (a re-index must not mint a second clip).
		sum := sha256.Sum256([]byte(seg.Name + "\n" + seg.Summary))
		identity, err := detail.NewYouTubeClipIdentity(detail.YouTubeClipIdentityParams{
			VideoID:     dollyPartonVideoID,
			StartSec:    start,
			EndSec:      end,
			PolicyVer:   ytusecase.ProcessSegmentPolicyVersion,
			ContentHash: hex.EncodeToString(sum[:]),
		})
		require.NoError(t, err, "segment[%d]: canonical clip identity", i+1)

		require.Equal(t,
			fmt.Sprintf("yt_%s_%d_%d_%s", dollyPartonVideoID, start, end, ytusecase.ProcessSegmentPolicyVersion),
			identity.AssetID,
			"segment[%d]: the clip id format is the canonical yt_<videoID>_<start>_<end>_<policyVersion>", i+1)

		// Deterministic replay: recomputing the identity yields the exact
		// same asset id AND index-event key, so the upsert is a no-op.
		replay, err := detail.NewYouTubeClipIdentity(detail.YouTubeClipIdentityParams{
			VideoID:     dollyPartonVideoID,
			StartSec:    start,
			EndSec:      end,
			PolicyVer:   ytusecase.ProcessSegmentPolicyVersion,
			ContentHash: hex.EncodeToString(sum[:]),
		})
		require.NoError(t, err)
		require.Equal(t, identity.AssetID, replay.AssetID, "segment[%d]: clip id must be deterministic", i+1)
		require.Equal(t, identity.IndexEventKey, replay.IndexEventKey, "segment[%d]: index event key must be deterministic", i+1)

		gotIDs = append(gotIDs, identity.AssetID)
		seen[identity.AssetID]++
	}

	require.Equal(t, wantIDs, gotIDs, "the batch's canonical clip ids are pinned")
	require.Len(t, seen, len(req.Segments),
		"each moment must own a distinct clip id; a collision would overwrite a sibling clip")
}

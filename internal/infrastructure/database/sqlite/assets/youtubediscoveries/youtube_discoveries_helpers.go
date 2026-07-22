package youtubediscoveries

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// deriveDiscoveryID computes the canonical ledger id from
// (channel_id, video_id, policy_version). The id is the sha256 of
// the join, hex-truncated to 16 chars with the "disc_" prefix, so
// the cell stays human-readable during debugging while the
// underlying hash space is wide enough to avoid collisions across
// migrations.
//
// Deterministic derivation is intentional: concurrent
// retry-after-error paths must converge on the same id so the
// UNIQUE(channel_id, video_id, policy_version) key continues to
// gate correctly even after the underlying row went through a hot
// update. Including policy_version in the hash means a v1 row and
// a v2 row for the SAME (channelID, videoID) produce different ids
// — important because the two co-exist under UNIQUE.
func deriveDiscoveryID(channelID, videoID, policyVersion string) string {
	h := sha256.Sum256([]byte(channelID + ":" + videoID + ":" + policyVersion))
	return "disc_" + hex.EncodeToString(h[:8])
}

// ComputeRetryBackoffSeconds returns the exponential-backoff delay
// (capped, in seconds) for the given attempt_count. attempt_count=1
// (first retry) → 30s. attempt_count=12 (twelfth retry) → 300s (capped).
//
// Formula: min(30 * 2^(attempt_count-1), RetryableBackoffCapSeconds).
//
// Exported so tests can lock the curve without re-deriving the
// formula (see TestComputeRetryBackoffSeconds_Monotonic). Returns
// seconds (caller multiplies by time.Second to derive the timestamp
// offset).
func ComputeRetryBackoffSeconds(attemptCount int) int {
	if attemptCount < 1 {
		attemptCount = 1
	}
	// 2^(attempt_count-1) capped at RetryableBackoffCapSeconds/30.
	// Hardening: if attempt_count > 30, the bit-shift wraps; we
	// cap explicitly via the cap branch below so the math is stable.
	if attemptCount > 30 {
		return RetryableBackoffCapSeconds
	}
	delay := 30
	for i := 1; i < attemptCount; i++ {
		delay *= 2
		if delay >= RetryableBackoffCapSeconds {
			return RetryableBackoffCapSeconds
		}
	}
	return delay
}

// ResolveDateAfter bridges channel.LastCursor (an RFC3339
// timestamp stored in category_channels.last_cursor) and channel.LookbackDays
// (the channel's lookback fallback) into a YYYYMMDD string the
// yt-dlp Downloader.ListChannelVideos port accepts in
// ListChannelVideosRequest.DateAfter.
//
// Precedence (caller's intent): LastCursor wins when parseable as
// RFC3339 (the canonical cursor format from migration 113 onward);
// LookbackDays wins as fallback (now - LookbackDays*24h formatted as
// YYYYMMDD). Empty LastCursor + zero LookbackDays → empty DateAfter
// (yt-dlp's no-filter path).
//
// Exposed in pkg/portable? Not yet — callers (monitor package's
// ListChannelVideos seam) import the function directly. Stability
// note: this function's callers are internal package boundaries;
// renaming / re-signing requires updating the callers in lockstep.
func ResolveDateAfter(lastCursorRFC3339 string, lookbackDays int) string {
	if lastCursorRFC3339 != "" {
		// Truncate RFC3339 to YYYYMMDD. The first 10 characters of
		// "2026-06-30T15:04:05Z" are "2026-06-30" — drop the rest.
		if len(lastCursorRFC3339) >= 10 {
			datePart := lastCursorRFC3339[:10]
			// Sanity-check: all 10 char[0..4] = digits + dashes.
			dash1, dash2 := datePart[4], datePart[7]
			if dash1 == '-' && dash2 == '-' {
				// Re-format YYYY-MM-DD to YYYYMMDD (dash removal).
				return datePart[:4] + datePart[5:7] + datePart[8:10]
			}
		}
	}
	if lookbackDays > 0 {
		t := time.Now().UTC().Add(-time.Duration(lookbackDays) * 24 * time.Hour)
		return t.Format("20060102")
	}
	return ""
}

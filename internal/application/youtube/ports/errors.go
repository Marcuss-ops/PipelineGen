// Package ports — sentinel errors for fail-closed behaviour (PR2, June 2026).
//
// These errors are returned by SearchRunnerPort implementations when the
// underlying infrastructure is unavailable. Callers match with `errors.Is`
// to differentiate between:
//
//   - transient unavailability (e.g. yt-dlp subprocess timed out)
//   - structural unavailability (composition root never wired the port)
//   - cancellation (parent context was already done)
//
// The fail-closed contract guarantees that a SearchRunnerPort NEVER returns
// an empty result set with a nil error: this would be indistinguishable
// from "search succeeded with no hits" and is the failure mode that PR2
// specifically addresses. Sentry-style naming + errors.Is support means
// callers can either log and bubble up, or downgrade to "search skipped"
// when ErrSearchRunnerUnavailable is the cause.
package ports

import "errors"

// ErrSearchRunnerUnavailable is returned when SearchLive cannot proceed
// because the underlying infrastructure (yt-dlp subprocess, network,
// configuration) is not reachable. Callers should distinguish this from
// "search succeeded with zero hits" — the former is a runtime error and
// must be surfaced to the caller, the latter is data.
var ErrSearchRunnerUnavailable = errors.New("youtube: search runner unavailable (underlying infrastructure not reachable)")

// ErrSearchRunnerVideoInfoUnavailable is returned when GetVideoInfo cannot
// proceed for the same reasons as ErrSearchRunnerUnavailable but applies
// specifically to per-video metadata fetches. Split out from the search
// sentinel because callers may want to retry info fetches independently
// of searches (e.g. background refresh vs user-initiated query).
var ErrSearchRunnerVideoInfoUnavailable = errors.New("youtube: video info runner unavailable (underlying infrastructure not reachable)")

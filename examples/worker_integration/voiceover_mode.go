// Package main — voiceover_mode.go
//
// Voiceover-mode runner (-mode=voiceover): self-contained flow for
// POST /api/media/voiceover/generate. It mirrors the script-mode flow
// at the submit/poll layer (sharing poll.go::submitAndPoll) but builds
// a GenerateVoiceoversRequest-shaped payload (NOT GenerationEnvelopeV2)
// and uses a different idempotency contract — MD5(itemID|locale|voice),
// where itemID is the operator-supplied or auto-derived logical anchor
// and locale+voice are the TTS-differentiation axes (same text +
// different voice = different jobs server-side).
package main

import (
	"context"
	"log"
	"os/signal"
	"strings"
	"syscall"

	veloxclient "github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// runVoiceoverMode is the self-contained voiceover flow invoked from
// main() when -mode=voiceover.
//
// Fail-closed validation chain (godlike/07 + PR-VO-C1 invariant):
//   - text:    non-empty
//   - locale:  non-empty (BCP-47; server normalizes lower-case)
//   - destKind: 'explicit' OR 'group' (canonical enum)
//   - destKind=explicit → destFolder non-empty
//   - destKind=group    → destGroup  non-empty
//   - strategy: in {verify, skip, replace} (server normalizes unknown
//     to 'verify' silently; we fail-closed to surface typos)
//   - parallelism: in [1, 16] (TTS fan-out cap; godlike/07 no-overflow)
//
// Idempotency contract: X-Request-ID = MD5(itemID|locale|voice). Matches
// examples/test_remote_generate_voiceover.sh (LC_ALL=C enforced) so the
// two tools collide on the same logical key for the same logical inputs.
// See voiceover/types.go::parentActiveKey for the server-side ActiveKey
// fingerprint (SHA256 covering Text + Languages + Destination.FolderID
// + Project) — the worker derives X-Request-ID from logical inputs;
// the server derives ActiveKey from payload contents.
func runVoiceoverMode(cfg *workerConfig) {
	// Fail-closed validation chain (godlike/07 + PR-VO-C1).
	if strings.TrimSpace(cfg.VoText) == "" {
		log.Fatalf("-mode=voiceover requires non-empty -text (godlike/07 fail-closed)")
	}
	if strings.TrimSpace(cfg.VoLocale) == "" {
		log.Fatalf("-mode=voiceover requires non-empty -locale (BCP-47 code)")
	}
	if cfg.VoDestKind != "explicit" && cfg.VoDestKind != "group" {
		log.Fatalf("-destination-kind must be 'explicit' or 'group' (godlike/06 canonical set); got %q", cfg.VoDestKind)
	}
	if cfg.VoDestKind == "explicit" && strings.TrimSpace(cfg.VoDestFolder) == "" {
		log.Fatalf("-destination-kind=explicit requires non-empty -destination-folder-id (godlike/07 fail-closed PR-VO-C1)")
	}
	if cfg.VoDestKind == "group" && strings.TrimSpace(cfg.VoDestGroup) == "" {
		log.Fatalf("-destination-kind=group requires non-empty -destination-group (godlike/07 fail-closed PR-VO-C1)")
	}
	switch cfg.VoStrategy {
	case "verify", "skip", "replace":
		// canonical set; pass-through.
	default:
		log.Fatalf("-strategy must be one of verify|skip|replace (godlike/06 canonical set); got %q (server-side NormalizeStrategy would silently coerce to 'verify' — fail-closed here surfaces the typo)", cfg.VoStrategy)
	}
	if cfg.VoParallelism < 1 || cfg.VoParallelism > 16 {
		log.Fatalf("-parallelism must be in [1, 16] (godlike/07 fan-out cap); got %d", cfg.VoParallelism)
	}

	payload, reqID := buildVoiceoverPayload(cfg)

	log.Printf("worker starting mode=voiceover endpoint=%s url=%s", voiceoverGenerateEndpoint, cfg.BaseURL)

	if cfg.DryRun {
		printDryRun(voiceoverGenerateEndpoint, cfg.BaseURL, reqID, payload)
		return
	}

	// SIGINT/SIGTERM cancels the long poll loop without orphaning the
	// job (the job keeps running server-side — that's the whole point
	// of the submit/poll separation).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cli := veloxclient.New(cfg.BaseURL, cfg.Token)
	submitAndPoll(ctx, cli, voiceoverGenerateEndpoint, payload, reqID, cfg.PollEvery, cfg.MaxWait, cfg.Verbose)
}

// buildVoiceoverPayload assembles the GenerateVoiceoversRequest-shaped
// payload and the deterministic reqID for voiceover mode.
//
// Idempotency anchor: itemID is derived deterministically if the
// operator did not supply one, using the same MD5 helper as the
// canonical reqID so the wire request_id stays 32-hex stable across
// retries (server accepts up to 64 alphanumeric chars; 32-hex fits
// with room for prefixes).
//
// Destination map — only the field required by the canonical kind is
// populated (godlike/07 fail-closed: kind=explicit carries folder_id
// only; kind=group carries group only; mirror of
// internal/api/assets/voiceover/types.go::GenerateVoiceoversRequest.Validate
// PR-VO-C1 invariant).
//
// Payload is the canonical GenerateVoiceoversRequest-shaped payload
// (wire-shape/payload split preserved per AGENTS.md Pattern 6 — the
// request_id field is the wire-correlation-id, NOT a separate
// header in this mode; X-Request-ID is set by veloxclient.SubmitAsync
// from the reqID argument).
//
// reqID = MD5(itemID|locale|voice). itemID is the operator-supplied or
// auto-derived logical anchor (the SAME hash used for the wire
// request_id); locale+voice are the TTS-differentiation axes. Same text
// + different voice ⇒ different reqID ⇒ different server job. Same text
// + same voice + same itemID across retries ⇒ same reqID ⇒ server
// returns existing job (the godlike/07 fail-closed idempotency-anchor
// contract).
func buildVoiceoverPayload(cfg *workerConfig) (map[string]any, string) {
	itemID := cfg.VoItemID
	if strings.TrimSpace(itemID) == "" {
		itemID = buildStableReqID(cfg.VoText, cfg.VoLocale, cfg.VoVoice)
	}
	filename := cfg.VoFilename
	if strings.TrimSpace(filename) == "" {
		filename = itemID + ".mp3"
	}

	// Build destination map — only the field required by the canonical
	// kind is populated (godlike/07 fail-closed: kind=explicit carries
	// folder_id only; kind=group carries group only; mirror of
	// internal/api/assets/voiceover/types.go::GenerateVoiceoversRequest.Validate
	// PR-VO-C1 invariant).
	destination := map[string]any{"kind": cfg.VoDestKind}
	switch cfg.VoDestKind {
	case "explicit":
		destination["folder_id"] = cfg.VoDestFolder
	case "group":
		destination["group"] = cfg.VoDestGroup
	}

	payload := map[string]any{
		"request_id": itemID,
		"items": []map[string]any{
			{
				"text":     cfg.VoText,
				"language": cfg.VoLocale,
				"voice":    cfg.VoVoice,
				"filename": filename,
				"required": cfg.VoRequired,
			},
		},
		"destination": destination,
		"options": map[string]any{
			"remove_silence": false,
			"strategy":       cfg.VoStrategy,
			"parallelism":    cfg.VoParallelism,
		},
	}
	if strings.TrimSpace(cfg.VoProject) != "" {
		// ThreadingCampaign 2026-07-08: project is forwarded verbatim
		// for {project}/{language}/ Drive subdir layout. Empty project
		// falls through to the pre-P12 default — do NOT add a default
		// here, that would change byte-identical behavior for existing
		// callers (back-compat invariant per PR-PROMOTE-REQUIRED-FIX).
		payload["project"] = cfg.VoProject
	}

	return payload, buildStableReqID(itemID, cfg.VoLocale, cfg.VoVoice)
}

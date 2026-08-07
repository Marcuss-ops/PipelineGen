// Package main is a minimal reference worker that demonstrates calling
// pipelinegen from a separate process over the cross-worker veloxclient:
//
//  1. builds a deterministic X-Request-ID from the inputs (so a crashed
//     retry lands on the same server-side job via the
//     (type, correlation_id) UNIQUE constraint),
//  2. submits the job to the selected mode endpoint,
//  3. polls until the job reaches a terminal state,
//  4. pretty-prints the result on stdout (or stderr on failure).
//
// Run it with:
//
//	go run ./examples/worker_integration \
//	  -url "http://127.0.0.1:8080" \
//	  -token "$(cat ~/.config/pipelinegen/worker_token)" \
//	  -topic "the great barrier reef" \
//	  -video-name reef-documentary -language en
//
// Or set VELOX_WORKER_TOKEN and PIPELINEGEN_URL env vars. Use -dry-run to
// inspect the payload + derived reqID without contacting the server.
//
// File layout (split 2026-08-07 to satisfy the strict per-file LOC cap
// and the cmd_main_max_lines=200 entry-point cap in
// architecture/policy.yaml):
//   - main.go:            slim dispatcher (flags → mode runners)
//   - config.go:          workerConfig struct + parseFlags()
//   - script_mode.go:     -mode=script flow + payload builder
//   - voiceover_mode.go:  -mode=voiceover flow + payload builder
//   - poll.go:            shared submit/poll loop + output rendering
package main

import (
	"log"
	"strings"
)

// scriptGenerateEndpoint is the canonical submission route for all
// script-generation requests (text, clips, catalog, search sources).
// The legacy per-source endpoints (`/api/script/generate-with-images`,
// `/api/script/generate-from-clips`, `/api/script/generate-from-catalog`,
// `/api/script/curate`) are retired; clients flow through this single
// canonical endpoint with the GenerationEnvelopeV2 shape and select
// the source via `items[].source.type`. See architecture/current.yaml
// for the deprecation ticket.
const scriptGenerateEndpoint = "/api/script/generate"

// voiceoverGenerateEndpoint is the canonical submission route for
// POST /api/media/voiceover/generate — the parent voiceover job that
// fans out one voiceover.generate_item child per items[] row
// (PR-VO-C1 fanout contract). Wire shape matches
// GenerateVoiceoversRequest in internal/api/assets/voiceover/types.go;
// destination.kind in {explicit, group} is enforced via the godlike/07
// fail-closed PR-VO-C1 invariant (kind=explicit requires non-empty
// folder_id; kind=group requires non-empty group).
const voiceoverGenerateEndpoint = "/api/media/voiceover/generate"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg := parseFlags()

	if strings.TrimSpace(cfg.Token) == "" {
		log.Fatal("-token (or VELOX_WORKER_TOKEN env var) is required")
	}
	if cfg.Mode != "script" && cfg.Mode != "voiceover" {
		log.Fatalf("-mode must be 'script' or 'voiceover' (godlike/06 canonical set); got %q", cfg.Mode)
	}

	// Mode dispatch: each runner owns its validation, payload shape
	// (GenerationEnvelopeV2 vs GenerateVoiceoversRequest), idempotency
	// hash, and submit+poll hand-off. The two modes share only the
	// lifecycle plumbing in poll.go (endpoint + payload are the only
	// differences between their submit+poll calls).
	if cfg.Mode == "voiceover" {
		runVoiceoverMode(cfg)
		return
	}
	runScriptMode(cfg)
}

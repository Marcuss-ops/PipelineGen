// Package main — config.go
//
// CLI configuration for the worker_integration reference worker: the
// workerConfig struct (single bundle for every flag) and parseFlags()
// which owns the canonical flag registry. Keeping the flag surface in
// its own file means main() stays a slim entry point (archcheck
// cmd_main_max_lines: 200) and both mode runners receive one config
// value instead of 18 positional parameters.
//
// Flag semantics are documented inline next to each definition — these
// are the operator-facing contract (godlike/06 canonical sets are
// enforced in the mode runners, not here).
package main

import (
	"flag"
	"os"
	"time"
)

// workerConfig bundles every CLI flag into one value passed to the
// mode runners. Zero-value semantics match the flag defaults below
// (parseFlags fills all fields before returning).
type workerConfig struct {
	// Shared transport
	BaseURL string
	Token   string

	// Script mode (GenerationEnvelopeV2)
	Topic     string
	VideoName string
	Language  string
	Source    string
	ClipIDs   string

	// Voiceover mode (GenerateVoiceoversRequest)
	Mode          string
	VoText        string
	VoLocale      string
	VoVoice       string
	VoFilename    string
	VoDestKind    string
	VoDestFolder  string
	VoDestGroup   string
	VoStrategy    string
	VoParallelism int
	VoRequired    bool
	VoProject     string
	VoItemID      string

	// Job lifecycle
	PollEvery time.Duration
	MaxWait   time.Duration
	DryRun    bool
	Verbose   bool
}

// parseFlags registers the canonical worker flag set and returns the
// populated workerConfig. Called exactly once from main() before any
// mode dispatch.
func parseFlags() *workerConfig {
	cfg := &workerConfig{}

	flag.StringVar(&cfg.BaseURL, "url", envDefault("PIPELINEGEN_URL", envDefault("VELOX_MASTER_URL", "http://127.0.0.1:8000")), "pipelinegen base URL (or env PIPELINEGEN_URL / VELOX_MASTER_URL)")
	flag.StringVar(&cfg.Token, "token", os.Getenv("VELOX_WORKER_TOKEN"), "bearer token (or env VELOX_WORKER_TOKEN)")
	flag.StringVar(&cfg.Topic, "topic", "the great barrier reef", "script topic (becomes items[].source.topic)")
	flag.StringVar(&cfg.VideoName, "video-name", "reef-documentary", "video name — used as items[].id (idempotency key) + items[].title")
	flag.StringVar(&cfg.Language, "language", "en", "script output language (items[].language)")
	flag.StringVar(&cfg.Source, "source", "text", "items[].source.type — 'text' (topic-driven) or 'clips' (media-clip-driven); godlike/06 canonical set")
	flag.StringVar(&cfg.ClipIDs, "clip-ids", "", "comma-separated clip ids; REQUIRED iff -source=clips (fail-closed per godlike/07); example: yt_RRJvrDKunyA_32_37_v1,yt_RRJvrDKunyA_993_998_v1")
	// Mode switch + voiceover-mode flag set (ITEM 6 — canonical CLI
	// for POST /api/media/voiceover/generate; mirror of ITEM 4 source=clips
	// but the wire shape is GenerateVoiceoversRequest, NOT
	// GenerationEnvelopeV2, so the payload-builder is mode-specific —
	// see voiceover_mode.go::runVoiceoverMode).
	flag.StringVar(&cfg.Mode, "mode", "script", "worker mode: 'script' (POST /api/script/generate with -source=text|clips) or 'voiceover' (POST /api/media/voiceover/generate); godlike/06 canonical set")
	flag.StringVar(&cfg.VoText, "text", "", "voiceover text to convert via TTS; REQUIRED iff -mode=voiceover (godlike/07 fail-closed)")
	flag.StringVar(&cfg.VoLocale, "locale", "it-IT", "BCP-47 language tag for the voiceover; e.g. it-IT, en-US, pt-BR; voiceover mode only")
	flag.StringVar(&cfg.VoVoice, "voice", "it-IT-DiegoNeural", "TTS voice name (e.g. it-IT-DiegoNeural); empty lets the server VoiceRegistry resolve a default for the locale; voiceover mode only")
	flag.StringVar(&cfg.VoFilename, "filename", "", "voiceover output filename (default: derived from -item-id or MD5(text|locale|voice)); voiceover mode only")
	flag.StringVar(&cfg.VoDestKind, "destination-kind", "explicit", "destination.kind: 'explicit' (Drive folder_id) or 'group' (Drive group name); godlike/07 fail-closed PR-VO-C1 invariant; voiceover mode only")
	flag.StringVar(&cfg.VoDestFolder, "destination-folder-id", "", "Drive folder_id; REQUIRED iff -destination-kind=explicit (PR-VO-C1 fail-closed); voiceover mode only")
	flag.StringVar(&cfg.VoDestGroup, "destination-group", "", "Drive group name; REQUIRED iff -destination-kind=group (PR-VO-C1 fail-closed); voiceover mode only")
	flag.StringVar(&cfg.VoStrategy, "strategy", "verify", "pipeline strategy: 'verify' (default) | 'skip' | 'replace'; server-side asset.NormalizeStrategy coerces unknown values to 'verify'; voiceover mode only")
	flag.IntVar(&cfg.VoParallelism, "parallelism", 1, "fan-out concurrency (1..16); server clamps to min(requested, MaxParallelism, len(items)); voiceover mode only")
	flag.BoolVar(&cfg.VoRequired, "required", true, "items[].required flag (godlike/07 no-fake-availability: parent treats failed required items as a parent failure); voiceover mode only")
	flag.StringVar(&cfg.VoProject, "project", "", "optional project name for {project}/{language}/ Drive subdir layout (ThreadingCampaign 2026-07-08); voiceover mode only")
	flag.StringVar(&cfg.VoItemID, "item-id", "", "logical idempotency anchor used as wire request_id (default: derived deterministically from MD5(text|locale|voice)); voiceover mode only")
	flag.DurationVar(&cfg.PollEvery, "poll-every", 5*time.Second, "status poll interval")
	flag.DurationVar(&cfg.MaxWait, "max-wait", 30*time.Minute, "max wall-time before giving up on the job")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "print the payload + reqID without calling the server")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "print every poll result (default: only on status changes)")

	flag.Parse()
	return cfg
}

// envDefault returns key's value if set, else fallback. Mirrors the
// bash ${VAR:-fallback} convention used by the operator smoke scripts.
func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

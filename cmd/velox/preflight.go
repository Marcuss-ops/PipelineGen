package main

// preflight.go — local, offline structural validation of a job payload BEFORE
// it is submitted.
//
// Why it exists (2026-11-22 Augias r27): twelve submissions edited the same
// payload and each mistake was discovered only after the expensive stages had
// run (voiceover, localized renders, audio compile), 28-410 s per attempt. The
// ingress already validates the envelope structurally; this command runs the
// SAME canonical validator (GenerationEnvelopeV2.Validate) on the SAME strict
// decoder the server uses (DisallowUnknownFields), so a payload that would be
// rejected with 400 fails here in milliseconds and without a job id.
//
// Since 2026-11-23 the command also runs the Stage 0 checks that need the live
// catalog: for every bound clip it asks the operator read model
// (GET /api/assets/operator/assets/:id) whether the asset exists, is ACTIVE and
// READY, has a known duration and — when the segment needs subtitles — carries
// a text track. That is exactly the set of failures that used to surface after
// ASR or after the first render attempt (NO_SPEECH, decoder EOF, missing
// asset), and every one of them is a property of ONE asset, so one request per
// clip is enough: no second catalog is built here. The disk guard measures the
// data directory because a 98%-full volume fails late and opaquely.
//
// What remains server-side is the compiled plan (certified renders, stock
// isolation, media certificates) because it needs the resolved plan, not the
// envelope. A green preflight therefore means "this body and its assets can
// start", not "the plan is certified": use --offline to keep the old
// structural-only behaviour when no server is reachable.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/payloadcheck"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type preflightReport struct {
	OK       bool         `json:"ok"`
	Payload  string       `json:"payload"`
	Bytes    int          `json:"bytes"`
	Items    int          `json:"items"`
	FinalJob bool         `json:"final_job"`
	Clips    int          `json:"clips"`
	Assets   []assetCheck `json:"assets,omitempty"`
	Disk     *diskCheck   `json:"disk,omitempty"`
	Errors   []string     `json:"errors,omitempty"`
	// Warnings are non-fatal asset degradations (a Drive-only clip). They are
	// reported so a green preflight is never silent about them.
	Warnings []string `json:"warnings,omitempty"`

	// structural records whether the failures are ingress-contract failures
	// (HTTP 400) or Stage 0 asset/disk failures (the body is fine, the run is
	// not). The closing hint must not claim a 400 the server would not send.
	structural bool

	// Offline records that the catalog was deliberately not consulted. The
	// success line must then say "non verificati": claiming verified clips
	// while --offline skipped every request is exactly the false green the
	// command exists to prevent.
	Offline bool `json:"offline,omitempty"`
}

func cmdPreflight(args []string) int {
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox preflight: %v\n", err)
		return exitUsage
	}
	payloadPath := fs.get("payload", "")
	if payloadPath == "" {
		fmt.Fprintln(os.Stderr, "velox preflight: --payload FILE is required")
		return exitUsage
	}
	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox preflight: read payload: %v\n", err)
		return exitUsage
	}
	payload, err := cleanPayload(raw)
	if err != nil {
		return emitPreflight(preflightReport{Payload: payloadPath, Bytes: len(raw), Errors: []string{err.Error()}}, fs.has("json"))
	}

	// Same strictness as POST /api/script/generate, through the SAME decoder:
	// a removed or typoed field is a 400 at ingress, never silently ignored.
	// The kernel decoder also reaches inside the custom UnmarshalJSON blocks
	// (output, audio, media_plan), which a plain DisallowUnknownFields decoder
	// cannot see.
	env, err := scriptpkg.DecodeGenerationEnvelopeStrict(payload)
	if err != nil {
		return emitPreflight(preflightReport{Payload: payloadPath, Bytes: len(payload), Errors: []string{"invalid payload: " + err.Error()}}, fs.has("json"))
	}
	report := preflightReport{Payload: payloadPath, Bytes: len(payload), Items: len(env.Items), FinalJob: env.FinalJob}
	report.Errors = envelopeErrors(env)
	// Version 3 refuses the V2-only duplicate spellings, exactly as the
	// ingress does; version 2 keeps them readable for the recorded corpus.
	if reason := scriptpkg.RetiredContractFieldReason(payload, env.Version); reason != "" {
		report.Errors = append(report.Errors, reason)
	}
	// Structural validation cannot see two blocks disagreeing with each
	// other (explicit_only + entity images, two audio.mode owners, an
	// oversized phrase budget). Apply the same semantic gate payloadlint
	// uses, before any Stage 0 asset work.
	report.Errors = append(report.Errors, payloadcheck.SemanticErrors(env)...)
	report.structural = len(report.Errors) > 0
	if len(report.Errors) == 0 {
		// Stage 0: the body is admissible, now prove the run has what it needs
		// (an active, ready, spoken clip per binding and a disk with room).
		// A structural problem stops here: the asset ids of an invalid
		// envelope are not authoritative yet.
		disk := checkDisk(fs.get("data-dir", defaultPreflightDataDir()), uint64(attrInt(fs.get("min-free-gb", ""), 5))<<30)
		report.Disk = &disk
		if !disk.OK {
			report.Errors = append(report.Errors, "disk: "+disk.Message)
		}
		if reqs := scriptpkg.CollectClipRequirements(env); len(reqs) > 0 {
			report.Clips = len(reqs)
			token := strings.TrimSpace(os.Getenv("VELOX_ADMIN_TOKEN"))
			switch {
			case fs.has("offline"):
				report.Offline = true
				fmt.Fprintf(os.Stderr, "preflight: --offline — %d clip(s) non verificati sul catalogo\n", len(reqs))
			case token == "":
				// Without a token every request answers 401, so N clips would
				// print N identical lines and bury the one fact that matters:
				// nothing was verified because no credential was supplied.
				report.Errors = append(report.Errors, fmt.Sprintf(
					"asset: token API mancante (VELOX_ADMIN_TOKEN non impostato) — %d clip(s) non verificati; esporta il token (scripts/with-velox-auth) o usa --offline",
					len(reqs)))
			default:
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				report.Assets = checkAssets(ctx, checkAssetsServerURL(fs.get("server", "")), token, reqs)
				cancel()
				for _, check := range report.Assets {
					report.Warnings = append(report.Warnings, check.Warnings...)
				}
				if rejected := assetAuthRejection(report.Assets); rejected != "" {
					// The token reached the server and was refused: the catalog
					// verdict per clip is meaningless, so report the credential
					// problem once instead of once per clip.
					report.Errors = append(report.Errors, rejected)
				} else {
					for _, check := range report.Assets {
						if check.OK {
							continue
						}
						where := check.ClipID
						if len(check.Scenes) > 0 {
							where += " (" + strings.Join(check.Scenes, ", ") + ")"
						}
						report.Errors = append(report.Errors, "asset "+where+": "+strings.Join(check.Problems, "; "))
					}
				}
			}
		}
	}
	return emitPreflight(report, fs.has("json"))
}

// defaultPreflightDataDir resolves the directory the disk guard measures. The
// CLI runs from the repo root or from refactored/ depending on the caller, so
// both spellings are checked. A candidate must LOOK like the pipeline data
// root (it owns at least one of the known scratch/state entries): the super
// repo also has a bare `data/` directory used by other tools, and picking it
// would measure and clean the wrong volume.
func defaultPreflightDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("VELOX_DATA_DIR")); dir != "" {
		return dir
	}
	markers := []string{"final-job-fragments", "cas", "tmp", "pipelinegen.db", "velox.db"}
	candidates := []string{"refactored/data", "data", filepath.Join("..", "data")}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(candidate, marker)); err == nil {
				return candidate
			}
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return "."
}

// checkAssetsServerURL resolves the server the asset gate talks to. An empty
// flag keeps the canonical loopback default so a wrapper is never required.
func checkAssetsServerURL(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue)
	}
	return loadConfig().baseURL
}

// envelopeErrors returns the canonical structural verdict as flat strings, so
// the CLI prints the same facts the server would put in its 400 body. A
// PayloadValidationError keeps its stable code in front of the message.
func envelopeErrors(env *scriptpkg.GenerationEnvelopeV2) []string {
	err := env.Validate()
	if err == nil {
		return nil
	}
	var pve *scriptpkg.PayloadValidationError
	if errors.As(err, &pve) {
		return []string{pve.Code + ": " + pve.Message}
	}
	return []string{err.Error()}
}

func emitPreflight(report preflightReport, asJSON bool) int {
	report.OK = len(report.Errors) == 0
	if asJSON {
		if code := emitJSON(report); code != exitOK {
			return code
		}
		if !report.OK {
			return exitFailure
		}
		return exitOK
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(os.Stderr, "  warning: %s\n", warning)
	}
	if report.OK {
		clips := fmt.Sprintf("%d clip(s) verificati", report.Clips)
		if report.Offline && report.Clips > 0 {
			clips = fmt.Sprintf("%d clip(s) non verificati (--offline)", report.Clips)
		}
		fmt.Printf("preflight: ok — %s (%s bytes, %d item(s), final_job=%t, %s)\n",
			report.Payload, strconv.Itoa(report.Bytes), report.Items, report.FinalJob, clips)
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "preflight: FAIL — %s\n", report.Payload)
	nested := false
	for _, problem := range report.Errors {
		fmt.Fprintf(os.Stderr, "  - %s\n", problem)
		if strings.Contains(problem, "unknown field") {
			nested = true
		}
	}
	if nested {
		// The decoder names the key but not its nesting, and a key that exists
		// one level too deep (e.g. max_overlays_per_scene under script_params
		// instead of the item root) reads like "this field does not exist".
		fmt.Fprintln(os.Stderr, "  hint: an unknown field may exist in the contract at a different nesting; see docs/operations/script-generate-payload-schema.md")
	}
	if report.structural {
		fmt.Fprintln(os.Stderr, "preflight: the server would reject this payload with HTTP 400; fix it before submitting.")
	} else {
		fmt.Fprintln(os.Stderr, "preflight: the envelope is valid but the run would fail on the items above; fix them before submitting.")
	}
	return exitFailure
}

// Package script — handler_preflight.go owns POST /api/script/preflight.
//
// Why a server-side preflight (2026-11-23, Augias r42): the CLI already ran
// the same Stage 0 checks, but every other client of the pipeline (the console,
// a remote operator, a script in ops/) had to re-implement them or discover the
// failure minutes into a run. The route answers the same question the command
// asks — "can this exact body start, and does every clip it binds exist, is it
// ACTIVE/READY, does it have a known duration and, when the segment burns
// subtitles, a text track?" — using the SAME validator the submit path uses
// (bindGenerateEnvelope → PayloadValidator) so a green preflight and a 400 can
// never disagree about the contract.
//
// What it deliberately does NOT do: no TTS, no GPU, no render, no job row. The
// compiled plan (certified renders, STOCK ISOLATION, media certificates) still
// needs the resolved plan and stays validated during the run.
//
// Response contract:
//
//	200  ok=true   — the body is admissible and every clip passed;
//	400            — the canonical ingress error (unknown field, contract
//	                 violation): the same body the submit would reject;
//	422  ok=false  — the body is valid but at least one clip/asset would fail;
//	                 the report lists every clip with its problems;
//	200  unverified=true — no catalog reader is wired; the contract was
//	                 checked but no asset was verified. Never reported as ok.
package script

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AssetPreflightFact is the narrow catalog view the gate reads. It mirrors the
// operator read model (GET /api/assets/operator/assets/:id) without importing
// it: the script capability must not depend on another capability's DTO.
type AssetPreflightFact struct {
	ClipID         string
	LifecycleState string
	AssetState     string
	DurationMS     int64
	TextTracks     int

	// Reachability facts (OVL-06): the resolver needs a local binary, or a
	// Drive reference when the segment allows a drive-only clip. Without
	// these the preflight could only say "asset non trovato" and leave the
	// operator without the path the resolver expects or the reason it is
	// unreachable.
	LocalPath    string
	DriveLink    string
	HasLocalFile bool
	HasDriveFile bool
}

// AssetPreflightReader resolves one clip's catalog facts. found=false means the
// id is not in the catalog (a 404 in the operator API), which is a preflight
// failure, never an empty success. A non-nil error means "not verified".
type AssetPreflightReader interface {
	PreflightAssetFacts(ctx context.Context, clipID string) (fact AssetPreflightFact, found bool, err error)
}

// PreflightDeps is the optional dependency bag for POST /preflight. A nil
// Assets reader keeps the route mounted (contract-only) and reports
// unverified=true.
type PreflightDeps struct {
	Assets AssetPreflightReader
}

// assetPreflightCheck is the per-clip JSON verdict.
type assetPreflightCheck struct {
	ClipID     string   `json:"clip_id"`
	OK         bool     `json:"ok"`
	Scenes     []string `json:"scenes,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Problems   []string `json:"problems,omitempty"`
	// Warnings are degradations the render can still survive (a clip with a
	// Drive copy but no local binary). They never flip ok, so a warning is
	// never read as a pass either: the two lists stay distinct in the JSON.
	Warnings []string `json:"warnings,omitempty"`
}

// preflightReport is the JSON body of the route.
type preflightReport struct {
	OK     bool                  `json:"ok"`
	Items  int                   `json:"items"`
	Clips  int                   `json:"clips"`
	Assets []assetPreflightCheck `json:"assets,omitempty"`
	Errors []string              `json:"errors,omitempty"`

	// Unverified is set when no catalog reader is wired: the contract was
	// checked, the assets were not. It is explicit so a green contract can
	// never be read as a verified catalog.
	Unverified bool `json:"unverified,omitempty"`
}

// PreflightRoute registers POST /preflight on the script router group.
func (h *HandlerGenerate) PreflightRoute(r *gin.RouterGroup) {
	if h == nil {
		return
	}
	r.POST("/preflight", h.Preflight)
}

// SetAssetPreflight wires the catalog reader used by POST /preflight. It is a
// setter (not a constructor argument) so every existing NewHandlerGenerate
// caller — including the test fixtures — keeps its signature.
func (h *HandlerGenerate) SetAssetPreflight(reader AssetPreflightReader) {
	if h == nil {
		return
	}
	h.preflightAssets = reader
}

// Preflight handles POST /api/script/preflight.
func (h *HandlerGenerate) Preflight(c *gin.Context) {
	// Same binding + validator as POST /generate: identical 400s, identical
	// unknown-field and retired-field handling.
	env, ok := bindGenerateEnvelope(c, h.validator)
	if !ok {
		return
	}
	report := preflightReport{Items: len(env.Items)}
	reqs := scriptpkg.CollectClipRequirements(env)
	report.Clips = len(reqs)

	switch {
	case len(reqs) == 0:
		// No clip bound (pure text run): nothing to verify in the catalog.
	case h.preflightAssets == nil:
		report.Unverified = true
		report.OK = false
		report.Errors = append(report.Errors, fmt.Sprintf(
			"asset: catalogo non collegato a questa istanza — %d clip(s) non verificati", len(reqs)))
	default:
		report.Assets = h.checkPreflightAssets(c.Request.Context(), reqs)
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
	report.OK = len(report.Errors) == 0 && !report.Unverified

	status := http.StatusOK
	if !report.OK && !report.Unverified {
		status = http.StatusUnprocessableEntity
	}
	c.JSON(status, report)
}

// checkPreflightAssets resolves every requirement sequentially. The
// architecture gate forbids raw goroutines in the transport layer
// (TestStaticGate_NoConcreteInfrastructureInTransport), and a catalog read per
// clip is a single indexed lookup: a 22-clip payload is one short request, not
// a fan-out that would need bounding. Parallelism, when it is needed, belongs
// behind the port (a read model that batches), not in the handler.
func (h *HandlerGenerate) checkPreflightAssets(ctx context.Context, reqs []scriptpkg.ClipRequirement) []assetPreflightCheck {
	out := make([]assetPreflightCheck, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, preflightAssetCheck(ctx, h.preflightAssets, req))
	}
	return out
}

// preflightAssetCheck is the pure verdict for one clip, split from the HTTP
// handler so the rules can be unit-tested without a router or a database.
func preflightAssetCheck(ctx context.Context, reader AssetPreflightReader, req scriptpkg.ClipRequirement) (check assetPreflightCheck) {
	check = assetPreflightCheck{ClipID: req.ClipID, Scenes: append([]string(nil), req.Scenes...)}
	if reader == nil {
		check.Problems = []string{"catalogo non collegato"}
		return check
	}
	fact, found, err := reader.PreflightAssetFacts(ctx, req.ClipID)
	if err != nil {
		check.Problems = []string{"catalogo non consultabile: " + err.Error()}
		return check
	}
	if !found {
		check.Problems = []string{"asset non trovato nel catalogo: nessuna riga in media_assets per l'id (atteso local_path o drive_link)"}
		return check
	}
	check.DurationMS = fact.DurationMS
	if !strings.EqualFold(strings.TrimSpace(fact.LifecycleState), "ACTIVE") {
		check.Problems = append(check.Problems, "lifecycle_state="+fallbackValue(fact.LifecycleState, "sconosciuto")+" (serve ACTIVE)")
	}
	if !strings.EqualFold(strings.TrimSpace(fact.AssetState), "READY") {
		check.Problems = append(check.Problems, "asset_state="+fallbackValue(fact.AssetState, "sconosciuto")+" (serve READY)")
	}
	if fact.DurationMS <= 0 {
		check.Problems = append(check.Problems, "durata sconosciuta (duration_ms<=0: il renderer non ha una finestra valida)")
	}
	if req.NeedSpeech && fact.TextTracks == 0 {
		check.Problems = append(check.Problems, "NO_SPEECH: nessuna traccia testo (il render sottotitoli fallirà dopo l'ASR)")
	}
	// Reachability is the OVL-06 verdict: a clip with neither a local binary
	// nor a Drive copy is a fatal preflight failure, named with the path the
	// resolver expects; a Drive-only clip is a warning, not a pass and not a
	// block.
	reachProblems, reachWarnings := scriptpkg.ClipMediaProblems(scriptpkg.ClipMediaReachability{
		ClipID:       req.ClipID,
		LocalPath:    fact.LocalPath,
		DriveLink:    fact.DriveLink,
		HasLocalFile: fact.HasLocalFile,
		HasDriveFile: fact.HasDriveFile,
	})
	check.Problems = append(check.Problems, reachProblems...)
	check.Warnings = append(check.Warnings, reachWarnings...)
	check.OK = len(check.Problems) == 0
	return check
}

func fallbackValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

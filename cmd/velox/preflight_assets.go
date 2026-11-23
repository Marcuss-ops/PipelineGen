package main

// preflight_assets.go — the part of the preflight that needs the live
// catalog, not just the JSON body.
//
// Why (2026-11-23, Augias r42): the structural preflight proved only that the
// body would be accepted, so four classes of failure were still discovered
// after minutes of work — a clip absent or not READY, a clip with no speech
// (NO_SPEECH after the ASR stage), a clip whose declared duration is past the
// real file tail (decoder EOF at frame 924) and a full disk. Each of them is a
// property of ONE asset that the running server already exposes through
// GET /api/assets/operator/assets/:id, so it belongs here, before the submit.
//
// The check is deliberately read-only and one request per clip: no catalog is
// duplicated in the CLI, and the verdict comes from the same read model the
// operator UI uses.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// clipRequirement is the kernel's canonical ClipRequirement. The walk over
// the envelope lives in internal/kernel/script so the CLI and the server-side
// POST /api/script/preflight can never disagree about which clips are bound
// (or about which of them must speak).
type clipRequirement = scriptpkg.ClipRequirement

type assetCheck struct {
	ClipID   string   `json:"clip_id"`
	OK       bool     `json:"ok"`
	Scenes   []string `json:"scenes,omitempty"`
	Duration int64    `json:"duration_ms,omitempty"`
	Problems []string `json:"problems,omitempty"`

	// authRejected marks a 401/403 from the catalog. It is not part of the
	// JSON report: it exists so the caller can replace N identical credential
	// errors with the single fact they all carry.
	authRejected bool
}

type diskCheck struct {
	Path      string `json:"path"`
	FreeBytes uint64 `json:"free_bytes"`
	MinBytes  uint64 `json:"min_bytes"`
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
}

// operatorAsset mirrors only the fields the gate reads. Unknown fields are
// ignored on purpose: the operator read model is free to grow.
type operatorAsset struct {
	ID             string `json:"id"`
	LifecycleState string `json:"lifecycle_state"`
	AssetState     string `json:"asset_state"`
	DurationMS     int64  `json:"duration_ms"`
	TextTracks     []struct {
		LanguageCode string `json:"language_code"`
	} `json:"text_tracks"`
}

// checkAssets queries the running server for every bound clip. A request
// error is a problem for that clip, never a silent pass: an unreachable
// server means "not verified", which must not read as "verified".
func checkAssets(ctx context.Context, baseURL, token string, reqs []clipRequirement) []assetCheck {
	out := make([]assetCheck, len(reqs))
	if len(reqs) == 0 {
		return out
	}
	const workers = 8
	client := &http.Client{Timeout: 8 * time.Second}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Add(1)
		go func(i int, req clipRequirement) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = checkOneAsset(ctx, client, baseURL, token, req)
		}(i, req)
	}
	wg.Wait()
	return out
}

func checkOneAsset(ctx context.Context, client *http.Client, baseURL, token string, req clipRequirement) (check assetCheck) {
	check = assetCheck{ClipID: req.ClipID, Scenes: append([]string(nil), req.Scenes...)}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/assets/operator/assets/" + url.PathEscape(req.ClipID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		check.Problems = []string{"request: " + err.Error()}
		return check
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		check.Problems = []string{"server non raggiungibile: " + err.Error()}
		return check
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		check.Problems = []string{"asset non trovato nel catalogo"}
		return check
	case http.StatusUnauthorized, http.StatusForbidden:
		check.authRejected = true
		check.Problems = []string{"token non valido per l'API asset (HTTP " + fmt.Sprint(resp.StatusCode) + ")"}
		return check
	default:
		check.Problems = []string{"HTTP " + fmt.Sprint(resp.StatusCode) + " dal catalogo asset"}
		return check
	}
	var asset operatorAsset
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		check.Problems = []string{"risposta asset illeggibile: " + err.Error()}
		return check
	}
	check.Duration = asset.DurationMS
	if !strings.EqualFold(strings.TrimSpace(asset.LifecycleState), "ACTIVE") {
		check.Problems = append(check.Problems, "lifecycle_state="+fallbackString(asset.LifecycleState, "sconosciuto")+" (serve ACTIVE)")
	}
	if !strings.EqualFold(strings.TrimSpace(asset.AssetState), "READY") {
		check.Problems = append(check.Problems, "asset_state="+fallbackString(asset.AssetState, "sconosciuto")+" (serve READY)")
	}
	if asset.DurationMS <= 0 {
		check.Problems = append(check.Problems, "durata sconosciuta (duration_ms<=0: il renderer non ha una finestra valida)")
	}
	if req.NeedSpeech && len(asset.TextTracks) == 0 {
		check.Problems = append(check.Problems, "NO_SPEECH: nessuna traccia testo (il render sottotitoli fallirà dopo l'ASR)")
	}
	check.OK = len(check.Problems) == 0
	return check
}

// assetAuthRejection collapses the per-clip credential verdict into one line.
// The caller must render it in place of the per-asset problems: when the token
// is refused, every "asset X: token non valido" is the same fact repeated.
func assetAuthRejection(checks []assetCheck) string {
	rejected := 0
	for _, check := range checks {
		if check.authRejected {
			rejected++
		}
	}
	if rejected == 0 {
		return ""
	}
	return fmt.Sprintf("asset: il token API è stato rifiutato (HTTP 401/403) — %d clip(s) su %d non verificati; controlla VELOX_ADMIN_TOKEN (scripts/with-velox-auth)",
		rejected, len(checks))
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// checkDisk guards the one resource every stage shares. The 2026-11-23 host
// had 7 GB free while a run needs the final MP4 (405 MB) plus fragments; a
// full disk fails late and opaquely, so it fails here instead.
func checkDisk(path string, minBytes uint64) diskCheck {
	check := diskCheck{Path: path, MinBytes: minBytes}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		check.Message = "statfs: " + err.Error()
		return check
	}
	check.FreeBytes = stat.Bavail * uint64(stat.Bsize)
	check.OK = check.FreeBytes >= minBytes
	if !check.OK {
		check.Message = fmt.Sprintf("spazio libero insufficiente: %.1f GiB, minimo %.1f GiB",
			float64(check.FreeBytes)/(1<<30), float64(minBytes)/(1<<30))
	}
	return check
}

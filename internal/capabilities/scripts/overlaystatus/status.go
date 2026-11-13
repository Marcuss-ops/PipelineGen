// Package overlaystatus persists a compact, human-readable state for one
// overlay run. It is deliberately a sink: the pipeline packages own the facts,
// this package only merges them into a durable JSON document so an operator can
// see background resolution, entity admission, the compiled plan and the
// preflight outcome without waiting for the terminal job error.
package overlaystatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const SchemaVersion = "pipelinegen.overlay-status.v1"

// Request is the caller's overlay contract, copied before planning.
type Request struct {
	BackgroundKind    string   `json:"background_kind,omitempty"`
	BackgroundAssetID string   `json:"background_asset_id,omitempty"`
	BackgroundFit     string   `json:"background_fit,omitempty"`
	EntityImages      bool     `json:"entity_images_enabled"`
	EntityTypes       []string `json:"entity_types,omitempty"`
	EntityScope       string   `json:"entity_scope,omitempty"`
	MaxImageOverlays  int      `json:"max_image_overlays,omitempty"`
	FinalJob          bool     `json:"final_job,omitempty"`
}

// Background is the resolved plate identity, or the failure that stopped it.
type Background struct {
	AssetID   string `json:"asset_id,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Resolved  bool   `json:"resolved"`
	Error     string `json:"error,omitempty"`
}

// PlanSummary is the compiled plan shape, independent of the full plan wire.
type PlanSummary struct {
	PlanID           string         `json:"plan_id,omitempty"`
	PlanSHA256       string         `json:"plan_sha256,omitempty"`
	Items            int            `json:"items"`
	ByKind           map[string]int `json:"by_kind,omitempty"`
	ImageItems       int            `json:"image_items"`
	EntityImageItems int            `json:"entity_image_items"`
}

// Preflight records the final-job gate outcome.
type Preflight struct {
	Passed bool   `json:"passed"`
	Error  string `json:"error,omitempty"`
}

// Status is the durable document. Audit is intentionally an opaque value: the
// scripts package owns OverlayAudit and this recorder must not create a second,
// drift-prone copy of that contract.
type Status struct {
	SchemaVersion string       `json:"schema_version"`
	RunID         string       `json:"run_id"`
	PlanID        string       `json:"plan_id,omitempty"`
	JobID         string       `json:"job_id,omitempty"`
	Stage         string       `json:"stage"`
	UpdatedAt     time.Time    `json:"updated_at"`
	Requested     *Request     `json:"requested,omitempty"`
	Background    *Background  `json:"background,omitempty"`
	Audit         any          `json:"audit,omitempty"`
	Plan          *PlanSummary `json:"plan,omitempty"`
	Preflight     *Preflight   `json:"preflight,omitempty"`
	Error         string       `json:"error,omitempty"`
}

var writeMu sync.Mutex

// Dir returns the status directory. The default lives under the system temp
// directory so tests and ad-hoc runs never pollute the repository; production
// can pin a durable path with VELOX_OVERLAY_STATUS_DIR.
func Dir() string {
	if dir := strings.TrimSpace(os.Getenv("VELOX_OVERLAY_STATUS_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "pipelinegen-overlay-status")
}

// Path returns the document path for one run.
func Path(runID string) string {
	return filepath.Join(Dir(), safeName(runID)+".json")
}

// Update merges one stage into the run document. It is safe to call from the
// plan, background and final-job stages; an absent document is created, and a
// missing run id is a no-op rather than a shared "unknown" bucket.
func Update(runID string, mutate func(*Status)) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()

	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		zap.L().Warn("overlay status: create directory failed", zap.String("dir", dir), zap.Error(err))
		return
	}
	path := Path(runID)
	var status Status
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &status); err != nil {
			zap.L().Warn("overlay status: decode existing state failed; replacing", zap.String("path", path), zap.Error(err))
			status = Status{}
		}
	} else if !os.IsNotExist(err) {
		zap.L().Warn("overlay status: read existing state failed", zap.String("path", path), zap.Error(err))
	}
	if status.SchemaVersion == "" {
		status.SchemaVersion = SchemaVersion
	}
	if status.RunID == "" {
		status.RunID = runID
	}
	if mutate != nil {
		mutate(&status)
	}
	status.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		zap.L().Warn("overlay status: marshal failed", zap.String("run_id", runID), zap.Error(err))
		return
	}
	tmp, err := os.CreateTemp(dir, "."+safeName(runID)+".*.tmp")
	if err != nil {
		zap.L().Warn("overlay status: create temp failed", zap.String("dir", dir), zap.Error(err))
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		zap.L().Warn("overlay status: write temp failed", zap.String("path", tmpName), zap.Error(err))
		return
	}
	if err := tmp.Close(); err != nil {
		zap.L().Warn("overlay status: close temp failed", zap.String("path", tmpName), zap.Error(err))
		return
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		zap.L().Warn("overlay status: chmod failed", zap.String("path", tmpName), zap.Error(err))
	}
	if err := os.Rename(tmpName, path); err != nil {
		zap.L().Warn("overlay status: publish failed", zap.String("path", path), zap.Error(err))
		return
	}
	zap.L().Debug("overlay status updated", zap.String("run_id", runID), zap.String("stage", status.Stage), zap.String("path", path))
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

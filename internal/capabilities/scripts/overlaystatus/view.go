package overlaystatus

// view.go owns the read-only operator projection of the durable overlay
// status documents. It lives beside the recorder so the thin command only
// parses flags and delegates; it never grows a second copy of the wire shape.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNoDir reports that the status directory has not been created yet. It is
// distinct from "empty directory" so the command can print the accurate
// first-run message instead of an empty list.
var ErrNoDir = errors.New("overlay status directory does not exist")

type AuditView struct {
	PlannedOverlayCount int            `json:"planned_overlay_count"`
	PlannedByKind       map[string]int `json:"planned_by_kind"`
	EntityImages        *struct {
		Budget     int            `json:"budget"`
		BudgetUsed int            `json:"budget_used"`
		Selected   int            `json:"selected_entities"`
		Bound      int            `json:"bound"`
		Missing    int            `json:"missing"`
		Reasons    map[string]int `json:"reasons"`
	} `json:"entity_images"`
}

// View is the operator projection of one Status document. It deliberately
// mirrors only the fields the CLI reads, so a future internal field cannot
// change the human output or leak into the list JSON by accident.
type View struct {
	SchemaVersion string    `json:"schema_version"`
	RunID         string    `json:"run_id"`
	PlanID        string    `json:"plan_id"`
	JobID         string    `json:"job_id"`
	Stage         string    `json:"stage"`
	UpdatedAt     time.Time `json:"updated_at"`
	Requested     *struct {
		BackgroundAssetID string `json:"background_asset_id"`
		EntityImages      bool   `json:"entity_images_enabled"`
		MaxImageOverlays  int    `json:"max_image_overlays"`
	} `json:"requested"`
	Background *struct {
		AssetID   string `json:"asset_id"`
		LocalPath string `json:"local_path"`
		SHA256    string `json:"sha256"`
		Resolved  bool   `json:"resolved"`
		Error     string `json:"error"`
	} `json:"background"`
	Audit *AuditView `json:"audit"`
	Plan  *struct {
		Items            int            `json:"items"`
		ByKind           map[string]int `json:"by_kind"`
		ImageItems       int            `json:"image_items"`
		EntityImageItems int            `json:"entity_image_items"`
	} `json:"plan"`
	Preflight *struct {
		Passed bool   `json:"passed"`
		Error  string `json:"error"`
	} `json:"preflight"`
	Error string `json:"error"`
}

// Load reads and decodes one status document, returning the raw bytes too so
// --json can print the durable document verbatim instead of a re-marshalled
// approximation.
func Load(path string) (View, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return View{}, nil, err
	}
	var view View
	if err := json.Unmarshal(raw, &view); err != nil {
		return View{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	return view, raw, nil
}

// List returns the newest status documents in the directory, capped by limit
// when positive. Unreadable individual documents are skipped (one corrupt
// run must not hide every other run); a missing directory is ErrNoDir.
func List(dir string, limit int) ([]View, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoDir
		}
		return nil, err
	}
	views := make([]View, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		view, _, err := Load(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].UpdatedAt.After(views[j].UpdatedAt) })
	if limit > 0 && len(views) > limit {
		views = views[:limit]
	}
	return views, nil
}

// PrintDetail prints one run as aligned human-readable fields.
func PrintDetail(view View) {
	fmt.Printf("run:        %s\n", view.RunID)
	fmt.Printf("plan:       %s\n", view.PlanID)
	fmt.Printf("job:        %s\n", view.JobID)
	fmt.Printf("stage:      %s\n", view.Stage)
	fmt.Printf("updated:    %s\n", view.UpdatedAt.Local().Format(time.RFC3339))
	if view.Requested != nil {
		fmt.Printf("requested:  background=%s entity_images=%v max_images=%d\n",
			view.Requested.BackgroundAssetID, view.Requested.EntityImages, view.Requested.MaxImageOverlays)
	}
	if view.Background != nil {
		fmt.Printf("background: asset=%s resolved=%v local=%s sha=%s\n",
			view.Background.AssetID, view.Background.Resolved, view.Background.LocalPath, short(view.Background.SHA256, 16))
		if view.Background.Error != "" {
			fmt.Printf("background_error: %s\n", view.Background.Error)
		}
	}
	if view.Plan != nil {
		fmt.Printf("plan_items: %d image=%d entity_image=%d by_kind=%v\n",
			view.Plan.Items, view.Plan.ImageItems, view.Plan.EntityImageItems, view.Plan.ByKind)
	}
	if view.Audit != nil {
		fmt.Printf("audit:      planned=%d by_kind=%v\n", view.Audit.PlannedOverlayCount, view.Audit.PlannedByKind)
		if view.Audit.EntityImages != nil {
			images := view.Audit.EntityImages
			fmt.Printf("entities:   selected=%d bound=%d missing=%d budget_used=%d/%d reasons=%v\n",
				images.Selected, images.Bound, images.Missing, images.BudgetUsed, images.Budget, images.Reasons)
		}
	}
	if view.Preflight != nil {
		fmt.Printf("preflight:  passed=%v error=%s\n", view.Preflight.Passed, view.Preflight.Error)
	}
	if view.Error != "" {
		fmt.Printf("error:      %s\n", view.Error)
	}
}

// PrintList prints the compact table of runs, newest first.
func PrintList(views []View) {
	fmt.Printf("%-20s %-18s %-34s %6s %9s %-10s %s\n", "UPDATED", "STAGE", "RUN", "PLAN", "ENTITIES", "BACKGROUND", "ERROR")
	for _, view := range views {
		fmt.Printf("%-20s %-18s %-34s %6s %9s %-10s %s\n",
			view.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
			short(view.Stage, 18), short(view.RunID, 34), planCount(view),
			entityCount(view), backgroundState(view), short(view.Error, 80))
	}
}

// Sanitize maps a run id onto the file-name component the recorder writes.
func Sanitize(value string) string { return safeName(value) }

func planCount(view View) string {
	if view.Plan == nil {
		return "-"
	}
	return fmt.Sprintf("%d", view.Plan.Items)
}

func entityCount(view View) string {
	if view.Audit == nil || view.Audit.EntityImages == nil {
		return "-"
	}
	return fmt.Sprintf("%d/%d", view.Audit.EntityImages.Bound, view.Audit.EntityImages.Selected)
}

func backgroundState(view View) string {
	if view.Background == nil {
		return "-"
	}
	if view.Background.Error != "" {
		return "error"
	}
	if view.Background.Resolved {
		return "ok"
	}
	return "pending"
}

func short(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max == 1 {
		return value[:1]
	}
	return value[:max-1] + "…"
}

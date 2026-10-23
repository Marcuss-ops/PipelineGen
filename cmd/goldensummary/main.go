// Command goldensummary projects one overlay-throughput golden run into the
// canonical per-item-class summary (schema pipelinegen.golden-overlay-summary.v1).
//
// It replaces the ad-hoc heredocs that produced ops/jobs/**/summary.json: the
// item classes come from the semantic overlay plan in job.full.json, the
// per-item render service comes from render_attempt_analytics (render_ms — the
// same field the historical summaries called per_item_render_service_ms), and
// the per-class statistics come from the pure projection in the rendermetrics
// package.
//
// Usage:
//
//	goldensummary --job-full ops/jobs/.../results/overlay-only-vN/job.full.json \
//	              --db data/media/media.db.sqlite [--out summary.json]
//
// The database is opened read-only; this tool never writes.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	rendermetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
)

const summarySchema = "pipelinegen.golden-overlay-summary.v1"

// envelope is the on-disk summary shape. The classic keys stay compatible with
// the historical ops summaries; per_class is the canonical addition.
type envelope struct {
	Schema               string                             `json:"schema"`
	JobID                string                             `json:"job_id"`
	RunID                string                             `json:"run_id,omitempty"`
	Status               string                             `json:"status"`
	OverlayPlanItems     int                                `json:"overlay_plan_items"`
	PlanKinds            map[string]int                     `json:"plan_kinds"`
	OverlayRenderItems   int                                `json:"overlay_render_items"`
	OverlayRenderStatus  map[string]int                     `json:"overlay_render_status"`
	OverlayLinksDrive    int                                `json:"overlay_links_drive,omitempty"`
	OverlayWindow        *windowBlock                       `json:"overlay_window,omitempty"`
	PerItemRenderService rendermetrics.ServiceStat          `json:"per_item_render_service_ms"`
	PerClass             map[string]rendermetrics.ClassStat `json:"per_class"`
	TotalServiceMS       int64                              `json:"total_service_ms"`
	UnknownClass         int                                `json:"unknown_class,omitempty"`
}

type windowBlock struct {
	From        string  `json:"from,omitempty"`
	To          string  `json:"to,omitempty"`
	Seconds     float64 `json:"seconds"`
	ItemsPerMin float64 `json:"items_per_min"`
}

// jobFull is the minimal job.full.json projection this tool needs.
type jobFull struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Job    struct {
		Result struct {
			RunID  string         `json:"run_id"`
			Result map[string]any `json:"result"`
		} `json:"result"`
	} `json:"job"`
	Result map[string]any `json:"result"`
}

func main() {
	jobFullPath := flag.String("job-full", "", "path to the run's job.full.json (required)")
	dbPath := flag.String("db", "data/media/media.db.sqlite", "render-attempt analytics SQLite database (opened read-only)")
	outPath := flag.String("out", "", "output path (default: stdout)")
	flag.Parse()
	if *jobFullPath == "" {
		fmt.Fprintln(os.Stderr, "goldensummary: --job-full is required")
		flag.Usage()
		os.Exit(2)
	}

	raw, err := os.ReadFile(*jobFullPath)
	if err != nil {
		fatalf("read job.full.json: %v", err)
	}
	var full jobFull
	if err := json.Unmarshal(raw, &full); err != nil {
		fatalf("parse job.full.json: %v", err)
	}

	result := nestedResult(full)
	planItems := overlayPlanItems(result)
	renderItems := overlayRenderItems(result)
	runID := itemRunID(result, full)

	samples, links, err := loadSamples(*dbPath, runID, planItems, renderItems)
	if err != nil {
		fatalf("load render attempts: %v", err)
	}

	summary := rendermetrics.BuildGoldenSummary(samples)
	out := envelope{
		Schema:               summarySchema,
		JobID:                full.ID,
		RunID:                runID,
		Status:               full.Status,
		OverlayPlanItems:     len(planItems),
		PlanKinds:            summary.ClassCounts,
		OverlayRenderItems:   len(renderItems),
		OverlayRenderStatus:  summary.StatusCounts,
		OverlayLinksDrive:    links,
		PerItemRenderService: summary.PerItemServiceMS,
		PerClass:             summary.PerClass,
		TotalServiceMS:       summary.TotalServiceMS,
		UnknownClass:         summary.UnknownClass,
	}
	if summary.WindowSeconds > 0 {
		from, to := windowBounds(samples)
		out.OverlayWindow = &windowBlock{
			From:        from,
			To:          to,
			Seconds:     summary.WindowSeconds,
			ItemsPerMin: summary.ItemsPerMin,
		}
	}

	encoded, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		fatalf("encode summary: %v", err)
	}
	encoded = append(encoded, '\n')
	if *outPath == "" {
		os.Stdout.Write(encoded)
		return
	}
	if err := os.WriteFile(*outPath, encoded, 0o644); err != nil {
		fatalf("write summary: %v", err)
	}
}

// nestedResult walks job.full.json to the semantic result payload. Both the
// API shape (`result.result`) and the stored-job shape (`job.result.result`)
// are accepted; the first non-empty wins.
func nestedResult(full jobFull) map[string]any {
	for _, candidate := range []map[string]any{full.Result, full.Job.Result.Result} {
		if inner, ok := candidate["result"].(map[string]any); ok && len(inner) > 0 {
			return inner
		}
		if len(candidate) > 0 {
			return candidate
		}
	}
	return map[string]any{}
}

func overlayPlanItems(result map[string]any) map[string]string {
	items := map[string]string{}
	plan, _ := result["overlay_plan"].(map[string]any)
	list, _ := plan["items"].([]any)
	for _, entry := range list {
		obj, _ := entry.(map[string]any)
		id, _ := obj["id"].(string)
		kind, _ := obj["kind"].(string)
		if id != "" {
			items[id] = kind
		}
	}
	return items
}

func overlayRenderItems(result map[string]any) map[string]string {
	items := map[string]string{}
	render, _ := result["overlay_render"].(map[string]any)
	list, _ := render["items"].([]any)
	for _, entry := range list {
		obj, _ := entry.(map[string]any)
		id, _ := obj["item_id"].(string)
		status, _ := obj["status"].(string)
		if id != "" {
			items[id] = status
		}
	}
	return items
}

// itemRunID recovers the render fan-out run identity the analytics job ids
// embed (`<run_id>:item:<nnn>:<item_id>`). The overlay_render job id carries
// it verbatim; the stored result run id is the fallback.
func itemRunID(result map[string]any, full jobFull) string {
	if render, ok := result["overlay_render"].(map[string]any); ok {
		if jobID, ok := render["job_id"].(string); ok && strings.Contains(jobID, ":item:") {
			return strings.SplitN(jobID, ":", 2)[0]
		}
	}
	if full.Job.Result.RunID != "" {
		return full.Job.Result.RunID
	}
	return ""
}

// loadSamples joins render_attempt_analytics rows onto the plan/render item
// identities. Rows are restricted to the fan-out run id when known so a rerun
// of the same immutable payload cannot bleed into this run's numbers.
func loadSamples(dbPath, runID string, planItems, renderItems map[string]string) ([]rendermetrics.ItemSample, int, error) {
	db, err := sql.Open("sqlite3", readOnlyDSN(dbPath))
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()

	query := `SELECT item_id, render_ms, encode_ms, completion_wait_ms,
	                 queue_queued_at, queue_completed_at
	          FROM render_attempt_analytics WHERE item_id <> ''`
	args := []any{}
	if runID != "" {
		query += ` AND job_id LIKE ?`
		args = append(args, "%"+runID+"%")
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	samples := []rendermetrics.ItemSample{}
	for rows.Next() {
		var itemID string
		var renderMS, encodeMS, waitMS int64
		var queuedAt, completedAt sql.NullString
		if err := rows.Scan(&itemID, &renderMS, &encodeMS, &waitMS, &queuedAt, &completedAt); err != nil {
			return nil, 0, err
		}
		samples = append(samples, rendermetrics.ItemSample{
			ItemID:    itemID,
			Class:     planItems[itemID],
			Status:    renderItems[itemID],
			RenderMS:  renderMS,
			EncodeMS:  encodeMS,
			QueuedMS:  waitMS,
			StartedAt: parseMillis(queuedAt),
			EndedAt:   parseMillis(completedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return samples, driveLinkCount(db, runID), nil
}

// driveLinkCount counts certified Drive links of this run's items — the
// historical summary's overlay_links_drive, kept honest (published links, not
// planned ones).
func driveLinkCount(db *sql.DB, runID string) int {
	query := `SELECT COUNT(*) FROM render_attempt_analytics
	          WHERE item_id <> '' AND drive_link <> ''`
	args := []any{}
	if runID != "" {
		query += ` AND job_id LIKE ?`
		args = append(args, "%"+runID+"%")
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0
	}
	return count
}

func windowBounds(samples []rendermetrics.ItemSample) (string, string) {
	var minStart, maxEnd int64
	for _, s := range samples {
		if s.StartedAt <= 0 || s.EndedAt <= 0 {
			continue
		}
		if minStart == 0 || s.StartedAt < minStart {
			minStart = s.StartedAt
		}
		if s.EndedAt > maxEnd {
			maxEnd = s.EndedAt
		}
	}
	if minStart == 0 || maxEnd == 0 {
		return "", ""
	}
	return time.UnixMilli(minStart).UTC().Format(time.RFC3339Nano),
		time.UnixMilli(maxEnd).UTC().Format(time.RFC3339Nano)
}

func parseMillis(value sql.NullString) int64 {
	if !value.Valid || value.String == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

func readOnlyDSN(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	return "file:" + path + "?mode=ro"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "goldensummary: "+format+"\n", args...)
	os.Exit(1)
}

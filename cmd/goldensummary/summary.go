package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/rendermetrics"
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
	CacheTiming          rendermetrics.CacheTimingSummary   `json:"cache_timing"`
	TotalServiceMS       int64                              `json:"total_service_ms"`
	UnknownClass         int                                `json:"unknown_class,omitempty"`
	OverlayRenderBatches int                                `json:"overlay_render_batches"`
	BatchedOverlayItems  int                                `json:"batched_overlay_items"`
	BatchRenderServiceMS int64                              `json:"batch_render_service_ms"`
	Batches              []batchBlock                       `json:"batches,omitempty"`
}

type batchBlock struct {
	BatchID            string   `json:"batch_id"`
	SceneID            string   `json:"scene_id,omitempty"`
	ItemIDs            []string `json:"item_ids"`
	WorkerItemCount    int      `json:"worker_item_count,omitempty"`
	WorkerCountKnown   bool     `json:"worker_item_count_known"`
	WorkerMarkedBatch  bool     `json:"worker_marked_batch"`
	WorkerCountMatches bool     `json:"worker_count_matches"`
	StartUS            int64    `json:"start_us"`
	EndUS              int64    `json:"end_us"`
	Status             string   `json:"status,omitempty"`
	RenderMS           int64    `json:"render_ms,omitempty"`
	EncodeMS           int64    `json:"encode_ms,omitempty"`
	QueueMS            int64    `json:"queue_wait_ms,omitempty"`
	CacheLookupUS      int64    `json:"cache_lookup_us,omitempty"`
	CacheProbeUS       int64    `json:"cache_probe_us,omitempty"`
	CacheStoreUS       int64    `json:"cache_store_us,omitempty"`
	CacheOutcome       string   `json:"cache_outcome,omitempty"`
	SHA256             string   `json:"sha256,omitempty"`
	DriveID            string   `json:"drive_file_id,omitempty"`
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

func countPlanKinds(items map[string]string) map[string]int {
	kinds := make(map[string]int)
	for _, kind := range items {
		if strings.TrimSpace(kind) == "" {
			kind = "unknown"
		}
		kinds[kind]++
	}
	return kinds
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
	defer func() { _ = db.Close() }()

	query := `SELECT item_id, render_ms, encode_ms, completion_wait_ms,
	                 cache_lookup_us, cache_probe_us, cache_store_us, cache_outcome,
	                 queue_queued_at, queue_completed_at
	          FROM render_attempt_analytics WHERE item_id <> '' AND item_id NOT LIKE 'batch:%'`
	args := []any{}
	if runID != "" {
		query += ` AND job_id LIKE ?`
		args = append(args, "%"+runID+"%")
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	samples := []rendermetrics.ItemSample{}
	for rows.Next() {
		var itemID, cacheOutcome string
		var renderMS, encodeMS, waitMS int64
		var cacheLookupUS, cacheProbeUS, cacheStoreUS int64
		var queuedAt, completedAt sql.NullString
		if err := rows.Scan(&itemID, &renderMS, &encodeMS, &waitMS, &cacheLookupUS, &cacheProbeUS, &cacheStoreUS, &cacheOutcome, &queuedAt, &completedAt); err != nil {
			return nil, 0, err
		}
		samples = append(samples, rendermetrics.ItemSample{
			ItemID:        itemID,
			Class:         planItems[itemID],
			Status:        renderItems[itemID],
			RenderMS:      renderMS,
			EncodeMS:      encodeMS,
			QueuedMS:      waitMS,
			CacheLookupUS: cacheLookupUS,
			CacheProbeUS:  cacheProbeUS,
			CacheStoreUS:  cacheStoreUS,
			CacheOutcome:  cacheOutcome,
			StartedAt:     parseMillis(queuedAt),
			EndedAt:       parseMillis(completedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return samples, driveLinkCount(db, runID), nil
}

type batchAttemptSample struct {
	Attempt rendermetrics.ItemSample
	BatchID string
	ItemIDs []string
	StartUS int64
	EndUS   int64
}

func loadBatchSamples(dbPath, runID string) ([]batchAttemptSample, error) {
	db, err := sql.Open("sqlite3", readOnlyDSN(dbPath))
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	query := `SELECT item_id, batch_id, batch_item_ids_json, batch_start_us, batch_end_us,
	                 render_ms, encode_ms, completion_wait_ms,
	                 cache_lookup_us, cache_probe_us, cache_store_us, cache_outcome,
	                 queue_queued_at, queue_completed_at
	          FROM render_attempt_analytics WHERE item_id LIKE 'batch:%'`
	args := []any{}
	if runID != "" {
		query += ` AND job_id LIKE ?`
		args = append(args, "%"+runID+"%")
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var samples []batchAttemptSample
	for rows.Next() {
		var itemID, batchID, itemIDsJSON, cacheOutcome string
		var startUS, endUS int64
		var renderMS, encodeMS, waitMS int64
		var cacheLookupUS, cacheProbeUS, cacheStoreUS int64
		var queuedAt, completedAt sql.NullString
		if err := rows.Scan(&itemID, &batchID, &itemIDsJSON, &startUS, &endUS, &renderMS, &encodeMS, &waitMS, &cacheLookupUS, &cacheProbeUS, &cacheStoreUS, &cacheOutcome, &queuedAt, &completedAt); err != nil {
			return nil, err
		}
		if batchID == "" {
			batchID = strings.TrimPrefix(itemID, "batch:")
		}
		var itemIDs []string
		if err := json.Unmarshal([]byte(itemIDsJSON), &itemIDs); err != nil {
			return nil, fmt.Errorf("decode batch item ids for %s: %w", batchID, err)
		}
		samples = append(samples, batchAttemptSample{
			BatchID: batchID, ItemIDs: itemIDs, StartUS: startUS, EndUS: endUS,
			Attempt: rendermetrics.ItemSample{
				ItemID: itemID, Class: "batch", Status: "COMPLETED",
				RenderMS: renderMS, EncodeMS: encodeMS, QueuedMS: waitMS,
				CacheLookupUS: cacheLookupUS, CacheProbeUS: cacheProbeUS,
				CacheStoreUS: cacheStoreUS, CacheOutcome: cacheOutcome,
				StartedAt: parseMillis(queuedAt), EndedAt: parseMillis(completedAt),
			},
		})
	}
	return samples, rows.Err()
}

func overlayRenderBatches(result map[string]any, samples []batchAttemptSample) []batchBlock {
	render, _ := result["overlay_render"].(map[string]any)
	list, _ := render["batches"].([]any)
	sampleByID := make(map[string]batchAttemptSample, len(samples))
	for _, sample := range samples {
		sampleByID[sample.BatchID] = sample
	}
	batches := make([]batchBlock, 0, len(list))
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		batch := batchBlock{}
		batch.BatchID, _ = obj["batch_id"].(string)
		batch.SceneID, _ = obj["scene_id"].(string)
		batch.StartUS = numericInt64(obj["start_us"])
		batch.EndUS = numericInt64(obj["end_us"])
		batch.Status, _ = obj["status"].(string)
		if ids, ok := obj["item_ids"].([]any); ok {
			for _, id := range ids {
				if itemID, ok := id.(string); ok && itemID != "" {
					batch.ItemIDs = append(batch.ItemIDs, itemID)
				}
			}
		}
		if artifact, ok := obj["artifact"].(map[string]any); ok {
			batch.RenderMS = numericInt64(artifact["render_ms"])
			batch.EncodeMS = numericInt64(artifact["encode_ms"])
			batch.SHA256, _ = artifact["sha256"].(string)
			batch.DriveID, _ = artifact["drive_file_id"].(string)
			if metrics, ok := artifact["metrics"].(map[string]any); ok {
				if count, present := metrics["overlay_item_count"]; present {
					batch.WorkerItemCount = int(numericInt64(count))
					batch.WorkerCountKnown = true
					batch.WorkerCountMatches = batch.WorkerItemCount == len(batch.ItemIDs)
				}
				batch.WorkerMarkedBatch = numericInt64(metrics["overlay_render_batch"]) == 1
			}
		}
		if sample, ok := sampleByID[batch.BatchID]; ok {
			batch.RenderMS, batch.EncodeMS, batch.QueueMS = sample.Attempt.RenderMS, sample.Attempt.EncodeMS, sample.Attempt.QueuedMS
			batch.CacheLookupUS = sample.Attempt.CacheLookupUS
			batch.CacheProbeUS = sample.Attempt.CacheProbeUS
			batch.CacheStoreUS = sample.Attempt.CacheStoreUS
			batch.CacheOutcome = sample.Attempt.CacheOutcome
			if len(batch.ItemIDs) == 0 {
				batch.ItemIDs = append([]string(nil), sample.ItemIDs...)
			}
			if batch.StartUS == 0 {
				batch.StartUS = sample.StartUS
			}
			if batch.EndUS == 0 {
				batch.EndUS = sample.EndUS
			}
		}
		batches = append(batches, batch)
	}
	return batches
}

func numericInt64(value any) int64 {
	switch n := value.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func completedOverlayItemCount(statuses map[string]string) int {
	count := 0
	for _, status := range statuses {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "completed", "succeeded", "success":
			count++
		}
	}
	return count
}

func sampleWindowSeconds(samples []rendermetrics.ItemSample) float64 {
	var start, end int64
	for _, sample := range samples {
		if sample.StartedAt <= 0 || sample.EndedAt <= sample.StartedAt {
			continue
		}
		if start == 0 || sample.StartedAt < start {
			start = sample.StartedAt
		}
		if sample.EndedAt > end {
			end = sample.EndedAt
		}
	}
	if start == 0 || end <= start {
		return 0
	}
	return float64(end-start) / 1000
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

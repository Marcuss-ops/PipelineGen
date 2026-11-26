// Package editorialgate is the automatic pre-render editorial QA gate
// (ITCLOSE-05 §4.8, AUGRENDER-04/05).
//
// The defects it covers were all measured on a real plan but only discovered
// after the GPU render: 14.64% overlay coverage with a 59.64 s hole, ambiguous
// entity cards, a 125 ms numeric card, categories enabled in the payload that
// produced zero items, and imperative stage directions that reached the TTS.
// The gate runs on the SEALED plan, before any render work, and reports each
// criterion as a pass/fail value. Warn publishes the report and lets the run
// continue; strict fails closed with ErrEditorialQualityGate.
package editorialgate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Gate policies. Warn is the compatibility default: the run keeps rendering and
// the persisted report explains what would fail. Strict refuses the render.
const (
	PolicyWarn   = "warn"
	PolicyStrict = "strict"
)

// SchemaVersion is the wire version of the persisted report.
const SchemaVersion = "editorial-gate.v1"

// Criterion statuses.
const (
	StatusPass = "pass"
	StatusFail = "fail"
)

// Editorial thresholds. They are the ITCLOSE-05 §4.8 contract, not tunables of
// convenience: a plan that misses one of them is not publishable.
const (
	DefaultMaxGapMS           int64   = 25_000
	DefaultMinCoveragePercent float64 = 25.0
	DefaultMinCardDurationMS  int64   = 600
)

// ErrEditorialQualityGate marks a strict-policy refusal. It is typed so a
// durable runner can classify the failure without parsing the message.
var ErrEditorialQualityGate = errors.New("editorial quality gate failed")

// Config is everything the gate needs, all of it already certified upstream.
type Config struct {
	// Policy is PolicyWarn (default) or PolicyStrict.
	Policy string
	// DurationMS is the plan extent. Zero derives it from the items.
	DurationMS int64
	Items      []capmodel.OverlayItem
	// EnabledCategories is the run-level overlay toggle map. Only entries set
	// to "enabled" are checked for silent zero-item output.
	EnabledCategories map[string]string
	// SourceTexts are the authored narration surfaces. Each one is scanned for
	// imperative editorial directives.
	SourceTexts []string
	// MediaContract is the plan's declared contract ID. Empty means the
	// renderer default (alpha-bearing overlay-v1).
	MediaContract string
	// MaxGapMS, MinCoveragePercent and MinCardDurationMS override the
	// editorial defaults when positive.
	MaxGapMS           int64
	MinCoveragePercent float64
	MinCardDurationMS  int64
}

// Criterion is one measured rule.
type Criterion struct {
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Detail    string  `json:"detail,omitempty"`
}

// Report is the persisted gate verdict. It is additive by contract: a consumer
// that only knows status/violations reads the same document.
type Report struct {
	SchemaVersion   string      `json:"schema_version"`
	Policy          string      `json:"policy"`
	Status          string      `json:"status"`
	CoveragePercent float64     `json:"coverage_percent"`
	MaxGapMS        int64       `json:"max_gap_ms"`
	OverlapCount    int         `json:"overlap_count"`
	Criteria        []Criterion `json:"criteria"`
	Violations      []string    `json:"violations,omitempty"`
}

// OK reports whether every criterion passed.
func (r Report) OK() bool { return r.Status == StatusPass }

// Enforce returns ErrEditorialQualityGate when the report has violations and
// the policy is strict. A warn report is always enforced as a no-op: the report
// itself is the warning.
func (r Report) Enforce() error {
	if r.OK() || r.Policy != PolicyStrict {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrEditorialQualityGate, strings.Join(r.Violations, "; "))
}

func (cfg Config) normalized() Config {
	policy := strings.ToLower(strings.TrimSpace(cfg.Policy))
	if policy != PolicyStrict {
		policy = PolicyWarn
	}
	cfg.Policy = policy
	if cfg.MaxGapMS <= 0 {
		cfg.MaxGapMS = DefaultMaxGapMS
	}
	if cfg.MinCoveragePercent <= 0 {
		cfg.MinCoveragePercent = DefaultMinCoveragePercent
	}
	if cfg.MinCardDurationMS <= 0 {
		cfg.MinCardDurationMS = DefaultMinCardDurationMS
	}
	return cfg
}

// Evaluate measures the sealed plan against the editorial contract.
func Evaluate(cfg Config) Report {
	cfg = cfg.normalized()
	report := Report{SchemaVersion: SchemaVersion, Policy: cfg.Policy, Status: StatusPass}

	intervals := coverageIntervals(cfg.Items)
	planEndMS := cfg.DurationMS
	if planEndMS <= 0 {
		for _, interval := range intervals {
			if interval[1] > planEndMS {
				planEndMS = interval[1]
			}
		}
	}
	coveredMS := int64(0)
	for _, interval := range intervals {
		end := interval[1]
		if planEndMS > 0 && end > planEndMS {
			end = planEndMS
		}
		if end > interval[0] {
			coveredMS += end - interval[0]
		}
	}
	coveragePercent := 0.0
	if planEndMS > 0 {
		coveragePercent = float64(coveredMS) / float64(planEndMS) * 100
	}
	report.CoveragePercent = round2(coveragePercent)
	report.Criteria = append(report.Criteria, criterion(
		"coverage",
		coveragePercent,
		cfg.MinCoveragePercent,
		coveragePercent+1e-9 >= cfg.MinCoveragePercent,
		fmt.Sprintf("covered %d ms of %d ms", coveredMS, planEndMS),
	))

	maxGapMS := largestGapMS(intervals, planEndMS)
	report.MaxGapMS = maxGapMS
	report.Criteria = append(report.Criteria, criterion(
		"max_gap",
		float64(maxGapMS),
		float64(cfg.MaxGapMS),
		maxGapMS <= cfg.MaxGapMS,
		"",
	))

	overlaps := overlappingTextCards(cfg.Items)
	report.OverlapCount = len(overlaps)
	report.Criteria = append(report.Criteria, criterion(
		"overlap",
		float64(len(overlaps)),
		0,
		len(overlaps) == 0,
		strings.Join(overlaps, ", "),
	))

	shortCards := unreadableCards(cfg.Items, cfg.MinCardDurationMS)
	report.Criteria = append(report.Criteria, criterion(
		"card_duration",
		float64(shortestCardMS(cfg.Items, cfg.MinCardDurationMS)),
		float64(cfg.MinCardDurationMS),
		len(shortCards) == 0,
		strings.Join(shortCards, ", "),
	))

	silentCategories := emptyEnabledCategories(cfg.Items, cfg.EnabledCategories)
	report.Criteria = append(report.Criteria, criterion(
		"category_population",
		0,
		0,
		len(silentCategories) == 0,
		strings.Join(silentCategories, ", "),
	))

	transparency := unsupportedTransparency(cfg.Items, cfg.MediaContract)
	report.Criteria = append(report.Criteria, criterion(
		"alpha_contract",
		0,
		0,
		len(transparency) == 0,
		strings.Join(transparency, ", "),
	))

	directives := sourceDirectives(cfg.SourceTexts)
	report.Criteria = append(report.Criteria, criterion(
		"source_directives",
		0,
		0,
		len(directives) == 0,
		strings.Join(directives, ", "),
	))

	for _, entry := range report.Criteria {
		if entry.Status != StatusFail {
			continue
		}
		detail := entry.Detail
		if detail == "" {
			detail = fmt.Sprintf("value %.2f, threshold %.2f", entry.Value, entry.Threshold)
		}
		report.Violations = append(report.Violations, entry.Name+": "+detail)
		report.Status = StatusFail
	}
	return report
}

func criterion(name string, value, threshold float64, ok bool, detail string) Criterion {
	status := StatusPass
	if !ok {
		status = StatusFail
	}
	return Criterion{Name: name, Status: status, Value: round2(value), Threshold: threshold, Detail: detail}
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}

// coverageKind mirrors the planner's coverage owner: decorative light leaks and
// full-canvas backgrounds are not editorial cards and cannot mask a desert.
func coverageKind(item capmodel.OverlayItem) bool {
	if strings.EqualFold(strings.TrimSpace(item.Kind), "light_leak") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.TemplateID)) {
	case "background", "video_background":
		return false
	}
	return true
}

func itemWindowMS(item capmodel.OverlayItem) (int64, int64) {
	start, end := item.StartMs, item.EndMs
	if item.DurationUS > 0 {
		start = item.StartUS / 1000
		end = (item.StartUS + item.DurationUS + 999) / 1000
	}
	if end < start {
		end = start
	}
	return start, end
}

func coverageIntervals(items []capmodel.OverlayItem) [][2]int64 {
	intervals := make([][2]int64, 0, len(items))
	for _, item := range items {
		if !coverageKind(item) {
			continue
		}
		start, end := itemWindowMS(item)
		if end <= start {
			continue
		}
		intervals = append(intervals, [2]int64{start, end})
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i][0] != intervals[j][0] {
			return intervals[i][0] < intervals[j][0]
		}
		return intervals[i][1] < intervals[j][1]
	})
	merged := intervals[:0]
	for _, interval := range intervals {
		if len(merged) > 0 && interval[0] <= merged[len(merged)-1][1] {
			if interval[1] > merged[len(merged)-1][1] {
				merged[len(merged)-1][1] = interval[1]
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

func largestGapMS(intervals [][2]int64, planEndMS int64) int64 {
	var largest int64
	cursor := int64(0)
	for _, interval := range intervals {
		if interval[0] > cursor && interval[0]-cursor > largest {
			largest = interval[0] - cursor
		}
		if interval[1] > cursor {
			cursor = interval[1]
		}
	}
	if planEndMS > cursor && planEndMS-cursor > largest {
		largest = planEndMS - cursor
	}
	return largest
}

// textSurface is true for the card kinds that share the centered text plane.
// Images are a background layer and may legally sit under a phrase.
func textSurface(item capmodel.OverlayItem) bool {
	switch strings.ToLower(strings.TrimSpace(item.Kind)) {
	case "text_phrase", "quote", "number", "entity_card", "organization", "location", "concept", "lower_third":
		return true
	}
	return false
}

func overlappingTextCards(items []capmodel.OverlayItem) []string {
	var violations []string
	for i := range items {
		if !textSurface(items[i]) {
			continue
		}
		start, end := itemWindowMS(items[i])
		if end <= start {
			continue
		}
		for j := i + 1; j < len(items); j++ {
			if !textSurface(items[j]) {
				continue
			}
			if items[i].SceneID != "" && items[j].SceneID != "" && items[i].SceneID != items[j].SceneID {
				continue
			}
			otherStart, otherEnd := itemWindowMS(items[j])
			if otherEnd <= otherStart {
				continue
			}
			if start < otherEnd && otherStart < end {
				violations = append(violations, fmt.Sprintf("%s/%s [%d,%d)", items[i].ID, items[j].ID, start, end))
			}
		}
	}
	return violations
}

// shortestCardMS reports the smallest centered-text hold in the plan, so the
// criterion value is a measurement rather than a restatement of its threshold.
func shortestCardMS(items []capmodel.OverlayItem, fallback int64) int64 {
	shortest := int64(0)
	for _, item := range items {
		if !textSurface(item) {
			continue
		}
		start, end := itemWindowMS(item)
		if end <= start {
			continue
		}
		if duration := end - start; shortest == 0 || duration < shortest {
			shortest = duration
		}
	}
	if shortest == 0 {
		return fallback
	}
	return shortest
}

func unreadableCards(items []capmodel.OverlayItem, minMS int64) []string {
	var violations []string
	for _, item := range items {
		if !textSurface(item) {
			continue
		}
		start, end := itemWindowMS(item)
		if end-start >= minMS {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s (%d ms)", item.ID, end-start))
	}
	return violations
}

// CategoryFor maps an item onto the run-level overlay category that owns it.
// It mirrors the planner's group/category mapping so a zero-item category is
// measured on the same vocabulary the payload used.
func CategoryFor(item capmodel.OverlayItem) string {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	role := ""
	if item.Params != nil {
		if value, ok := item.Params["editorial_role"].(string); ok {
			role = value
		}
	}
	switch kind {
	case "text_phrase":
		switch role {
		case "scene_title":
			return "titles"
		case "scene_bullet", "chapter_bullet":
			return "bullets"
		}
		return "phrases"
	case "image", "entity_image", "product", "logo":
		return "images"
	case "map":
		return "maps"
	case "number":
		return "numbers"
	case "quote":
		return "quotes"
	case "brand_text":
		return "brand_text"
	case "entity_card", "organization", "location", "concept":
		return "entities"
	}
	return "phrases"
}

func emptyEnabledCategories(items []capmodel.OverlayItem, enabled map[string]string) []string {
	if len(enabled) == 0 {
		return nil
	}
	counts := make(map[string]int, len(enabled))
	for _, item := range items {
		counts[CategoryFor(item)]++
	}
	names := make([]string, 0, len(enabled))
	for name := range enabled {
		names = append(names, name)
	}
	sort.Strings(names)
	var silent []string
	for _, name := range names {
		if strings.ToLower(strings.TrimSpace(enabled[name])) != "enabled" {
			continue
		}
		if counts[name] == 0 {
			silent = append(silent, name)
		}
	}
	return silent
}

// unsupportedTransparency finds items that ask the compositor for a real alpha
// channel while the plan declares a contract that has none. r80 forced
// transparency on `overlay-v1-noalpha` and the opaque windows turned into black
// artifacts. The structural rule is simple: alpha is a property of the
// contract, never of an individual item.
func unsupportedTransparency(items []capmodel.OverlayItem, contractID string) []string {
	supported, err := capmodel.SupportsTransparency(contractID)
	if err == nil && supported {
		return nil
	}
	var violations []string
	for _, item := range items {
		if !requestsTransparency(item) {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s (contract %q has no alpha)", item.ID, strings.TrimSpace(contractID)))
	}
	return violations
}

func requestsTransparency(item capmodel.OverlayItem) bool {
	if truthy(item.Params, "transparent") || truthy(item.Params, "alpha") {
		return true
	}
	if item.Params != nil {
		if value, ok := item.Params["background"].(string); ok && strings.EqualFold(strings.TrimSpace(value), "transparent") {
			return true
		}
	}
	return truthy(item.Style, "transparent") || truthy(item.Style, "alpha")
}

func truthy(object map[string]any, key string) bool {
	if object == nil {
		return false
	}
	value, ok := object[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	case float64:
		return typed != 0
	}
	return false
}

func sourceDirectives(texts []string) []string {
	var violations []string
	for index, text := range texts {
		for _, directive := range scriptpkg.FindEditorialDirectives(text) {
			violations = append(violations, fmt.Sprintf("source_text[%d]: %q", index, clamp(directive.Phrase, 96)))
		}
	}
	return violations
}

func clamp(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

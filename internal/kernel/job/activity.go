package job

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

type activitySequenceContextKey struct{}

type activitySequence struct {
	value atomic.Uint64
	mu    sync.RWMutex
	trace ActivityTrace
}

// WithActivityTrace starts a monotonic sequence and binds the known execution
// identity to the context. Each job attempt should bind its own context.
func WithActivityTrace(ctx context.Context, trace ActivityTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, activitySequenceContextKey{}, &activitySequence{trace: trace})
}

// WithActivitySequence starts a monotonic sequence without an initial identity.
func WithActivitySequence(ctx context.Context) context.Context {
	return WithActivityTrace(ctx, ActivityTrace{})
}

// WithActivityTraceIfAbsent preserves the sequence already attached upstream.
func WithActivityTraceIfAbsent(ctx context.Context, trace ActivityTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if seq, ok := ctx.Value(activitySequenceContextKey{}).(*activitySequence); ok && seq != nil {
		seq.mu.Lock()
		seq.trace = mergeActivityTrace(seq.trace, trace)
		seq.mu.Unlock()
		return ctx
	}
	return WithActivityTrace(ctx, trace)
}

// WithActivityTraceFrom carries the same counter and identity into a detached
// persistence context, such as terminal finalization.
func WithActivityTraceFrom(ctx, source context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if source != nil {
		if seq, ok := source.Value(activitySequenceContextKey{}).(*activitySequence); ok && seq != nil {
			return context.WithValue(ctx, activitySequenceContextKey{}, seq)
		}
	}
	return ctx
}

// ActivityTraceFromContext returns the identity and allocates the next sequence.
func ActivityTraceFromContext(ctx context.Context) ActivityTrace {
	if ctx == nil {
		return ActivityTrace{}
	}
	seq, ok := ctx.Value(activitySequenceContextKey{}).(*activitySequence)
	if !ok || seq == nil {
		return ActivityTrace{}
	}
	seq.mu.RLock()
	trace := seq.trace
	seq.mu.RUnlock()
	trace.Sequence = seq.value.Add(1)
	return trace
}

// ActivityTraceFromData reuses a producer's trace when an event crosses a
// worker boundary; otherwise it allocates a sequence from ctx.
func ActivityTraceFromData(data map[string]any, ctx context.Context) ActivityTrace {
	trace := traceFromMap(data)
	if nested, ok := data["trace"].(map[string]any); ok {
		if nestedTrace := traceFromMap(nested); hasActivityTrace(nestedTrace) {
			trace = mergeActivityTrace(trace, nestedTrace)
		}
	}
	if ctx == nil {
		return trace
	}
	seq, ok := ctx.Value(activitySequenceContextKey{}).(*activitySequence)
	if !ok || seq == nil {
		return trace
	}
	seq.mu.RLock()
	fallback := seq.trace
	seq.mu.RUnlock()
	if trace.Sequence == 0 {
		fallback.Sequence = seq.value.Add(1)
	} else {
		advanceActivitySequence(seq, trace.Sequence)
	}
	return mergeActivityTrace(trace, fallback)
}

func advanceActivitySequence(seq *activitySequence, sequence uint64) {
	for current := seq.value.Load(); current < sequence; current = seq.value.Load() {
		if seq.value.CompareAndSwap(current, sequence) {
			return
		}
	}
}

func hasActivityTrace(trace ActivityTrace) bool {
	return trace.RunID != "" || trace.AttemptID != "" || trace.ParentRunID != "" || trace.CorrelationID != "" || trace.Sequence > 0
}

func mergeActivityTrace(preferred, fallback ActivityTrace) ActivityTrace {
	if preferred.RunID == "" {
		preferred.RunID = fallback.RunID
	}
	if preferred.AttemptID == "" {
		preferred.AttemptID = fallback.AttemptID
	}
	if preferred.ParentRunID == "" {
		preferred.ParentRunID = fallback.ParentRunID
	}
	if preferred.CorrelationID == "" {
		preferred.CorrelationID = fallback.CorrelationID
	}
	if preferred.Sequence == 0 {
		preferred.Sequence = fallback.Sequence
	}
	return preferred
}

func traceFromMap(data map[string]any) ActivityTrace {
	return ActivityTrace{
		RunID: stringValue(data["run_id"]), AttemptID: stringValue(data["attempt_id"]),
		ParentRunID: stringValue(data["parent_run_id"]), CorrelationID: stringValue(data["correlation_id"]),
		Sequence: positiveUint(data["sequence"]),
	}
}

func positiveUint(value any) uint64 {
	switch number := value.(type) {
	case json.Number:
		parsed, _ := strconv.ParseUint(string(number), 10, 64)
		return parsed
	case uint64:
		return number
	case uint:
		return uint64(number)
	case int:
		if number > 0 {
			return uint64(number)
		}
	case int64:
		if number > 0 {
			return uint64(number)
		}
	case float64:
		if number > 0 {
			return uint64(number)
		}
	}
	return 0
}

// ActivityTrace identifies one action within a reproducible execution attempt.
// The job timeline row itself carries JobID and timestamp.
type ActivityTrace struct {
	RunID         string `json:"run_id,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	ParentRunID   string `json:"parent_run_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Sequence      uint64 `json:"sequence,omitempty"`
}

// ActivityData returns the canonical job-timeline activity envelope.
func ActivityData(kind, subKind, status, detail string, payload map[string]any) map[string]any {
	return ActivityDataWithTrace(kind, subKind, status, detail, payload, ActivityTrace{})
}

// ActivityDataWithTrace normalizes legacy payloads and already-enveloped data.
// Existing sub_kind remains the compatibility alias for micro_kind.
func ActivityDataWithTrace(kind, subKind, status, detail string, payload map[string]any, trace ActivityTrace) map[string]any {
	out := cloneActivityMap(payload)
	trace = mergeActivityTrace(trace, ActivityTraceFromData(out, nil))
	if hasActivityTrace(trace) {
		trace = mergeActivityTrace(trace, traceFromMap(out))
	}
	_, hasKind := out["kind"]
	_, hasSubKind := out["sub_kind"]
	alreadyEnveloped := hasKind && hasSubKind && stringValue(out["kind"]) != "" && stringValue(out["sub_kind"]) != ""

	if stringValue(out["kind"]) == "" {
		out["kind"] = kind
	}
	if stringValue(out["sub_kind"]) == "" {
		if microKind := stringValue(out["micro_kind"]); microKind != "" {
			subKind = microKind
		}
		out["sub_kind"] = subKind
	}
	if stringValue(out["status"]) == "" {
		out["status"] = normalizeActivityStatus(status)
	} else {
		out["status"] = normalizeActivityStatus(stringValue(out["status"]))
	}
	if stringValue(out["detail"]) == "" {
		out["detail"] = detail
	}
	if nested, ok := out["payload"].(map[string]any); ok {
		out["payload"] = cloneActivityMap(nested)
	} else if alreadyEnveloped {
		out["payload"] = activityPayloadFields(payload)
	} else {
		out["payload"] = cloneActivityMap(payload)
	}
	microKind := stringValue(out["micro_kind"])
	if microKind == "" {
		microKind = stringValue(out["sub_kind"])
	}
	out["micro_kind"] = microKind

	if trace.RunID != "" {
		out["run_id"] = trace.RunID
	}
	if trace.AttemptID != "" {
		out["attempt_id"] = trace.AttemptID
	}
	if trace.ParentRunID != "" {
		out["parent_run_id"] = trace.ParentRunID
	}
	if trace.CorrelationID != "" {
		out["correlation_id"] = trace.CorrelationID
	}
	if trace.Sequence > 0 {
		out["sequence"] = trace.Sequence
	}
	if trace.RunID != "" || trace.AttemptID != "" || trace.ParentRunID != "" || trace.CorrelationID != "" || trace.Sequence > 0 {
		out["trace"] = map[string]any{
			"run_id": trace.RunID, "attempt_id": trace.AttemptID,
			"parent_run_id": trace.ParentRunID, "correlation_id": trace.CorrelationID,
			"sequence": trace.Sequence,
		}
	}
	return out
}

func activityPayloadFields(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		switch key {
		case "kind", "sub_kind", "micro_kind", "status", "detail", "payload",
			"trace", "run_id", "attempt_id", "parent_run_id", "correlation_id", "sequence":
			continue
		}
		out[key] = value
	}
	return out
}

func cloneActivityMap(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		out[key] = value
	}
	return out
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

// ProgressActivityDataWithTrace adapts a legacy percentage/message update to
// the canonical activity envelope.
func ProgressActivityDataWithTrace(kind string, progress int, message string, trace ActivityTrace) map[string]any {
	status := "running"
	lower := strings.ToLower(message)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "error") {
		status = "failed"
	} else if progress >= 100 {
		status = "completed"
	}
	subKind := progressSubKind(message)
	payload := map[string]any{"progress": progress, "message": message}
	return ActivityDataWithTrace(kind, subKind, status, message, payload, trace)
}

// ProgressActivityData adapts a legacy update without an attempt trace.
func ProgressActivityData(kind string, progress int, message string) map[string]any {
	return ProgressActivityDataWithTrace(kind, progress, message, ActivityTrace{})
}

// ActivityStatus infers a stable lifecycle status for typed timeline events.
func ActivityStatus(eventType string, payload map[string]any) string {
	if status := stringValue(payload["status"]); status != "" {
		return normalizeActivityStatus(status)
	}
	eventType = strings.ToLower(eventType)
	switch eventType {
	case "queued", "job_queued":
		return "queued"
	case "leased", "job_running", "started":
		return "running"
	case "job_completed", "job.aggregate_completed", "job.completed":
		return "completed"
	case "job_failed", "job.aggregate_failed":
		return "failed"
	case "job_retry_wait":
		return "retry_wait"
	case "job_cancelled":
		return "cancelled"
	case "job_deferred":
		return "deferred"
	}
	switch {
	case strings.HasSuffix(eventType, ".failed"), eventType == "stage.failed":
		return "failed"
	case strings.HasSuffix(eventType, ".completed"), strings.HasSuffix(eventType, ".created"),
		strings.HasSuffix(eventType, ".validated"), strings.HasSuffix(eventType, ".planned"),
		strings.HasSuffix(eventType, ".generated"), strings.HasSuffix(eventType, ".bound"),
		strings.HasSuffix(eventType, ".checked"), strings.HasSuffix(eventType, ".hydrated"):
		return "completed"
	default:
		return "running"
	}
}

func normalizeActivityStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "succeeded", "success", "complete":
		return "completed"
	case "failure":
		return "failed"
	case "canceled":
		return "cancelled"
	case "in_progress", "in-progress", "leased", "finalizing", "started":
		return "running"
	default:
		return status
	}
}

// progressLexiconStopWords is the linguistic stop-word set installed by the
// composition root. internal/kernel must not import internal/capabilities
// (percheck_kernel_boundary), so the LexiconRegistry data reaches this filter
// by injection at bootstrap — see wiring/script.InitLinguistics — rather than
// being mirrored as a hardcoded map here (godlike/06 SSOT).
var progressLexiconStopWords atomic.Pointer[map[string]struct{}]

// SetProgressStopWords installs the lexicon stop-word set once, at bootstrap.
// The map is owned by the linguistics registry and treated as read-only here.
func SetProgressStopWords(words map[string]struct{}) {
	progressLexiconStopWords.Store(&words)
}

// progressStatusNoise is the JOB vocabulary stripped from a progress message:
// the lifecycle words a status message already carries in its own kind/status
// fields. This is job-domain vocabulary owned by this package, not linguistic
// data — the stop-word lexicon itself lives in config/lexicons/** behind the
// registry and is injected through SetProgressStopWords.
var progressStatusNoise = map[string]struct{}{
	"completed": {}, "completion": {}, "failed": {},
	"generating": {}, "generation": {}, "running": {},
	"starting": {}, "start": {}, "updating": {},
}

// progressNoiseWord reports whether word must not contribute to a sub-kind.
func progressNoiseWord(word string) bool {
	if _, ok := progressStatusNoise[word]; ok {
		return true
	}
	if stop := progressLexiconStopWords.Load(); stop != nil {
		_, ok := (*stop)[word]
		return ok
	}
	return false
}

func progressSubKind(message string) string {
	value := strings.TrimSpace(message)
	if value == "" {
		return "progress"
	}
	if strings.HasPrefix(value, "[") {
		if end := strings.IndexByte(value, ']'); end >= 0 {
			value = strings.TrimSpace(value[end+1:])
		}
	}
	if colon := strings.IndexByte(value, ':'); colon > 0 && colon < 32 {
		prefix := strings.TrimSpace(value[:colon])
		if strings.EqualFold(prefix, strings.ToUpper(prefix)) {
			value = strings.TrimSpace(value[colon+1:])
		}
	}
	words := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	selected := make([]string, 0, 2)
	for _, word := range words {
		word = strings.ToLower(word)
		if progressNoiseWord(word) {
			continue
		}
		selected = append(selected, word)
		if len(selected) == 2 {
			break
		}
	}
	if len(selected) == 0 {
		return "progress"
	}
	return strings.Join(selected, "_")
}

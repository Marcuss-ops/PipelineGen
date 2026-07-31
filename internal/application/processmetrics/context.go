package processmetrics

import "context"

type contextKey uint8

const (
	jobIDKey contextKey = iota
	parentJobIDKey
)

// WithRun annotates a context with the identifiers shared by all phases in a run.
func WithRun(ctx context.Context, jobID, parentJobID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, jobIDKey, jobID)
	return context.WithValue(ctx, parentJobIDKey, parentJobID)
}

// RunIDs returns the identifiers previously attached with WithRun.
func RunIDs(ctx context.Context) (jobID, parentJobID string) {
	if ctx == nil {
		return "", ""
	}
	jobID, _ = ctx.Value(jobIDKey).(string)
	parentJobID, _ = ctx.Value(parentJobIDKey).(string)
	return jobID, parentJobID
}

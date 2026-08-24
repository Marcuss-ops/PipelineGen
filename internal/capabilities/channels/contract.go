// Package channels — contract.go:
//
// Capability Standard contract.go holds the typed commands (what
// callers ask for), queries (read-only asks), results (what callers
// receive), and public ports (the narrow Repository surface the
// service consumes).
//
// Handlers translate JSON requests to commands; Service translates
// commands to the domain model and delegates persistence to the
// Repository port implemented by the SQLite adapter in adapters.go.
package channels

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Repository is the typed persistence port consumed by Service.
// Mirrors exactly what the capability persists today; new operations
// must be added here first and then implemented in the SQLite
// adapter (or any swap-in transport).
//
// Future operations that callers may want to expose (e.g.
// DeleteByCategory, CountByCategory) start here, not at the concrete
// repository, per the Capability Standard rule that ports must be
// consumer-owned first.
type Repository interface {
	// ListAll returns every category-channel association in the store.
	ListAll(ctx context.Context) ([]*asset.CategoryChannel, error)
	// ListCategories returns the distinct set of categories that have
	// at least one channel assigned.
	ListCategories(ctx context.Context) ([]string, error)
	// ListEnabled returns all enabled channels (enabled=1). Used by the
	// channel monitor to discover which channels to check.
	ListEnabled(ctx context.Context) ([]*asset.CategoryChannel, error)
	// GetByID returns a single channel association by its primary key.
	// Returns a wrapped not-found error when the id is unknown.
	GetByID(ctx context.Context, id string) (*asset.CategoryChannel, error)
	// Upsert creates or updates a channel association keyed by ID.
	Upsert(ctx context.Context, ch *asset.CategoryChannel) error
	// Delete removes a channel association by ID. Idempotent: deleting
	// a missing id returns nil (matches the existing infra behaviour).
	Delete(ctx context.Context, id string) error
	// MarkChecked updates the scheduling columns (next_check_at,
	// last_checked_at, last_error, last_success_at, consecutive_failures)
	// after a channel-sync check completes. PR 3 (June 2026).
	MarkChecked(ctx context.Context, cmd MarkCheckedCommand) error
	// ClaimDue atomically claims channels that are due for checking.
	// PR 5 (June 2026): lease-based scheduling.
	ClaimDue(ctx context.Context, cmd ClaimDueCommand) ([]*asset.CategoryChannel, error)
	// UpdateCursor updates the incremental sync cursor for a channel.
	// PR 5 (June 2026).
	UpdateCursor(ctx context.Context, cmd UpdateCursorCommand) error
}

// Channel is the result DTO returned by Service.List* /
// Service.GetByID. Field names + JSON tags mirror the pre-migration
// domain model exactly (PascalCase keys, Keywords and
// SemanticKeywords are JSON-encoded strings, not Go slices) so
// existing HTTP clients see no wire-format change. The split exists
// so future fields that callers receive can diverge from the domain
// model without breaking persistence.
type Channel struct {
	ID                  string `json:"ID"`
	Category            string `json:"Category"`
	ChannelURL          string `json:"ChannelURL"`
	ChannelName         string `json:"ChannelName,omitempty"`
	Keywords            string `json:"Keywords,omitempty"`
	MinViews            int    `json:"MinViews"`
	MaxClipDuration     int    `json:"MaxClipDuration"`
	DriveFolderID       string `json:"DriveFolderID,omitempty"`
	SemanticKeywords    string `json:"SemanticKeywords,omitempty"`
	MinSemanticScore    int    `json:"MinSemanticScore"`
	PlaylistEnd         int    `json:"PlaylistEnd"`
	CheckInterval       string `json:"CheckInterval,omitempty"`
	MaxVideosPerRun     int    `json:"MaxVideosPerRun"`
	Priority            int    `json:"Priority"`
	LookbackDays        int    `json:"LookbackDays"`
	MaxSegments         int    `json:"MaxSegments"`
	SegmentPrompt       string `json:"SegmentPrompt,omitempty"`
	Enabled             int    `json:"Enabled,omitempty"`
	NextCheckAt         string `json:"NextCheckAt,omitempty"`
	LastCheckedAt       string `json:"LastCheckedAt,omitempty"`
	ConsecutiveFailures int    `json:"ConsecutiveFailures,omitempty"`
	// LeaseOwner + LeaseUntil + LastCursor are monitor-state fields
	// (Commit A, June 2026): the monitor's recordCheckOutcome reads
	// LeaseOwner to fence the eventual MarkChecked UPDATE; the cursor
	// lives on the same row so a single fetched Channel carries both.
	// omitempty: hidden on wire for handlers that don't surface
	// monitor state; visible when the Channel is returned by
	// Service.ClaimDue (the monitor's canonical entry point).
	LeaseOwner string `json:"LeaseOwner,omitempty"`
	LeaseUntil string `json:"LeaseUntil,omitempty"`
	LastCursor string `json:"LastCursor,omitempty"`
	CreatedAt  string `json:"CreatedAt,omitempty"`
	UpdatedAt  string `json:"UpdatedAt,omitempty"`
}

// ListAllResult is the result payload of Service.ListAll.
type ListAllResult struct {
	Channels []Channel `json:"channels"`
}

// ListEnabledResult is the result payload of Service.ListEnabled.
type ListEnabledResult struct {
	Channels []Channel `json:"channels"`
}

// ListCategoriesResult is the result payload of Service.ListCategories.
type ListCategoriesResult struct {
	Categories []string `json:"categories"`
}

// UpsertChannelCommand is the canonical command to upsert a single
// channel. Fields encoded as JSON strings at the persistence layer
// (e.g. keywords, semantic_keywords) are typed here as []string;
// Service marshals them before calling the Repository port, never
// the handler.
//
// If ID is empty, Service derives a deterministic ID via the
// configured IDGenerator (sha256(category+":"+url)[:8] formatted
// as <category>_<hex> when DefaultIDGenerator is in use).
//
// Fields that distinguish "not set" from an explicit zero use *int:
//   - PlaylistEnd: nil=use default, 0=all videos, >0=count
//   - MaxVideosPerRun: nil=use default, 0=no limit, >0=limit
//   - LookbackDays: nil=use default, 0=no lookback, >0=days
type UpsertChannelCommand struct {
	ID               string
	Category         string
	ChannelURL       string
	ChannelName      string
	Keywords         []string
	MinViews         int
	MaxClipDuration  int
	DriveFolderID    string
	SemanticKeywords []string
	MinSemanticScore int
	PlaylistEnd      *int
	CheckInterval    string
	MaxVideosPerRun  *int
	Priority         int
	LookbackDays     *int
	MaxSegments      int
	SegmentPrompt    string
}

// BulkUpsertChannelsCommand is the canonical command for batch upserts.
// Default policy (see service.go) is always applied; callers cannot
// opt out — that keeps the upsert surface mechanical and prevents
// drift between handler and admin paths.
type BulkUpsertChannelsCommand struct {
	Channels []UpsertChannelCommand
}

// BulkUpsertResult captures the per-row outcome of a bulk upsert.
type BulkUpsertResult struct {
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	Errors  []string `json:"errors"`
}

// DeleteResult is the result of Service.Delete: the channel that
// was deleted (looked up before the DELETE fired). Lets the HTTP
// handler return a meaningful response body without re-querying.
type DeleteResult struct {
	Deleted Channel `json:"deleted"`
}

// ClaimDueCommand claims channels that are due for checking.
// PR 5 (June 2026): job-based sync with lease-based scheduling.
type ClaimDueCommand struct {
	Now        string
	WorkerID   string
	LeaseUntil string
	Limit      int
}

// ClaimDueResult is the result of Service.ClaimDue.
type ClaimDueResult struct {
	Channels []Channel `json:"channels"`
}

// UpdateCursorCommand updates the incremental sync cursor for a channel.
// PR 5 (June 2026): tracks the last video ID processed so the monitor
// can resume from where it left off.
type UpdateCursorCommand struct {
	ID     string
	Cursor string
}

// MarkCheckedCommand records the outcome of a channel-sync check.
// PR 3 (June 2026): the monitor + job handler call Service.MarkChecked
// after each channel sync to update scheduling state.
// PR A.June-2026 (Commit A): LeaseToken added so the SQLite repository
// can fence the UPDATE `WHERE id=? AND lease_owner=?` and surface
// ErrLeaseLost via errors.Is when the worker lost its lease between
// ClaimDue and MarkChecked. Empty LeaseToken means "no fence, fire
// the un-fenced UPDATE" and is the back-compat path for any caller
// that doesn't hold a lease (e.g., admin CLIs replaying state).
// Production callers (the ChannelMonitor's recordCheckOutcome) always
// pass ch.LeaseOwner, so the fence is always active in practice.
type MarkCheckedCommand struct {
	ID          string
	LeaseToken  string
	NextCheckAt string
	Success     bool
	LastError   string
}

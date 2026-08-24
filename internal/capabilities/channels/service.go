// Package channels — service.go: the canonical use case layer for
// the CategoryChannel domain.
//
// Capability Standard service.go rules:
//
//   - translate typed commands (contract.go) to the domain model
//   - apply default policy when callers leave fields zero
//   - delegate persistence to the Repository port (contract.go)
//   - return typed Results (contract.go)
//
// Handlers (internal/application/channels/handler.go) call Service
// exclusively — they never reach into asset.CategoryChannel or
// Repository directly. Admin one-shot CLIs
// (cmd/admin/backfill_monitored_sources_to_category_channels.go) call
// NewService with the canonical adapters, bypassing the registry path.
//
// Default policy is centralised here: the concrete SQLite repository
// used to apply the same defaults as a defensive fallback inside its
// Upsert SQL. Capability Standard rule "Defaults and normalisation
// happen once" requires the application layer to be the single
// source of defaults — the SQL fallback was removed in this
// migration (so any direct Upsert call must supply fully-defaulted
// commands; today the only caller is Service.toDomain).
package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// DefaultPolicy holds the field defaults applied by Service when
// the caller leaves a field zero. Mirrors what the previous
// admin/seed and bulk handler used to apply inline, plus what the
// concrete repository's Upsert used to fill in SQL.
//
// Centralising the policy here means the bulk HTTP path, the
// single-row HTTP path, and the admin seed all agree on what zero
// means — drift is no longer possible.
type DefaultPolicy struct {
	MaxClipDuration  int
	Priority         int
	MaxSegments      int
	MaxVideosPerRun  int
	CheckInterval    string
	MinSemanticScore int
	PlaylistEnd      int
}

// Default is the canonical set of defaults applied to every channel
// upsert. Edit here when the product wants to change defaults; the
// concrete SQLite repository and the HTTP handlers no longer
// override these.
var Default = DefaultPolicy{
	MaxClipDuration:  60,
	Priority:         2,
	MaxSegments:      2,
	MaxVideosPerRun:  3,
	CheckInterval:    "7d",
	MinSemanticScore: 60,
	// PlaylistEnd uses -1 as the SQL "use global config" sentinel —
	// matches the previous concrete repository behaviour.
	PlaylistEnd: -1,
}

// IDGenerator is the port for deterministic ID derivation. Tests
// inject a stub to assert exactly which IDs are produced; production
// uses DefaultIDGenerator.
type IDGenerator interface {
	IDFor(category, url string) string
}

// DefaultIDGenerator derives IDs deterministically from
// (category, url) using sha256; returns "" when either input is
// empty so the service can surface a typed validation error.
type DefaultIDGenerator struct{}

// IDFor returns "<category>_<hex>" where <hex> is the first 8 bytes
// of sha256(category + ":" + url).
func (DefaultIDGenerator) IDFor(category, url string) string {
	if category == "" || url == "" {
		return ""
	}
	hash := digest.SHA256Bytes([]byte(category + ":" + url))
	return fmt.Sprintf("%s_%x", category, hash[:8])
}

// NewDefaultIDGenerator returns the canonical generator as an
// IDGenerator interface. Convenience constructor for callers that
// prefer the interface form.
func NewDefaultIDGenerator() IDGenerator { return DefaultIDGenerator{} }

// Service is the canonical use-case orchestrator for the channels
// capability. Constructed once at composition (Build in module.go)
// and shared across the HTTP handler, the registry registration,
// and the admin CLI seed path.
type Service struct {
	repo  Repository
	idGen IDGenerator
	log   *zap.Logger
}

// NewService constructs the Service with the canonical defaults.
// Use this constructor when the caller is a one-shot CLI; the
// registry path goes through Build (module.go). Panics on nil
// Repository by design — at the composition root the nil check
// lives in Build; here, a one-shot CLI calling NewService directly
// should fail loud at the boundary rather than silently swallow.
func NewService(repo Repository, log *zap.Logger) *Service {
	if repo == nil {
		panic("channels.NewService: Repository is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, idGen: DefaultIDGenerator{}, log: log}
}

// WithIDGenerator is the explicit injection seam for tests. The
// default constructor in NewService already wires DefaultIDGenerator,
// so use this only when a test wants to substitute a stub.
func (s *Service) WithIDGenerator(g IDGenerator) *Service {
	if g != nil {
		s.idGen = g
	}
	return s
}

// IDFor exposes the IDGenerator's derivation to callers that need
// to preview an ID without invoking the persistence layer (e.g.
// admin/seed --dry-run).
func (s *Service) IDFor(category, url string) string {
	if s.idGen == nil {
		return ""
	}
	return s.idGen.IDFor(category, url)
}

// ListAll returns every channel in the repository.
func (s *Service) ListAll(ctx context.Context) (ListAllResult, error) {
	rows, err := s.repo.ListAll(ctx)
	if err != nil {
		return ListAllResult{}, err
	}
	return ListAllResult{Channels: fromDomainList(rows)}, nil
}

// ListEnabled returns all enabled channels. Used by the channel monitor
// to discover which channels to check.
func (s *Service) ListEnabled(ctx context.Context) (ListEnabledResult, error) {
	rows, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return ListEnabledResult{}, err
	}
	return ListEnabledResult{Channels: fromDomainList(rows)}, nil
}

// ListCategories returns the distinct set of categories with at
// least one channel assigned.
func (s *Service) ListCategories(ctx context.Context) (ListCategoriesResult, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return ListCategoriesResult{}, err
	}
	return ListCategoriesResult{Categories: rows}, nil
}

// GetByID returns a single channel by its primary key.
func (s *Service) GetByID(ctx context.Context, id string) (Channel, error) {
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	return fromDomain(row), nil
}

// Upsert creates or updates a single channel.
//
// If cmd.ID is empty, a deterministic ID is derived from
// (Category, ChannelURL) via the configured IDGenerator. If
// cmd.ChannelName is empty, the last path segment of URL is used
// (matches the previous admin seed behaviour).
//
// Default policy is applied: zero-valued numeric fields and empty
// CheckInterval are filled from Default. The concrete SQLite
// repository no longer re-applies defaults (single-source rule).
func (s *Service) Upsert(ctx context.Context, cmd UpsertChannelCommand) (Channel, error) {
	if cmd.Category == "" || cmd.ChannelURL == "" {
		return Channel{}, fmt.Errorf("channels.Upsert: category and channel_url are required")
	}
	if cmd.ID == "" && s.idGen != nil {
		cmd.ID = s.idGen.IDFor(cmd.Category, cmd.ChannelURL)
	}
	if cmd.ChannelName == "" {
		cmd.ChannelName = extractChannelName(cmd.ChannelURL)
	}
	domain := s.toDomain(cmd)
	if err := s.repo.Upsert(ctx, domain); err != nil {
		return Channel{}, err
	}
	return s.GetByID(ctx, domain.ID)
}

// UpsertBulk creates or updates many channels in one call. The
// per-row Default policy is always applied; the BulkUpsertResult
// partitions Created vs Updated by checking GetByID before each
// write (preserves the previous bulk handler semantics).
func (s *Service) UpsertBulk(ctx context.Context, cmd BulkUpsertChannelsCommand) (BulkUpsertResult, error) {
	res := BulkUpsertResult{}
	for _, ch := range cmd.Channels {
		if ch.Category == "" || ch.ChannelURL == "" {
			res.Errors = append(res.Errors, ch.Category+"/"+ch.ChannelURL+": category and channel_url are required")
			continue
		}
		if ch.ID == "" && s.idGen != nil {
			ch.ID = s.idGen.IDFor(ch.Category, ch.ChannelURL)
		}
		isUpdate := false
		if existing, err := s.repo.GetByID(ctx, ch.ID); err == nil && existing != nil {
			isUpdate = true
		}
		domain := s.toDomain(ch)
		if err := s.repo.Upsert(ctx, domain); err != nil {
			res.Errors = append(res.Errors, ch.ID+": "+err.Error())
			continue
		}
		if isUpdate {
			res.Updated = append(res.Updated, ch.ID)
		} else {
			res.Created = append(res.Created, ch.ID)
		}
	}
	return res, nil
}

// MarkChecked updates scheduling state after a channel-sync check
// completes. Delegates to Repository.MarkChecked. PR 3 (June 2026).
func (s *Service) MarkChecked(ctx context.Context, cmd MarkCheckedCommand) error {
	if cmd.ID == "" {
		return fmt.Errorf("channels.MarkChecked: id is required")
	}
	return s.repo.MarkChecked(ctx, cmd)
}

// ClaimDue atomically claims channels that are due for checking.
// PR 5 (June 2026): lease-based scheduling with ClaimDue/MarkChecked.
func (s *Service) ClaimDue(ctx context.Context, cmd ClaimDueCommand) (ClaimDueResult, error) {
	rows, err := s.repo.ClaimDue(ctx, cmd)
	if err != nil {
		return ClaimDueResult{}, err
	}
	return ClaimDueResult{Channels: fromDomainList(rows)}, nil
}

// UpdateCursor updates the incremental sync cursor for a channel.
// PR 5 (June 2026): tracks the last video ID processed.
func (s *Service) UpdateCursor(ctx context.Context, cmd UpdateCursorCommand) error {
	if cmd.ID == "" {
		return fmt.Errorf("channels.UpdateCursor: id is required")
	}
	return s.repo.UpdateCursor(ctx, cmd)
}

// Delete removes a single channel by ID. Returns the channel as it
// existed before the delete so the HTTP handler can echo it back
// without re-querying.
func (s *Service) Delete(ctx context.Context, id string) (DeleteResult, error) {
	if id == "" {
		return DeleteResult{}, fmt.Errorf("channels.Delete: id is required")
	}
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return DeleteResult{}, err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{Deleted: fromDomain(existing)}, nil
}

// ── Internal: command → domain / domain → result ───────────────

// toDomain converts a typed command into a *asset.CategoryChannel
// after applying Default policy. The marshal of []string fields uses
// encoding/json so consumers can read them in SQL; an empty slice is
// encoded as "[]" (not "") so the SQLite TEXT column never sees an
// empty string and the consumer's decodeJSONStrings / decode-by-tag
// remains symmetric.
func (s *Service) toDomain(cmd UpsertChannelCommand) *asset.CategoryChannel {
	ch := &asset.CategoryChannel{
		ID:            cmd.ID,
		Category:      cmd.Category,
		ChannelURL:    cmd.ChannelURL,
		ChannelName:   cmd.ChannelName,
		MinViews:      cmd.MinViews,
		DriveFolderID: cmd.DriveFolderID,
		SegmentPrompt: cmd.SegmentPrompt,
	}
	// Normalize nil slices to empty so json.Marshal emits "[]" (not
	// "null"). The persistence column is TEXT NOT NULL holding JSON-encoded
	// arrays; persisting "null" would break client-side decoders that
	// call splitJSONArray / json.Unmarshal on the wire response.
	keywords := cmd.Keywords
	if keywords == nil {
		keywords = []string{}
	}
	if b, err := json.Marshal(keywords); err == nil {
		ch.Keywords = string(b)
	} else {
		ch.Keywords = "[]"
	}
	semanticKeywords := cmd.SemanticKeywords
	if semanticKeywords == nil {
		semanticKeywords = []string{}
	}
	if b, err := json.Marshal(semanticKeywords); err == nil {
		ch.SemanticKeywords = string(b)
	} else {
		ch.SemanticKeywords = "[]"
	}
	ch.MaxClipDuration = applyDefaultPositive(cmd.MaxClipDuration, Default.MaxClipDuration)
	ch.Priority = applyDefaultPositive(cmd.Priority, Default.Priority)
	ch.MaxSegments = applyDefaultPositive(cmd.MaxSegments, Default.MaxSegments)
	ch.MaxVideosPerRun = applyDefaultIntPtr(cmd.MaxVideosPerRun, Default.MaxVideosPerRun)
	ch.MinSemanticScore = applyDefaultPositive(cmd.MinSemanticScore, Default.MinSemanticScore)
	ch.PlaylistEnd = applyDefaultIntPtr(cmd.PlaylistEnd, Default.PlaylistEnd)
	ch.LookbackDays = applyDefaultIntPtr(cmd.LookbackDays, 0)
	ch.CheckInterval = applyDefaultString(cmd.CheckInterval, Default.CheckInterval)

	return ch
}

func fromDomain(ch *asset.CategoryChannel) Channel {
	if ch == nil {
		return Channel{}
	}
	return Channel{
		ID:                  ch.ID,
		Category:            ch.Category,
		ChannelURL:          ch.ChannelURL,
		ChannelName:         ch.ChannelName,
		Keywords:            ch.Keywords,
		MinViews:            ch.MinViews,
		MaxClipDuration:     ch.MaxClipDuration,
		DriveFolderID:       ch.DriveFolderID,
		SemanticKeywords:    ch.SemanticKeywords,
		MinSemanticScore:    ch.MinSemanticScore,
		PlaylistEnd:         ch.PlaylistEnd,
		CheckInterval:       ch.CheckInterval,
		MaxVideosPerRun:     ch.MaxVideosPerRun,
		Priority:            ch.Priority,
		LookbackDays:        ch.LookbackDays,
		MaxSegments:         ch.MaxSegments,
		SegmentPrompt:       ch.SegmentPrompt,
		Enabled:             ch.Enabled,
		NextCheckAt:         ch.NextCheckAt,
		LastCheckedAt:       ch.LastCheckedAt,
		ConsecutiveFailures: ch.ConsecutiveFailures,
		// Commit A: surface monitor-state fields so the monitor's
		// recordCheckOutcome can read LeaseOwner (the lease token
		// fencing acquired by the prior ClaimDue call) without an
		// extra round-trip. LeaseUntil + LastCursor travel with it
		// to keep the DTO round-trip consistent.
		LeaseOwner: ch.LeaseOwner,
		LeaseUntil: ch.LeaseUntil,
		LastCursor: ch.LastCursor,
		CreatedAt:  ch.CreatedAt,
		UpdatedAt:  ch.UpdatedAt,
	}
}

func fromDomainList(rows []*asset.CategoryChannel) []Channel {
	out := make([]Channel, 0, len(rows))
	for _, ch := range rows {
		out = append(out, fromDomain(ch))
	}
	return out
}

func extractChannelName(url string) string {
	url = normalizeURL(url)
	url = strings.TrimSuffix(url, "/videos")
	url = strings.TrimSuffix(url, "/")
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		return url[idx+1:]
	}
	return url
}

// normalizeURL strips query parameters, fragments, and normalizes the scheme
// so equivalent YouTube URLs produce the same deterministic ID.
//
// Examples:
//
//	https://www.youtube.com/@TeamCoco?si=xxx → https://www.youtube.com/@TeamCoco
//	http://youtube.com/@TeamCoco#videos     → https://youtube.com/@TeamCoco
func normalizeURL(raw string) string {
	// Strip fragment
	if idx := strings.Index(raw, "#"); idx >= 0 {
		raw = raw[:idx]
	}
	// Strip query params
	if idx := strings.Index(raw, "?"); idx >= 0 {
		raw = raw[:idx]
	}
	// Normalize scheme: http:// → https://
	raw = strings.Replace(raw, "http://", "https://", 1)
	return raw
}

// applyDefaultPositive returns `v` if non-zero, else `fallback`.
// Used for numeric defaults applied by Service.toDomain.
func applyDefaultPositive(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// applyDefaultIntPtr returns the dereferenced pointer value if non-nil,
// otherwise the fallback. Used for fields where nil means "use default"
// and an explicit zero has a distinct meaning (e.g. PlaylistEnd=0 = all videos).
func applyDefaultIntPtr(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

func applyDefaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

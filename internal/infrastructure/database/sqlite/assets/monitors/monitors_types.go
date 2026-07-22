// Package assets — SQLite-specific persistence types for the assets domain.
//
// PR4.B (June 2026): infrastructure DTOs that carry SQL tags (db:"...",
// TableName) are isolated here. Upstream code in internal/domain/asset/
// uses Domain types that only carry JSON tags so the domain layer has
// zero knowledge of the underlying SQLite schema. Boundary mappers
// (FromDomain + ToDomain) live alongside the row types so adapters can
// convert without leaking SQL details.
//
// Pattern rationale: previously internal/domain/asset.MonitoredSource
// held both json:"..." and db:"..." tags + a TableName() method, leaking
// the SQLite schema into the domain layer. This file splits the SQL
// concerns out; callers that need to persist convert via FromDomain /
// ToDomain. The 4 public methods on *assets.MonitorsRepository continue
// to expose the domain type to consumers (D3 — repo signatures unchanged).

package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// MonitoredSourceRow is the SQLite-only projection of asset.MonitoredSource.
// It carries the canonical column mapping (`db:"..."` tags) and the table
// name. Domain code MUST NOT import this type; convert at the boundary
// via FromDomain / ToDomain.
type MonitoredSourceRow struct {
	ID             string `db:"id"`
	Source         string `db:"source"`
	ExternalID     string `db:"external_id"`
	ExternalURL    string `db:"external_url"`
	Title          string `db:"title"`
	ChannelID      string `db:"channel_id"`
	ChannelURL     string `db:"channel_url"`
	Keyword        string `db:"keyword"`
	GroupName      string `db:"group_name"`
	Category       string `db:"category"`
	Status         string `db:"status"`
	LastSeenAt     string `db:"last_seen_at"`
	LastCheckedAt  string `db:"last_checked_at"`
	ProcessedCount int    `db:"processed_count"`
	MetadataJSON   string `db:"metadata_json"`
	CreatedAt      string `db:"created_at"`
	UpdatedAt      string `db:"updated_at"`
}

// TableName returns the database table name for MonitoredSourceRow.
func (MonitoredSourceRow) TableName() string {
	return "monitored_sources"
}

// FromDomain converts the domain projection to the SQLite row type. The
// conversion is field-by-field (no JSON serialisation step) because both
// types share the same scalar shape; the only difference is the tag set.
func FromMonitoredSourceDomain(src *asset.MonitoredSource) *MonitoredSourceRow {
	if src == nil {
		return nil
	}
	return &MonitoredSourceRow{
		ID:             src.ID,
		Source:         src.Source,
		ExternalID:     src.ExternalID,
		ExternalURL:    src.ExternalURL,
		Title:          src.Title,
		ChannelID:      src.ChannelID,
		ChannelURL:     src.ChannelURL,
		Keyword:        src.Keyword,
		GroupName:      src.GroupName,
		Category:       src.Category,
		Status:         src.Status,
		LastSeenAt:     src.LastSeenAt,
		LastCheckedAt:  src.LastCheckedAt,
		ProcessedCount: src.ProcessedCount,
		MetadataJSON:   src.MetadataJSON,
		CreatedAt:      src.CreatedAt,
		UpdatedAt:      src.UpdatedAt,
	}
}

// ToDomain converts the SQLite row type back to the domain projection.
// Pointer receiver so nil rows produce nil domains at the call sites.
func (r *MonitoredSourceRow) ToDomain() *asset.MonitoredSource {
	if r == nil {
		return nil
	}
	return &asset.MonitoredSource{
		ID:             r.ID,
		Source:         r.Source,
		ExternalID:     r.ExternalID,
		ExternalURL:    r.ExternalURL,
		Title:          r.Title,
		ChannelID:      r.ChannelID,
		ChannelURL:     r.ChannelURL,
		Keyword:        r.Keyword,
		GroupName:      r.GroupName,
		Category:       r.Category,
		Status:         r.Status,
		LastSeenAt:     r.LastSeenAt,
		LastCheckedAt:  r.LastCheckedAt,
		ProcessedCount: r.ProcessedCount,
		MetadataJSON:   r.MetadataJSON,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

// FromMonitoredSourceDomainList maps a slice of domain items to a slice of
// rows. Nil/empty input produces nil/empty output without surprises.
func FromMonitoredSourceDomainList(src []*asset.MonitoredSource) []*MonitoredSourceRow {
	if len(src) == 0 {
		return nil
	}
	out := make([]*MonitoredSourceRow, 0, len(src))
	for _, s := range src {
		out = append(out, FromMonitoredSourceDomain(s))
	}
	return out
}

// ToMonitoredSourceDomainList maps a slice of infra rows to a slice of
// domain projections. Implemented as a free function (not a method on
// `[]MonitoredSourceRow`) because Go does not allow methods to be
// defined on anonymous slice types. Used by the repository's ListDue so
// the SQL→domain conversion stays in infra-land.
func ToMonitoredSourceDomainList(rows []MonitoredSourceRow) []*asset.MonitoredSource {
	if len(rows) == 0 {
		return nil
	}
	out := make([]*asset.MonitoredSource, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ToDomain())
	}
	return out
}

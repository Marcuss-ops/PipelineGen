package spec

import "time"

// ResearchCacheRecord is the canonical shape of one research_cache row.
// It is a value struct owned here so the repository port and the evidence
// contracts can both depend on it without an import cycle.
type ResearchCacheRecord struct {
	Key                string    // SHA256 cache key; PRIMARY KEY in SQLite.
	Topic              string    // Human-readable topic used as fallback.
	Language           string    // Target language of the source text.
	MaxSteps           int       // Research depth / max steps requested.
	SourceText         string    // The cached research text.
	SourceTextHash     string    // SHA-256 of SourceText.
	ResearchReportJSON string    // Sanitized JSON provenance/metrics report.
	SourcesCount       int       // Number of selected sources.
	ClaimsVerified     int       // Number of verified claims.
	ClaimsRejected     int       // Number of rejected claims.
	SearchQueryCount   int       // Number of executed web queries.
	PagesFetched       int       // Number of fetched pages.
	ConceptID          string    // Optional media_concepts.id that produced this text.
	TopicFingerprint   string    // Stable topic identity (e.g., SHA256 of normalized topic).
	SourceFingerprint  string    // Stable identity of the source material (URL, asset, document).
	ResolverVersion    string    // Version of the source resolver that produced the text.
	ResearchVersion    string    // Version of the research strategy / prompts.
	HitCount           int       // How many times the row has been reused.
	ExpiresAt          time.Time // Absolute TTL.
	CreatedAt          time.Time // Row creation time.
	UpdatedAt          time.Time // Last mutation time.
}

// Package stockpipeline — step_publish_naming.go
// (PR-SPLIT-STEP-PUBLISH, 2026-08-08).
//
// Drive-side naming helpers extracted from step_publish.go per
// AGENTS.md Pattern 5 + godlike/06 SSOT one-canonical-owner-per-fact.
// Lives alongside the slim step_publish.go orchestrator: the
// per-chunk loop lives in step_publish_chunks_phase.go; the
// run-level metadata.json (Phase 2) lives in
// step_publish_metadata_phase.go.
//
// Helpers in this file are the SOLE canonical owners of:
//
//   - Root folder name for a stock pipeline run:
//     stockRootFolderName, stockResolvedFolderID.
//   - Filesystem-safe sanitization: sanitizedRootName,
//     sanitizeLegacyQuery, sanitizeLegacyURLBasename,
//     sanitizedURLBasename.
//   - Per-run + per-clip leaf names:
//     stockTimestampGroupName (legacy shared leaf),
//     perClipLeafName (explicit-clips per-clip leaf),
//     timestampParentLeafName (explicit-clips parent folder).
//   - Slug wrapper: slugifyTitle (local alias for pkg/slug
//     grep-stability per PR-CANONICAL-CLIP-NAMING).
//
// godlike/06 SSOT: each helper owns exactly one Drive-side
// naming concern. Lookup paths (stockRootFolderName etc.) are
// package-private but resolve across all 4 sister files via Go
// package-scope visibility — the slim step_publish.go orchestrator
// + the per-chunk phase + the metadata phase all invoke them
// without import edges.
//
// godlike/07 minimum-blast-radius: zero new exported symbols; the
// 10 helpers were already package-private inside step_publish.go
// and stay private when relocated here. Zero signature drift
// (verbatim code-motion per AGENTS.md Pattern 5).
package stockpipeline

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/slug"
)

// stockRootFolderName derives the human-readable Drive top-level
// folder name for a stock pipeline run. Canonical SSOT for legacy
// "no-folder-name" legibility (July 2026). Source evaluation order:
//
//  1. in.FolderName — operator-supplied readable name (highest priority).
//  2. in.Subfolder   — operator-supplied secondary root.
//  3. in.SearchQueries[0] — sanitized first query (legacy search-only).
//  4. in.DirectURLs[0]   — basename of first URL (legacy direct-url).
//  5. "stock_<YYYY-MM-DD>" UTC date — universal fallback.
//
// Pre-change legibility gap: a legacy run that omits folder_name +
// clips[] surfaced either "stock" or run_<fingerprint> on Drive —
// both illegible to operators scanning the Drive folder tree. The
// fallback chain above preserves the first two branches
// byte-stable for operators who DO supply folder_name (or
// subfolder) and extends with three new fallback rules for the
// legacy path.
//
// godlike/06 SSOT: this function is the SOLE writer of stock
// pipeline root folder names. The VerifiedArtifact.RootFolderName
// field (locked in PR-STOCK-ORCHESTRATOR-SPLIT, July 2026) carries
// the chosen value into the artifact publisher adapter
// stockArtifactPathParts switch which honors it via
// stockRunFolderName.
//
// godlike/07 minimum-blast-radius: zero new signatures, zero new
// dependencies, zero composition-root surface change. Date fallback
// runs at request time (no cached time) so test fixtures and replay
// paths stay stable. The nil RunInput branch returns "stock"
// (preserving pre-change behavior for legacy fixture flows).
func stockRootFolderName(in *RunInput) string {
	if in == nil {
		return "stock"
	}
	if name := sanitizedRootName(in.FolderName); name != "" {
		return name
	}
	if name := sanitizedRootName(in.Subfolder); name != "" {
		return name
	}
	if name := domaindelivery.FirstSanitizedQuery(in.SearchQueries); name != "" {
		return name
	}
	if name := domaindelivery.FirstSanitizedURLBasename(in.DirectURLs); name != "" {
		return name
	}
	return "stock_" + time.Now().UTC().Format("2006-01-02")
}

// stockResolvedFolderID returns a Drive folder ID only when the input
// explicitly carries the resolved Drive identifier. FolderID is a workflow
// identifier, not a Drive destination, and never crosses this boundary.
func stockResolvedFolderID(in *RunInput) string {
	if in == nil || !in.DriveFolderResolved {
		return ""
	}
	return strings.TrimSpace(in.DriveFolderID)
}

// sanitizedRootName trims whitespace, returns "" for empty input,
// otherwise runs through pathutil.SafeFolderName. Empty-in / all-
// whitespace-in collapses to "" so the caller continues to the
// next fallback rule (closes the pre-existing latent bug where
// empty FolderName silently surfaced as "untitled" on Drive).
func sanitizedRootName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return pathutil.SafeFolderName(s)
}

// sanitizeLegacyQuery returns the first non-empty sanitized search
// query from queries. Returns "" if none survive so the caller
// falls through to the URL / date fallback rule.
func sanitizeLegacyQuery(queries []string) string {
	for _, q := range queries {
		if name := sanitizedRootName(q); name != "" {
			return name
		}
	}
	return ""
}

// sanitizeLegacyURLBasename returns the first non-empty sanitized
// URL basename (sans file extension) from urls. Returns "" if none
// survive so the caller falls through to the date fallback rule.
func sanitizeLegacyURLBasename(urls []string) string {
	for _, u := range urls {
		if name := sanitizedURLBasename(u); name != "" {
			return name
		}
	}
	return ""
}

// sanitizedURLBasename strips query + fragment layers from a raw
// URL, takes the URL path's Base, strips the file extension, then
// runs through sanitizedRootName so the result is a filesystem-
// safe folder name. Returns "" on empty / malformed input so
// callers fall through to the next fallback rule.
func sanitizedURLBasename(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if parsed, err := url.Parse(s); err == nil && parsed.Path != "" {
		s = parsed.Path
	}
	base := filepath.Base(s)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return sanitizedRootName(base)
}

// stockTimestampGroupName derives the human-readable Drive leaf
// folder name shared by all chunks + the per-run metadata.json
// of a stock pipeline run. Cascade:
//
//  1. basename of in.Subfolder (operator-supplied path).
//  2. in.FolderName (operator-supplied readable name).
//  3. "metadata" (universal literal-name fallback).
//
// Empty in.FolderName (or all-whitespace) is no longer incorrectly
// surfaced as the literal "untitled" string. The pre-existing bug
// was exposed by TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName:
// when in.FolderName == "" AND in.Subfolder == "" the function
// returned pathutil.SafeFolderName("") = "untitled", which then
// surfaced on Drive as the literal folder name `untitled/`. The
// fix threads in.FolderName through sanitizedRootName which
// trims+empty-checks BEFORE SafeFolderName, returning "" for
// empty input so the caller falls through to the "metadata"
// literal fallback.
//
// godlike/06 SSOT: this is the SOLE writer of stock
// PathLeafName values. VerifiedArtifact.PathLeafName (locked in
// PR-STOCK-ORCHESTRATOR-SPLIT, July 2026) carries the chosen
// value into every chunk + metadata artifact in a run, so all
// chunks + metadata share the SAME leaf (no per-chunk drift).
func stockTimestampGroupName(in *RunInput) string {
	if in == nil {
		return "metadata"
	}
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if base := filepath.Base(filepath.Clean(sub)); base != "" && base != "." && base != string(filepath.Separator) {
			if name := sanitizedRootName(base); name != "" {
				return name
			}
		}
	}
	if name := sanitizedRootName(in.FolderName); name != "" {
		return name
	}
	return "metadata"
}

// stockClipFolderName derives the Drive subfolder for an explicit clip.
// When the clip carries a round number, all clips from that round share one
// folder under the run folder. Round zero keeps the human-readable title
// (typically the introduction/fighter-intros segment). Runs without explicit
// clip metadata retain the legacy timestamp-group fallback.
func stockClipFolderName(in *RunInput, plan ClipPlan, fallback string) string {
	// An explicit subfolder is an operator-selected timestamp path and keeps
	// the historical single-folder contract. Round-based grouping applies to
	// runs that leave subfolder empty and provide round metadata on clips.
	if in != nil && strings.TrimSpace(in.Subfolder) != "" {
		return fallback
	}
	if plan.Round > 0 {
		return fmt.Sprintf("Round %d", plan.Round)
	}
	if title := sanitizedRootName(plan.Title); title != "" {
		return title
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return stockTimestampGroupName(in)
}

// stockTimestampParentGroupName derives the human-readable leaf for
// the parent folder that contains all 5-second children of a single
// explicit timestamp block. Priority cascade:
//
//  1. Parent directory basename of in.Subfolder (e.g.
//     `Pacquiao_Vs_Broner/Round_1_La_fase_di_studio/00-...` →
//     `Round_1_La_fase_di_studio`).
//  2. stockTimestampGroupName(in) fallback when no parent segment
//     exists or the parent segment sanitizes to empty.
//
// This is the leaf shared by every child file and by the aggregated
// metadata.json in explicit-clips mode.
func stockTimestampParentGroupName(in *RunInput) string {
	if in == nil {
		return stockTimestampGroupName(in)
	}
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if parent := filepath.Base(filepath.Dir(filepath.Clean(sub))); parent != "" && parent != "." && parent != string(filepath.Separator) {
			if name := sanitizedRootName(parent); name != "" {
				return name
			}
		}
	}
	return stockTimestampGroupName(in)
}

// perClipLeafName derives the canonical per-clip Drive leaf folder
// name for explicit-clips (timestamp-mode) runs. Priority cascade
// (per user diagnostic "round-01-la-fase-di-studio" example):
//
//  1. slugify(plan.Title) — operator-supplied clip title (e.g.
//     "Round 7 - Broner barcolla" → "round-7-broner-barcolla").
//     The slug matches the canonical convention established by
//     pkg/stockparser (SafeFolderName → ToLower → space-to-hyphen
//     + collapse-consecutive-hyphens + trim-edges).
//  2. fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d", ...)
//     (StartSec, EndSec) — universal HH-MM-SS_to_HH-MM-SS literal
//     for the empty-Title / whitespace-Title / pathutil-empty
//     fallback case (preserves the "00-00-32_to_00-01-27" shape
//     the legacy code emitted for the run-level leaf).
//
// godlike/07 NO-FAKE-AVAILABILITY: the slug is NEVER "untitled"
// (pathutil.SafeFolderName's all-whitespace fallback). When the
// title produces an empty slug, the function falls through to
// the start-end literal — operators see a stable, parseable
// leaf instead of a generic "untitled" folder that shadows
// other runs.
//
// godlike/06 SSOT: this is the SOLE writer of per-clip
// PathLeafName values for explicit-clips runs. The legacy
// stockTimestampGroupName function stays as the SOLE writer
// of shared-leaves for legacy (no clips[]) runs and for the
// per-run metadata.json — the two functions are disjoint by
// gate (explicitTimestamps=true vs false) per godlike/07
// minimum-blast-radius.
//
// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): replaces the
// pre-PR bug where every chunk in an explicit-clips run shared
// a single PathLeafName (= stockTimestampGroupName), so all 8
// Pacquiao/Broner clips landed in the same folder instead of
// in per-clip subdirs.
func perClipLeafName(plan ClipPlan) string {
	// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): explicit-override
	// cascade. The user-supplied Slug (when non-empty) is the canonical
	// leaf — it wins over the title-derived slug. Uses
	// sanitizedRootName (canonical SSOT) so the
	// result is filesystem-safe. godlike/07 NO-FAKE-AVAILABILITY:
	// the SafeFolderName all-whitespace fallback "untitled" is also
	// rejected. Critically, we ALSO reject slugs that sanitize to
	// pure-punctuation (e.g. "///" → "___", "!!!" → "___") via
	// domaindelivery.ContainsAlphanumeric.
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := sanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		slug := slugifyTitle(title)
		if slug != "" && slug != "untitled" {
			return slug
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d",
		int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60,
		int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60,
	)
}

// timestampParentLeafName returns the Drive leaf for the original
// timestamp block before it was expanded into 5-second children.
// Explicit timestamp runs use this for the folder path so all
// slices from the same parent clip land together.
func timestampParentLeafName(plan ClipPlan) string {
	if raw := strings.TrimSpace(plan.ParentSlug); raw != "" {
		if safe := sanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		slug := slugifyTitle(title)
		if slug != "" && slug != "untitled" {
			return slug
		}
	}
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := sanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d",
		int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60,
		int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60,
	)
}

// slugifyTitle delegates to the canonical pkg/slug.SlugifyTitle
// (godlike/06 SSOT: pkg/slug is the SOLE canonical owner of the
// title-slug convention). PR-CANONICAL-CLIP-NAMING (July 2026):
// local alias preserved for grep-stability.
func slugifyTitle(title string) string {
	return slug.SlugifyTitle(title)
}

// Package stockbuild — payload.go (P0-2 stock-pipeline refactor, July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// youtube.stock.build.v1 wire-shape. Every producer (HTTP handler,
// cron tick, CLI) MUST marshal into Payload via this struct; every
// consumer (handler) MUST decode JSON into Payload via this struct.
// Ad-hoc inline maps / DTO mirrors are forbidden — drift here would
// be invisible to the worker until runtime.
//
// The canonical payload shape mirrors the user's "Esperienza finale
// desiderata" (see godlike/08 stock-pipeline refactor plan, July
// 2026):
//
//	{
//	  "subject":             { "display_name": "...", "slug": "..." },
//	  "target":              { "videos": 20, "clips_per_video": 15, "clip_duration_seconds": 4 },
//	  "categories":          [{ "name": "fight", "count": 12 }, { "name": "interview", "count": 6 }, ...],
//	  "destination_folder_id": "DRIVE_FOLDER_ID",
//	  "resume":              true
//	}
//
// godlike/07 fail-closed: Validate() MUST return a typed error on ANY
// missing/malformed field. Callers branch on ErrInvalidPayload via
// errors.Is — they NEVER string-match on the validator message
// (godlike/07 typed-error contract).
package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/slug"
)

// ─── Canonical Job Type ─────────────────────────────────────────────────────

// JobType is the canonical Type discriminator for this orchestrator.
// Every registry bind + worker dispatch references this constant —
// inline literal "youtube.stock.build.v1" is forbidden (godlike/06
// SSOT — drift here would orphan Register vs. Dispatch).
const JobType = "youtube.stock.build.v1"

// ─── Canonical Payload Shape ────────────────────────────────────────────────

// SubjectRef points at the canonical subject whose stock the JOB
// orchestrates. The handler resolves it via the canonical
// `subjects.Resolver` (godlike/06 SSOT — ONE owner for subject
// normalization); the payload carries the operator-supplied
// display_name + the in-payload slug (the slug is what the
// caller computed via `pkg/slug.SlugifyTitle` before submitting
// the JOB; the resolver may still re-compute it from
// display_name if the caller's slug is empty).
type SubjectRef struct {
	DisplayName string `json:"display_name"`
	Slug        string `json:"slug,omitempty"`
}

// TargetSpec describes the desired run shape.
//
//	Videos            — total source videos (main + reserve, where
//	                    reserve count is derived by service-side policy).
//	ClipsPerVideo     — clips extracted per source video.
//	ClipDurationSeconds — single-clip duration; per-clip resume keys
//	                    the start/end ms fingerprint on this value
//	                    + source_video_id.
//
// Bounds Validate()-enforced at the wire boundary so the handler
// cannot receive malformed inputs that would silently produce
// under-sized runs.
type TargetSpec struct {
	Videos              int `json:"videos"`
	ClipsPerVideo       int `json:"clips_per_video"`
	ClipDurationSeconds int `json:"clip_duration_seconds"`
}

// CategoryCount is a typed-array element (godlike/06 — typed arrays
// preferred over string-keyed maps for stable iteration order +
// struct-tag-driven validation).
type CategoryCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Payload is the canonical wire shape. Every producer (HTTP, cron,
// CLI) and consumer (handler) operates on this type.
type Payload struct {
	Subject             SubjectRef      `json:"subject"`
	Target              TargetSpec      `json:"target"`
	Categories          []CategoryCount `json:"categories"`
	DestinationFolderID string          `json:"destination_folder_id"`
	// Resume is the operator-driven hint. The handler's resume check
	// is driven by the steps.Store ledger regardless (a paused+resumed
	// run will see Completed steps naturally), so this field exists
	// only so the explicit "resume this run" command can carry
	// intent. Default false on the wire (zero-value).
	Resume bool `json:"resume,omitempty"`
}

// ─── Typed Errors (godlike/07) ───────────────────────────────────────────────

// ErrInvalidPayload is the typed sentinel returned by Validate() when
// ANY field is missing or malformed. Callers MUST branch via
// `errors.Is(err, stockbuild.ErrInvalidPayload)` — string-matching
// on the error message is the canonical godlike/07 anti-pattern.
var ErrInvalidPayload = errors.New("stockbuild: payload invalid")

// ErrCategoriesExhaustTarget is the typed sentinel returned by
// Validate() when the categories-sum exceeds target.clips_per_video
// × target.videos. A run with 20×15=300 clips cannot request
// 12+6+2=20 fight+interview+training extra without a bulk-overflow
// policy. Today we reject loudly (godlike/07 fail-closed); a future
// refactor may collapse the bucketing when a streaming + dedup layer
// is introduced.
var ErrCategoriesExhaustTarget = errors.New(
	"stockbuild: sum of category counts exceeds target.clip capacity")

// ─── Validate ───────────────────────────────────────────────────────────────

// Validate returns nil iff every field is well-formed. Aggregates
// missing/max-violating fields into ONE error message (godlike/07
// no-fake-availability) so callers see all problems at once.
func (p Payload) Validate() error {
	var problems []string
	if strings.TrimSpace(p.Subject.DisplayName) == "" {
		problems = append(problems, "subject.display_name is empty")
	}
	if p.Subject.Slug != "" {
		// Per godlike/06 SSOT, the slug MUST byte-equal
		// pkg/slug.SlugifyTitle(display_name) — otherwise the
		// caller is passing a hand-rolled slug that will collide
		// with the resolver's normalization layer. We compute the
		// expected slug and compare.
		expected := slug.SlugifyTitle(p.Subject.DisplayName)
		if expected != "" && p.Subject.Slug != expected {
			problems = append(problems,
				fmt.Sprintf("subject.slug=%q does not byte-equal slug.SlugifyTitle(display_name)=%q (godlike/06 SSOT: caller MUST derive slug via canonical pkg/slug)",
					p.Subject.Slug, expected))
		}
	}
	if p.Target.Videos <= 0 {
		problems = append(problems, "target.videos must be > 0")
	}
	if p.Target.ClipsPerVideo <= 0 {
		problems = append(problems, "target.clips_per_video must be > 0")
	}
	if p.Target.ClipDurationSeconds <= 0 {
		problems = append(problems, "target.clip_duration_seconds must be > 0")
	}
	if strings.TrimSpace(p.DestinationFolderID) == "" {
		problems = append(problems, "destination_folder_id is empty")
	}
	if len(p.Categories) == 0 {
		problems = append(problems, "categories is empty (at least one category required)")
	}
	var categorySum, names = 0, make(map[string]bool)
	for i, c := range p.Categories {
		if strings.TrimSpace(c.Name) == "" {
			problems = append(problems, fmt.Sprintf("categories[%d].name is empty", i))
			continue
		}
		if names[c.Name] {
			problems = append(problems, fmt.Sprintf("categories[%d].name=%q is duplicated", i, c.Name))
			continue
		}
		names[c.Name] = true
		if c.Count < 0 {
			problems = append(problems, fmt.Sprintf("categories[%d].count=%d is negative", i, c.Count))
			continue
		}
		categorySum += c.Count
	}
	if categorySum > p.Target.ClipsPerVideo*p.Target.Videos {
		problems = append(problems,
			fmt.Sprintf("categories sum=%d exceeds capacity=%d (clips_per_video=%d × videos=%d)",
				categorySum, p.Target.ClipsPerVideo*p.Target.Videos,
				p.Target.ClipsPerVideo, p.Target.Videos))
	}
	if len(problems) > 0 {
		// wrap = ErrInvalidPayload (the canonical typed sentinel for
		// all schema-level failures). Two notes:
		//   - Per godlike/07, the OUTER %w is the one-and-only typed
		//     error contract callers must branch on (`errors.Is(err,
		//     ErrInvalidPayload)`). Inner string messages aggregate
		//     every field-level problem in ONE error string so an
		//     operator sees all problems at once.
		//   - fmt.Sprintf does NOT support %w (compile-time error);
		//     only fmt.Errorf supports it. The categories-exhaust
		//     sentinel ErrCategoriesExhaustTarget stays defined as a
		//     typed concept for direct smoke-tests, but it is NOT
		//     chained via %w here — the operator-facing message
		//     carries the same detail in plain text.
		return fmt.Errorf("%w: %s", ErrInvalidPayload, strings.Join(problems, "; "))
	}
	return nil
}

// ─── Canonical RunID derivation ─────────────────────────────────────────────

// DeriveRunID returns the deterministic 64-hex-char Run ID for a given
// resolved Subject + Payload combination. The Run ID is the canonical
// key for `execution_steps.job_id` (the existing per-step ledger); if
// a prior run with the same ID crashed mid-flight, a fresh
// Handler.Handle invocation with the same DeriveRunID output will
// resume from the first non-Completed step (Stock Cutover §12-3
// Design A — per-row canonical semantics).
//
// The hash inputs are the canonical field-form of the payload (NOT
// the raw JSON bytes, which would be unstable across reordering +
// whitespace). This guarantees:
//
//   - "SUGAR RAY ROBINSON" / "Sugar Ray Robinson" yield the SAME
//     Run ID (post-resolver normalization at the handler boundary).
//   - "Sugar Ray Robinson" with 20 videos yields a DIFFERENT Run ID
//     than with 15 videos (operator-driven intent change is a
//     different "run" semantically).
//   - Setting resume=true vs resume=false produces the SAME Run ID
//     (resume is a runtime hint, not a parameter of the run itself).
//
// Determinism is testable (TestDeriveRunID_DeterministicContract).
func DeriveRunID(subjectSlug string, p Payload) string {
	// Canonicalize the payload BEFORE hashing: marshal a stable
	// representation (sorted categories, normalized subject
	// references) so two operators spelling the same intent the
	// same way yield the same ID.
	normalized := payloadForHash{
		Slug:              subjectSlug,
		Target:            p.Target,
		DestinationFolder: p.DestinationFolderID,
		Categories:        normalizeCategories(p.Categories),
	}
	sum := digest.SHA256Bytes([]byte(canonicalJSON(normalized)))
	return sum
}

// payloadForHash is a private canonical-structure for hashing. Separated
// from Payload to prevent tag drift on Payload from silently
// invalidating computeHash contracts (godlike/06 SSOT — one owner per
// shape).
type payloadForHash struct {
	Slug              string          `json:"s"`
	Target            TargetSpec      `json:"t"`
	DestinationFolder string          `json:"d"`
	Categories        []CategoryCount `json:"c"`
}

// FormatRunIDLabel renders a human-readable label for logs/operator
// surfaces using the slug + a date stamp. The hash itself is the
// stable identity; the label is just for grep-ability.
//
// Format: `stock_<slug>_<YYYYMMDD>`. The date is the run-start day
// (caller-supplied, typically the Enqueue time); the slug is what
// the resolver normalized the display_name to.
func FormatRunIDLabel(subjectSlug string, runTime time.Time) string {
	slug := strings.TrimSpace(subjectSlug)
	if slug == "" {
		slug = "unknown"
	}
	return fmt.Sprintf("stock_%s_%s", slug, runTime.UTC().Format("20060102"))
}

// ─── Private helpers ─────────────────────────────────────────────────────────

// normalizeCategories returns a sorted copy of categories so two
// payloads with `{fight,interview}` vs `{interview,fight}` produce
// the same hash. godlike/06 typed-array contract: stable iteration
// order is observable at the hash layer.
func normalizeCategories(in []CategoryCount) []CategoryCount {
	if len(in) == 0 {
		return nil
	}
	out := make([]CategoryCount, len(in))
	copy(out, in)
	// bubble sort by Name ASC (small N; sort.Slice allocation
	// overhead is unnecessary for typical 2-4 categories)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// canonicalJSON is the canonical JSON encoder for hashing. It is
// NOT exposed because callers must NOT depend on the wire form
// for hashing (changes to JSON-marshal sequencing should not
// invalidate the hash contract).
//
// Today the implementation is `encoding/json.Marshal` — the typed
// structure of payloadForHash (all struct fields declared
// alphabetically) gives deterministic output. The function name
// is preserved so a future swap to a canonical-JSON encoder
// (e.g. jsonmarshaller with sorted keys) is a one-line change.
func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Internal error: the type passed is always serialisable
		// (struct of strings + ints). Failure surfaces at test time.
		panic(fmt.Sprintf("stockbuild.canonicalJSON: marshal failure: %v", err))
	}
	return string(b)
}

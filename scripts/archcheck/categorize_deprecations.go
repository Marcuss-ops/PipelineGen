package main

import "strings"

// deprecationBucket is the canonical subsystem grouping used by
// the planned sharded layout under architecture/deprecations/.
//
// Records on disk are split per bucket (one shard under
// `records/<bucket>.yaml`), and the bucketing key is the record's
// `owner_capability` field. Stability of these bucket names is
// part of the public-facing contract that the future split will
// preserve: renaming a bucket is a hard file-system move, not a
// re-mapping, so the names below are chosen to match the
// subsystem rooted at the first 2-3 import-path segments of each
// record's owner_capability value as observed in
// architecture/deprecations.yaml today.
//
// Records whose owner_capability does not match any explicit
// prefix fall through to `misc`. Operators wanting to add a new
// bucket should update this map AND the directory-mode shard
// names; the unit test suite enforces that every record loaded
// from the live registry maps to a non-empty, non-"misc" bucket
// so silent overflow does not regress.
type deprecationBucket string

const (
	bucketDrive       deprecationBucket = "drive"
	bucketTranslation deprecationBucket = "translation"
	bucketVoiceover   deprecationBucket = "voiceover"
	bucketJobs        deprecationBucket = "jobs"
	bucketQdrant      deprecationBucket = "qdrant"
	bucketAssets      deprecationBucket = "assets"
	bucketScripts     deprecationBucket = "scripts"
	bucketMedia       deprecationBucket = "media"
	bucketClip        deprecationBucket = "clip"
	bucketMonitor     deprecationBucket = "monitor"
	bucketSearch      deprecationBucket = "search"
	bucketMisc        deprecationBucket = "misc"
)

// subsystemPrefixes defines the owner_capability-to-bucket map.
// Matching is longest-prefix-wins on the slash-delimited import
// path; a record with owner_capability "internal/application/youtube"
// matches "internal/application/youtube" if present else falls
// back to "internal/application" -> bucketMedia, else "misc".
//
// This map is the categorization rule and the only source of
// truth for it — the planned `architecture/deprecations/records/*`
// shards will be generated from it. Editing this map without
// running `go test ./scripts/archcheck/...` (which fails loud on
// every record whose owner_capability falls into `misc`) hides
// migrations and is forbidden by AGENTS.md.
var subsystemPrefixes = []struct {
	prefix string
	bucket deprecationBucket
}{
	// Drive / files.
	{"internal/infrastructure/drive", bucketDrive},
	{"internal/application/assets/upload_intent", bucketDrive},
	{"internal/application/assets/duplicates", bucketDrive},
	{"internal/application/assets/bundle", bucketDrive},
	// Translation.
	{"internal/application/translation", bucketTranslation},
	{"pkg/translation", bucketTranslation},
	// Voiceover.
	{"internal/application/voiceover", bucketVoiceover},
	{"internal/capabilities/voiceover", bucketVoiceover},
	{"pkg/voiceover", bucketVoiceover},
	// Jobs / kernel.
	{"internal/kernel/job", bucketJobs},
	{"compatibility/domain/job", bucketJobs},
	// NOTE: P1-7 retired `internal/domain/job/` (atomic cutover →
	// `internal/kernel/job/`, 2026-07-30). The canonical records
	// below now live under the kernel prefix; the back-compat
	// `compatibility/domain/job/` entry survives because it is
	// an unrelated umbrella package (not the legacy root). Any
	// new record pointing at `internal/domain/job/<x>` will fall
	// through to `bucketMisc` so the regression is auditable.
	// Qdrant projection.
	{"internal/infrastructure/qdrant", bucketQdrant},
	{"pkg/architecturecatalog", bucketQdrant},
	// Assets.
	{"internal/application/assets", bucketAssets},
	{"pkg/assets", bucketAssets},
	// Scripts.
	{"internal/application/scripts", bucketScripts},
	{"internal/kernel/script", bucketScripts},
	{"pkg/immutability", bucketScripts},
	// Media + YouTube (modalities that touch the assets surface).
	{"internal/application/youtube", bucketMedia},
	{"internal/capabilities/youtube", bucketMedia},
	{"internal/application/media", bucketMedia},
	{"pkg/youtube", bucketMedia},
	// Clip / pre-planner.
	{"internal/application/clip", bucketClip},
	{"pkg/clip", bucketClip},
	// Monitor + channels.
	{"internal/application/monitor", bucketMonitor},
	{"internal/application/channels", bucketMonitor},
	{"pkg/monitor", bucketMonitor},
	// Search (mediasearch, etc.).
	{"internal/api/mediasearch", bucketSearch},
	{"internal/application/search", bucketSearch},
	{"pkg/search", bucketSearch},
}

// determineDeprecationBucket maps a record's owner_capability
// (a Go-import-path-style string) onto the subsystem bucket that
// will host its shard under `architecture/deprecations/records/`.
//
// Matching is longest-prefix-wins: a more-specific prefix always
// wins over a less-specific one (e.g. "internal/application/youtube"
// wins over "internal/application"). Records that do not match
// any prefix return bucketMisc so callers can audit the overflow
// set rather than silently dropping the categorization decision.
func determineDeprecationBucket(ownerCapability string) deprecationBucket {
	if ownerCapability == "" {
		return bucketMisc
	}
	bestLen := -1
	bestBucket := bucketMisc
	for _, rule := range subsystemPrefixes {
		if rule.bucket == bucketMisc {
			continue
		}
		if strings.HasPrefix(ownerCapability, rule.prefix) &&
			len(rule.prefix) > bestLen {
			bestLen = len(rule.prefix)
			bestBucket = rule.bucket
		}
	}
	return bestBucket
}

// groupDeprecationsByBucket partitions the registry's records
// into per-bucket slices, preserving the original Deprecations
// order within each bucket so test assertions against the legacy
// single-file order remain meaningful.
//
// Empty records (zero value) are routed to bucketMisc and
// surfaced as a single violation-length warning so authoring
// tools do not silently lose records during the planned manual
// split.
func groupDeprecationsByBucket(records []deprecationRecord) map[deprecationBucket][]deprecationRecord {
	grouped := make(map[deprecationBucket][]deprecationRecord, len(subsystemPrefixes)+1)
	for _, rec := range records {
		bucket := determineDeprecationBucket(rec.OwnerCapability)
		grouped[bucket] = append(grouped[bucket], rec)
	}
	return grouped
}

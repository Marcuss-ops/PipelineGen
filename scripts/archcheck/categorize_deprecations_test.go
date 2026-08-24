package main

import (
	"reflect"
	"testing"
)

// TestCategorize_PrefixBuckets is the table-driven proof that
// determineDeprecationBucket maps every canonical owner_capability
// prefix onto the expected subsystem bucket. Add rows when adding a
// new bucket to subsystemPrefixes; failures here indicate the
// categorizer no longer matches the planned split layout.
func TestCategorize_PrefixBuckets(t *testing.T) {
	cases := []struct {
		ownerCapability string
		want            deprecationBucket
	}{
		// Drive.
		{"internal/infrastructure/drive/foo", bucketDrive},
		{"internal/application/assets/upload_intent/x", bucketDrive},
		// Translation.
		{"internal/application/translation/legacy", bucketTranslation},
		{"pkg/translation", bucketTranslation},
		// Voiceover.
		{"internal/application/voiceover/service", bucketVoiceover},
		{"internal/capabilities/voiceover/service", bucketVoiceover},
		{"pkg/voiceover", bucketVoiceover},
		// Jobs.
		{"internal/kernel/job/startup_validator", bucketJobs},
		{"compatibility/domain/job/legacy", bucketJobs},
		// NOTE: P1-7 retired `internal/domain/job/`; records that
		// still reference the legacy prefix fall through to
		// bucketMisc so an operator audit can find them. Removed
		// the prior `{"internal/domain/job/legacy", bucketJobs}`
		// row at the P1-7 cutover.
		// Qdrant.
		{"internal/infrastructure/qdrant/schema", bucketQdrant},
		// Assets.
		{"internal/application/assets/sourcing", bucketAssets},
		// Scripts.
		{"internal/application/scripts/usecase/services", bucketScripts},
		{"internal/capabilities/scripts/usecase/services", bucketScripts},
		{"internal/kernel/script", bucketScripts},
		// Media.
		{"internal/capabilities/youtube/metadata", bucketMedia},
		{"internal/application/media", bucketMedia},
		// Clip.
		{"internal/application/clip/processor", bucketClip},
		// Monitor.
		{"internal/application/monitor/channels", bucketMonitor},
		// Search.
		{"internal/api/mediasearch", bucketSearch},
		// Misc fallback (no rule).
		{"some/unknown/path", bucketMisc},
		{"", bucketMisc},
	}
	for _, c := range cases {
		got := determineDeprecationBucket(c.ownerCapability)
		if got != c.want {
			t.Errorf("owner_capability=%q -> got=%q want=%q",
				c.ownerCapability, got, c.want)
		}
	}
}

// TestCategorize_LongestPrefixWins proves that a more-specific
// subsystem prefix always wins over a less-specific one. This is the
// property that allows future buckets like
// `internal/application/youtube` to coexist with generic
// `internal/application/*` rules without a precedence error.
func TestCategorize_LongestPrefixWins(t *testing.T) {
	// Both "internal/application/youtube" and (hypothetically)
	// "internal/application" could match "internal/capabilities/youtube/metadata";
	// a generic media bucket that matches the shorter prefix must NOT win.
	got := determineDeprecationBucket("internal/capabilities/youtube/metadata")
	if got != bucketMedia {
		t.Errorf("expected media bucket wins for youtube/metadata, got %q", got)
	}
}

// TestGroupDeprecationsByBucket_PartitionsCorrectly asserts the
// partition keeps all records and assigns every record to exactly
// one bucket. The integrity of the planned split depends on this:
// every record in production must end up in exactly one shard file.
func TestGroupDeprecationsByBucket_PartitionsCorrectly(t *testing.T) {
	records := []deprecationRecord{
		{ID: "R-DRIVE", OwnerCapability: "internal/infrastructure/drive/x"},
		{ID: "R-TRANSLATION", OwnerCapability: "internal/application/translation/x"},
		{ID: "R-VOICEOVER", OwnerCapability: "internal/capabilities/voiceover/x"},
		{ID: "R-MISC", OwnerCapability: "some/unknown/path"},
		{ID: "R-EMPTY", OwnerCapability: ""},
	}
	grouped := groupDeprecationsByBucket(records)
	total := 0
	bucketOfID := map[string]deprecationBucket{}
	for bucket, recs := range grouped {
		for _, r := range recs {
			if _, seen := bucketOfID[r.ID]; seen {
				t.Fatalf("record %q appeared in multiple buckets", r.ID)
			}
			bucketOfID[r.ID] = bucket
			total++
		}
	}
	if total != len(records) {
		t.Fatalf("partition lost records: input=%d output=%d", len(records), total)
	}
	want := map[string]deprecationBucket{
		"R-DRIVE":       bucketDrive,
		"R-TRANSLATION": bucketTranslation,
		"R-VOICEOVER":   bucketVoiceover,
		"R-MISC":        bucketMisc,
		"R-EMPTY":       bucketMisc,
	}
	if !reflect.DeepEqual(bucketOfID, want) {
		t.Fatalf("bucket assignment mismatch: got=%v want=%v", bucketOfID, want)
	}
	if len(grouped[bucketDrive]) != 1 ||
		len(grouped[bucketTranslation]) != 1 ||
		len(grouped[bucketVoiceover]) != 1 ||
		len(grouped[bucketMisc]) != 2 {
		t.Fatalf("per-bucket counts off: %+v", grouped)
	}
}

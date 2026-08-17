package usecase

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func docsItem() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:     "docs-item",
		Title:  "Docs Script",
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic"},
	}
}

func TestValidateItemDocsFailClosedWithoutFolder(t *testing.T) {
	item := docsItem()
	item.Docs = scriptpkg.DocumentsSpec{Enabled: true, Languages: []string{"it"}}
	err := ValidateItem(item)
	if err == nil {
		t.Fatal("docs.enabled=true with no resolvable folder must fail validation")
	}
	var planErr *scriptpkg.PlanInvalidError
	if !errors.As(err, &planErr) {
		t.Fatalf("expected *PlanInvalidError, got %T", err)
	}
	found := false
	for _, d := range planErr.Details {
		if strings.Contains(d, "docs enabled but no script docs folder configured") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected docs folder failure detail, got %v", planErr.Details)
	}
}

func TestValidateItemDocsResolvedFolderPasses(t *testing.T) {
	item := docsItem()
	item.Docs = scriptpkg.DocumentsSpec{Enabled: true, Languages: []string{"it"}, FolderID: "RESOLVED_FOLDER"}
	if err := ValidateItem(item); err != nil {
		t.Fatalf("docs.enabled=true with a resolved folder must validate: %v", err)
	}
}

func TestValidateDocsDisabledNeverFails(t *testing.T) {
	item := docsItem()
	item.Docs = scriptpkg.DocumentsSpec{Enabled: false}
	if err := ValidateItem(item); err != nil {
		t.Fatalf("docs disabled must not fail: %v", err)
	}
}

func TestValidateSegmentIDsUniqueAndEmpty(t *testing.T) {
	item := scriptpkg.GenerationItemV2{
		ScriptParams: scriptpkg.ScriptSpec{
			Segments: []scriptpkg.ScriptSegment{
				{ID: "intro", Topic: "Intro"},
				{ID: "body", Topic: "Body"},
				{Topic: "Legacy segment without id"},
			},
		},
	}
	got := validateSegmentIDs(item, "item")
	if len(got) != 0 {
		t.Fatalf("expected no errors, got %v", got)
	}
}

func TestValidateSegmentIDsDuplicate(t *testing.T) {
	item := scriptpkg.GenerationItemV2{
		ScriptParams: scriptpkg.ScriptSpec{
			Segments: []scriptpkg.ScriptSegment{
				{ID: "dup", Topic: "First"},
				{ID: "dup", Topic: "Second"},
			},
		},
	}
	got := validateSegmentIDs(item, "item")
	if len(got) != 1 {
		t.Fatalf("expected 1 error, got %v", got)
	}
	if got[0] != "item: script_params.segments[1].id \"dup\" is duplicate" {
		t.Fatalf("unexpected error: %s", got[0])
	}
}

func TestValidateMediaPlanValid(t *testing.T) {
	mp := media.MediaPlanSpec{
		Mode: "hybrid",
		Assignments: []media.SegmentMediaAssignment{
			{SegmentID: "s1", Slot: "primary_video", Asset: media.MediaRef{Kind: "clip", ClipID: "clip-1"}},
		},
		Searches: []media.SegmentMediaSearch{
			{SegmentID: "s2", Slot: "secondary_image", Limit: 5},
		},
	}
	segments := []scriptpkg.ScriptSegment{
		{ID: "s1", Topic: "Seg 1"},
		{ID: "s2", Topic: "Seg 2"},
	}
	got := validateMediaPlan(mp, segments, "item")
	if len(got) != 0 {
		t.Fatalf("expected no errors, got %v", got)
	}
}

func TestValidateMediaPlanDuplicateAssignment(t *testing.T) {
	mp := media.MediaPlanSpec{
		Assignments: []media.SegmentMediaAssignment{
			{SegmentID: "s1", Slot: "primary_video", Asset: media.MediaRef{Kind: "clip", ClipID: "a"}},
			{SegmentID: "s1", Slot: "primary_video", Asset: media.MediaRef{Kind: "clip", ClipID: "b"}},
		},
	}
	segments := []scriptpkg.ScriptSegment{{ID: "s1", Topic: "Seg 1"}}
	got := validateMediaPlan(mp, segments, "item")
	if len(got) != 1 {
		t.Fatalf("expected 1 error, got %v", got)
	}
	if got[0] != "item: media_plan.assignments[1]: duplicate assignment for segment_id \"s1\" and slot \"primary_video\"" {
		t.Fatalf("unexpected error: %s", got[0])
	}
}

func TestValidateMediaPlanUnknownSlotAndInvalidMode(t *testing.T) {
	mp := media.MediaPlanSpec{
		Mode: "unknown_mode",
		Assignments: []media.SegmentMediaAssignment{
			{SegmentID: "s1", Slot: "unknown_slot", Asset: media.MediaRef{Kind: "clip", ClipID: "a"}},
		},
	}
	segments := []scriptpkg.ScriptSegment{{ID: "s1", Topic: "Seg 1"}}
	got := validateMediaPlan(mp, segments, "item")
	if len(got) != 2 {
		t.Fatalf("expected 2 errors, got %v", got)
	}
}

func TestValidateMediaPlanMissingClipID(t *testing.T) {
	mp := media.MediaPlanSpec{
		Assignments: []media.SegmentMediaAssignment{
			{SegmentID: "s1", Slot: "primary_video", Asset: media.MediaRef{Kind: "clip", AssetID: "asset-1"}},
		},
	}
	segments := []scriptpkg.ScriptSegment{{ID: "s1", Topic: "Seg 1"}}
	got := validateMediaPlan(mp, segments, "item")
	if len(got) != 1 {
		t.Fatalf("expected 1 error, got %v", got)
	}
	if got[0] != "item: media_plan.assignments[0]: asset.kind=clip requires clip_id" {
		t.Fatalf("unexpected error: %s", got[0])
	}
}

func TestValidateMediaPlanSegmentIDMismatch(t *testing.T) {
	mp := media.MediaPlanSpec{
		Searches: []media.SegmentMediaSearch{
			{SegmentID: "missing", Slot: "primary_video"},
		},
	}
	segments := []scriptpkg.ScriptSegment{{ID: "s1", Topic: "Seg 1"}}
	got := validateMediaPlan(mp, segments, "item")
	if len(got) != 1 {
		t.Fatalf("expected 1 error, got %v", got)
	}
	if got[0] != "item: media_plan.searches[0]: segment_id \"missing\" does not match any segment" {
		t.Fatalf("unexpected error: %s", got[0])
	}
}

func TestValidateMediaRefCases(t *testing.T) {
	tests := []struct {
		name    string
		ref     media.MediaRef
		wantErr string
	}{
		{
			name:    "empty kind",
			ref:     media.MediaRef{AssetID: "x"},
			wantErr: "asset.kind is required",
		},
		{
			name:    "clip without clip_id",
			ref:     media.MediaRef{Kind: "clip", AssetID: "x"},
			wantErr: "asset.kind=clip requires clip_id",
		},
		{
			name:    "stock without asset_id or provider",
			ref:     media.MediaRef{Kind: "stock", SourceURL: "http://x"},
			wantErr: "asset.kind=stock requires asset_id or provider+provider_asset_id",
		},
		{
			name:    "stock with provider",
			ref:     media.MediaRef{Kind: "stock", Provider: "drive", ProviderAssetID: "x"},
			wantErr: "",
		},
		{
			name:    "image with source_url",
			ref:     media.MediaRef{Kind: "image", SourceURL: "http://x"},
			wantErr: "",
		},
		{
			name:    "invalid time range",
			ref:     media.MediaRef{Kind: "clip", ClipID: "c", StartMs: 5000, EndMs: 1000},
			wantErr: "start_ms must be less than end_ms",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateMediaRef(tc.ref, "prefix")
			if tc.wantErr == "" {
				if got != "" {
					t.Fatalf("expected no error, got %q", got)
				}
				return
			}
			if got != "prefix: "+tc.wantErr {
				t.Fatalf("expected error %q, got %q", "prefix: "+tc.wantErr, got)
			}
		})
	}
}

package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"
)

func renderIdentityTestJob(plan string, assetHash string) RenderQueueJob {
	return RenderQueueJob{
		ID:          "job-1",
		ParentJobID: "run-1",
		OverlaySpec: json.RawMessage(plan),
		Assets: []RenderQueueAsset{{
			SHA256: assetHash,
			URL:    "assets/source.mp4",
		}},
	}
}

func TestRenderRequestIdentityIsCanonicalAcrossPlanKeyOrder(t *testing.T) {
	first := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","width":1920,"height":1080}`, strings.Repeat("a", 64))
	second := renderIdentityTestJob(`{"height":1080,"schema_version":"chronon.render-plan.v2","width":1920}`, strings.Repeat("a", 64))

	firstIdentity, err := RenderRequestIdentityFor(first)
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	secondIdentity, err := RenderRequestIdentityFor(second)
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if firstIdentity != secondIdentity {
		t.Fatalf("identity is not canonical across plan key order: %+v != %+v", firstIdentity, secondIdentity)
	}
	if firstIdentity.Digest() != secondIdentity.Digest() {
		t.Fatalf("digest is not canonical: %q != %q", firstIdentity.Digest(), secondIdentity.Digest())
	}
	if firstIdentity.IdempotencyKey(first.ID) != secondIdentity.IdempotencyKey(second.ID) {
		t.Fatalf("idempotency key is not canonical for identical content")
	}
}

func TestRenderRequestIdentityChangesWithPlanOrAssetContent(t *testing.T) {
	base := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","width":1920,"height":1080}`, strings.Repeat("a", 64))
	baseIdentity, err := RenderRequestIdentityFor(base)
	if err != nil {
		t.Fatalf("base identity: %v", err)
	}

	changedPlan := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","width":1280,"height":720}`, strings.Repeat("a", 64))
	changedPlanIdentity, err := RenderRequestIdentityFor(changedPlan)
	if err != nil {
		t.Fatalf("changed plan identity: %v", err)
	}
	if baseIdentity.PlanSHA256 == changedPlanIdentity.PlanSHA256 || baseIdentity.Digest() == changedPlanIdentity.Digest() {
		t.Fatalf("plan change did not change the identity")
	}

	changedAssets := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","width":1920,"height":1080}`, strings.Repeat("b", 64))
	changedAssetsIdentity, err := RenderRequestIdentityFor(changedAssets)
	if err != nil {
		t.Fatalf("changed assets identity: %v", err)
	}
	if baseIdentity.AssetsSHA256 == changedAssetsIdentity.AssetsSHA256 || baseIdentity.Digest() == changedAssetsIdentity.Digest() {
		t.Fatalf("asset change did not change the identity")
	}
}

func TestRenderRequestIdentityBindsPreparedPackageVersionAndContent(t *testing.T) {
	base := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2"}`, strings.Repeat("a", 64))
	base.PreparedRenderPackage = &RenderQueuePreparedPackageRef{
		PackageID: strings.Repeat("b", 64), PlanSHA256: strings.Repeat("c", 64),
		AssetManifestSHA: strings.Repeat("d", 64), TotalFrames: 300, CompilerVersion: "chronon-1", ProfileID: "profile-1",
	}
	baseIdentity, err := RenderRequestIdentityFor(base)
	if err != nil {
		t.Fatalf("base identity: %v", err)
	}
	changed := base
	changed.PreparedRenderPackage = &RenderQueuePreparedPackageRef{
		PackageID: strings.Repeat("b", 64), PlanSHA256: strings.Repeat("c", 64),
		AssetManifestSHA: strings.Repeat("d", 64), TotalFrames: 300, CompilerVersion: "chronon-2", ProfileID: "profile-1",
	}
	changedIdentity, err := RenderRequestIdentityFor(changed)
	if err != nil {
		t.Fatalf("changed package identity: %v", err)
	}
	if baseIdentity.Digest() == changedIdentity.Digest() {
		t.Fatal("compiler version drift did not change the immutable request identity")
	}
	changed = base
	changed.PreparedRenderPackage = &RenderQueuePreparedPackageRef{
		PackageID: strings.Repeat("b", 64), PlanSHA256: strings.Repeat("c", 64),
		AssetManifestSHA: strings.Repeat("d", 64), TotalFrames: 301, CompilerVersion: "chronon-1", ProfileID: "profile-1",
	}
	changedIdentity, err = RenderRequestIdentityFor(changed)
	if err != nil {
		t.Fatalf("changed total frames identity: %v", err)
	}
	if baseIdentity.Digest() == changedIdentity.Digest() {
		t.Fatal("prepared package total frame drift did not change the immutable request identity")
	}
}

func TestRenderRequestIdentityBindsDispatchAndFrameRange(t *testing.T) {
	base := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2"}`, strings.Repeat("a", 64))
	base.JobType = "overlay.render"
	base.ChunkIndex = 2
	base.FrameRange = &RenderFrameRange{Start: 96, End: 144}
	baseIdentity, err := RenderRequestIdentityFor(base)
	if err != nil {
		t.Fatalf("base identity: %v", err)
	}
	variants := []struct {
		name string
		edit func(*RenderQueueJob)
	}{
		{"job type", func(job *RenderQueueJob) { job.JobType = "overlay.prepare" }},
		{"chunk index", func(job *RenderQueueJob) { job.ChunkIndex++ }},
		{"frame range", func(job *RenderQueueJob) { job.FrameRange.End++ }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			changed := base
			changed.FrameRange = &RenderFrameRange{Start: base.FrameRange.Start, End: base.FrameRange.End}
			variant.edit(&changed)
			identity, err := RenderRequestIdentityFor(changed)
			if err != nil {
				t.Fatalf("changed identity: %v", err)
			}
			if identity.Digest() == baseIdentity.Digest() {
				t.Fatalf("%s change did not change the request identity", variant.name)
			}
		})
	}
}

func TestRenderRequestFamilyIdentityBindsMembershipOrderAndRanges(t *testing.T) {
	anchor := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","audio":true}`, strings.Repeat("a", 64))
	anchor.ID = "family-anchor"
	anchor.JobType = "render.segment"
	first := renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","audio":false}`, strings.Repeat("a", 64))
	first.ID, first.ParentJobID = "family-chunk-1", anchor.ID
	first.JobType, first.ChunkIndex = "render.segment", 1
	first.FrameRange = &RenderFrameRange{Start: 0, End: 48}
	second := first
	second.ID, second.ChunkIndex = "family-chunk-2", 2
	second.FrameRange = &RenderFrameRange{Start: 48, End: 96}

	identityFor := func(family []RenderQueueJob) []RenderRequestIdentity {
		t.Helper()
		identities, err := RenderRequestFamilyIdentities(family)
		if err != nil {
			t.Fatalf("family identity: %v", err)
		}
		return identities
	}
	ordered := identityFor([]RenderQueueJob{anchor, first, second})
	reversed := identityFor([]RenderQueueJob{anchor, second, first})
	changedRange := second
	changedRange.FrameRange = &RenderFrameRange{Start: 48, End: 120}
	changed := identityFor([]RenderQueueJob{anchor, first, changedRange})
	if ordered[0].FamilySHA256 == "" || ordered[0].FamilySHA256 != ordered[1].FamilySHA256 || ordered[0].FamilySHA256 != ordered[2].FamilySHA256 {
		t.Fatalf("family digest must bind the same family to every member: %+v", ordered)
	}
	if ordered[0].FamilySHA256 == reversed[0].FamilySHA256 {
		t.Fatal("reordering chunks did not change the family identity")
	}
	if ordered[0].FamilySHA256 == changed[0].FamilySHA256 {
		t.Fatal("changing a chunk frame range did not change the family identity")
	}
}

func TestRenderRequestIdentityRejectsInvalidFrameRange(t *testing.T) {
	job := renderIdentityTestJob(`{}`, strings.Repeat("a", 64))
	job.FrameRange = &RenderFrameRange{Start: 10, End: 10}
	if _, err := RenderRequestIdentityFor(job); err == nil {
		t.Fatal("empty frame range must be rejected")
	}
}

func TestRenderRequestIdentityPreservesLargeJSONIntegers(t *testing.T) {
	first := renderIdentityTestJob(`{"start_us":9007199254740992}`, strings.Repeat("a", 64))
	second := renderIdentityTestJob(`{"start_us":9007199254740993}`, strings.Repeat("a", 64))
	firstIdentity, err := RenderRequestIdentityFor(first)
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	secondIdentity, err := RenderRequestIdentityFor(second)
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if firstIdentity.PlanSHA256 == secondIdentity.PlanSHA256 {
		t.Fatal("distinct large integers must not collapse into the same plan digest")
	}
}

func TestRenderRequestIdentityBindsOperationalID(t *testing.T) {
	identity, err := RenderRequestIdentityFor(renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2"}`, strings.Repeat("a", 64)))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if identity.IdempotencyKey("job-1") == identity.IdempotencyKey("job-2") {
		t.Fatalf("idempotency key must bind the operational job id")
	}
}

func TestRenderRequestIdentityExtractsContractID(t *testing.T) {
	identity, err := RenderRequestIdentityFor(renderIdentityTestJob(`{"schema_version":"chronon.render-plan.v2","output_profile_id":"velox-h264-1080p30-v1"}`, strings.Repeat("a", 64)))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if identity.ContractID != "velox-h264-1080p30-v1" {
		t.Fatalf("contract id = %q, want velox-h264-1080p30-v1", identity.ContractID)
	}
}

func TestRenderRequestIdentityRejectsInvalidPlanAndHashlessAsset(t *testing.T) {
	if _, err := RenderRequestIdentityFor(renderIdentityTestJob(`{not json`, strings.Repeat("a", 64))); err == nil {
		t.Fatal("invalid plan must be rejected")
	}
	if _, err := RenderRequestIdentityFor(renderIdentityTestJob(`{}`, "")); err == nil {
		t.Fatal("asset without a content hash must be rejected")
	}
}

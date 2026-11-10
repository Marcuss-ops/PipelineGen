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

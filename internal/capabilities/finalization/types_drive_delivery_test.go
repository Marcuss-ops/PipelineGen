// types_drive_delivery_test.go — pins the durable delivery-intent contract:
// an incomplete intent fails closed, and the rebuilt envelope keeps the
// routing identity the synchronous publication resolves the Drive tree with.
package finalization

import (
	"strings"
	"testing"
)

func validDeliveryRequest() FinalizeDriveDeliveryRequest {
	return NewFinalizeDriveDeliveryRequest(FinalizeDriveDeliveryRequest{
		JobID: "job-1", AssetID: "job-1:script_json", ArtifactID: "job-1:script_json",
		Kind: KindScript, Filename: "script.json", MIMEType: "application/json",
		SHA256: strings.Repeat("a", 64), SizeBytes: 128, IdempotencyKey: "job-1:script_json",
		Requirement: ArtifactRequirementRequired, StorageKey: strings.Repeat("a", 64),
		ProjectID: "golden", Language: "it", DriveSubpath: []string{"video", "script"},
		ArtifactMetadata: map[string]any{"script_name": "golden"},
	})
}

func TestFinalizeDriveDeliveryRequestValidate(t *testing.T) {
	if err := validDeliveryRequest().Validate(); err != nil {
		t.Fatalf("complete intent must validate: %v", err)
	}
	mutations := map[string]func(*FinalizeDriveDeliveryRequest){
		"schema":                    func(r *FinalizeDriveDeliveryRequest) { r.SchemaVersion = "v9" },
		"asset":                     func(r *FinalizeDriveDeliveryRequest) { r.AssetID = "" },
		"storage key":               func(r *FinalizeDriveDeliveryRequest) { r.StorageKey = "" },
		"digest":                    func(r *FinalizeDriveDeliveryRequest) { r.SHA256 = "" },
		"locator/digest divergence": func(r *FinalizeDriveDeliveryRequest) { r.SHA256 = strings.Repeat("b", 64) },
		"size":                      func(r *FinalizeDriveDeliveryRequest) { r.SizeBytes = 0 },
		"requirement":               func(r *FinalizeDriveDeliveryRequest) { r.Requirement = ArtifactRequirementInvalid },
		"idempotency":               func(r *FinalizeDriveDeliveryRequest) { r.IdempotencyKey = "" },
	}
	for name, mutate := range mutations {
		req := validDeliveryRequest()
		mutate(&req)
		if err := req.Validate(); err == nil {
			t.Errorf("%s: incomplete intent must fail closed", name)
		}
	}
}

func TestFinalizeDriveDeliveryRequestToVerifiedArtifact(t *testing.T) {
	req := validDeliveryRequest()
	va := req.ToVerifiedArtifact()
	if va.ArtifactID != req.ArtifactID || va.Kind != KindScript || va.Requirement != ArtifactRequirementRequired {
		t.Fatalf("rebuilt envelope = %+v, lost identity", va)
	}
	if va.ProjectID != "golden" || va.Language != "it" || len(va.DriveSubpath) != 2 {
		t.Fatalf("rebuilt routing = %+v, want project/language/subpath preserved", va)
	}
	if va.LocalPath != "" {
		t.Fatalf("rebuilt LocalPath = %q, want empty (the consumer resolves the storage key)", va.LocalPath)
	}
}

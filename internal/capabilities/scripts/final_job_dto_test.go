package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildFinalJobEnvelopeV1TypesPlanAndSerializesDeployedWire(t *testing.T) {
	envelope, err := BuildFinalJobEnvelopeV1(context.Background(), "run-phrase", GenerateRequest{
		Title: "Garlasco", SourceLanguage: "it",
	}, phraseOnlyFinalJobResult("scene-phrase"), finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobEnvelopeV1: %v", err)
	}
	if envelope.SchemaVersion != FinalJobEnvelopeSchemaV1 || envelope.RunID != "run-phrase" {
		t.Fatalf("envelope identity = schema %q run %q", envelope.SchemaVersion, envelope.RunID)
	}
	if len(envelope.Plan.VideoTracks) == 0 || len(envelope.Plan.Assets) == 0 || envelope.Plan.FinalAudio.AssetID == "" {
		t.Fatalf("compiled plan DTO lost typed timeline/audio/assets: %+v", envelope.Plan)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	prepare, finalize, err := envelope.MasterPayloads()
	if err != nil {
		t.Fatalf("MasterPayloads: %v", err)
	}
	if _, exists := prepare["schema_version"]; exists {
		t.Fatal("producer DTO version must not invent an unsupported Master wire field")
	}
	if prepare["job_type"] != "video.render.v2" || prepare["compiled_render_plan_sha256"] != envelope.Prepare.CompiledRenderPlanSHA256 {
		t.Fatalf("PREPARE payload = %#v", prepare)
	}
	assets, ok := finalize["runtime_assets"].([]any)
	if !ok || len(assets) == 0 {
		t.Fatalf("runtime_assets = %#v", finalize["runtime_assets"])
	}
	var roundTrip FinalJobFinalizeV1
	encoded, err := json.Marshal(finalize)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("decode serialized FINALIZE DTO: %v", err)
	}
	if roundTrip.RuntimeAssets[0].AssetID == "" || len(roundTrip.RuntimeAssets[0].SHA256) != 64 {
		t.Fatalf("asset identity was not preserved: %+v", roundTrip.RuntimeAssets[0])
	}
}

func TestFinalJobEnvelopeV1PreservesCurrentMasterWireBytes(t *testing.T) {
	result := phraseOnlyFinalJobResult("scene-phrase")
	req := GenerateRequest{Title: "Garlasco", SourceLanguage: "it"}
	legacyPrepare, legacyFinalize, err := BuildFinalJobPayloads(context.Background(), "run-phrase", req, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("legacy payload builder: %v", err)
	}
	envelope, err := BuildFinalJobEnvelopeV1(context.Background(), "run-phrase", req, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("typed payload builder: %v", err)
	}
	typedPrepare, typedFinalize, err := envelope.MasterPayloads()
	if err != nil {
		t.Fatalf("MasterPayloads: %v", err)
	}
	for _, pair := range []struct {
		name string
		old  any
		new  any
	}{{"PREPARE", legacyPrepare, typedPrepare}, {"FINALIZE", legacyFinalize, typedFinalize}} {
		oldJSON, err := json.Marshal(pair.old)
		if err != nil {
			t.Fatal(err)
		}
		newJSON, err := json.Marshal(pair.new)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(oldJSON, newJSON) {
			t.Fatalf("typed %s wire changed\nlegacy: %s\ntyped:  %s", pair.name, oldJSON, newJSON)
		}
	}
}

func TestFinalJobEnvelopeV1RejectsPlanOrIdentityDrift(t *testing.T) {
	envelope, err := BuildFinalJobEnvelopeV1(context.Background(), "run-phrase", GenerateRequest{
		Title: "Garlasco", SourceLanguage: "it",
	}, phraseOnlyFinalJobResult("scene-phrase"), finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobEnvelopeV1: %v", err)
	}

	driftedPlan := envelope
	driftedPlan.Plan.DurationUS++
	if err := driftedPlan.Validate(); err == nil || !strings.Contains(err.Error(), "differs from PREPARE bytes") {
		t.Fatalf("plan drift error = %v, want the canonical plan-byte mismatch", err)
	}

	driftedAsset := envelope
	driftedAsset.Finalize.RuntimeAssets = append([]FinalJobRuntimeAssetV1(nil), envelope.Finalize.RuntimeAssets...)
	driftedAsset.Finalize.RuntimeAssets[0].AssetID = ""
	if err := driftedAsset.Validate(); err == nil || !strings.Contains(err.Error(), "immutable identity") {
		t.Fatalf("asset identity error = %v, want fail-closed identity validation", err)
	}
}

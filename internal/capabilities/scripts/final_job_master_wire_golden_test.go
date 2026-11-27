package scriptgeneration

// final_job_master_wire_golden_test.go pins the exact JSON the deployed Master
// receives for one deterministic final-job fixture. The typing refactor moves
// the construction of the stable PREPARE/FINALIZE keys from hand-built maps to
// typed DTOs; this test is the proof that the wire did not move while the
// construction changed.
//
// Regenerate (only after an intentional contract change):
//
//	go test ./internal/capabilities/scripts -run TestFinalJobMasterWireMatchesGolden -update-finaljob-wire-golden

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateFinalJobWireGolden = flag.Bool(
	"update-finaljob-wire-golden", false,
	"rewrite testdata/final_job_master_wire.golden.json from the current builder",
)

type finalJobWireGolden struct {
	Prepare  json.RawMessage `json:"prepare"`
	Finalize json.RawMessage `json:"finalize"`
}

const finalJobWireGoldenPath = "testdata/final_job_master_wire.golden.json"

func readFinalJobWireGolden(t *testing.T) finalJobWireGolden {
	t.Helper()
	raw, err := os.ReadFile(finalJobWireGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update-finaljob-wire-golden once)", finalJobWireGoldenPath, err)
	}
	var golden finalJobWireGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode %s: %v", finalJobWireGoldenPath, err)
	}
	return golden
}

func marshalFinalJobWire(t *testing.T, prepare, finalize map[string]any) finalJobWireGolden {
	t.Helper()
	prepareJSON, err := json.Marshal(prepare)
	if err != nil {
		t.Fatalf("encode PREPARE: %v", err)
	}
	finalizeJSON, err := json.Marshal(finalize)
	if err != nil {
		t.Fatalf("encode FINALIZE: %v", err)
	}
	return finalJobWireGolden{Prepare: prepareJSON, Finalize: finalizeJSON}
}

func assertFinalJobWirePart(t *testing.T, name string, got map[string]any, want json.RawMessage) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	// json.Marshal compacts a RawMessage, so both sides are canonical bytes.
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode golden %s: %v", name, err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s wire moved\n--- got ---\n%s\n--- want ---\n%s", name, gotJSON, wantJSON)
	}
}

func assertFinalJobWire(t *testing.T, got finalJobWireGolden) {
	t.Helper()
	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("encode golden document: %v", err)
	}
	gotJSON = append(gotJSON, '\n')
	if *updateFinalJobWireGolden {
		if err := os.MkdirAll(filepath.Dir(finalJobWireGoldenPath), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(finalJobWireGoldenPath, gotJSON, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(finalJobWireGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v", finalJobWireGoldenPath, err)
	}
	if !bytes.Equal(gotJSON, want) {
		t.Fatalf("final-job Master wire moved\n--- got ---\n%s\n--- want ---\n%s", gotJSON, want)
	}
}

// TestFinalJobMasterWireMatchesGolden builds the deployed PREPARE/FINALIZE for
// a certified phrase-only run and compares them with the committed bytes.
func TestFinalJobMasterWireMatchesGolden(t *testing.T) {
	prepare, finalize, err := BuildFinalJobPayloads(
		context.Background(), "run-phrase",
		GenerateRequest{Title: "Garlasco", SourceLanguage: "it"},
		phraseOnlyFinalJobResult("scene-phrase"), finalJobPayloadResolver{},
	)
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	assertFinalJobWire(t, marshalFinalJobWire(t, prepare, finalize))
}

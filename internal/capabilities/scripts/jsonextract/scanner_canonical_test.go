package jsonextract_test

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jsonextract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestScannerCanonicalV1(t *testing.T) {
	raw := []byte(`{"schema_version":1,"text":"Opening.","specscene":{"version":1,"scenes":[{"id":"scene-1","index":0,"text":"Opening.","kind":"narration"}]}}`)
	out, err := jsonextract.NewScanner(jsonextract.ModeFreshPlainText).Scan(raw, "test")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.Text != "Opening." || len(out.SpecScene.Scenes) != 1 {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestScannerCanonicalPlainProse(t *testing.T) {
	const prose = "The champion enters the ring."
	out, err := jsonextract.NewScanner(jsonextract.ModeFreshPlainText).Scan([]byte(prose), "test")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.Text != prose || len(out.SpecScene.Scenes) != 0 {
		t.Fatalf("unexpected prose output: %#v", out)
	}
}

func TestScannerCanonicalRejectsMalformedJSON(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"schema_version":1,"text":"bad"`),
		[]byte(`{"schema_version":99,"text":"bad","specscene":{"version":1,"scenes":[]}}`),
	} {
		_, err := jsonextract.NewScanner(jsonextract.ModeFreshPlainText).Scan(raw, "test")
		if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
			t.Fatalf("err=%v, want ErrModelOutputMalformed", err)
		}
	}
}

func TestScannerCanonicalRejectsEmpty(t *testing.T) {
	for _, raw := range [][]byte{nil, {}} {
		_, err := jsonextract.NewScanner(jsonextract.ModeFreshPlainText).Scan(raw, "test")
		if !errors.Is(err, scriptpkg.ErrModelOutputMalformed) {
			t.Fatalf("err=%v, want ErrModelOutputMalformed", err)
		}
	}
}

func TestScannerNilReceiverUsesCanonicalMode(t *testing.T) {
	var scanner *jsonextract.Scanner
	out, err := scanner.Scan([]byte("plain prose"), "test")
	if err != nil || out.Text != "plain prose" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestScannerNormalizesNestedTextEnvelope(t *testing.T) {
	raw := []byte(`{"schema_version":1,"text":"{\"schema_version\":1,\"text\":\"Nested prose.\",\"specscene\":{\"version\":1,\"scenes\":[]}}","specscene":{"version":1,"scenes":[]}}`)
	out, err := jsonextract.NewScanner(jsonextract.ModeFreshPlainText).Scan(raw, "test")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.Text != "Nested prose." {
		t.Fatalf("text=%q, want Nested prose.", out.Text)
	}
}

package payloadcap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnforceLeavesPayloadUnderLimitUntouched(t *testing.T) {
	raw := `{"result":"ok"}`
	got, truncated := Enforce(raw, 1024)
	if truncated {
		t.Fatal("payload under the limit must not be truncated")
	}
	if got != raw {
		t.Fatalf("payload changed: got %q want %q", got, raw)
	}
}

func TestEnforceReturnsExplicitValidJSONEnvelope(t *testing.T) {
	raw := strings.Repeat("x", 4096)
	got, truncated := Enforce(raw, 1024)
	if !truncated {
		t.Fatal("payload over the limit must report truncation")
	}

	var env struct {
		Truncated    bool   `json:"truncated"`
		LimitBytes   int    `json:"limit_bytes"`
		OriginalSize int    `json:"original_bytes"`
		OriginalHash string `json:"original_sha256"`
		Head         string `json:"head"`
		Tail         string `json:"tail"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v (%q)", err, got)
	}
	if !env.Truncated {
		t.Error("envelope must set truncated=true")
	}
	if env.OriginalSize != len(raw) {
		t.Errorf("original_bytes = %d, want %d", env.OriginalSize, len(raw))
	}
	if env.LimitBytes != 1024 {
		t.Errorf("limit_bytes = %d, want 1024", env.LimitBytes)
	}
	sum := sha256.Sum256([]byte(raw))
	if want := hex.EncodeToString(sum[:]); env.OriginalHash != want {
		t.Errorf("original_sha256 = %q, want %q", env.OriginalHash, want)
	}
	if len(env.Head) != excerptBytes || len(env.Tail) != excerptBytes {
		t.Errorf("excerpts must be %d bytes each, got head=%d tail=%d", excerptBytes, len(env.Head), len(env.Tail))
	}
	if !strings.HasPrefix(raw, env.Head) || !strings.HasSuffix(raw, env.Tail) {
		t.Error("excerpts must come from the original payload")
	}
}

func TestEnforceNonPositiveLimitDisablesCap(t *testing.T) {
	raw := strings.Repeat("y", 2048)
	got, truncated := Enforce(raw, 0)
	if truncated || got != raw {
		t.Fatalf("limit<=0 must disable the cap, got truncated=%v len=%d", truncated, len(got))
	}
}

func TestEnforceResultAndReportLimits(t *testing.T) {
	result := strings.Repeat("r", ResultBytes+1)
	if _, truncated := EnforceResult(result); !truncated {
		t.Error("result one byte over ResultBytes must be truncated")
	}
	report := strings.Repeat("p", ReportBytes+1)
	if _, truncated := EnforceReport(report); !truncated {
		t.Error("report one byte over ReportBytes must be truncated")
	}
	if _, truncated := EnforceResult(strings.Repeat("r", ResultBytes)); truncated {
		t.Error("result exactly at the limit must not be truncated")
	}
}

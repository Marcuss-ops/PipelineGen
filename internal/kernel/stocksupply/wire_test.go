package stocksupply

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Validation ─────────────────────────────────────────────────────────────

func TestContractValidate_EmptyModeDefaultsToOff_EmptyQueriesOk(t *testing.T) {
	c := Contract{}
	if err := c.Validate(); err != nil {
		t.Fatalf("empty Contract should be valid: %v", err)
	}
	q := c.ToSupplyQuery()
	if q.Mode != ModeOff {
		t.Fatalf("empty mode → ModeOff, got %s", q.Mode)
	}
}

func TestContractValidate_ActiveModeRequiresQueries(t *testing.T) {
	for _, mode := range []SupplyMode{ModePrefetch, ModeFallback, ModeHybrid} {
		t.Run(string(mode), func(t *testing.T) {
			c := Contract{Mode: mode}
			err := c.Validate()
			if err == nil {
				t.Fatalf("mode=%s with empty queries should fail", mode)
			}
			if !strings.Contains(err.Error(), "queries list is empty") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestContractValidate_BadMode(t *testing.T) {
	c := Contract{Mode: "invalid", Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("bad mode should fail")
	}
	if !strings.Contains(err.Error(), "unsupported mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContractValidate_BadStrategy(t *testing.T) {
	c := Contract{ProviderStrategy: "bad", Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("bad strategy should fail")
	}
	if !strings.Contains(err.Error(), "unsupported provider_strategy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContractValidate_NegativeTargetDuration(t *testing.T) {
	c := Contract{TargetDurationSec: -1, Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("negative target_duration_sec should fail")
	}
}

func TestContractValidate_NegativeMinimumReady(t *testing.T) {
	c := Contract{MinimumReadySec: -5, Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("negative minimum_ready_sec should fail")
	}
}

func TestContractValidate_MinimumAboveTarget(t *testing.T) {
	c := Contract{
		Mode:              ModeHybrid,
		Queries:           []string{"x"},
		TargetDurationSec: 300,
		MinimumReadySec:   600,
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("minimum_ready_sec > target_duration_sec should fail")
	}
	if !strings.Contains(err.Error(), "must be ≤ target") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContractValidate_NegativeSearchLimit(t *testing.T) {
	c := Contract{SearchLimit: -1, Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("negative search_limit should fail")
	}
}

func TestContractValidate_NegativeMaxDownloads(t *testing.T) {
	c := Contract{MaxDownloads: -1, Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("negative max_downloads should fail")
	}
}

func TestContractValidate_NegativeClipBounds(t *testing.T) {
	c := Contract{ClipDuration: ClipDuration{MinSec: -1}, Queries: []string{"x"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("negative clip_duration.min_sec should fail")
	}
}

func TestContractValidate_ClipMinAboveMax(t *testing.T) {
	c := Contract{
		ClipDuration: ClipDuration{MinSec: 45, MaxSec: 15},
		Queries:      []string{"x"},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("min_sec > max_sec should fail")
	}
	if !strings.Contains(err.Error(), "min_sec") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Conversion ─────────────────────────────────────────────────────────────

func TestContractToSupplyQuery_FullMapping(t *testing.T) {
	reuseTrue := true
	reuseFalse := false

	tests := []struct {
		name string
		c    Contract
		want SupplyQuery
	}{
		{
			name: "all fields set",
			c: Contract{
				Mode:              ModeHybrid,
				Queries:           []string{"Mike Tyson interview", "boxing gym"},
				TargetDurationSec: 600,
				MinimumReadySec:   120,
				Providers:         []string{"local", "youtube"},
				ProviderStrategy:  StrategyYouTubeFirst,
				SearchLimit:       10,
				ClipDuration:      ClipDuration{MinSec: 8, MaxSec: 45},
				MaxDownloads:      30,
				ReuseExisting:     &reuseTrue,
			},
			want: SupplyQuery{
				Queries:       []string{"Mike Tyson interview", "boxing gym"},
				Strategy:      StrategyYouTubeFirst,
				Mode:          ModeHybrid,
				Providers:     []string{"local", "youtube"},
				ReuseExisting: true,
				SearchLimit:   10,
				Target: SupplyTarget{
					TargetDurationSec:  600,
					MinimumReadySec:    120,
					MaxClips:           30,
					ClipDurationMinSec: 8,
					ClipDurationMaxSec: 45,
				},
			},
		},
		{
			name: "defaults",
			c:    Contract{Queries: []string{"sunsets"}, Mode: ModePrefetch},
			want: SupplyQuery{
				Queries:       []string{"sunsets"},
				Strategy:      StrategyFallback,
				Mode:          ModePrefetch,
				ReuseExisting: true,
				Target: SupplyTarget{
					TargetDurationSec:  0,
					MinimumReadySec:    0,
					MaxClips:           0,
					ClipDurationMinSec: 0,
					ClipDurationMaxSec: 0,
				},
			},
		},
		{
			name: "explicit reuse false",
			c: Contract{
				Mode:          ModePrefetch,
				Queries:       []string{"x"},
				ReuseExisting: &reuseFalse,
			},
			want: SupplyQuery{
				Queries:       []string{"x"},
				Strategy:      StrategyFallback,
				Mode:          ModePrefetch,
				ReuseExisting: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.c.Validate(); err != nil {
				t.Fatalf("expected valid: %v", err)
			}
			q := tt.c.ToSupplyQuery()
			// Compare key fields
			if q.Mode != tt.want.Mode {
				t.Errorf("Mode: got %s, want %s", q.Mode, tt.want.Mode)
			}
			if q.Strategy != tt.want.Strategy {
				t.Errorf("Strategy: got %s, want %s", q.Strategy, tt.want.Strategy)
			}
			if q.ReuseExisting != tt.want.ReuseExisting {
				t.Errorf("ReuseExisting: got %v, want %v", q.ReuseExisting, tt.want.ReuseExisting)
			}
			if q.SearchLimit != tt.want.SearchLimit {
				t.Errorf("SearchLimit: got %d, want %d", q.SearchLimit, tt.want.SearchLimit)
			}
			if q.Target.TargetDurationSec != tt.want.Target.TargetDurationSec {
				t.Errorf("TargetDurationSec: got %d, want %d", q.Target.TargetDurationSec, tt.want.Target.TargetDurationSec)
			}
			if q.Target.MinimumReadySec != tt.want.Target.MinimumReadySec {
				t.Errorf("MinimumReadySec: got %d, want %d", q.Target.MinimumReadySec, tt.want.Target.MinimumReadySec)
			}
			if q.Target.MaxClips != tt.want.Target.MaxClips {
				t.Errorf("MaxClips: got %d, want %d", q.Target.MaxClips, tt.want.Target.MaxClips)
			}
			if q.Target.ClipDurationMinSec != tt.want.Target.ClipDurationMinSec {
				t.Errorf("ClipDurationMinSec: got %d, want %d", q.Target.ClipDurationMinSec, tt.want.Target.ClipDurationMinSec)
			}
			if q.Target.ClipDurationMaxSec != tt.want.Target.ClipDurationMaxSec {
				t.Errorf("ClipDurationMaxSec: got %d, want %d", q.Target.ClipDurationMaxSec, tt.want.Target.ClipDurationMaxSec)
			}
		})
	}
}

// ── JSON round-trip ───────────────────────────────────────────────────────

func TestContract_JSONRoundTrip(t *testing.T) {
	payload := `{
		"mode": "hybrid",
		"queries": ["Mike Tyson interview", "boxing gym", "knockout"],
		"target_duration_sec": 600,
		"minimum_ready_sec": 120,
		"providers": ["local", "artlist", "youtube"],
		"provider_strategy": "fallback",
		"search_limit": 10,
		"clip_duration": {"min_sec": 8, "max_sec": 45},
		"max_downloads": 30,
		"reuse_existing": true
	}`

	var c Contract
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	q := c.ToSupplyQuery()
	if q.Mode != ModeHybrid {
		t.Errorf("Mode=%s want hybrid", q.Mode)
	}
	if len(q.Queries) != 3 {
		t.Errorf("len(Queries)=%d want 3", len(q.Queries))
	}
	if q.Target.TargetDurationSec != 600 {
		t.Errorf("TargetDurationSec=%d want 600", q.Target.TargetDurationSec)
	}
	if q.Target.MinimumReadySec != 120 {
		t.Errorf("MinimumReadySec=%d want 120", q.Target.MinimumReadySec)
	}
	if q.Target.MaxClips != 30 {
		t.Errorf("MaxClips=%d want 30", q.Target.MaxClips)
	}
	if q.Target.ClipDurationMinSec != 8 || q.Target.ClipDurationMaxSec != 45 {
		t.Errorf("ClipDuration=%d/%d want 8/45", q.Target.ClipDurationMinSec, q.Target.ClipDurationMaxSec)
	}
	if q.Strategy != StrategyFallback {
		t.Errorf("Strategy=%s want fallback", q.Strategy)
	}
	if !q.ReuseExisting {
		t.Error("ReuseExisting should be true")
	}
	if q.SearchLimit != 10 {
		t.Errorf("SearchLimit=%d want 10", q.SearchLimit)
	}
	if len(q.Providers) != 3 {
		t.Errorf("len(Providers)=%d want 3", len(q.Providers))
	}

	// Serialize back (reuse_existing was a *bool, so it should round-trip)
	back, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var c2 Contract
	if err := json.Unmarshal(back, &c2); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	// reuse_existing pointer preserved
	if c2.ReuseExisting == nil || !*c2.ReuseExisting {
		t.Error("reuse_existing not preserved on round-trip")
	}
}

func TestContract_JSONMinimalDefaults(t *testing.T) {
	payload := `{
		"mode": "prefetch",
		"queries": ["sunset beach"]
	}`

	var c Contract
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	q := c.ToSupplyQuery()
	if q.Strategy != StrategyFallback {
		t.Errorf("default strategy should be fallback, got %s", q.Strategy)
	}
	if !q.ReuseExisting {
		t.Error("default reuse should be true")
	}
	if q.Target.ClipDurationMinSec != 0 {
		t.Errorf("default min clip = 0, got %d", q.Target.ClipDurationMinSec)
	}
}

func TestContract_JSONReuseExistingExplicitFalse(t *testing.T) {
	payload := `{"mode":"fallback","queries":["x"],"reuse_existing":false}`
	var c Contract
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if c.ReuseExisting == nil || *c.ReuseExisting {
		t.Fatal("reuse_existing should be false")
	}
	q := c.ToSupplyQuery()
	if q.ReuseExisting {
		t.Error("ToSupplyQuery should preserve explicit false")
	}
}

// ── Result JSON tags ──────────────────────────────────────────────────────

func TestSupplyResult_JSONRoundTrip(t *testing.T) {
	orig := SupplyResult{
		State:            StatePartialReady,
		TotalDurationSec: 85,
		NewAssets:        2,
		ReusedAssets:     1,
		Queries: []SupplyQueryResult{
			{
				Query:           "Mike Tyson interview",
				State:           StateReady,
				DurationSec:     85,
				AssetCount:      3,
				ReuseCount:      1,
				ProviderUsed:    "youtube",
				FallbackReason:  "",
				LocalCandidates: 1,
				SearchMs:        3655,
				DownloadMs:      62734,
				IngestMs:        350,
			},
		},
	}

	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back SupplyResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.State != StatePartialReady {
		t.Errorf("State: got %s", back.State)
	}
	if back.TotalDurationSec != 85 {
		t.Errorf("TotalDurationSec: got %d", back.TotalDurationSec)
	}
	if back.NewAssets != 2 || back.ReusedAssets != 1 {
		t.Errorf("New/Reused: %d/%d", back.NewAssets, back.ReusedAssets)
	}
	if len(back.Queries) != 1 {
		t.Fatalf("Queries len: %d", len(back.Queries))
	}
	q0 := back.Queries[0]
	if q0.Query != "Mike Tyson interview" || q0.State != StateReady {
		t.Errorf("Q[0]: %s/%s", q0.Query, q0.State)
	}
}

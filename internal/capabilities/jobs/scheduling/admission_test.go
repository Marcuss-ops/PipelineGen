package scheduling

import "testing"

func TestAdmissionPolicySlots(t *testing.T) {
	unbounded := int(^uint(0) >> 1)

	cases := []struct {
		name          string
		policy        AdmissionPolicy
		active        int
		promotedToday int
		want          int
	}{
		{"unbounded", AdmissionPolicy{}, 0, 0, unbounded},
		{"concurrency idle", AdmissionPolicy{MaxConcurrent: 1}, 0, 0, 1},
		{"concurrency full", AdmissionPolicy{MaxConcurrent: 1}, 1, 0, 0},
		{"concurrency overdrawn", AdmissionPolicy{MaxConcurrent: 1}, 5, 0, 0},
		{"daily 5000 fresh", AdmissionPolicy{DailyQuota: 5000}, 0, 0, 5000},
		{"daily last slot", AdmissionPolicy{DailyQuota: 5000}, 3, 4999, 1},
		{"daily exhausted", AdmissionPolicy{DailyQuota: 5000}, 3, 5000, 0},
		{"daily overdrawn", AdmissionPolicy{DailyQuota: 5000}, 3, 6000, 0},
		{"tightest wins", AdmissionPolicy{MaxConcurrent: 4, DailyQuota: 5000}, 1, 4999, 1},
		{"concurrency tighter", AdmissionPolicy{MaxConcurrent: 2, DailyQuota: 5000}, 1, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Slots(tc.active, tc.promotedToday); got != tc.want {
				t.Fatalf("Slots(active=%d, promotedToday=%d) = %d, want %d", tc.active, tc.promotedToday, got, tc.want)
			}
		})
	}
}

func TestAdmissionPolicyValidate(t *testing.T) {
	if err := (AdmissionPolicy{}).Validate(); err != nil {
		t.Fatalf("zero policy must be valid: %v", err)
	}
	if err := (AdmissionPolicy{MaxConcurrent: -1}).Validate(); err == nil {
		t.Fatal("negative MaxConcurrent must be rejected")
	}
	if err := (AdmissionPolicy{DailyQuota: -5}).Validate(); err == nil {
		t.Fatal("negative DailyQuota must be rejected")
	}
}

func TestAdmissionPolicyUnbounded(t *testing.T) {
	if !(AdmissionPolicy{}).Unbounded() {
		t.Fatal("zero policy must be unbounded")
	}
	if (AdmissionPolicy{MaxConcurrent: 1}).Unbounded() {
		t.Fatal("a configured ceiling is not unbounded")
	}
}

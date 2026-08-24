// Package api — route_descriptor_test.go (Fase 7(a), Push 7).
//
// Hermetic test pins:
//   - 4-named AuthPolicy values + IsValid rejection of typos.
//   - 10-named Capability values + IsValid rejection of typos.
//   - CanonicalMethodSet covers the 7 HTTP verbs gin supports.
//   - RouteDescriptor.ValidateAll aggregates AuthPolicy + Capability
//     validation into one typed-error surface.
package httpserver

import "testing"

func TestAuthPolicy_IsValid_Accepts4CanonicalValues(t *testing.T) {
	for _, p := range []AuthPolicy{
		AuthPolicyAdmin,
		AuthPolicyWorker,
		AuthPolicyAnonymous,
		AuthPolicyInternal,
	} {
		if !p.IsValid() {
			t.Errorf("AuthPolicy %q must be valid (canonical 4-value set)", string(p))
		}
	}
}

func TestAuthPolicy_IsValid_RejectsTypos(t *testing.T) {
	for _, p := range []AuthPolicy{
		"admine", "Admin", "WORKER", "", "internal-extra",
	} {
		if AuthPolicy(p).IsValid() {
			t.Errorf("AuthPolicy %q must be rejected (typo or empty)", p)
		}
	}
}

func TestCapability_IsValid_Accepts10CanonicalValues(t *testing.T) {
	for _, c := range []Capability{
		CapabilityAssets, CapabilityArtlist, CapabilityYouTube,
		CapabilityScripts, CapabilityImages, CapabilityVoiceover,
		CapabilityContent, CapabilityChannels, CapabilityJobs, CapabilitySystem,
	} {
		if !c.IsValid() {
			t.Errorf("Capability %q must be valid (canonical 10-value set)", string(c))
		}
	}
}

func TestCapability_IsValid_RejectsUnknownValues(t *testing.T) {
	for _, c := range []Capability{
		"unknown", "Assets", "JOBS", "scripts/legacy", "",
		"scriptengine", // ScriptEngine is a service name, not a Capability
	} {
		if Capability(c).IsValid() {
			t.Errorf("Capability %q must be rejected (typo, case-mismatch, or non-canonical)", c)
		}
	}
}

func TestCanonicalMethodSet_CoversGinVerbs(t *testing.T) {
	// The canonical Gin engine supports exactly these 7 verbs.
	want := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	if len(CanonicalMethodSet) != len(want) {
		t.Errorf("CanonicalMethodSet has %d entries; want %d (the 7 gin verbs)",
			len(CanonicalMethodSet), len(want))
	}
	for _, m := range want {
		if !CanonicalMethodSet[m] {
			t.Errorf("missing canonical method %q", m)
		}
	}
}

func TestIsKnownMethod_AcceptsCanonical(t *testing.T) {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		if !IsKnownMethod(m) {
			t.Errorf("IsKnownMethod(%q) must be true", m)
		}
	}
}

func TestIsKnownMethod_AcceptsLowercase(t *testing.T) {
	// IsKnownMethod normalises case (gin accepts both POST and post in
	// practice; the canonical set uses upper).
	for _, m := range []string{"get", "Post", "patch"} {
		if !IsKnownMethod(m) {
			t.Errorf("IsKnownMethod(%q) must accept lowercase variants", m)
		}
	}
}

func TestIsKnownMethod_RejectsUnknown(t *testing.T) {
	for _, m := range []string{"INVALID", "TRACE", "CONNECT", ""} {
		if IsKnownMethod(m) {
			t.Errorf("IsKnownMethod(%q) must reject non-canonical methods", m)
		}
	}
}

func TestRouteDescriptor_ValidateAll_AggregatesPerField(t *testing.T) {
	tests := []struct {
		name     string
		desc     RouteDescriptor
		wantAuth bool
		wantCap  bool
	}{
		{
			name: "all canonical",
			desc: RouteDescriptor{
				Method: "POST", Path: "/api/test",
				AuthPolicy: AuthPolicyAdmin, Capability: CapabilityJobs,
			},
			wantAuth: true, wantCap: true,
		},
		{
			name: "auth typo (cap valid)",
			desc: RouteDescriptor{
				Method: "POST", Path: "/api/test",
				AuthPolicy: "admine", Capability: CapabilityJobs,
			},
			wantAuth: false, wantCap: true,
		},
		{
			name: "cap typo (auth valid)",
			desc: RouteDescriptor{
				Method: "POST", Path: "/api/test",
				AuthPolicy: AuthPolicyWorker, Capability: "unknown",
			},
			wantAuth: true, wantCap: false,
		},
		{
			name: "both invalid",
			desc: RouteDescriptor{
				Method: "POST", Path: "/api/test",
				AuthPolicy: "", Capability: "",
			},
			wantAuth: false, wantCap: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			authOK, capOK := tc.desc.ValidateAll()
			if authOK != tc.wantAuth {
				t.Errorf("authOK = %v, want %v", authOK, tc.wantAuth)
			}
			if capOK != tc.wantCap {
				t.Errorf("capOK = %v, want %v", capOK, tc.wantCap)
			}
		})
	}
}

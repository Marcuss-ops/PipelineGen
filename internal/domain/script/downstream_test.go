// Package script — downstream_test.go (Step 11A canonical downstream-types).
//
// Test scope (per user spec, July 2026):
//   - JSON marshal/unmarshal round-trip preserves typed envelope + pointer fields.
//   - Table test for every preset of combination voiceover×images.
//   - NoInlineAssets defaults to true (NewManifestV2) and stays false on zero-value
//     &ManifestV2{} (legacy-mode sentinel).
//   - IsCanonicalMode is safe on nil receiver (returns false; never panics).
//   - IsValid() rejects any DownstreamKind not in the 3-value canonical enum.
//   - NewVoiceoverRequirements / NewImagesRequirements reject empty voiceID /
//     count<=0 (fail-closed sentinel — nil pointer means "do not construct").
//   - omitempty sanity for pointer sub-structs + DocumentTitle + pace / style_preset.
package script

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── DownstreamKind enum coverage ─────────────────────────────────────

// TestDownstreamKind_IsValid: all 3 canonical values pass; legacy/invalid reject.
func TestDownstreamKind_IsValid(t *testing.T) {
	cases := []struct {
		name  string
		kind  DownstreamKind
		valid bool
	}{
		{"voiceover", DownstreamVoiceover, true},
		{"images", DownstreamImages, true},
		{"both", DownstreamBoth, true},
		{"empty", DownstreamKind(""), false},
		{"legacy_inline_marker", DownstreamKind("inline_voiceover"), false},
		{"capitalised", DownstreamKind("Voiceover"), false},
		{"whitespace", DownstreamKind(" "), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, tc.kind.IsValid(),
				"DownstreamKind(%q).IsValid() expected %v", tc.kind, tc.valid)
		})
	}
}

// ── ManifestV2 NoInlineAssets invariants ─────────────────────────────

// TestNewManifestV2_Defaults: NoInlineAssets=true + Items non-nil + empty.
func TestNewManifestV2_Defaults(t *testing.T) {
	m := NewManifestV2()
	require.NotNil(t, m)
	assert.True(t, m.NoInlineAssets,
		"NoInlineAssets should default true per user spec Step 11A (b)")
	assert.NotNil(t, m.Items,
		"Items should be non-nil []DownstreamRequest (NOT nil slice — distinguish empty from absent)")
	assert.Empty(t, m.Items,
		"fresh Items slice should be empty (NOT nil)")
	assert.True(t, m.IsCanonicalMode(),
		"NewManifestV2() should report canonical mode")
}

// TestManifestV2_ZeroValue_IsLegacyMode: zero-value keeps legacy sentinel
// + IsCanonicalMode=false (intentional back-compat for pre-Step 11A callers).
func TestManifestV2_ZeroValue_IsLegacyMode(t *testing.T) {
	m := &ManifestV2{}
	assert.False(t, m.NoInlineAssets,
		"zero-value &ManifestV2{} keeps NoInlineAssets=false as legacy-mode sentinel")
	assert.False(t, m.IsCanonicalMode(),
		"zero-value &ManifestV2{} should NOT report canonical mode")
	assert.Nil(t, m.Items,
		"zero-value Items is nil (NOT empty []DownstreamRequest{})")
}

// TestManifestV2_NilReceiver_IsCanonicalModeSafe: nil receiver must NOT panic.
func TestManifestV2_NilReceiver_IsCanonicalModeSafe(t *testing.T) {
	var m *ManifestV2
	assert.False(t, m.IsCanonicalMode(),
		"nil receiver must NOT panic; safe-fail to false")
}

// ── Constuctor fail-closed sentinels (NilAcceptance contract) ────────

// TestNewVoiceoverRequirements_EmptyVoiceID_Rejects: empty voiceID ⇒ nil.
func TestNewVoiceoverRequirements_EmptyVoiceID_Rejects(t *testing.T) {
	assert.Nil(t, NewVoiceoverRequirements("edge-tts", "", "", ""),
		"empty voiceID must produce nil (fail-closed: no voice selected)")
	assert.NotNil(t, NewVoiceoverRequirements("edge-tts", "en-US-JennyNeural", "", ""),
		"non-empty voiceID must produce non-nil pointer")
}

// TestNewImagesRequirements_ZeroOrNegativeCount_Rejects: count <= 0 ⇒ nil.
func TestNewImagesRequirements_ZeroOrNegativeCount_Rejects(t *testing.T) {
	assert.Nil(t, NewImagesRequirements(0, ""),
		"zero count must produce nil")
	assert.Nil(t, NewImagesRequirements(-1, "vivid"),
		"negative count must produce nil")
	imgs := NewImagesRequirements(1, "")
	require.NotNil(t, imgs)
	assert.Equal(t, 1, imgs.Count)
	// Defaults applied (FASE 2 canonical):
	assert.Equal(t, "google_slides", imgs.Provider,
		"Provider must default to 'google_slides' per FASE 2 canonical")
	assert.Equal(t, "1920x1080", imgs.Resolution,
		"Resolution must default to '1920x1080' per canonical scene size")
}

// ── Helper pairwise invariants (Kind / sub-struct pairing) ───────────

// TestNewDownstreamRequestVoiceover_KindSubstructPairing: helper enforces
// DownstreamVoiceover route + Voiceover!=nil + Images==nil.
func TestNewDownstreamRequestVoiceover_KindSubstructPairing(t *testing.T) {
	v := NewVoiceoverRequirements("edge-tts", "it-IT-IsabellaNeural", "0.95", "narrative")
	require.NotNil(t, v)
	env := NewDownstreamRequestVoiceover("item-X", true, v, OutputDestination{
		Kind: "drive_folder", FolderID: "F1",
	})
	require.NotNil(t, env)
	assert.Equal(t, DownstreamVoiceover, env.Kind,
		"helper must pin Kind=DownstreamVoiceover")
	assert.Equal(t, "item-X", env.ItemRef)
	assert.True(t, env.Required)
	require.NotNil(t, env.AssetRequirements.Voiceover,
		"voiceover-only helper MUST set AssetRequirements.Voiceover")
	assert.Nil(t, env.AssetRequirements.Images,
		"voiceover-only helper MUST leave AssetRequirements.Images=nil")
	assert.True(t, env.AssetRequirements.Voiceover == v,
		"helper must preserve the supplied voiceover pointer")
}

// TestNewDownstreamRequestImages_KindSubstructPairing: inverse invariant.
func TestNewDownstreamRequestImages_KindSubstructPairing(t *testing.T) {
	i := NewImagesRequirements(4, "realistic")
	require.NotNil(t, i)
	env := NewDownstreamRequestImages("item-Y", false, i, OutputDestination{
		Kind: "google_doc", FolderID: "F2", DocumentTitle: "Doc",
	})
	require.NotNil(t, env)
	assert.Equal(t, DownstreamImages, env.Kind,
		"helper must pin Kind=DownstreamImages")
	assert.Equal(t, "item-Y", env.ItemRef)
	assert.False(t, env.Required)
	require.NotNil(t, env.AssetRequirements.Images,
		"images-only helper MUST set AssetRequirements.Images")
	assert.Nil(t, env.AssetRequirements.Voiceover,
		"images-only helper MUST leave AssetRequirements.Voiceover=nil")
	assert.Equal(t, 4, env.AssetRequirements.Images.Count)
}

// ── JSON round-trip ──────────────────────────────────────────────────

// TestDownstreamRequest_JSONRoundTrip: full envelope survives marshaling.
// Uses reflect.DeepEqual because both typed enum + pointer fields must
// round-trip identically (testify assert.Equal doesn't recurse into pointers).
func TestDownstreamRequest_JSONRoundTrip(t *testing.T) {
	orig := DownstreamRequest{
		Kind:     DownstreamBoth,
		ItemRef:  "item-12345",
		Required: true,
		AssetRequirements: AssetRequirements{
			Voiceover: &VoiceoverRequirements{
				Provider:    "edge-tts",
				VoiceID:     "it-IT-IsabellaNeural",
				Pace:        "0.95",
				StylePreset: "narrative",
			},
			Images: &ImagesRequirements{
				Count:       3,
				StylePreset: "cinematic",
				Provider:    "google_slides",
				Resolution:  "1920x1080",
			},
		},
		OutputDest: OutputDestination{
			Kind:          "drive_folder",
			FolderID:      "1aBcDeFgHiJkLmNoPq",
			DocumentTitle: "",
		},
	}
	data, err := json.Marshal(orig)
	require.NoError(t, err)
	var got DownstreamRequest
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	// DeepEqual catches pointer-field drift that testify assert.Equal misses.
	if !reflect.DeepEqual(orig, got) {
		t.Errorf("JSON round-trip mismatch:\n  orig: %#v\n  got:  %#v\n  json: %s",
			orig, got, string(data))
	}
}

// TestManifestV2_JSONRoundTrip: full manifest with multiple items survives.
func TestManifestV2_JSONRoundTrip(t *testing.T) {
	orig := NewManifestV2()
	orig.Items = append(orig.Items, DownstreamRequest{
		Kind:     DownstreamVoiceover,
		ItemRef:  "item-A",
		Required: true,
		AssetRequirements: AssetRequirements{
			Voiceover: &VoiceoverRequirements{
				Provider: "edge-tts",
				VoiceID:  "en-US-JennyNeural",
			},
		},
		OutputDest: OutputDestination{
			Kind:          "google_doc",
			FolderID:      "1xYzFolder",
			DocumentTitle: "Maestri del cinema asiatico — doc",
		},
	})
	orig.Items = append(orig.Items, DownstreamRequest{
		Kind:     DownstreamImages,
		ItemRef:  "item-B",
		Required: false,
		AssetRequirements: AssetRequirements{
			Images: NewImagesRequirements(2, "vivid"),
		},
		OutputDest: OutputDestination{
			Kind:     "drive_folder",
			FolderID: "1xYzFolder",
		},
	})
	data, err := json.Marshal(orig)
	require.NoError(t, err)
	var got ManifestV2
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.True(t, got.NoInlineAssets,
		"NoInlineAssets=true must survive marshaling")
	require.Equal(t, 2, len(got.Items))
	// Pointer-field comparison: use DeepEqual on Items slice.
	if !reflect.DeepEqual(orig.Items, got.Items) {
		t.Errorf("Items round-trip mismatch:\n  orig: %#v\n  got:  %#v\n  json: %s",
			orig.Items, got.Items, string(data))
	}
}

// ── omitempty sanity ─────────────────────────────────────────────────

// TestAssetRequirements_Omitempty_DropsNilPointers: JSON marshal drops
// nil pointer sub-structs (the canonical contract that lets the
// dispatcher route on Kind with no marshaled-string discriminator).
func TestAssetRequirements_Omitempty_DropsNilPointers(t *testing.T) {
	ar := AssetRequirements{}
	data, err := json.Marshal(ar)
	require.NoError(t, err)
	// Both pointer fields empty + AssetRequirements has no required scalars
	// → JSON must be "{}" (NOT a struct echo).
	assert.Equal(t, "{}", string(data),
		"empty AssetRequirements must marshal to {} (omitempty honour)")
}

// TestOutputDestination_DocumentTitle_Omitempty: empty DocumentTitle drops from JSON.
func TestOutputDestination_DocumentTitle_Omitempty(t *testing.T) {
	o := OutputDestination{Kind: "drive_folder", FolderID: "F1"}
	data, err := json.Marshal(o)
	require.NoError(t, err)
	assert.Equal(t, `{"kind":"drive_folder","folder_id":"F1"}`, string(data),
		"empty DocumentTitle must drop from JSON")
}

// TestVoiceoverRequirements_Pace_and_StylePreset_Omitempty.
func TestVoiceoverRequirements_Pace_and_StylePreset_Omitempty(t *testing.T) {
	v := VoiceoverRequirements{Provider: "edge-tts", VoiceID: "en-US-JennyNeural"}
	data, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t,
		`{"provider":"edge-tts","voice_id":"en-US-JennyNeural"}`,
		string(data),
		"empty Pace + StylePreset must drop from JSON (omitempty)")
}

// ── Voiceover × Images permutations table test ───────────────────────

// TestDownstreamRequestTable_VoiceoverXImages_Permutations: 3 canonical
// combinations per user spec "ogni preset di combinazione voiceover×images".
// (4th combo tested separately as TestNewDownstreamRequestBoth_KinPairing
// below — required to explicitly assert DownstreamBoth + both-pointers-set.)
func TestDownstreamRequestTable_VoiceoverXImages_Permutations(t *testing.T) {
	cases := []struct {
		name           string
		kind           DownstreamKind
		required       bool
		expectedVoice  *VoiceoverRequirements
		expectedImages *ImagesRequirements
	}{
		{
			name:     "voiceover-only-required",
			kind:     DownstreamVoiceover,
			required: true,
			expectedVoice: &VoiceoverRequirements{
				Provider: "eleven-labs", VoiceID: "EXV_default",
			},
			expectedImages: nil,
		},
		{
			name:          "images-only-optional",
			kind:          DownstreamImages,
			required:      false,
			expectedVoice: nil,
			expectedImages: &ImagesRequirements{
				Count: 2, StylePreset: "cinematic",
				Provider: "google_slides", Resolution: "1920x1080",
			},
		},
		{
			name:     "both-with-both-required",
			kind:     DownstreamBoth,
			required: true,
			expectedVoice: &VoiceoverRequirements{
				Provider: "eleven-labs", VoiceID: "EXV_default",
			},
			expectedImages: &ImagesRequirements{
				Count: 4, StylePreset: "photorealistic",
				Provider: "google_slides", Resolution: "1920x1080",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := DownstreamRequest{
				Kind: tc.kind, ItemRef: "item-" + tc.name, Required: tc.required,
				AssetRequirements: AssetRequirements{
					Voiceover: tc.expectedVoice, Images: tc.expectedImages,
				},
				OutputDest: OutputDestination{
					Kind: "drive_folder", FolderID: "F1",
				},
			}
			// Marshal + Unmarshal round-trip.
			data, err := json.Marshal(req)
			require.NoError(t, err)
			var got DownstreamRequest
			err = json.Unmarshal(data, &got)
			require.NoError(t, err)

			// Field-by-field assertions (assert.Equal handles pointer
			// comparison by value — works because pointer fields are
			// round-tripped through JSON and re-allocated on the
			// receiving side; we assert structural equality, not
			// pointer identity).
			assert.Equal(t, tc.kind, got.Kind,
				"Kind must survive round-trip")
			assert.Equal(t, tc.expectedVoice, got.AssetRequirements.Voiceover,
				"Voiceover sub-struct must survive round-trip")
			assert.Equal(t, tc.expectedImages, got.AssetRequirements.Images,
				"Images sub-struct must survive round-trip")
			assert.Equal(t, tc.required, got.Required,
				"Required flag must survive round-trip")
		})
	}
}

// TestDownstreamBoth_BothSubstructsSet: DownstreamBoth explicitly with
// both Voiceover and Images pointers set (the "both-with-both" row above
// exercised independently for stronger invariant coverage).
func TestDownstreamBoth_BothSubstructsSet(t *testing.T) {
	req := DownstreamRequest{
		Kind:     DownstreamBoth,
		ItemRef:  "item-Z",
		Required: true,
		AssetRequirements: AssetRequirements{
			Voiceover: &VoiceoverRequirements{
				Provider: "edge-tts", VoiceID: "en-US-AriaNeural",
			},
			Images: NewImagesRequirements(3, "saturated"),
		},
		OutputDest: OutputDestination{
			Kind: "drive_folder", FolderID: "F9",
		},
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var got DownstreamRequest
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, DownstreamBoth, got.Kind)
	require.NotNil(t, got.AssetRequirements.Voiceover)
	require.NotNil(t, got.AssetRequirements.Images,
		"DownstreamBoth with both point-set survives round-trip")
	assert.Equal(t, 3, got.AssetRequirements.Images.Count)
}

// TestDownstreamBoth_OnePointerNil_StillRoundTrips: DownstreamBoth
// with only Voiceover set (Images=nil) is still a legal DownstreamBoth
// envelope — the dispatcher fans out the "both" preset but skips the
// nil side. Confirms the canonical "pointer singleton" semantic.
func TestDownstreamBoth_OnePointerNil_StillRoundTrips(t *testing.T) {
	req := DownstreamRequest{
		Kind:     DownstreamBoth,
		ItemRef:  "item-W",
		Required: false,
		AssetRequirements: AssetRequirements{
			Voiceover: NewVoiceoverRequirements("edge-tts", "en-US-GuyNeural", "", ""),
			Images:    nil, // NOT set even though Kind=Both
		},
		OutputDest: OutputDestination{Kind: "drive_folder", FolderID: "F8"},
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var got DownstreamRequest
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, DownstreamBoth, got.Kind)
	require.NotNil(t, got.AssetRequirements.Voiceover)
	assert.Nil(t, got.AssetRequirements.Images,
		"Images=nil must survive round-trip (omitempty drops the field from JSON)")
}

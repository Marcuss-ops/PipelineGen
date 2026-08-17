package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func personAnnotations(names ...string) *scriptpkg.SceneAnnotations {
	ann := &scriptpkg.SceneAnnotations{Version: 1}
	for _, name := range names {
		ann.PrimaryEntities = append(ann.PrimaryEntities, scriptpkg.AnnotatedEntity{Type: "PERSON", CanonicalName: name, Text: name})
	}
	return ann
}

func TestValidateSceneClipIdentityExactMatchPasses(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland"),
		Clip:        &ClipReference{ID: "clip-tom", Speakers: []string{"Tom Holland"}},
	}}}
	if err := ValidateSceneClipIdentity(result); err != nil {
		t.Fatalf("matching identity must pass: %v", err)
	}
}

func TestValidateSceneClipIdentityMentionedPeopleAndSubjectPass(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Annotations: personAnnotations("Manny Pacquiao"), Clip: &ClipReference{ID: "clip-a", MentionedPeople: []string{"Manny Pacquiao"}}},
		{ID: "scene-1", Index: 1, Annotations: personAnnotations("Jeffrey Wright"), Clip: &ClipReference{ID: "clip-b", Subject: "Jeffrey Wright"}},
	}}
	if err := ValidateSceneClipIdentity(result); err != nil {
		t.Fatalf("mentioned_people/subject identity must pass: %v", err)
	}
}

func TestValidateSceneClipIdentityPartialNameMatchPasses(t *testing.T) {
	// The scene names a surname; the clip carries the full name. Ordered
	// token-subsequence matching must accept it without requiring equality.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Pacquiao"),
		Clip:        &ClipReference{ID: "clip-a", MentionedPeople: []string{"Manny Pacquiao"}},
	}}}
	if err := ValidateSceneClipIdentity(result); err != nil {
		t.Fatalf("partial (surname) identity must pass: %v", err)
	}
}

func TestValidateSceneClipIdentityMismatchFailsClosed(t *testing.T) {
	// The canonical failure: the scene narrates Tom Holland but the clip
	// features Adam Sandler. It must block, not silently render the wrong man.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland"),
		Clip:        &ClipReference{ID: "clip-adam", Speakers: []string{"Adam Sandler"}},
	}}}
	if err := ValidateSceneClipIdentity(result); err == nil {
		t.Fatal("mismatched person identity must fail closed")
	}
}

func TestValidateSceneClipIdentityMissingIdentityFailsClosed(t *testing.T) {
	// A scene that names a person but whose clip carries no subject identity
	// must fail closed — an unavailable identity is never treated as a match.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland"),
		Clip:        &ClipReference{ID: "clip-unknown"},
	}}}
	if err := ValidateSceneClipIdentity(result); err == nil {
		t.Fatal("missing clip identity must fail closed")
	}
}

func TestValidateSceneClipIdentitySkipsWithoutPersons(t *testing.T) {
	// No PERSON annotation → nothing to certify, even with a clip present.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: &scriptpkg.SceneAnnotations{Version: 1, PrimaryEntities: []scriptpkg.AnnotatedEntity{{Type: "ORG", CanonicalName: "Marvel"}}},
		Clip:        &ClipReference{ID: "clip-a"},
	}}}
	if err := ValidateSceneClipIdentity(result); err != nil {
		t.Fatalf("non-PERSON annotations must skip the gate: %v", err)
	}
}

func TestValidateSceneClipIdentitySkipsWithoutClips(t *testing.T) {
	// Narration-only scene: no clip bound, nothing to certify against.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland"),
	}}}
	if err := ValidateSceneClipIdentity(result); err != nil {
		t.Fatalf("clip-less scenes must skip the gate: %v", err)
	}
}

func TestValidateSceneClipIdentityUnionCoversMultiClip(t *testing.T) {
	// Coverage is collective across a scene's clips: one clip featuring the
	// person is enough, even if the other is B-roll with no identity.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland"),
		Clips: []*ClipReference{
			{ID: "clip-broll"},
			{ID: "clip-tom", Speakers: []string{"Tom Holland"}},
		},
	}}}
	if err := ValidateSceneClipIdentity(result); err != nil {
		t.Fatalf("collective clip identity must pass: %v", err)
	}
}

func TestValidateSceneClipIdentityMissingPersonAcrossUnionFailsClosed(t *testing.T) {
	// Two persons narrated, only one covered by the clips → fail closed.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland", "Andrew Scott"),
		Clips: []*ClipReference{
			{ID: "clip-tom", Speakers: []string{"Tom Holland"}},
			{ID: "clip-broll", Speakers: []string{"commentator"}},
		},
	}}}
	if err := ValidateSceneClipIdentity(result); err == nil {
		t.Fatal("a narrated person not covered by the clips must fail closed")
	}
}

func TestAuditSceneClipIdentityReportsStructuredMismatches(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Annotations: personAnnotations("Tom Holland"), Clip: &ClipReference{ID: "clip-no-identity"}},
		{ID: "scene-1", Index: 1, Annotations: personAnnotations("Andrew Scott"), Clip: &ClipReference{ID: "clip-adam", Speakers: []string{"Adam Sandler"}}},
		{ID: "scene-2", Index: 2, Annotations: personAnnotations("Manny Pacquiao"), Clip: &ClipReference{ID: "clip-manny", MentionedPeople: []string{"Manny Pacquiao"}}},
	}}
	mismatches := AuditSceneClipIdentity(result)
	require.Len(t, mismatches, 2)
	require.Equal(t, "scene-0", mismatches[0].SceneID)
	require.Equal(t, ClipIdentityReasonMissing, mismatches[0].Reason)
	require.Equal(t, []string{"Tom Holland"}, mismatches[0].Persons)
	require.Equal(t, "scene-1", mismatches[1].SceneID)
	require.Equal(t, ClipIdentityReasonMismatch, mismatches[1].Reason)
	require.Equal(t, []string{"Andrew Scott"}, mismatches[1].Persons)
	require.Equal(t, []string{"Adam Sandler"}, mismatches[1].ClipIdentity)
	require.Equal(t, []string{"scene-0", "scene-1"}, clipIdentityMismatchSceneIDs(mismatches))
}

func TestAuditSceneClipIdentityEmptyWhenAllMatch(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-0", Index: 0,
		Annotations: personAnnotations("Tom Holland"),
		Clip:        &ClipReference{ID: "clip-tom", Speakers: []string{"Tom Holland"}},
	}}}
	require.Empty(t, AuditSceneClipIdentity(result))
}

func TestIdentityNamesOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Tom Holland", "Tom Holland", true},
		{"tom holland", "TOM HOLLAND", true},
		{"Pacquiao", "Manny Pacquiao", true},
		{"Manny Pacquiao", "Pacquiao", true},
		{"Tom Holland", "Adam Sandler", false},
		{"Tom", "Tommy", false},
		{"Will", "William", false},
		{"Tom Hanks", "Tom Holland", false},
		{"", "Tom Holland", false},
		{"Tom Holland", "", false},
	}
	for _, tc := range cases {
		if got := identityNamesOverlap(normalizeIdentityName(tc.a), normalizeIdentityName(tc.b)); got != tc.want {
			t.Errorf("identityNamesOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

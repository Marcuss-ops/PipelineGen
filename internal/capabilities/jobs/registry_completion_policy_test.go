package jobs

import (
	"errors"
	"testing"
)

func TestRegistry_CompletionDeclarationRejectsIncoherentPolicies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		owner ArtifactOwnership
		final FinalizationStrategy
	}{
		{"worker spine cannot use legacy complete", ArtifactOwnershipWorkerSpine, FinalizationStrategyLegacyComplete},
		{"application transaction cannot use artifact completion", ArtifactOwnershipApplication, FinalizationStrategyCompleteWithArtifacts},
		{"artifact free cannot use artifact completion", ArtifactOwnershipNone, FinalizationStrategyCompleteWithArtifacts},
		{"unknown owner is rejected", ArtifactOwnership("unknown"), FinalizationStrategyLegacyComplete},
		{"unknown strategy is rejected", ArtifactOwnershipNone, FinalizationStrategy("unknown")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := RegistryEntry{Completion: CompletionDeclaration{JobType: "test.completion", ArtifactOwnership: tc.owner, FinalizationStrategy: tc.final}}
			if err := entry.ValidateCompletion(); !errors.Is(err, ErrInvalidCompletionDeclaration) {
				t.Fatalf("ValidateCompletion() error = %v, want ErrInvalidCompletionDeclaration", err)
			}
			reg := NewRegistry()
			if err := reg.Register(entry); !errors.Is(err, ErrInvalidCompletionDeclaration) {
				t.Fatalf("Register() error = %v, want ErrInvalidCompletionDeclaration", err)
			}
		})
	}
}

func TestRegistry_CompletionDeclarationAcceptsCanonicalPolicies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		owner ArtifactOwnership
		final FinalizationStrategy
	}{
		{"artifact free", ArtifactOwnershipNone, FinalizationStrategyLegacyComplete},
		{"application transaction", ArtifactOwnershipApplication, FinalizationStrategyLegacyComplete},
		{"worker spine", ArtifactOwnershipWorkerSpine, FinalizationStrategyCompleteWithArtifacts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := RegistryEntry{Completion: CompletionDeclaration{JobType: "test." + tc.name, ArtifactOwnership: tc.owner, FinalizationStrategy: tc.final}}
			if err := entry.ValidateCompletion(); err != nil {
				t.Fatalf("ValidateCompletion() = %v, want nil", err)
			}
			reg := NewRegistry()
			if err := reg.Register(entry); err != nil {
				t.Fatalf("Register() = %v, want nil", err)
			}
		})
	}
}

func TestCompose_CompletionPolicyHasOneCanonicalProjection(t *testing.T) {
	t.Parallel()

	reg := Compose()
	artifactTypes := reg.ProducesArtifactsMap()
	for _, jobType := range reg.AllTypes() {
		want := reg.FinalizationStrategy(jobType) == FinalizationStrategyCompleteWithArtifacts
		_, got := artifactTypes[jobType]
		if want != got {
			t.Fatalf("completion policy drift for %q: strategy=%q, map membership=%t", jobType, reg.FinalizationStrategy(jobType), got)
		}
		entry, ok := reg.Get(jobType)
		if !ok {
			t.Fatalf("job type %q disappeared from registry", jobType)
		}
		if err := entry.ValidateCompletion(); err != nil {
			t.Fatalf("job type %q has invalid completion declaration: %v", jobType, err)
		}
	}
}

func TestCompose_CompletionPolicyPinsCanonicalOwners(t *testing.T) {
	t.Parallel()

	reg := Compose()
	cases := []struct {
		name  string
		typ   string
		owner ArtifactOwnership
		final FinalizationStrategy
	}{
		{name: "stock uses JobFinalizer spine", typ: TypeMediaStock, owner: ArtifactOwnershipWorkerSpine, final: FinalizationStrategyCompleteWithArtifacts},
		{name: "script parent uses JobFinalizer spine", typ: TypeScriptGenerate, owner: ArtifactOwnershipWorkerSpine, final: FinalizationStrategyCompleteWithArtifacts},
		{name: "voiceover batch uses application finalizer", typ: TypeVoiceoverBatch, owner: ArtifactOwnershipApplication, final: FinalizationStrategyLegacyComplete},
		{name: "voiceover child uses application finalizer", typ: TypeVoiceoverGenerateItem, owner: ArtifactOwnershipApplication, final: FinalizationStrategyLegacyComplete},
		{name: "youtube clip uses application finalizer", typ: TypeYouTubeClipExtract, owner: ArtifactOwnershipApplication, final: FinalizationStrategyLegacyComplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reg.ArtifactOwnership(tc.typ); got != tc.owner {
				t.Fatalf("job type %q ownership=%q, want %q", tc.typ, got, tc.owner)
			}
			if got := reg.FinalizationStrategy(tc.typ); got != tc.final {
				t.Fatalf("job type %q strategy=%q, want %q", tc.typ, got, tc.final)
			}
		})
	}
}

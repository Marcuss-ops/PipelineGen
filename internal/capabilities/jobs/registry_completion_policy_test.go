package jobs

import (
	"errors"
	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	"testing"
)

func TestRegistry_CompletionDeclarationRejectsIncoherentPolicies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		owner jobscheduling.ArtifactOwnership
		final jobscheduling.FinalizationStrategy
	}{
		{"worker spine cannot use legacy complete", jobscheduling.ArtifactOwnershipWorkerSpine, jobscheduling.FinalizationStrategyLegacyComplete},
		{"application transaction cannot use artifact completion", jobscheduling.ArtifactOwnershipApplication, jobscheduling.FinalizationStrategyCompleteWithArtifacts},
		{"artifact free cannot use artifact completion", jobscheduling.ArtifactOwnershipNone, jobscheduling.FinalizationStrategyCompleteWithArtifacts},
		{"unknown owner is rejected", jobscheduling.ArtifactOwnership("unknown"), jobscheduling.FinalizationStrategyLegacyComplete},
		{"unknown strategy is rejected", jobscheduling.ArtifactOwnershipNone, jobscheduling.FinalizationStrategy("unknown")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := jobscheduling.RegistryEntry{Completion: jobscheduling.CompletionDeclaration{JobType: "test.completion", ArtifactOwnership: tc.owner, FinalizationStrategy: tc.final}}
			if err := entry.ValidateCompletion(); !errors.Is(err, jobscheduling.ErrInvalidCompletionDeclaration) {
				t.Fatalf("ValidateCompletion() error = %v, want jobscheduling.ErrInvalidCompletionDeclaration", err)
			}
			reg := jobscheduling.NewRegistry()
			if err := reg.Register(entry); !errors.Is(err, jobscheduling.ErrInvalidCompletionDeclaration) {
				t.Fatalf("Register() error = %v, want jobscheduling.ErrInvalidCompletionDeclaration", err)
			}
		})
	}
}

func TestRegistry_CompletionDeclarationAcceptsCanonicalPolicies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		owner jobscheduling.ArtifactOwnership
		final jobscheduling.FinalizationStrategy
	}{
		{"artifact free", jobscheduling.ArtifactOwnershipNone, jobscheduling.FinalizationStrategyLegacyComplete},
		{"application transaction", jobscheduling.ArtifactOwnershipApplication, jobscheduling.FinalizationStrategyLegacyComplete},
		{"worker spine", jobscheduling.ArtifactOwnershipWorkerSpine, jobscheduling.FinalizationStrategyCompleteWithArtifacts},
		// The staged pair is deliberate, not a synonym: the worker still
		// publishes its own spine artifacts, but the terminal flip is deferred
		// to the parent aggregator (TICKET-CORE-READY-DURABLE-DAG blocker B1).
		{"worker spine then defer", jobscheduling.ArtifactOwnershipWorkerSpine, jobscheduling.FinalizationStrategyCompleteWithArtifactsThenDefer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := jobscheduling.RegistryEntry{Completion: jobscheduling.CompletionDeclaration{JobType: "test." + tc.name, ArtifactOwnership: tc.owner, FinalizationStrategy: tc.final}}
			if err := entry.ValidateCompletion(); err != nil {
				t.Fatalf("ValidateCompletion() = %v, want nil", err)
			}
			reg := jobscheduling.NewRegistry()
			if err := reg.Register(entry); err != nil {
				t.Fatalf("Register() = %v, want nil", err)
			}
		})
	}
}

func TestCompose_CompletionPolicyHasOneCanonicalProjection(t *testing.T) {
	t.Parallel()

	reg := jobscheduling.Compose()
	artifactTypes := reg.ProducesArtifactsMap()
	for _, jobType := range reg.AllTypes() {
		// The projection is OWNERSHIP-derived, because that is what the
		// production path reads: finalizeJob branches on
		// reg.ProducesArtifacts(j.Type) and JobTypeRegistry.ProducesArtifacts
		// keys on jobscheduling.ArtifactOwnership == WorkerSpine. Asserting the strategy
		// itself would have made every WorkerSpine job whose strategy is not
		// EXACTLY CompleteWithArtifacts read as a regression, even though its
		// ownership — and therefore the whole production projection — is
		// unchanged. The staged strategy exists precisely to be such a job.
		want := reg.ArtifactOwnership(jobType) == jobscheduling.ArtifactOwnershipWorkerSpine
		_, got := artifactTypes[jobType]
		if want != got {
			t.Fatalf("completion policy drift for %q: ownership=%q (produces artifacts=%t), map membership=%t", jobType, reg.ArtifactOwnership(jobType), want, got)
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

	reg := jobscheduling.Compose()
	cases := []struct {
		name  string
		typ   string
		owner jobscheduling.ArtifactOwnership
		final jobscheduling.FinalizationStrategy
	}{
		{name: "stock uses JobFinalizer spine", typ: jobscheduling.TypeMediaStock, owner: jobscheduling.ArtifactOwnershipWorkerSpine, final: jobscheduling.FinalizationStrategyCompleteWithArtifacts},
		// The script parent publishes its own spine AND defers the terminal flip
		// to the aggregator (the only job type with both roles).
		{name: "script parent uses JobFinalizer spine", typ: jobscheduling.TypeScriptGenerate, owner: jobscheduling.ArtifactOwnershipWorkerSpine, final: jobscheduling.FinalizationStrategyCompleteWithArtifactsThenDefer},
		{name: "voiceover batch uses application finalizer", typ: jobscheduling.TypeVoiceoverBatch, owner: jobscheduling.ArtifactOwnershipApplication, final: jobscheduling.FinalizationStrategyLegacyComplete},
		{name: "voiceover child uses application finalizer", typ: jobscheduling.TypeVoiceoverGenerateItem, owner: jobscheduling.ArtifactOwnershipApplication, final: jobscheduling.FinalizationStrategyLegacyComplete},
		{name: "youtube clip uses application finalizer", typ: jobscheduling.TypeYouTubeClipExtract, owner: jobscheduling.ArtifactOwnershipApplication, final: jobscheduling.FinalizationStrategyLegacyComplete},
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

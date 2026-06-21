package qdrant

import "testing"

// TestQdrantClient_operationCollection_Routing pins the 3-case routing
// matrix documented on QdrantClient.operationCollection. Each sub-case
// is a single Client with no I/O; the test asserts which name the
// resolver returns. A future refactor that reorders the gates,
// reintroduces a dead tail, or breaks the aliasName invariant will trip
// these cases immediately.
func TestQdrantClient_operationCollection_Routing(t *testing.T) {
	const logical = "pipelinegen_clips"

	cases := []struct {
		name              string
		collectionVersion string
		collectionAlias   string
		disableAlias      bool
		want              string
	}{
		{
			// Case 1: legacy mode. resolver -> Collection.
			name:              "legacy empty version returns logical Collection",
			collectionVersion: "",
			want:              logical,
		},
		{
			// Case 1: legacy mode overrides DisableAlias too (the first
			// gate shadows everything).
			name:              "legacy empty version + disableAlias still returns Collection",
			collectionVersion: "",
			disableAlias:      true,
			want:              logical,
		},
		{
			// Case 2: versioned-direct via DisableAlias.
			name:              "DisableAlias true returns versioned name regardless of CollectionAlias",
			collectionVersion: "v3",
			collectionAlias:   "ignored_in_case2",
			disableAlias:      true,
			want:              logical + "_v3",
		},
		{
			// Case 3a: alias-routed with explicit CollectionAlias.
			name:              "alias-routed with explicit CollectionAlias returns that alias",
			collectionVersion: "v3",
			collectionAlias:   "pipelinegen_clips_prod",
			want:              "pipelinegen_clips_prod",
		},
		{
			// Case 3b: alias-routed without explicit alias -> default
			// "{Collection}_current" is used (NOT versionedCollectionName).
			// This is the case that the deleted tail would have mishandled.
			name:              "alias-routed without explicit alias defaults to current",
			collectionVersion: "v3",
			collectionAlias:   "",
			want:              logical + "_current",
		},
		{
			// Stress case: when the explicit alias string is identical to
			// the versionedCollectionName output, the route is still
			// determined by which gate fires, not by string equality.
			// Without this case a buggy implementation that emitted
			// versionedCollectionName here would coincidentally pass the
			// tests where the strings diverge.
			name:              "alias string coincidentally equals versioned name",
			collectionVersion: "v1",
			collectionAlias:   logical + "_v1", // same as versionedCollectionName()
			want:              logical + "_v1",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := (&QdrantClient{
				cfg: Config{
					Collection:        logical,
					CollectionVersion: tc.collectionVersion,
					CollectionAlias:   tc.collectionAlias,
					DisableAlias:      tc.disableAlias,
				},
			}).operationCollection()
			if got != tc.want {
				t.Fatalf("operationCollection = %q, want %q", got, tc.want)
			}
		})
	}
}

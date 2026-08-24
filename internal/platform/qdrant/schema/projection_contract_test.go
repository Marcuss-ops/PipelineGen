package schema

import (
	"strings"
	"testing"

)

func TestProjectionContracts_CanonicalIdentity(t *testing.T) {
	cases := []struct {
		name       string
		contract   ProjectionContract
		wantKind   ProjectionKind
		wantAlias  string
		wantPhys   string
		wantPrefix string
	}{
		{
			name:       "media_assets",
			contract:   MediaAssetsProjection(),
			wantKind:   ProjectionMediaAssets,
			wantAlias:  "media_assets_current",
			wantPhys:   DefaultV3Schema().PhysicalName,
			wantPrefix: AssetPointIDPrefix,
		},
		{
			name:       "media_frames",
			contract:   MediaFramesProjection(),
			wantKind:   ProjectionMediaFrames,
			wantAlias:  FrameCollectionName,
			wantPhys:   FrameCollectionName,
			wantPrefix: FramePointIDPrefix,
		},
		{
			name:       "media_concepts",
			contract:   MediaConceptsProjection(),
			wantKind:   ProjectionMediaConcepts,
			wantAlias:  ConceptCollectionName,
			wantPhys:   ConceptCollectionName,
			wantPrefix: ConceptPointIDPrefix,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.contract
			if c.Kind != tc.wantKind {
				t.Fatalf("Kind=%q, want %q", c.Kind, tc.wantKind)
			}
			if c.Alias() != tc.wantAlias {
				t.Fatalf("Alias=%q, want %q", c.Alias(), tc.wantAlias)
			}
			if c.PhysicalName() != tc.wantPhys {
				t.Fatalf("PhysicalName=%q, want %q", c.PhysicalName(), tc.wantPhys)
			}
			if c.PointIDPrefix != tc.wantPrefix {
				t.Fatalf("PointIDPrefix=%q, want %q", c.PointIDPrefix, tc.wantPrefix)
			}
			if c.RetentionPrefix != tc.wantPhys {
				t.Fatalf("RetentionPrefix=%q, want %q", c.RetentionPrefix, tc.wantPhys)
			}
			// The contract's Schema must be the canonical manifest, not a
			// divergent copy: re-deriving the schema yields the same alias
			// and physical name.
			if c.Schema.RuntimeAlias != c.Alias() || c.Schema.PhysicalName != c.PhysicalName() {
				t.Fatalf("contract schema diverges: alias=%q phys=%q vs alias=%q phys=%q",
					c.Schema.RuntimeAlias, c.Schema.PhysicalName, c.Alias(), c.PhysicalName())
			}
		})
	}
}

func TestProjectionContract_Validate(t *testing.T) {
	for _, c := range AllProjections() {
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate(%q) = %v, want nil", c.Kind, err)
		}
	}
}

func TestProjectionContract_Validate_FailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ProjectionContract)
		want   string
	}{
		{
			name: "unknown kind",
			mutate: func(c *ProjectionContract) {
				c.Kind = "other"
			},
			want: "unknown kind",
		},
		{
			name: "nil schema",
			mutate: func(c *ProjectionContract) {
				c.Schema = nil
			},
			want: "nil schema",
		},
		{
			name: "empty retention prefix",
			mutate: func(c *ProjectionContract) {
				c.RetentionPrefix = ""
			},
			want: "empty retention prefix",
		},
		{
			name: "retention prefix not a prefix of physical name",
			mutate: func(c *ProjectionContract) {
				c.RetentionPrefix = "media_other"
			},
			want: "not a prefix",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := MediaAssetsProjection()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateProjectionSeparation(t *testing.T) {
	if err := ValidateProjectionSeparation(AllProjections()); err != nil {
		t.Fatalf("ValidateProjectionSeparation(all) = %v, want nil", err)
	}
}

func TestValidateProjectionSeparation_FailClosed(t *testing.T) {
	t.Run("duplicate kind", func(t *testing.T) {
		cs := AllProjections()
		cs[1].Kind = ProjectionMediaAssets
		if err := ValidateProjectionSeparation(cs); err == nil {
			t.Fatal("expected duplicate-kind error, got nil")
		}
	})
	t.Run("shared physical name", func(t *testing.T) {
		cs := AllProjections()
		cs[1].Schema.PhysicalName = cs[0].Schema.PhysicalName
		if err := ValidateProjectionSeparation(cs); err == nil {
			t.Fatal("expected shared physical-name error, got nil")
		}
	})
	t.Run("shared runtime alias", func(t *testing.T) {
		cs := AllProjections()
		cs[2].Schema.RuntimeAlias = cs[0].Schema.RuntimeAlias
		if err := ValidateProjectionSeparation(cs); err == nil {
			t.Fatal("expected shared alias error, got nil")
		}
	})
	t.Run("shared point-id prefix", func(t *testing.T) {
		cs := AllProjections()
		cs[2].PointIDPrefix = cs[1].PointIDPrefix
		if err := ValidateProjectionSeparation(cs); err == nil {
			t.Fatal("expected shared point-id prefix error, got nil")
		}
	})
	t.Run("distinct point-id prefixes by default", func(t *testing.T) {
		cs := AllProjections()
		seen := make(map[string]bool)
		for _, c := range cs {
			if seen[c.PointIDPrefix] {
				t.Fatalf("point-id prefix %q is shared across projections", c.PointIDPrefix)
			}
			seen[c.PointIDPrefix] = true
		}
	})
}

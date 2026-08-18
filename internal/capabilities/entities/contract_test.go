package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOverlayPlanContract_EntityResolverEmitsRef pins the full contract on
// the resolver path: every entity card item carries the entity_ref with the
// STABLE content-addressed entity id (not a slug), the NLP type and the
// canonical name — RenderingGen never has to guess who the card is about.
func TestOverlayPlanContract_EntityResolverEmitsRef(t *testing.T) {
	timeline := entityTimelineFixture(t)
	plan, err := ResolveEntityOverlayPlan(timeline, "plan-contract-001", "video-contract-001", "", 1920, 1080, 30)
	require.NoError(t, err)
	require.NoError(t, plan.Validate())

	require.NotEmpty(t, plan.Items, "the timeline must resolve to entity cards")
	for _, item := range plan.Items {
		require.NotNil(t, item.EntityRef, "every entity card must carry entity_ref")
		require.NotEmpty(t, item.EntityRef.EntityID, "entity_ref must carry the stable entity id")
		require.NotEqual(t, SafeEntityID(item.EntityRef.Name), item.EntityRef.EntityID,
			"entity_ref.entity_id must be the content-addressed StableEntityID, not the readable slug")
		require.NotEmpty(t, item.EntityRef.Type)
		require.NotEmpty(t, item.EntityRef.Name)
		require.Equal(t, item.Text, item.EntityRef.Name, "rendered text must be the canonical entity name")
	}
}
